package util

import (
	"context"
	"slices"
	"testing"
)

func TestFetchPgConfig_KeyScopeIsSmallAndCurated(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	all, err := FetchPgConfig(ctx, testDB, PgConfigOptions{Scope: PgConfigScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	key, err := FetchPgConfig(ctx, testDB, PgConfigOptions{Scope: PgConfigScopeKey})
	if err != nil {
		t.Fatal(err)
	}
	if key.Total == 0 || key.Total >= all.Total/2 {
		t.Fatalf("key scope should be a small subset: %d of %d", key.Total, all.Total)
	}
	names := make([]string, 0, len(key.Settings))
	for _, setting := range key.Settings {
		if !slices.Contains(keyPgSettings, setting.Name) {
			t.Errorf("%s is not a key parameter", setting.Name)
		}
		names = append(names, setting.Name)
	}
	for _, required := range []string{"shared_buffers", "work_mem", "autovacuum_vacuum_cost_delay", "checkpoint_timeout", "random_page_cost", "statement_timeout"} {
		if !slices.Contains(names, required) {
			t.Errorf("key scope is missing %s", required)
		}
	}
}

func TestFetchPgConfig_ModifiedAndPaging(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	modified, err := FetchPgConfig(ctx, testDB, PgConfigOptions{Scope: PgConfigScopeModified})
	if err != nil {
		t.Fatal(err)
	}
	// The test container sets shared_preload_libraries and wal_level on the command line.
	if modified.Total == 0 {
		t.Fatal("expected modified settings (command-line overrides)")
	}
	for _, setting := range modified.Settings {
		if setting.Source == "default" || setting.Source == "override" || setting.Source == "client" {
			t.Errorf("%s has source %s but was returned as modified", setting.Name, setting.Source)
		}
	}

	page, err := FetchPgConfig(ctx, testDB, PgConfigOptions{Scope: PgConfigScopeAll, Limit: 10, Offset: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Settings) != 10 || page.Total <= 15 {
		t.Errorf("expected a 10-row page of a larger total, got %d of %d", len(page.Settings), page.Total)
	}
	past, err := FetchPgConfig(ctx, testDB, PgConfigOptions{Scope: PgConfigScopeAll, Offset: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(past.Settings) != 0 || past.Total != page.Total {
		t.Errorf("offset past the end: want 0 rows with total %d, got %d rows, total %d", page.Total, len(past.Settings), past.Total)
	}
}
