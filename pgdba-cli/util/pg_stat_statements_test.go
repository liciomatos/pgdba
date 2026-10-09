package util

import "testing"

func TestCompareExtensionVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.7", "1.10", -1}, // numeric, not lexical: "1.7" > "1.10" as strings
		{"1.10", "1.7", 1},
		{"1.8", "1.8", 0},
		{"1.8", "1.8.0", 0},
		{"1.11", "1.12", -1},
	}
	for _, tc := range cases {
		if got := compareExtensionVersions(tc.left, tc.right); got != tc.want {
			t.Errorf("compareExtensionVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestPgStatStatementsColumns_RenamedIn18(t *testing.T) {
	for version, wantTotal := range map[string]string{
		"1.4": "total_time", "1.7": "total_time",
		"1.8": "total_exec_time", "1.10": "total_exec_time", "1.12": "total_exec_time",
	} {
		total, mean, stddev := pgStatStatementsColumns(version)
		if total != wantTotal {
			t.Errorf("version %s: total column = %q, want %q", version, total, wantTotal)
		}
		if wantTotal == "total_time" && (mean != "mean_time" || stddev != "stddev_time") {
			t.Errorf("version %s: got %q/%q, want mean_time/stddev_time", version, mean, stddev)
		}
		if wantTotal == "total_exec_time" && (mean != "mean_exec_time" || stddev != "stddev_exec_time") {
			t.Errorf("version %s: got %q/%q, want mean_exec_time/stddev_exec_time", version, mean, stddev)
		}
	}
}

func TestPgStatStatementsInfo_Outdated(t *testing.T) {
	outdated := PgStatStatementsInfo{Installed: true, Version: "1.7", DefaultVersion: "1.10"}
	if !outdated.Outdated() || outdated.UpdateHint() == "" {
		t.Error("1.7 on a server shipping 1.10 should be outdated with an ALTER EXTENSION hint")
	}
	current := PgStatStatementsInfo{Installed: true, Version: "1.10", DefaultVersion: "1.10"}
	if current.Outdated() || current.UpdateHint() != "" {
		t.Error("an up-to-date extension must not be flagged")
	}
	unknown := PgStatStatementsInfo{Installed: true, Version: "1.7"}
	if unknown.Outdated() {
		t.Error("without a known default_version the extension can't be called outdated")
	}
}
