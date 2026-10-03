package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVault_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "vault.json")
	original := New()
	if err := original.Add("prod", "postgres://admin:s3cret@db.example.com:5432/app?sslmode=require"); err != nil {
		t.Fatal(err)
	}
	if err := original.Save(path, "master"); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path, "master")
	if err != nil {
		t.Fatal(err)
	}
	got, err := loaded.Get("prod")
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://admin:s3cret@db.example.com:5432/app?sslmode=require" {
		t.Fatalf("unexpected URI %q", got)
	}
}

func TestVault_FileIsEncryptedAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.json")
	v := New()
	_ = v.Add("prod", "postgres://admin:s3cret@db.example.com/app")
	if err := v.Save(path, "master"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"s3cret", "db.example.com", "prod"} {
		if strings.Contains(string(data), leak) {
			t.Fatalf("vault file leaks %q in plaintext", leak)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 permissions, got %o", perm)
	}
}

func TestVault_WrongPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.json")
	if err := New().Save(path, "master"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "not-master"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

func TestVault_TamperedKDFParamsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.json")
	if err := New().Save(path, "master"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var file encryptedFile
	_ = json.Unmarshal(data, &file)
	file.ArgonTime = 1
	data, _ = json.Marshal(file)
	_ = os.WriteFile(path, data, 0o600)

	if _, err := Load(path, "master"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword after tampering, got %v", err)
	}
}

func TestVault_LoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json"), "master"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVault_AddRejectsNonPostgresURI(t *testing.T) {
	v := New()
	for _, uri := range []string{"host=x user=y", "mysql://u@h/db", ""} {
		if err := v.Add("x", uri); err == nil {
			t.Fatalf("expected error for %q", uri)
		}
	}
	if err := v.Add("", "postgres://h/db"); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestVault_RemoveAndNames(t *testing.T) {
	v := New()
	_ = v.Add("staging", "postgresql://h/db")
	_ = v.Add("prod", "postgres://h/db")
	if names := v.Names(); len(names) != 2 || names[0] != "prod" || names[1] != "staging" {
		t.Fatalf("unexpected names %v", names)
	}
	if err := v.Remove("prod"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove("prod"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("expected ErrEntryNotFound, got %v", err)
	}
	if _, err := v.Get("prod"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("expected ErrEntryNotFound, got %v", err)
	}
}

func TestRedact_MasksPassword(t *testing.T) {
	got := Redact("postgres://admin:s3cret@db/app")
	if strings.Contains(got, "s3cret") {
		t.Fatalf("password not redacted: %q", got)
	}
}
