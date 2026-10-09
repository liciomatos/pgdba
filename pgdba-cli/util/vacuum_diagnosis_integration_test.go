package util

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/liciomatos/pgdba-cli/config"
)

func findHolder(horizon XminHorizon, source, id string) *XminHolder {
	for i := range horizon.Holders {
		if horizon.Holders[i].Source == source && horizon.Holders[i].ID == id {
			return &horizon.Holders[i]
		}
	}
	return nil
}

func TestFetchXminHorizon_FindsEveryKindOfHolder(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	empty, err := FetchXminHorizon(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	if empty.FreezeMaxAge == 0 || empty.Holders == nil {
		t.Fatalf("expected settings and a non-nil holder list, got %+v", empty)
	}

	// The slot comes first: creating a logical slot waits for every transaction that
	// already has an XID, including the ones this test leaves open below. Its
	// catalog_xmin is the oldest, but catalog-only holders don't define the horizon.
	if _, err := testDB.Exec(`SELECT pg_create_logical_replication_slot('xmin_test_slot', 'pgoutput')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`SELECT pg_drop_replication_slot('xmin_test_slot')`) })

	// A session holding an XID: the oldest non-catalog holder, so it defines the horizon.
	session, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var sessionPID int
	if err := session.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&sessionPID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ExecContext(ctx, `BEGIN; SELECT txid_current();`); err != nil {
		t.Fatal(err)
	}
	defer session.ExecContext(ctx, `ROLLBACK`)

	// Burn a few XIDs so the session is measurably older than the prepared transaction.
	for i := 0; i < 3; i++ {
		if _, err := testDB.Exec(`SELECT txid_current()`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testDB.Exec(`CREATE TABLE IF NOT EXISTS xmin_prepared_t (id int)`); err != nil {
		t.Fatal(err)
	}
	prepareConn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareConn.ExecContext(ctx, `BEGIN; INSERT INTO xmin_prepared_t VALUES (1); PREPARE TRANSACTION 'xmin_test_gid'`); err != nil {
		t.Fatalf("PREPARE TRANSACTION (max_prepared_transactions must be > 0): %v", err)
	}
	prepareConn.Close()
	t.Cleanup(func() {
		_, _ = testDB.Exec(`ROLLBACK PREPARED 'xmin_test_gid'`)
		_, _ = testDB.Exec(`DROP TABLE IF EXISTS xmin_prepared_t`)
	})

	horizon, err := FetchXminHorizon(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	sessionHolder := findHolder(horizon, XminSourceSession, strconv.Itoa(sessionPID))
	if sessionHolder == nil {
		t.Fatalf("open transaction not listed: %+v", horizon.Holders)
	}
	if !sessionHolder.DefinesHorizon || horizon.HorizonHolder().ID != sessionHolder.ID {
		t.Errorf("the oldest transaction should define the horizon: %+v", horizon.Holders)
	}
	if sessionHolder.TransactionAgeSeconds == nil || sessionHolder.User != "postgres" {
		t.Errorf("session details missing: %+v", sessionHolder)
	}
	if prepared := findHolder(horizon, XminSourcePrepared, "xmin_test_gid"); prepared == nil || prepared.DefinesHorizon {
		t.Errorf("prepared transaction missing or wrongly defining the horizon: %+v", prepared)
	}
	slot := findHolder(horizon, XminSourceSlot, "xmin_test_slot")
	if slot == nil || !slot.CatalogOnly || slot.DefinesHorizon {
		t.Errorf("logical slot should hold catalog_xmin only, without defining the horizon: %+v", slot)
	}
}

func TestFetchVacuumProgress_SeesRunningVacuum(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if progress, err := FetchVacuumProgress(ctx, testDB); err != nil || progress == nil {
		t.Fatalf("idle server: want an empty list, got %v, %v", progress, err)
	}
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS vacuum_progress_t;
		CREATE TABLE vacuum_progress_t AS SELECT g AS id, md5(g::text) AS val FROM generate_series(1, 20000) g;
		DELETE FROM vacuum_progress_t WHERE id % 2 = 0;`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS vacuum_progress_t`) })

	// The most aggressive cost throttling makes the vacuum slow enough to observe.
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var vacuumPID int
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&vacuumPID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `SET vacuum_cost_delay = 100; SET vacuum_cost_limit = 1`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := conn.ExecContext(ctx, `VACUUM vacuum_progress_t`)
		done <- err
	}()
	defer func() {
		_, _ = testDB.Exec(`SELECT pg_cancel_backend($1)`, vacuumPID)
		<-done
		// The connection goes back to the pool: undo the session settings so later
		// tests don't read vacuum_cost_limit = 1 from pg_settings.
		if _, err := conn.ExecContext(ctx, `RESET vacuum_cost_delay; RESET vacuum_cost_limit`); err != nil {
			t.Errorf("resetting cost settings: %v", err)
		}
	}()

	var seen *VacuumProgress
	for deadline := time.Now().Add(10 * time.Second); seen == nil && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		progress, err := FetchVacuumProgress(ctx, testDB)
		if err != nil {
			t.Fatal(err)
		}
		for i := range progress {
			if progress[i].PID == vacuumPID {
				seen = &progress[i]
			}
		}
	}
	if seen == nil {
		t.Fatal("running VACUUM never showed up in pg_stat_progress_vacuum")
	}
	if seen.TableName != "vacuum_progress_t" || seen.IsAutovacuum || seen.IsWraparound || seen.Database != config.Config.DBName {
		t.Errorf("unexpected progress row %+v", seen)
	}
	if seen.HeapBlksTotal == 0 || seen.ScannedPct() == nil {
		t.Errorf("expected heap block totals, got %+v", seen)
	}
	if pgMajorVersion() >= 17 && seen.MaxDeadTupleBytes == nil || pgMajorVersion() < 17 && seen.MaxDeadTuples == nil {
		t.Errorf("version-specific dead-tuple capacity missing on PG %d: %+v", pgMajorVersion(), seen)
	}
}

func TestFetchAutovacuum_FlagsSlowCostOverride(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS autovacuum_throttled_t;
		CREATE TABLE autovacuum_throttled_t (id int) WITH (autovacuum_vacuum_cost_delay = 20, autovacuum_vacuum_cost_limit = 200);`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS autovacuum_throttled_t`) })

	tables, err := FetchAutovacuum(context.Background(), testDB, NoRowLimit)
	if err != nil {
		t.Fatal(err)
	}
	var throttled *AutovacuumTable
	for i := range tables {
		if tables[i].TableName == "autovacuum_throttled_t" {
			throttled = &tables[i]
		}
	}
	if throttled == nil {
		t.Fatal("table missing from the autovacuum list")
	}
	if !throttled.HasOverrides() || throttled.Overrides["autovacuum_vacuum_cost_delay"] != "20" {
		t.Errorf("overrides not reported: %+v", throttled.Overrides)
	}
	// Container defaults: autovacuum_vacuum_cost_delay 2 ms, limit -1 → vacuum_cost_limit 200.
	cost := throttled.Cost
	if cost.DelayMS != 20 || cost.Limit != 200 || !cost.SlowerThanGlobal || cost.SlowdownFactor == nil || *cost.SlowdownFactor < 9.99 {
		t.Errorf("expected a ~10x slower override, got %+v", cost)
	}
	if status := AutovacuumTableStatus(*throttled); status != StatusCritical {
		t.Errorf("expected critical, got %s", status)
	}
	if throttled.TotalSizeBytes == 0 && throttled.TotalSize == "" {
		t.Error("size not filled")
	}
}

func TestFetchServerMeta(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	meta, err := FetchServerMeta(context.Background(), testDB)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ServerVersion != config.Config.Version || meta.InRecovery || meta.UptimeSeconds < 0 {
		t.Errorf("unexpected meta %+v", meta)
	}
	if meta.PgStatStatementsVersion == "" {
		t.Error("pg_stat_statements is installed in the test database")
	}
	if pgMajorVersion() < 17 && meta.CheckpointerStatsReset != nil {
		t.Error("pg_stat_checkpointer doesn't exist before PG17")
	}
}
