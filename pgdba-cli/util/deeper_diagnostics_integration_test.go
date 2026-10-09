package util

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"time"
)

// flushStats makes the session's pending table counters visible: PG15+ backends flush
// at most once a second unless forced; PG13/14 send them to the collector after 500 ms.
func flushStats(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	if pgMajorVersion() >= 15 {
		if _, err := conn.ExecContext(ctx, `SELECT pg_stat_force_next_flush()`); err != nil {
			t.Fatal(err)
		}
		return
	}
	time.Sleep(700 * time.Millisecond)
	_, _ = conn.ExecContext(ctx, `SELECT 1`)
}

func findIndex(indexes []IndexUsage, name string) *IndexUsage {
	for i := range indexes {
		if indexes[i].IndexName == name {
			return &indexes[i]
		}
	}
	return nil
}

func TestFetchIndexUsage_FlagsRedundantIndexes(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS redundant_t;
		CREATE TABLE redundant_t (id int PRIMARY KEY, a int, b int);
		CREATE INDEX redundant_t_id_idx ON redundant_t (id);
		CREATE INDEX redundant_t_a_b_idx ON redundant_t (a, b);
		CREATE INDEX redundant_t_a_idx ON redundant_t (a);
		CREATE INDEX redundant_t_b_a_idx ON redundant_t (b, a);`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS redundant_t`) })
	ctx := context.Background()

	hidden, err := FetchIndexUsage(ctx, testDB, IndexUsageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if findIndex(hidden, "redundant_t_pkey") != nil {
		t.Error("the PK index should be hidden without include_constraints")
	}
	duplicate := findIndex(hidden, "redundant_t_id_idx")
	if duplicate == nil || !containsRedundancy(duplicate.RedundantWith, IndexRedundancy{"redundant_t_pkey", RedundancyDuplicate}) {
		t.Fatalf("the PK duplicate must be flagged even with the PK hidden: %+v", duplicate)
	}
	if IndexUsageStatus(*duplicate) != StatusWarning {
		t.Errorf("a redundant index should be a warning")
	}
	prefix := findIndex(hidden, "redundant_t_a_idx")
	if prefix == nil || !containsRedundancy(prefix.RedundantWith, IndexRedundancy{"redundant_t_a_b_idx", RedundancyPrefix}) {
		t.Errorf("(a) should be flagged as a prefix of (a, b): %+v", prefix)
	}
	sameColumns := findIndex(hidden, "redundant_t_b_a_idx")
	if sameColumns == nil || !containsRedundancy(sameColumns.RedundantWith, IndexRedundancy{"redundant_t_a_b_idx", RedundancySameColumns}) {
		t.Errorf("(b, a) should share its columns with (a, b): %+v", sameColumns)
	}

	all, err := FetchIndexUsage(ctx, testDB, IndexUsageOptions{IncludeConstraints: true})
	if err != nil {
		t.Fatal(err)
	}
	pkey := findIndex(all, "redundant_t_pkey")
	if pkey == nil || !pkey.IsConstraint || !pkey.IsPrimary || len(pkey.RedundantWith) != 0 {
		t.Errorf("the PK should be listed as a constraint index without redundancies: %+v", pkey)
	}
	limited, err := FetchIndexUsage(ctx, testDB, IndexUsageOptions{Limit: 2, IncludeConstraints: true})
	if err != nil || len(limited) != 2 {
		t.Errorf("limit 2: got %d rows, %v", len(limited), err)
	}
}

