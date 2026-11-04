package util

import (
	"context"
	"testing"
	"time"
)

func TestFetchRecoveryConflicts_Primary(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	conflicts, err := FetchRecoveryConflicts(context.Background(), testDB)
	if err != nil {
		t.Fatal(err)
	}
	if conflicts.InRecovery || conflicts.ReplayPaused != nil || conflicts.WALReceiver != nil {
		t.Errorf("the test server is a primary: %+v", conflicts)
	}
	if ReplayStatus(conflicts) != StatusOK {
		t.Error("a primary's replay status is ok")
	}
	found := false
	for _, d := range conflicts.Databases {
		if d.Database == "testdb" {
			found = true
			if d.Total != 0 {
				t.Errorf("a primary has no recovery conflicts: %+v", d)
			}
			if (pgMajorVersion() >= 16) != (d.ActiveLogicalSlot != nil) {
				t.Errorf("confl_active_logicalslot exists only on PG16+: %+v", d)
			}
		}
	}
	if !found {
		t.Errorf("testdb missing from %+v", conflicts.Databases)
	}
}

func TestFetchDatabaseHealth(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	databases, err := FetchDatabaseHealth(context.Background(), testDB)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range databases {
		if d.Database != "testdb" {
			continue
		}
		if d.XactCommit == 0 || d.RollbackPct == nil {
			t.Errorf("testdb should have committed transactions: %+v", d)
		}
		if (pgMajorVersion() >= 14) != (d.Sessions != nil && d.SessionTimeMS != nil) {
			t.Errorf("session counters exist only on PG14+: %+v", d)
		}
		if (pgMajorVersion() >= 18) != (d.ParallelWorkersLaunched != nil) {
			t.Errorf("parallel worker counters exist only on PG18+: %+v", d)
		}
		return
	}
	t.Errorf("testdb missing from %+v", databases)
}

func TestFetchUpsertAnalysis_FindsBurnedSequence(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`
		DROP TABLE IF EXISTS upsert_t;
		CREATE TABLE upsert_t (id serial PRIMARY KEY, sku text NOT NULL UNIQUE, qty int, note text);
		CREATE INDEX upsert_t_qty_idx ON upsert_t (qty);`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS upsert_t`) })
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Ten rows, upserted five times: the first round inserts, the other four conflict
	// and update, and every attempt calls nextval() for id.
	for round := 1; round <= 5; round++ {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO upsert_t (sku, qty) SELECT 'sku-' || g, $1::int FROM generate_series(1, 10) g
			ON CONFLICT (sku) DO UPDATE SET qty = EXCLUDED.qty`, round); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO upsert_t (sku) VALUES ('sku-1') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	flushStats(t, ctx, conn)

	var table *UpsertTable
	var analysis UpsertAnalysis
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		analysis, err = FetchUpsertAnalysis(ctx, testDB, "", NoRowLimit)
		if err != nil {
			t.Fatal(err)
		}
		table = nil
		for i := range analysis.Tables {
			if analysis.Tables[i].Churn.TableName == "upsert_t" && analysis.Tables[i].Churn.Updates >= 40 {
				table = &analysis.Tables[i]
			}
		}
		if table != nil {
			break
		}
	}
	if table == nil {
		t.Fatalf("upsert_t not analyzed: %+v", analysis)
	}

	actions := map[string]bool{}
	for _, s := range analysis.Statements {
		if s.ResolvedTable == "public.upsert_t" {
			actions[s.Action] = true
		}
	}
	if !actions["update"] || !actions["nothing"] || table.Statements < 2 {
		t.Errorf("both upsert statements should resolve to public.upsert_t: %+v", analysis.Statements)
	}
	if table.IndexCount != 3 || len(table.ArbiterIndexes) != 2 {
		t.Errorf("3 indexes, of which the PK and the sku key are arbiter candidates: %+v", table)
	}
	if table.UpdateSharePct == nil || *table.UpdateSharePct < 75 {
		t.Errorf("40 of 50 written rows were updates: %v", table.UpdateSharePct)
	}
	if len(table.Sequences) != 1 || table.Sequences[0].ColumnType != "integer" || table.Sequences[0].MaxValue != 2147483647 {
		t.Fatalf("serial id owns one integer sequence: %+v", table.Sequences)
	}
	if perRow := table.Sequences[0].ValuesPerLiveRow; perRow == nil || *perRow < 5 {
		t.Errorf("51 nextval() calls for 10 rows: %v", perRow)
	}

	if _, err := FetchUpsertAnalysis(ctx, testDB, "no_such_table", NoRowLimit); err == nil {
		t.Error("an unknown table should be an error")
	}
}
