package util

import (
	"context"
	"strings"
	"testing"
	"time"
)

func execOrFatal(t *testing.T, statements string) {
	t.Helper()
	if _, err := testDB.Exec(statements); err != nil {
		t.Fatal(err)
	}
}

func TestFetchSequences_ColumnTypeCapsTheRange(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	execOrFatal(t, `
		DROP TABLE IF EXISTS seq_owned_t, seq_default_t; DROP SEQUENCE IF EXISTS seq_wide_s, seq_unused_s;
		CREATE TABLE seq_owned_t (id serial PRIMARY KEY);
		SELECT setval('seq_owned_t_id_seq', 2000000000);
		CREATE SEQUENCE seq_wide_s AS bigint;
		CREATE TABLE seq_default_t (n integer DEFAULT nextval('seq_wide_s'));
		SELECT setval('seq_wide_s', 1700000000);
		CREATE SEQUENCE seq_unused_s;`)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DROP TABLE IF EXISTS seq_owned_t, seq_default_t; DROP SEQUENCE IF EXISTS seq_wide_s, seq_unused_s`)
	})
	sequences, err := FetchSequences(context.Background(), testDB, NoRowLimit)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]OwnedSequence{}
	for _, sequence := range sequences {
		byName[sequence.Name] = sequence
	}
	owned, wide, unused := byName["public.seq_owned_t_id_seq"], byName["public.seq_wide_s"], byName["public.seq_unused_s"]
	if owned.Usage != "owned" || owned.Column != "id" || SequenceStatus(owned) != StatusCritical {
		t.Errorf("serial at 2e9 of 2^31 is critical: %+v", owned)
	}
	if wide.Usage != "default" || wide.DataType != "bigint" || wide.MaxValue != 2147483647 || SequenceStatus(wide) != StatusWarning {
		t.Errorf("a bigint sequence feeding an integer column is capped at 2^31 (79%% used): %+v", wide)
	}
	if unused.Usage != "" || unused.LastValue != nil || unused.PctUsed != nil {
		t.Errorf("an unused, never-called sequence: %+v", unused)
	}
	if sequences[0].Name != owned.Name {
		t.Errorf("most used first, got %s", sequences[0].Name)
	}
}

func TestFetchMissingFKIndexes(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	execOrFatal(t, `
		DROP TABLE IF EXISTS fk_child_t, fk_indexed_t, fk_parent_t;
		CREATE TABLE fk_parent_t (a int, b int, PRIMARY KEY (a, b));
		CREATE TABLE fk_child_t (id int, a int, b int, FOREIGN KEY (a, b) REFERENCES fk_parent_t);
		CREATE TABLE fk_indexed_t (id int, a int, b int, FOREIGN KEY (a, b) REFERENCES fk_parent_t);
		CREATE INDEX ON fk_indexed_t (b, a, id);`)
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS fk_child_t, fk_indexed_t, fk_parent_t`) })
	missing, err := FetchMissingFKIndexes(context.Background(), testDB, NoRowLimit)
	if err != nil {
		t.Fatal(err)
	}
	var child *MissingFKIndex
	for i := range missing {
		switch missing[i].Table {
		case "public.fk_child_t":
			child = &missing[i]
		case "public.fk_indexed_t":
			t.Errorf("an index leading with the FK columns in another order covers it: %+v", missing[i])
		}
	}
	if child == nil || strings.Join(child.Columns, ",") != "a,b" || child.ReferencedTable != "public.fk_parent_t" ||
		child.Suggestion() != "CREATE INDEX CONCURRENTLY ON public.fk_child_t (a, b);" {
		t.Errorf("fk_child_t's foreign key has no index: %+v", child)
	}
}

