package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/liciomatos/pgdba-cli/vault"
	"golang.org/x/term"
)

// stdinReader is shared so consecutive non-interactive prompts (piped input) don't
// lose lines to separate bufio buffers.
var stdinReader = bufio.NewReader(os.Stdin)

// readSecret prompts on stderr and reads a line without echo when stdin is a
// terminal. When stdin is piped it reads a plain line, so scripts can feed secrets.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		secret, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(secret), err
	}
	line, err := stdinReader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// masterPassword returns $PGDBA_VAULT_PASSWORD when set (non-interactive use such
// as --mcp under a service manager), otherwise prompts. confirm asks twice, used
// when a new vault is being created so a typo doesn't lock the user out.
func masterPassword(confirm bool) (string, error) {
	if password := os.Getenv("PGDBA_VAULT_PASSWORD"); password != "" {
		return password, nil
	}
	password, err := readSecret("Vault master password: ")
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errors.New("master password must not be empty")
	}
	if confirm {
		repeated, err := readSecret("Confirm master password: ")
		if err != nil {
			return "", err
		}
		if repeated != password {
			return "", errors.New("master passwords do not match")
		}
	}
	return password, nil
}

// openVault loads the vault, or returns an empty one (and asks for password
// confirmation) when createIfMissing is set and no vault file exists yet.
func openVault(createIfMissing bool) (*vault.Vault, string, string, error) {
	path, err := vault.DefaultPath()
	if err != nil {
		return nil, "", "", err
	}
	if !vault.Exists(path) {
		if !createIfMissing {
			return nil, "", "", fmt.Errorf("no vault at %s — create one with: pgdba-cli vault add <name>", path)
		}
		fmt.Fprintf(os.Stderr, "Creating new vault at %s\n", path)
		password, err := masterPassword(true)
		if err != nil {
			return nil, "", "", err
		}
		return vault.New(), path, password, nil
	}
	password, err := masterPassword(false)
	if err != nil {
		return nil, "", "", err
	}
	loaded, err := vault.Load(path, password)
	if err != nil {
		return nil, "", "", err
	}
	return loaded, path, password, nil
}

// connectionURIFromVault resolves --vault NAME to the stored connection URI.
func connectionURIFromVault(name string) (string, error) {
	loaded, _, _, err := openVault(false)
	if err != nil {
		return "", err
	}
	connectionURI, err := loaded.Get(name)
	if errors.Is(err, vault.ErrEntryNotFound) {
		return "", fmt.Errorf("%w (available: %s)", err, strings.Join(loaded.Names(), ", "))
	}
	return connectionURI, err
}

const vaultUsage = `Usage:
  pgdba-cli vault add <name> [uri]   store a connection URI (prompted without echo if omitted)
  pgdba-cli vault list               list stored connections (passwords masked)
  pgdba-cli vault remove <name>      delete a stored connection
  pgdba-cli vault path               print the vault file location

Connect with:  pgdba-cli --vault <name>
Master password is prompted, or read from PGDBA_VAULT_PASSWORD.
Vault file defaults to <config dir>/pgdba-cli/vault.json (override with PGDBA_VAULT_FILE).
`

// runVaultCommand implements `pgdba-cli vault …` and returns the process exit code.
func runVaultCommand(args []string) int {
	if err := dispatchVaultCommand(args); err != nil {
		fmt.Fprintf(os.Stderr, "pgdba-cli vault: %v\n", err)
		return 1
	}
	return 0
}

func dispatchVaultCommand(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, vaultUsage)
		return errors.New("missing subcommand")
	}
	switch args[0] {
	case "add":
		if len(args) < 2 || len(args) > 3 {
			return errors.New("usage: pgdba-cli vault add <name> [uri]")
		}
		name := args[1]
		var connectionURI string
		if len(args) == 3 {
			connectionURI = args[2]
		} else {
			// Prompting keeps the password out of shell history and `ps` output.
			var err error
			if connectionURI, err = readSecret(fmt.Sprintf("Connection URI for %q: ", name)); err != nil {
				return err
			}
		}
		// Validate before asking for the master password so a typo fails fast.
		if err := vault.ValidateURI(connectionURI); err != nil {
			return err
		}
		loaded, path, password, err := openVault(true)
		if err != nil {
			return err
		}
		_, replacing := loaded.Connections[name]
		if err := loaded.Add(name, connectionURI); err != nil {
			return err
		}
		if err := loaded.Save(path, password); err != nil {
			return err
		}
		if replacing {
			fmt.Fprintf(os.Stderr, "Updated %q\n", name)
		} else {
			fmt.Fprintf(os.Stderr, "Added %q\n", name)
		}
		return nil
	case "list", "ls":
		loaded, _, _, err := openVault(false)
		if err != nil {
			return err
		}
		names := loaded.Names()
		if len(names) == 0 {
			fmt.Fprintln(os.Stderr, "Vault is empty")
			return nil
		}
		for _, name := range names {
			fmt.Printf("%-20s %s\n", name, vault.Redact(loaded.Connections[name]))
		}
		return nil
	case "remove", "rm":
		if len(args) != 2 {
			return errors.New("usage: pgdba-cli vault remove <name>")
		}
		loaded, path, password, err := openVault(false)
		if err != nil {
			return err
		}
		if err := loaded.Remove(args[1]); err != nil {
			return err
		}
		if err := loaded.Save(path, password); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Removed %q\n", args[1])
		return nil
	case "path":
		path, err := vault.DefaultPath()
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(os.Stderr, vaultUsage)
		return nil
	}
	fmt.Fprint(os.Stderr, vaultUsage)
	return fmt.Errorf("unknown subcommand %q", args[0])
}
