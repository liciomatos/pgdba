package util

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// backendPID returns the server PID behind a dedicated connection.
func backendPID(t *testing.T, ctx context.Context, conn *sql.Conn) int {
	t.Helper()
	var pid int
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

// dedicatedConn opens a connection that is closed when the test ends.
func dedicatedConn(t *testing.T, ctx context.Context) *sql.Conn {
	t.Helper()
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestFetchStatementStats_OrderingsAndDelta(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	for _, orderBy := range StatementOrderings {
		stats, err := FetchStatementStats(ctx, testDB, StatementStatsOptions{OrderBy: orderBy, Limit: 5})
		if err != nil {
			t.Fatalf("order_by %s: %v", orderBy, err)
		}
		if len(stats.Statements) == 0 || stats.BlockSize == 0 {
			t.Errorf("order_by %s returned nothing: %+v", orderBy, stats)
		}
		atLeast18 := compareExtensionVersions(stats.Info.Version, "1.8") >= 0
		if first := stats.Statements[0]; atLeast18 != (first.WALBytes != nil && first.Plans != nil) {
			t.Errorf("WAL/plan columns exist from extension 1.8 (installed %s): %+v", stats.Info.Version, first)
		}
	}
	if _, err := FetchStatementStats(ctx, testDB, StatementStatsOptions{OrderBy: "bogus"}); err == nil {
		t.Error("an unknown order_by should be an error")
	}

	conn := dedicatedConn(t, ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(300 * time.Millisecond)
		for i := 0; i < 7; i++ {
			_, _ = conn.ExecContext(ctx, `SELECT count(*) /* delta-marker */ FROM generate_series(1, 100)`)
		}
	}()
	stats, err := FetchStatementStats(ctx, testDB, StatementStatsOptions{OrderBy: "calls", Delta: 1500 * time.Millisecond})
	<-done
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range stats.Statements {
		if strings.Contains(s.Query, "delta-marker") {
			found = true
			if s.Calls != 7 {
				t.Errorf("the window saw 7 calls, got %d", s.Calls)
			}
		}
		if s.Calls <= 0 {
			t.Errorf("a delta row must have run in the window: %+v", s)
		}
	}
	if !found || stats.IntervalSeconds != 1.5 {
		t.Errorf("delta-marker statement missing from the 1.5 s window: %+v", stats)
	}
}

func TestFetchBlockedQueries_LockChain(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`DROP TABLE IF EXISTS lock_chain_t; CREATE TABLE lock_chain_t (id int PRIMARY KEY, v int);
		INSERT INTO lock_chain_t VALUES (1, 0)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS lock_chain_t`) })

	holder := dedicatedConn(t, ctx)
	holderPID := backendPID(t, ctx, holder)
	if _, err := holder.ExecContext(ctx, `BEGIN; UPDATE lock_chain_t SET v = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	// rowWaiter waits for the holder's row lock; ddl waits for both of them; reader is
	// only queued behind the pending ACCESS EXCLUSIVE request — no one holds a lock it
	// conflicts with, which a pg_locks self-join would miss.
	statements := []string{
		`UPDATE lock_chain_t SET v = 2 WHERE id = 1`,
		`BEGIN; LOCK TABLE lock_chain_t IN ACCESS EXCLUSIVE MODE; COMMIT`,
		`SELECT count(*) FROM lock_chain_t`,
	}
	pids := make([]int, len(statements))
	results := make(chan error, len(statements))
	for i, statement := range statements {
		conn := dedicatedConn(t, ctx)
		pids[i] = backendPID(t, ctx, conn)
		go func() { _, err := conn.ExecContext(ctx, statement); results <- err }()
		time.Sleep(300 * time.Millisecond)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if _, err := holder.ExecContext(ctx, `ROLLBACK`); err != nil {
			t.Error(err)
		}
		for range statements {
			if err := <-results; err != nil {
				t.Errorf("waiting statement: %v", err)
			}
		}
	}
	defer release()

	blocked, err := FetchBlockedQueries(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	byPID := map[int]BlockedQuery{}
	for _, b := range blocked {
		if b.RootBlockerPID != holderPID {
			t.Errorf("every chain starts at the holder %d: %+v", holderPID, b)
		}
		byPID[b.BlockedPID] = b
	}
	rowWaiter, ddl, reader := byPID[pids[0]], byPID[pids[1]], byPID[pids[2]]
	if rowWaiter.BlockingPID != holderPID || rowWaiter.Depth != 1 || rowWaiter.LockType != "transactionid" ||
		rowWaiter.BlockingState != "idle in transaction" {
		t.Errorf("row waiter waits on the holder's transaction: %+v", rowWaiter)
	}
	if ddl.LockMode != "AccessExclusiveLock" || ddl.Relation != "lock_chain_t" {
		t.Errorf("ddl requests ACCESS EXCLUSIVE on lock_chain_t: %+v", ddl)
	}
	if reader.BlockingPID != pids[1] || reader.Depth < 2 {
		t.Errorf("reader is queued behind the ddl, two or more waits from the holder: %+v", reader)
	}
	if (pgMajorVersion() >= 14) != (reader.WaitSeconds != nil) {
		t.Errorf("wait_seconds comes from pg_locks.waitstart (PG14+): %+v", reader)
	}
	roots := RootBlockers(blocked)
	if len(roots) != 1 || roots[0].PID != holderPID || roots[0].WaitingSessions != 3 {
		t.Errorf("one root holding three sessions: %+v", roots)
	}

	summary, err := FetchLockSummary(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	waiting := int64(0)
	for _, count := range summary.Counts {
		waiting += count.Waiting
	}
	if summary.Capacity <= 0 || summary.Total == 0 || waiting < 3 || LockTableStatus(summary) != StatusOK {
		t.Errorf("lock summary: %+v", summary)
	}
	release()
}

func TestFetchAutovacuum_OrderByAnalyze(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`DROP TABLE IF EXISTS stale_stats_t;
		CREATE TABLE stale_stats_t (id int) WITH (autovacuum_enabled = false);`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS stale_stats_t`) })
	conn := dedicatedConn(t, ctx)
	if _, err := conn.ExecContext(ctx, `INSERT INTO stale_stats_t SELECT generate_series(1, 5000)`); err != nil {
		t.Fatal(err)
	}
	flushStats(t, ctx, conn)

	var first AutovacuumTable
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		tables, err := FetchAutovacuum(ctx, testDB, 1, "analyze")
		if err != nil {
			t.Fatal(err)
		}
		if len(tables) == 1 && tables[0].TableName == "stale_stats_t" {
			first = tables[0]
			break
		}
	}
	if first.TableName != "stale_stats_t" || !first.NeverAnalyzed || first.AnalyzeRatio == nil || *first.AnalyzeRatio < 1 {
		t.Errorf("the never-analyzed table with 5,000 new rows should rank first: %+v", first)
	}
	if _, err := FetchAutovacuum(ctx, testDB, 1, "bogus"); err == nil {
		t.Error("an unknown order_by should be an error")
	}
}

