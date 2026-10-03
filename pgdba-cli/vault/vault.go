// Package vault stores named PostgreSQL connection URIs in a local file encrypted
// with a master password (Argon2id key derivation + AES-256-GCM).
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/crypto/argon2"
)

const (
	formatVersion = 1
	kdfName       = "argon2id"
	saltLength    = 16
	keyLength     = 32 // AES-256
)

// Argon2id cost parameters (RFC 9106 "second recommended" profile, 64 MiB).
// They are persisted in the file so they can be raised later without breaking
// vaults written with the old values.
const (
	defaultArgonTime    uint32 = 3
	defaultArgonMemory  uint32 = 64 * 1024
	defaultArgonThreads uint8  = 4
)

var (
	ErrNotFound      = errors.New("vault file does not exist")
	ErrWrongPassword = errors.New("wrong master password or corrupted vault")
	ErrEntryNotFound = errors.New("connection not found in vault")
)

// Vault is the decrypted content: connection name → connection URI.
type Vault struct {
	Connections map[string]string `json:"connections"`
}

// encryptedFile is the on-disk JSON layout. Only Ciphertext is secret; the
// header fields are bound to it as GCM additional data so tampering with the
// KDF parameters makes decryption fail instead of silently weakening the key.
type encryptedFile struct {
	Version      int    `json:"version"`
	KDF          string `json:"kdf"`
	ArgonTime    uint32 `json:"argon_time"`
	ArgonMemory  uint32 `json:"argon_memory"`
	ArgonThreads uint8  `json:"argon_threads"`
	Salt         []byte `json:"salt"`
	Nonce        []byte `json:"nonce"`
	Ciphertext   []byte `json:"ciphertext"`
}

func (f encryptedFile) additionalData() []byte {
	return fmt.Appendf(nil, "pgdba-vault:v%d:%s:%d:%d:%d",
		f.Version, f.KDF, f.ArgonTime, f.ArgonMemory, f.ArgonThreads)
}

// DefaultPath returns $PGDBA_VAULT_FILE if set, otherwise
// <user config dir>/pgdba-cli/vault.json (e.g. ~/.config/pgdba-cli/vault.json on Linux).
func DefaultPath() (string, error) {
	if path := os.Getenv("PGDBA_VAULT_FILE"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "pgdba-cli", "vault.json"), nil
}

// Exists reports whether a vault file is present at path.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// New returns an empty vault.
func New() *Vault {
	return &Vault{Connections: map[string]string{}}
}

// Load reads and decrypts the vault at path.
func Load(path, masterPassword string) (*Vault, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var file encryptedFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("invalid vault file %s: %w", path, err)
	}
	if file.Version != formatVersion || file.KDF != kdfName {
		return nil, fmt.Errorf("unsupported vault format (version %d, kdf %q)", file.Version, file.KDF)
	}
	gcm, err := newGCM(masterPassword, file.Salt, file.ArgonTime, file.ArgonMemory, file.ArgonThreads)
	if err != nil {
		return nil, err
	}
	if len(file.Nonce) != gcm.NonceSize() {
		return nil, ErrWrongPassword
	}
	plaintext, err := gcm.Open(nil, file.Nonce, file.Ciphertext, file.additionalData())
	if err != nil {
		return nil, ErrWrongPassword
	}
	vault := New()
	if err := json.Unmarshal(plaintext, vault); err != nil {
		return nil, fmt.Errorf("invalid vault content: %w", err)
	}
	if vault.Connections == nil {
		vault.Connections = map[string]string{}
	}
	return vault, nil
}

// Save encrypts the vault with a fresh salt and nonce and writes it atomically
// to path with 0600 permissions.
func (v *Vault) Save(path, masterPassword string) error {
	plaintext, err := json.Marshal(v)
	if err != nil {
		return err
	}
	file := encryptedFile{
		Version:      formatVersion,
		KDF:          kdfName,
		ArgonTime:    defaultArgonTime,
		ArgonMemory:  defaultArgonMemory,
		ArgonThreads: defaultArgonThreads,
		Salt:         make([]byte, saltLength),
	}
	if _, err := rand.Read(file.Salt); err != nil {
		return err
	}
	gcm, err := newGCM(masterPassword, file.Salt, file.ArgonTime, file.ArgonMemory, file.ArgonThreads)
	if err != nil {
		return err
	}
	file.Nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(file.Nonce); err != nil {
		return err
	}
	file.Ciphertext = gcm.Seal(nil, file.Nonce, plaintext, file.additionalData())

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Write to a temp file in the same directory and rename, so a crash mid-write
	// never leaves a truncated vault behind.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vault-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Add stores (or replaces) a connection URI under name.
func (v *Vault) Add(name, connectionURI string) error {
	if name == "" {
		return errors.New("connection name must not be empty")
	}
	if err := ValidateURI(connectionURI); err != nil {
		return err
	}
	v.Connections[name] = connectionURI
	return nil
}

// Remove deletes the connection stored under name.
func (v *Vault) Remove(name string) error {
	if _, ok := v.Connections[name]; !ok {
		return fmt.Errorf("%w: %q", ErrEntryNotFound, name)
	}
	delete(v.Connections, name)
	return nil
}

// Get returns the connection URI stored under name.
func (v *Vault) Get(name string) (string, error) {
	connectionURI, ok := v.Connections[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrEntryNotFound, name)
	}
	return connectionURI, nil
}

// Names returns the stored connection names in alphabetical order.
func (v *Vault) Names() []string {
	names := make([]string, 0, len(v.Connections))
	for name := range v.Connections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidateURI accepts only postgres:// / postgresql:// URIs, because the rest of
// pgdba-cli parses the stored value with net/url to fill host/user/dbname.
func ValidateURI(connectionURI string) error {
	parsed, err := url.Parse(connectionURI)
	if err != nil {
		return fmt.Errorf("invalid connection URI: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return errors.New("connection URI must start with postgres:// or postgresql://")
	}
	return nil
}

// Redact returns the URI with its password masked, safe for display.
func Redact(connectionURI string) string {
	parsed, err := url.Parse(connectionURI)
	if err != nil {
		return "<invalid URI>"
	}
	return parsed.Redacted()
}

func newGCM(masterPassword string, salt []byte, argonTime, argonMemory uint32, argonThreads uint8) (cipher.AEAD, error) {
	if masterPassword == "" {
		return nil, errors.New("master password must not be empty")
	}
	if len(salt) != saltLength || argonTime == 0 || argonMemory == 0 || argonThreads == 0 {
		return nil, errors.New("invalid vault key-derivation parameters")
	}
	key := argon2.IDKey([]byte(masterPassword), salt, argonTime, argonMemory, argonThreads, keyLength)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
