package util

import (
	"context"
	"testing"
	"time"
)

func TestFetchAutovacuumThresholds_TableOverridesAndGlobals(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS av_threshold_t;
		CREATE TABLE av_threshold_t (id int PRIMARY KEY, val text)
			WITH (autovacuum_enabled = false,
			      autovacuum_vacuum_scale_factor = 0.05,
			      autovacuum_freeze_max_age = 100000000);
		INSERT INTO av_threshold_t SELECT g, md5(g::text) FROM generate_series(1, 2000) g;
		ANALYZE av_threshold_t;`); err != nil {
		t.Fatal(err)
	}
	defer testDB.Exec(`DROP TABLE IF EXISTS av_threshold_t`)

	thresholds, err := FetchAutovacuumThresholds(ctx, testDB, "public", "av_threshold_t")
	if err != nil {
		t.Fatal(err)
	}
	if thresholds.AutovacuumEnabled || thresholds.AutovacuumEnabledSource != SourceTable {
		t.Errorf("autovacuum_enabled=false reloption not applied: %+v", thresholds)
	}
	if thresholds.Reltuples != 2000 {
		t.Errorf("reltuples = %v, want 2000 after ANALYZE", thresholds.Reltuples)
	}
	dead := thresholds.DeadTuples
	if dead.ScaleFactor.Source != SourceTable || dead.ScaleFactor.Value != 0.05 ||
		dead.Base.Source != SourceGlobal || dead.Base.Value != 50 {
		t.Errorf("unexpected dead-tuple inputs: %+v", dead)
	}
	if dead.Threshold != 50+0.05*2000 {
		t.Errorf("dead-tuple threshold = %v, want 150", dead.Threshold)
	}
	if thresholds.XIDWraparound.Limit.Value != 100_000_000 || thresholds.XIDWraparound.Limit.Source != SourceTable {
		t.Errorf("table autovacuum_freeze_max_age not applied: %+v", thresholds.XIDWraparound.Limit)
	}
	if thresholds.XIDAggressive.Limit.Source != SourceGlobal || thresholds.XIDAggressive.Limit.Value != 150_000_000 {
		t.Errorf("freeze_table_age should come from global vacuum_freeze_table_age: %+v", thresholds.XIDAggressive.Limit)
	}
	if (pgMajorVersion() >= 14) != (thresholds.XIDFailsafe != nil) {
		t.Errorf("failsafe presence wrong for PG%d", pgMajorVersion())
	}
	if (pgMajorVersion() >= 18) != (dead.MaxThreshold != nil) {
		t.Errorf("max threshold presence wrong for PG%d", pgMajorVersion())
	}

	params, err := FetchAutovacuumParams(ctx, testDB, "public", "av_threshold_t")
	if err != nil {
		t.Fatal(err)
	}
	for _, param := range params {
		if param.GlobalValue == "" {
			t.Errorf("parameter %s has no global value — reloption/GUC name mapping is wrong", param.Name)
		}
	}
}

// TestAutovacuumThresholds_PredictsRealAutovacuum checks the computed "Due" against
// what the autovacuum launcher actually does on the running server.
func TestAutovacuumThresholds_PredictsRealAutovacuum(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`ALTER SYSTEM SET autovacuum_naptime = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`SELECT pg_reload_conf()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		testDB.Exec(`ALTER SYSTEM RESET autovacuum_naptime`)
		testDB.Exec(`SELECT pg_reload_conf()`)
	}()

	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS av_due_t;
		CREATE TABLE av_due_t (id int PRIMARY KEY, val text)
			WITH (autovacuum_vacuum_threshold = 10, autovacuum_vacuum_scale_factor = 0,
			      autovacuum_analyze_threshold = 1000000, autovacuum_vacuum_insert_threshold = -1);
		INSERT INTO av_due_t SELECT g, md5(g::text) FROM generate_series(1, 100) g;
		DELETE FROM av_due_t WHERE id <= 30;`); err != nil {
		t.Fatal(err)
	}
	defer testDB.Exec(`DROP TABLE IF EXISTS av_due_t`)

	deadline := time.Now().Add(30 * time.Second)
	var predicted bool
	for time.Now().Before(deadline) && !predicted {
		thresholds, err := FetchAutovacuumThresholds(ctx, testDB, "public", "av_due_t")
		if err != nil {
			t.Fatal(err)
		}
		predicted = thresholds.DeadTuples.Due && thresholds.DeadTuples.Threshold == 10
		time.Sleep(200 * time.Millisecond)
	}
	if !predicted {
		t.Fatal("30 dead tuples > threshold 10 (table reloptions) was never reported as due")
	}
	for time.Now().Before(deadline) {
		var autovacuumCount int64
		if err := testDB.QueryRow(
			`SELECT autovacuum_count FROM pg_stat_user_tables WHERE relname = 'av_due_t'`,
		).Scan(&autovacuumCount); err != nil {
			t.Fatal(err)
		}
		if autovacuumCount > 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("thresholds reported the table as due, but autovacuum never processed it")
}