func TestFetchSeqScanTables(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	execOrFatal(t, `DROP TABLE IF EXISTS seqscan_t; CREATE TABLE seqscan_t AS SELECT generate_series(1, 1000) AS id;`)
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS seqscan_t`) })
	conn := dedicatedConn(t, ctx)
	for i := 0; i < 3; i++ {
		if _, err := conn.ExecContext(ctx, `SELECT count(*) FROM seqscan_t`); err != nil {
			t.Fatal(err)
		}
	}
	flushStats(t, ctx, conn)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		tables, err := FetchSeqScanTables(ctx, testDB, NoRowLimit)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range tables {
			if table.TableName == "seqscan_t" && table.SeqScan >= 3 {
				if table.AvgRowsPerScan < 1000 || *table.SeqScanPct != 100 || (pgMajorVersion() >= 16) != (table.LastSeqScan != nil) {
					t.Errorf("three full scans of 1,000 rows: %+v", table)
				}
				return
			}
		}
	}
	t.Error("seqscan_t never showed up")
}

func TestFetchColumnStats_Hints(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	execOrFatal(t, `
		DROP TABLE IF EXISTS colstats_t;
		CREATE TABLE colstats_t AS
			SELECT g AS id, (ARRAY['new', 'paid', 'shipped'])[1 + g % 3] AS status,
			       CASE WHEN g % 10 = 0 THEN g END AS rare, g % 7 AS a, g % 7 AS b
			FROM generate_series(1, 120000) g;
		CREATE INDEX ON colstats_t (status);
		CREATE INDEX ON colstats_t (rare);
		CREATE STATISTICS colstats_ab (dependencies) ON a, b FROM colstats_t;
		ANALYZE colstats_t;`)
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS colstats_t`) })
	// Before PG15 the stats collector reports last_analyze asynchronously.
	var stats TableColumnStats
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		var err error
		if stats, err = FetchColumnStats(context.Background(), testDB, "colstats_t"); err != nil {
			t.Fatal(err)
		}
		if stats.LastAnalyze != nil {
			break
		}
	}
	if stats.Table != "public.colstats_t" || stats.LastAnalyze == nil || len(stats.Columns) != 5 {
		t.Fatalf("table header: %+v", stats)
	}
	hintsOf := map[string]string{}
	for _, column := range stats.Columns {
		hintsOf[column.Name] = strings.Join(ColumnStatsHints(stats, column), " | ")
		if column.Name == "status" && (len(column.MostCommonValues) != 3 || !column.LeadsIndex) {
			t.Errorf("status has 3 common values and leads an index: %+v", column)
		}
	}
	if !strings.Contains(hintsOf["status"], "partial index") || !strings.Contains(hintsOf["id"], "BRIN") ||
		!strings.Contains(hintsOf["rare"], "IS NOT NULL") {
		t.Errorf("expected low-cardinality, BRIN and mostly-NULL hints: %v", hintsOf)
	}
	if len(stats.Extended) != 1 || stats.Extended[0].Kinds[0] != "dependencies" || strings.Join(stats.Extended[0].Columns, ",") != "a,b" {
		t.Errorf("extended statistics: %+v", stats.Extended)
	}
	if _, err := FetchColumnStats(context.Background(), testDB, "no_such_table"); err == nil {
		t.Error("an unknown table should be an error")
	}
}