func TestFetchTableChurn_HotRatio(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS churn_t, churn_quiet_t;
		CREATE TABLE churn_t (id int PRIMARY KEY, indexed int, payload int) WITH (fillfactor = 90);
		CREATE INDEX churn_t_indexed_idx ON churn_t (indexed);
		INSERT INTO churn_t SELECT g, g, g FROM generate_series(1, 10) g;
		CREATE TABLE churn_quiet_t (id int);`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS churn_t, churn_quiet_t`) })
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Updating an unindexed column on a page with room is HOT; changing an indexed
	// column never is.
	if _, err := conn.ExecContext(ctx, `UPDATE churn_t SET payload = payload + 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE churn_t SET indexed = indexed + 100`); err != nil {
		t.Fatal(err)
	}
	flushStats(t, ctx, conn)

	var churn, quiet *TableChurn
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		tables, err := FetchTableChurn(ctx, testDB, NoRowLimit)
		if err != nil {
			t.Fatal(err)
		}
		churn, quiet = nil, nil
		for i := range tables {
			switch tables[i].TableName {
			case "churn_t":
				churn = &tables[i]
			case "churn_quiet_t":
				quiet = &tables[i]
			}
		}
		if churn != nil && churn.Updates >= 20 {
			break
		}
		_, _ = conn.ExecContext(ctx, `SELECT 1`)
	}
	if churn == nil || quiet == nil {
		t.Fatal("tables missing from the churn list")
	}
	if churn.Updates != 20 || churn.HotUpdates != 10 || churn.HotPct == nil || math.Abs(*churn.HotPct-50) > 1e-9 {
		t.Errorf("expected 20 updates, 10 HOT (50%%), got %d / %d / %v", churn.Updates, churn.HotUpdates, churn.HotPct)
	}
	if churn.Fillfactor != 90 || !churn.FillfactorSet || churn.Inserts != 10 {
		t.Errorf("unexpected fillfactor/inserts: %+v", churn)
	}
	if pgMajorVersion() >= 16 && churn.NewPageUpdates == nil || pgMajorVersion() < 16 && churn.NewPageUpdates != nil {
		t.Errorf("n_tup_newpage_upd presence wrong on PG %d", pgMajorVersion())
	}
	if quiet.HotPct != nil || quiet.Fillfactor != 100 || quiet.FillfactorSet {
		t.Errorf("a table without updates has no HOT %% and the default fillfactor: %+v", quiet)
	}

	if _, err := testDB.Exec(`CREATE EXTENSION IF NOT EXISTS pgstattuple`); err != nil {
		t.Skipf("pgstattuple unavailable: %v", err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP EXTENSION IF EXISTS pgstattuple`) })
	tables := []TableChurn{*churn}
	estimates, err := FetchTableBloat(ctx, testDB, tables, DefaultBloatMaxTableBytes)
	if err != nil {
		t.Fatal(err)
	}
	if estimate := estimates["public.churn_t"]; estimate.SkippedReason != "" || estimate.TableLenBytes == 0 {
		t.Errorf("expected a bloat estimate, got %+v", estimate)
	}
	skipped, err := FetchTableBloat(ctx, testDB, tables, 1)
	if err != nil {
		t.Fatal(err)
	}
	if skipped["public.churn_t"].SkippedReason == "" {
		t.Error("a heap above bloat_max_table_bytes must be skipped, not scanned")
	}
}

func TestFetchIOAndWALStats_VersionGates(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	var unsupported UnsupportedError

	io, err := FetchIOStats(ctx, testDB)
	if pgMajorVersion() < 16 {
		if !errors.As(err, &unsupported) || unsupported.MinVersion != 16 {
			t.Errorf("pg_stat_io on PG %d: want UnsupportedError, got %v", pgMajorVersion(), err)
		}
	} else if err != nil || len(io.Rows) == 0 {
		t.Errorf("pg_stat_io on PG %d: got %d rows, %v", pgMajorVersion(), len(io.Rows), err)
	}

	wal, err := FetchWALStats(ctx, testDB)
	if pgMajorVersion() < 14 {
		if !errors.As(err, &unsupported) || unsupported.MinVersion != 14 {
			t.Errorf("pg_stat_wal on PG13: want UnsupportedError, got %v", err)
		}
		return
	}
	if err != nil || wal.Records == 0 || wal.Bytes == 0 || wal.FPIPct() == nil || wal.FullPageWrites == "" {
		t.Errorf("pg_stat_wal: got %+v, %v", wal, err)
	}
}

func TestCacheHit_DashboardAndMemoryStatsAgree(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dashboard, err := FetchDashboard(ctx, testDB, 1000)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := FetchMemoryStats(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	same := func(left, right *float64) bool {
		return left == nil && right == nil || left != nil && right != nil && math.Abs(*left-*right) < 0.5
	}
	if !same(dashboard.CacheHitRatio, memory.CacheHit.HeapHitPct) || !same(dashboard.IndexHitRatio, memory.CacheHit.IdxHitPct) {
		t.Errorf("dashboard %v/%v vs memory stats %v/%v", dashboard.CacheHitRatio, dashboard.IndexHitRatio,
			memory.CacheHit.HeapHitPct, memory.CacheHit.IdxHitPct)
	}
}

func TestFetchCacheHitReport_Delta(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS cache_delta_t;
		CREATE TABLE cache_delta_t AS SELECT g AS id FROM generate_series(1, 5000) g;`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS cache_delta_t`) })
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Warm up and flush so the cumulative counters already include earlier reads.
	if _, err := conn.ExecContext(ctx, `SELECT count(*) FROM cache_delta_t`); err != nil {
		t.Fatal(err)
	}
	flushStats(t, ctx, conn)
	time.Sleep(1200 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(300 * time.Millisecond)
		for i := 0; i < 3; i++ {
			_, _ = conn.ExecContext(ctx, `SELECT count(*) FROM cache_delta_t`)
		}
		flushStats(t, ctx, conn)
	}()
	report, err := FetchCacheHitReport(ctx, testDB, 2500*time.Millisecond, 0)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if report.IntervalSeconds != 2.5 {
		t.Errorf("interval = %v, want 2.5", report.IntervalSeconds)
	}
	var delta *CacheHitTable
	for i := range report.Tables {
		if report.Tables[i].TableName == "cache_delta_t" {
			delta = &report.Tables[i]
		}
	}
	cumulative, err := FetchCacheHit(ctx, testDB, NoRowLimit)
	if err != nil {
		t.Fatal(err)
	}
	var total *CacheHitTable
	for i := range cumulative {
		if cumulative[i].TableName == "cache_delta_t" {
			total = &cumulative[i]
		}
	}
	if delta == nil || total == nil {
		t.Fatal("cache_delta_t missing from the reports")
	}
	if delta.HeapBlksHit+delta.HeapBlksRead == 0 || delta.HeapHitPct == nil {
		t.Errorf("the interval should include the 3 scans: %+v", delta)
	}
	if delta.HeapBlksHit+delta.HeapBlksRead >= total.HeapBlksHit+total.HeapBlksRead {
		t.Errorf("interval counters (%d) should be below the cumulative ones (%d)",
			delta.HeapBlksHit+delta.HeapBlksRead, total.HeapBlksHit+total.HeapBlksRead)
	}
}
