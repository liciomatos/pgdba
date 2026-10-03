package util

import (
	"context"
	"testing"
	"time"
)

func TestCheckIndexUsage_NoError(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckIndexUsage(dummyInitialModel)
	if _, ok := result.(ErrorModel); ok {
		t.Error("expected non-error model, got ErrorModel")
	}
}

func TestCheckIndexUsage_ReturnsExpectedType(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	result := CheckIndexUsage(dummyInitialModel)
	if _, ok := result.(IndexUsageModel); !ok {
		t.Errorf("expected IndexUsageModel, got %T", result)
	}
}

// TestFetchIndexUsage_CountersMatchKnownWorkload runs a fixed number of plain index
// scans and checks Scans / Tup Read / Tup Fetch against pg_stat_user_indexes'
// documented semantics: idx_tup_read = index entries returned by the index,
// idx_tup_fetch = live heap tuples fetched by simple (non-bitmap) index scans.
func TestFetchIndexUsage_CountersMatchKnownWorkload(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS idx_counter_t;
		CREATE TABLE idx_counter_t (id int PRIMARY KEY, val text);
		INSERT INTO idx_counter_t SELECT g, md5(g::text) FROM generate_series(1, 1000) g;
		ANALYZE idx_counter_t;`); err != nil {
		t.Fatal(err)
	}
	defer testDB.Exec(`DROP TABLE IF EXISTS idx_counter_t`)

	// One dedicated session so the planner settings and the stats flush apply to
	// the same backend that ran the scans.
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, stmt := range []string{
		"SET enable_seqscan = off", "SET enable_bitmapscan = off", "SET enable_indexonlyscan = off",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	const scans = 3
	const rowsPerScan = 10
	for i := 0; i < scans; i++ {
		var count int
		if err := conn.QueryRowContext(ctx,
			// Literal mid-range bounds: a bound in the first/last histogram bucket makes
			// the planner probe the index for the real min/max (get_actual_variable_range),
			// which PostgreSQL also counts as an index scan + tuple read.
			`SELECT count(val) FROM idx_counter_t WHERE id BETWEEN 500 AND 509`,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
	// PG15+ backends buffer stats and flush them at most once per second (or on
	// demand via pg_stat_force_next_flush); PG13/14 send them to the stats collector
	// asynchronously. Either way, poll instead of asserting immediately.
	if pgMajorVersion() >= 15 {
		_, _ = conn.ExecContext(ctx, "SELECT pg_stat_force_next_flush()")
	}

	var found *IndexUsage
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		indexes, err := FetchIndexUsage(ctx, testDB, NoRowLimit)
		if err != nil {
			t.Fatal(err)
		}
		found = nil
		for i := range indexes {
			if indexes[i].IndexName == "idx_counter_t_pkey" {
				found = &indexes[i]
			}
		}
		if found != nil && found.IdxScan >= scans {
			break
		}
		time.Sleep(200 * time.Millisecond)
		// PG13/14 only send pending counters at the end of a later transaction once
		// 500ms have passed, so keep the scanning session busy with no-op queries.
		_, _ = conn.ExecContext(ctx, "SELECT 1")
	}
	if found == nil {
		t.Fatal("idx_counter_t_pkey not listed by FetchIndexUsage")
	}
	if found.IdxScan != scans || found.IdxTupRead != scans*rowsPerScan || found.IdxTupFetch != scans*rowsPerScan {
		t.Fatalf("got scans=%d tup_read=%d tup_fetch=%d, want %d/%d/%d",
			found.IdxScan, found.IdxTupRead, found.IdxTupFetch, scans, scans*rowsPerScan, scans*rowsPerScan)
	}
	if found.IndexColumns != "id" || !found.IsValid {
		t.Fatalf("unexpected columns=%q valid=%v", found.IndexColumns, found.IsValid)
	}
}