func TestFetchBloatEstimates_StatisticsThenPgstattuple(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	// Deleting 9 rows of every 10 spreads the free space over every page, so VACUUM
	// can't truncate the table: it keeps its size with a tenth of the rows.
	execOrFatal(t, `
		DROP EXTENSION IF EXISTS pgstattuple;
		DROP TABLE IF EXISTS bloat_t, bloat_unanalyzed_t;
		CREATE TABLE bloat_t AS SELECT g AS id, md5(g::text) AS payload FROM generate_series(1, 100000) g;
		CREATE INDEX bloat_t_payload_idx ON bloat_t (payload);
		DELETE FROM bloat_t WHERE id % 10 <> 0;
		CREATE TABLE bloat_unanalyzed_t AS SELECT g AS id FROM generate_series(1, 50000) g;`)
	// VACUUM can't run in the implicit transaction of a multi-statement Exec.
	execOrFatal(t, `VACUUM ANALYZE bloat_t`)
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS bloat_t, bloat_unanalyzed_t`) })
	findRelations := func(report BloatReport) map[string]BloatEstimate {
		found := map[string]BloatEstimate{}
		for _, estimate := range report.Relations {
			switch estimate.TableName {
			case "bloat_t":
				found[estimate.Kind] = estimate
			case "bloat_unanalyzed_t":
				found["unanalyzed"] = estimate
			}
		}
		return found
	}

	report, err := FetchBloatEstimates(ctx, testDB, 0, DefaultBloatMaxTableBytes, NoRowLimit)
	if err != nil {
		t.Fatal(err)
	}
	found := findRelations(report)
	if report.PgstattupleInstalled {
		t.Fatal("pgstattuple was dropped")
	}
	if table := found["table"]; table.Method != "statistics" || table.NotApplicable || table.BloatPct < 70 {
		t.Errorf("statistics estimate: the table keeps its pages for a tenth of the rows: %+v", table)
	}
	if index := found["index"]; index.Method != "statistics" || index.IndexName != "bloat_t_payload_idx" || index.BloatPct < 50 {
		t.Errorf("statistics estimate: the index keeps its pages too: %+v", index)
	}
	// On PG14+ a table that was never vacuumed or analyzed has reltuples -1; on PG13 it is 0.
	if unanalyzed, ok := found["unanalyzed"]; ok && pgMajorVersion() >= 14 && (!unanalyzed.NotApplicable || unanalyzed.BloatBytes != 0) {
		t.Errorf("a never-analyzed table has no statistics estimate: %+v", unanalyzed)
	}

	if _, err := testDB.Exec(`CREATE EXTENSION pgstattuple`); err != nil {
		t.Skipf("pgstattuple unavailable: %v", err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP EXTENSION IF EXISTS pgstattuple`) })
	if report, err = FetchBloatEstimates(ctx, testDB, 0, DefaultBloatMaxTableBytes, NoRowLimit); err != nil {
		t.Fatal(err)
	}
	found = findRelations(report)
	if !report.PgstattupleInstalled || len(report.Warnings) != 0 {
		t.Fatalf("pgstattuple installed, every relation measured: %+v", report.Warnings)
	}
	if table := found["table"]; table.Method != "pgstattuple_approx" || table.FreeBytes == nil || table.BloatPct < 70 {
		t.Errorf("measured table bloat: %+v", table)
	}
	if index := found["index"]; index.Method != "pgstatindex" || index.LeafDensityPct == nil || index.BloatPct < 50 {
		t.Errorf("measured index bloat: %+v", index)
	}
	if unanalyzed, ok := found["unanalyzed"]; ok && (unanalyzed.Method != "pgstattuple_approx" || unanalyzed.NotApplicable) {
		t.Errorf("pgstattuple measures a table without statistics too: %+v", unanalyzed)
	}

	// Relations above the size cap keep the statistics estimate.
	if report, err = FetchBloatEstimates(ctx, testDB, 0, 1, NoRowLimit); err != nil {
		t.Fatal(err)
	}
	if table := findRelations(report)["table"]; table.Method != "statistics" || len(report.Warnings) != 1 {
		t.Errorf("above max_measure_bytes the estimate is kept, with a warning: %+v %v", table, report.Warnings)
	}
}

