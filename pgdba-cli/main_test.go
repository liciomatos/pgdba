package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetEnv_WithValue(t *testing.T) {
	t.Setenv("PGDBA_TEST_KEY", "hello")
	if got := getEnv("PGDBA_TEST_KEY", "fallback"); got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}
}

func TestGetEnv_Fallback(t *testing.T) {
	os.Unsetenv("PGDBA_TEST_MISSING")
	if got := getEnv("PGDBA_TEST_MISSING", "default"); got != "default" {
		t.Errorf("expected default, got %s", got)
	}
}

func TestGetEnvInt_Valid(t *testing.T) {
	t.Setenv("PGDBA_TEST_PORT", "5433")
	if got := getEnvInt("PGDBA_TEST_PORT", 5432); got != 5433 {
		t.Errorf("expected 5433, got %d", got)
	}
}

func TestGetEnvInt_Invalid(t *testing.T) {
	t.Setenv("PGDBA_TEST_PORT", "notanumber")
	if got := getEnvInt("PGDBA_TEST_PORT", 5432); got != 5432 {
		t.Errorf("expected fallback 5432, got %d", got)
	}
}

func TestGetEnvInt_Missing(t *testing.T) {
	os.Unsetenv("PGDBA_TEST_PORT_MISSING")
	if got := getEnvInt("PGDBA_TEST_PORT_MISSING", 5432); got != 5432 {
		t.Errorf("expected fallback 5432, got %d", got)
	}
}

func writeTempPgPass(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".pgpass")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLookupPgPassFile_ExactMatch(t *testing.T) {
	path := writeTempPgPass(t, "localhost:5432:mydb:postgres:secret\n")
	if got := lookupPgPassFile(path, "localhost", 5432, "mydb", "postgres"); got != "secret" {
		t.Errorf("expected secret, got %q", got)
	}
}

func TestLookupPgPassFile_Wildcard(t *testing.T) {
	path := writeTempPgPass(t, "*:*:*:postgres:wildcardpass\n")
	if got := lookupPgPassFile(path, "anyhost", 9999, "anydb", "postgres"); got != "wildcardpass" {
		t.Errorf("expected wildcardpass, got %q", got)
	}
}

func TestLookupPgPassFile_PasswordWithColon(t *testing.T) {
	path := writeTempPgPass(t, "localhost:5432:mydb:postgres:pass:with:colons\n")
	if got := lookupPgPassFile(path, "localhost", 5432, "mydb", "postgres"); got != "pass:with:colons" {
		t.Errorf("expected pass:with:colons, got %q", got)
	}
}

func TestLookupPgPassFile_CommentIgnored(t *testing.T) {
	path := writeTempPgPass(t, "# this is a comment\nlocalhost:5432:mydb:postgres:realpass\n")
	if got := lookupPgPassFile(path, "localhost", 5432, "mydb", "postgres"); got != "realpass" {
		t.Errorf("expected realpass, got %q", got)
	}
}

func TestLookupPgPassFile_NoMatch(t *testing.T) {
	path := writeTempPgPass(t, "otherhost:5432:mydb:postgres:secret\n")
	if got := lookupPgPassFile(path, "localhost", 5432, "mydb", "postgres"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestLookupPgPassFile_NotFound(t *testing.T) {
	if got := lookupPgPassFile("/nonexistent/.pgpass", "localhost", 5432, "mydb", "postgres"); got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}
