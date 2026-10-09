package main

import (
	"strings"
	"testing"
)

func TestReplicaFlags_Set(t *testing.T) {
	var specs []replicaSpec
	flags := replicaFlags{specs: &specs}
	if err := flags.Set("replica1=postgres://user:pass@standby:5432/app"); err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Name != "replica1" || specs[0].URL != "postgres://user:pass@standby:5432/app" {
		t.Fatalf("unexpected specs %+v", specs)
	}
	for value, wantError := range map[string]string{
		"no-equals-sign":           "expected name=",
		"=postgres://x/db":         "expected name=",
		"bad name=postgres://x/db": "may only contain",
		"primary=postgres://x/db":  "reserved",
		"replica1=postgres://y/db": "given twice",
	} {
		err := flags.Set(value)
		if err == nil || !strings.Contains(err.Error(), wantError) {
			t.Errorf("Set(%q) = %v, want an error containing %q", value, err, wantError)
		}
	}
}

func TestMajorVersion(t *testing.T) {
	for version, want := range map[string]int{"16.4": 16, "17beta1": 17, "13.22 (Debian)": 13, "18": 18} {
		if got := majorVersion(version); got != want {
			t.Errorf("majorVersion(%q) = %d, want %d", version, got, want)
		}
	}
}