func TestFetchOperationProgress_CreateIndexConcurrentlyWaits(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if operations, err := FetchOperationProgress(ctx, testDB); err != nil || len(operations) != 0 {
		t.Fatalf("idle server: %v %+v", err, operations)
	}
	if _, err := testDB.Exec(`DROP TABLE IF EXISTS progress_t; CREATE TABLE progress_t (id int)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS progress_t`) })
	writer := dedicatedConn(t, ctx)
	if _, err := writer.ExecContext(ctx, `BEGIN; INSERT INTO progress_t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	builder := dedicatedConn(t, ctx)
	builderPID := backendPID(t, ctx, builder)
	done := make(chan error, 1)
	go func() {
		_, err := builder.ExecContext(ctx, `CREATE INDEX CONCURRENTLY progress_t_id_idx ON progress_t (id)`)
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	operations, err := FetchOperationProgress(ctx, testDB)
	if _, rollbackErr := writer.ExecContext(ctx, `ROLLBACK`); rollbackErr != nil {
		t.Error(rollbackErr)
	}
	if buildErr := <-done; buildErr != nil {
		t.Error(buildErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 {
		t.Fatalf("one CREATE INDEX CONCURRENTLY in progress: %+v", operations)
	}
	operation := operations[0]
	if operation.PID != builderPID || operation.Command != "CREATE INDEX CONCURRENTLY" || operation.Relation != "progress_t" ||
		OperationProgressStatus(operation) != StatusWarning || operation.LockersWaiting == nil || *operation.LockersWaiting != 1 {
		t.Errorf("the build waits for the open writer: %+v", operation)
	}
}

func TestFetchSLRU_SubtransactionOverflow(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`DROP TABLE IF EXISTS subxact_t; CREATE TABLE subxact_t (id int)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS subxact_t`) })
	conn := dedicatedConn(t, ctx)
	connPID := backendPID(t, ctx, conn)
	var script strings.Builder
	script.WriteString("BEGIN;")
	// Each savepoint that writes gets its own subtransaction XID; past 64 the cache
	// overflows.
	for i := 1; i <= 70; i++ {
		fmt.Fprintf(&script, " SAVEPOINT s%d; INSERT INTO subxact_t VALUES (%d);", i, i)
	}
	if _, err := conn.ExecContext(ctx, script.String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `ROLLBACK`) }()

	report, err := FetchSLRU(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range report.Caches {
		names[c.Name] = true
	}
	if len(report.Caches) == 0 || !(names["Subtrans"] || names["subtransaction"]) {
		t.Errorf("pg_stat_slru should list the subtransaction cache: %+v", report.Caches)
	}
	if pgMajorVersion() < 16 {
		if report.SubtransactionBackends != nil || len(report.Warnings) != 1 {
			t.Errorf("before PG16 only a warning: %+v", report)
		}
		return
	}
	for _, b := range report.SubtransactionBackends {
		if b.PID == connPID {
			if !b.Overflowed || SubtransactionStatus(b) != StatusWarning {
				t.Errorf("70 subtransactions overflow the cache: %+v", b)
			}
			return
		}
	}
	t.Errorf("session %d missing from %+v", connPID, report.SubtransactionBackends)
}