func TestFetchSchemaHygiene(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	execOrFatal(t, `
		DROP TABLE IF EXISTS hygiene_child_t, hygiene_parent_t, hygiene_unlogged_t, hygiene_part_t;
		CREATE TABLE hygiene_parent_t (id int PRIMARY KEY);
		CREATE TABLE hygiene_child_t (parent_id int REFERENCES hygiene_parent_t, n int) WITH (fillfactor = 80);
		ALTER TABLE hygiene_child_t DISABLE TRIGGER ALL;
		ALTER TABLE hygiene_child_t ADD CONSTRAINT hygiene_n_positive CHECK (n > 0) NOT VALID;
		CREATE UNLOGGED TABLE hygiene_unlogged_t (id int);
		CREATE TABLE hygiene_part_t (d date) PARTITION BY RANGE (d);
		CREATE TABLE hygiene_part_2026 PARTITION OF hygiene_part_t FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
		CREATE TABLE hygiene_part_default PARTITION OF hygiene_part_t DEFAULT;`)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DROP TABLE IF EXISTS hygiene_child_t, hygiene_parent_t, hygiene_unlogged_t, hygiene_part_t`)
	})
	hygiene, err := FetchSchemaHygiene(context.Background(), testDB)
	if err != nil {
		t.Fatal(err)
	}
	internalDisabled := false
	for _, trigger := range hygiene.DisabledTriggers {
		if trigger.Table == "public.hygiene_child_t" && trigger.Internal && DisabledTriggerStatus(trigger) == StatusCritical {
			internalDisabled = true
		}
	}
	if !internalDisabled {
		t.Errorf("DISABLE TRIGGER ALL turns off the FK trigger: %+v", hygiene.DisabledTriggers)
	}
	checks := map[string]bool{}
	for _, constraint := range hygiene.UnvalidatedConstraints {
		checks[constraint.Constraint] = true
	}
	for _, table := range hygiene.UnloggedTables {
		checks[table.Table] = true
	}
	for _, table := range hygiene.PartitionedTables {
		if table.Table == "public.hygiene_part_t" && table.Partitions == 2 && table.DefaultPartition == "hygiene_part_default" &&
			strings.HasPrefix(table.Strategy, "RANGE") {
			checks["partitioned"] = true
		}
	}
	for _, parameters := range hygiene.StorageParameters {
		if parameters.Relation == "public.hygiene_child_t" && strings.Join(parameters.Options, ",") == "fillfactor=80" {
			checks["fillfactor"] = true
		}
	}
	for _, expected := range []string{"hygiene_n_positive", "public.hygiene_unlogged_t", "partitioned", "fillfactor"} {
		if !checks[expected] {
			t.Errorf("missing %s in %+v", expected, hygiene)
		}
	}
}

func TestFetchFunctionStats(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	execOrFatal(t, `CREATE OR REPLACE FUNCTION fn_stats_probe(n int) RETURNS int LANGUAGE plpgsql AS $$ BEGIN RETURN n + 1; END $$;`)
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP FUNCTION IF EXISTS fn_stats_probe(int)`) })
	conn := dedicatedConn(t, ctx)
	if _, err := conn.ExecContext(ctx, `SET track_functions = 'pl'; SELECT fn_stats_probe(g) FROM generate_series(1, 5) g`); err != nil {
		t.Fatal(err)
	}
	flushStats(t, ctx, conn)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		report, err := FetchFunctionStats(ctx, testDB, NoRowLimit)
		if err != nil {
			t.Fatal(err)
		}
		if report.TrackFunctions != "none" {
			t.Fatalf("the server default is none, got %s", report.TrackFunctions)
		}
		for _, function := range report.Functions {
			if function.Function == "fn_stats_probe(integer)" && function.Calls == 5 {
				return
			}
		}
	}
	t.Error("fn_stats_probe(integer) with 5 calls never showed up")
}

func TestFetchSecurity(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	report, err := FetchSecurity(context.Background(), testDB)
	if err != nil {
		t.Fatal(err)
	}
	if report.SSL != "off" || len(report.Connections) == 0 || report.HBARules == nil || len(report.Warnings) != 0 {
		t.Fatalf("container without SSL, read as superuser: %+v", report)
	}
	for _, connection := range report.Connections {
		if connection.SSL || (connection.ClientAddr != "local" && ConnectionEncryptionStatus(connection) != StatusWarning) {
			t.Errorf("network connection without SSL is a warning: %+v", connection)
		}
	}
	localTrust := false
	for _, rule := range report.HBARules {
		if rule.Type == "local" && rule.AuthMethod == "trust" && HBARuleStatus(rule) == StatusWarning {
			localTrust = true
		}
	}
	if !localTrust {
		t.Errorf("the postgres image trusts the local socket: %+v", report.HBARules)
	}
}
