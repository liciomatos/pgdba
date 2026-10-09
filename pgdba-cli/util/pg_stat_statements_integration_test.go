package util

import (
	"context"
	"strings"
	"testing"

	"github.com/liciomatos/pgdba-cli/config"
)

// containsSubstring reports whether any of values contains substring.
func containsSubstring(values []string, substring string) bool {
	for _, value := range values {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}

// installPgStatStatementsVersion recreates pg_stat_statements at the given SQL version
// and restores the server's default version when the test ends. contrib ships the
// upgrade scripts from 1.4 on, so old versions stay installable on new servers — the
// situation of a database that went through a major upgrade without ALTER EXTENSION.
func installPgStatStatementsVersion(t *testing.T, version string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := testDB.Exec(`DROP EXTENSION IF EXISTS pg_stat_statements`); err != nil {
			t.Errorf("dropping pg_stat_statements: %v", err)
		}
		if _, err := testDB.Exec(`CREATE EXTENSION pg_stat_statements`); err != nil {
			t.Errorf("restoring pg_stat_statements: %v", err)
		}
	})
	if _, err := testDB.Exec(`DROP EXTENSION IF EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("dropping pg_stat_statements: %v", err)
	}
	if version == "" {
		return
	}
	if _, err := testDB.Exec(`CREATE EXTENSION pg_stat_statements VERSION '` + version + `'`); err != nil {
		t.Skipf("pg_stat_statements %s is not installable on this server: %v", version, err)
	}
}

func TestFetchPgStatStatements_PreExecTimeVersion(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	installPgStatStatementsVersion(t, "1.7")
	ctx := context.Background()
	if _, err := testDB.Exec(`SELECT pg_sleep(0.01)`); err != nil {
		t.Fatal(err)
	}

	info, err := FetchPgStatStatementsInfo(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.7" || info.MeanTimeColumn != "mean_time" {
		t.Fatalf("expected 1.7 with mean_time, got %+v", info)
	}
	if !info.Outdated() {
		t.Errorf("1.7 should be outdated on PG %s (default %s)", config.Config.Version, info.DefaultVersion)
	}

	slow, err := FetchSlowQueries(ctx, testDB, -1, 5)
	if err != nil {
		t.Fatalf("FetchSlowQueries on 1.7: %v", err)
	}
	if len(slow) == 0 {
		t.Error("expected pg_stat_statements rows with a negative threshold")
	}
	if _, err := FetchQueryLoad(ctx, testDB, 5); err != nil {
		t.Fatalf("FetchQueryLoad on 1.7: %v", err)
	}
	dashboard, err := FetchDashboard(ctx, testDB, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.SlowQueryCount == nil {
		t.Errorf("slow query count should be available on 1.7, reason: %s", dashboard.SlowQueryUnavailableReason)
	}
	if !containsSubstring(dashboard.Warnings, "ALTER EXTENSION pg_stat_statements UPDATE") {
		t.Errorf("expected an outdated-extension warning, got %v", dashboard.Warnings)
	}
}

func TestFetchDashboard_WithoutPgStatStatementsIsPartial(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	installPgStatStatementsVersion(t, "")
	ctx := context.Background()

	dashboard, err := FetchDashboard(ctx, testDB, 1000)
	if err != nil {
		t.Fatalf("a missing extension must not fail the dashboard: %v", err)
	}
	if dashboard.SlowQueryCount != nil || dashboard.SlowQueryUnavailableReason == "" {
		t.Errorf("expected nil slow query count with a reason, got %v / %q",
			dashboard.SlowQueryCount, dashboard.SlowQueryUnavailableReason)
	}
	if dashboard.MaxConnections == 0 || dashboard.UptimeSeconds == 0 && dashboard.DBSizePretty == "" {
		t.Errorf("the rest of the dashboard should still be filled: %+v", dashboard)
	}
	if _, err := FetchSlowQueries(ctx, testDB, 0, 5); err != ErrPgStatStatementsMissing {
		t.Errorf("FetchSlowQueries without the extension: got %v, want ErrPgStatStatementsMissing", err)
	}
}
