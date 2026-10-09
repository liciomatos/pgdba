package util

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

func TestFetchWaitEvents_SamplesLockWaitsAndSkipsIdle(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(`DROP TABLE IF EXISTS wait_lock_t; CREATE TABLE wait_lock_t (id int PRIMARY KEY); INSERT INTO wait_lock_t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`DROP TABLE IF EXISTS wait_lock_t`) })

	// Run the waiter's statement once to completion first: pg_stat_statements records a
	// statement when it finishes, and its normalized text is what the breakdown shows.
	if _, err := testDB.Exec(`UPDATE wait_lock_t SET id = 1 WHERE id = 1 /* blocked-waiter */`); err != nil {
		t.Fatal(err)
	}
	holder, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, `BEGIN; SELECT id FROM wait_lock_t WHERE id = 1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	waiter, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close()
	waiting := make(chan error, 1)
	go func() {
		_, err := waiter.ExecContext(ctx, `UPDATE wait_lock_t SET id = 1 WHERE id = 1 /* blocked-waiter */`)
		waiting <- err
	}()
	defer func() {
		_, _ = holder.ExecContext(ctx, `ROLLBACK`)
		<-waiting
	}()
	time.Sleep(300 * time.Millisecond)

	sampling, err := FetchWaitEvents(ctx, testDB, WaitEventOptions{Samples: 3, Interval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var lockAAS, clientReadAAS, sum float64
	for _, event := range sampling.Events {
		sum += event.AAS
		switch {
		case event.EventType == "Lock":
			lockAAS += event.AAS
		case event.EventType == "Client" && event.Event == "ClientRead":
			clientReadAAS += event.AAS
		}
	}
	// The lock holder is "idle in transaction" (Client:ClientRead) and counts — it holds
	// the lock. The pool's plain idle connections must not.
	if math.Abs(clientReadAAS-1) > 1e-9 {
		t.Errorf("only the idle-in-transaction holder should be in ClientRead, got %v AAS", clientReadAAS)
	}
	if math.Abs(lockAAS-1) > 1e-9 {
		t.Errorf("one session blocked during every sample should give 1 lock AAS, got %v (%+v)", lockAAS, sampling.Events)
	}
	if sampling.Samples != 3 || math.Abs(sum-sampling.TotalAAS) > 1e-9 {
		t.Errorf("total AAS %v must equal the sum of events %v", sampling.TotalAAS, sum)
	}
	if WaitEventStatus("Lock", lockAAS) != StatusCritical {
		t.Error("a permanently blocked session should be critical")
	}

	// The blocked statement must be the query behind the Lock event, and a top query
	// with all of its time on Lock. (The holder ran a different statement, SELECT … FOR
	// UPDATE, so it is a separate query, idle in transaction on Client:ClientRead.)
	var lockQueries []WaitEventQuery
	for _, event := range sampling.Events {
		if event.EventType == "Lock" {
			lockQueries = event.Queries
		}
	}
	if len(lockQueries) != 1 || !strings.Contains(lockQueries[0].Query, "UPDATE wait_lock_t") ||
		math.Abs(lockQueries[0].AAS-1) > 1e-9 || math.Abs(lockQueries[0].Pct-100) > 1e-9 {
		t.Fatalf("expected the blocked UPDATE behind the Lock event with 1 AAS / 100%%, got %+v", lockQueries)
	}
	blocked := lockQueries[0]
	// PG14+ identifies statements by query_id (computed because pg_stat_statements is
	// loaded) and shows pg_stat_statements' normalized text, without literals.
	if pgMajorVersion() >= 14 {
		if blocked.QueryID == nil || !blocked.Normalized || !strings.Contains(blocked.Query, "$1") {
			t.Errorf("expected a query_id and normalized text on PG %d, got %+v", pgMajorVersion(), blocked)
		}
	} else if blocked.QueryID != nil || !strings.Contains(blocked.Query, "blocked-waiter") {
		t.Errorf("PG13 has no query_id: expected the raw pg_stat_activity text, got %+v", blocked)
	}
	var topBlocked *WaitEventQuery
	for i := range sampling.TopQueries {
		if strings.Contains(sampling.TopQueries[i].Query, "UPDATE wait_lock_t") {
			topBlocked = &sampling.TopQueries[i]
		}
	}
	if topBlocked == nil || len(topBlocked.Events) != 1 || topBlocked.Events[0].EventType != "Lock" || math.Abs(topBlocked.AAS-1) > 1e-9 {
		t.Errorf("the blocked UPDATE should be a top query with all its time on Lock: %+v", topBlocked)
	}

	// The lock holder sits idle in its transaction; with include_idle the pool's idle
	// connections (Client:ClientRead) show up too.
	withIdle, err := FetchWaitEvents(ctx, testDB, WaitEventOptions{Samples: 1, IncludeIdle: true})
	if err != nil {
		t.Fatal(err)
	}
	if withIdle.TotalAAS <= sampling.TotalAAS {
		t.Errorf("include_idle should count more sessions: %v vs %v", withIdle.TotalAAS, sampling.TotalAAS)
	}
}

func TestFetchConnections_SourcesAndIdleTransactions(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var pid int
	if err := conn.QueryRowContext(ctx, `SET application_name = 'idle-tx-test'; SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN; SELECT 1 /* idle-tx-test */`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `ROLLBACK; RESET application_name`)
	// Well over the 1 s asserted below: VM clock adjustments can shift ages by ~1 s.
	time.Sleep(2200 * time.Millisecond)

	result, err := FetchConnections(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) > 0 {
		t.Fatalf("unexpected warnings %v", result.Warnings)
	}
	var idle *IdleTransaction
	for i := range result.IdleInTransaction {
		if result.IdleInTransaction[i].PID == pid {
			idle = &result.IdleInTransaction[i]
		}
	}
	if idle == nil || idle.StateAgeSeconds < 1 || idle.Application != "idle-tx-test" || !strings.Contains(idle.Query, "idle-tx-test") {
		t.Fatalf("idle-in-transaction session not reported correctly: %+v", result.IdleInTransaction)
	}
	if result.OldestTransactionAgeSeconds == nil || *result.OldestTransactionAgeSeconds < 1 {
		t.Errorf("oldest transaction age should cover the open transaction: %v", result.OldestTransactionAgeSeconds)
	}
	foundUser := false
	for _, group := range result.TopUsers {
		foundUser = foundUser || group.Name == "postgres" && group.Count > 0
	}
	foundApplication := false
	for _, group := range result.TopApplications {
		foundApplication = foundApplication || group.Name == "idle-tx-test"
	}
	if !foundUser || !foundApplication {
		t.Errorf("source breakdowns missing: users %+v, applications %+v", result.TopUsers, result.TopApplications)
	}
}

func TestFetchTempFiles_AverageAndTopQueries(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	conn, err := testDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A sort far larger than the minimum work_mem must spill to a temp file.
	if _, err := conn.ExecContext(ctx, `SET work_mem = '64kB'`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `RESET work_mem`)
	if _, err := conn.ExecContext(ctx, `SELECT g FROM generate_series(1, 200000) g ORDER BY md5(g::text) /* temp-spill-test */`); err != nil {
		t.Fatal(err)
	}
	flushStats(t, ctx, conn)

	var current *TempFileUsage
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		usage, err := FetchTempFileUsage(ctx, testDB)
		if err != nil {
			t.Fatal(err)
		}
		for i := range usage {
			if usage[i].Database == "testdb" && usage[i].TempFiles > 0 {
				current = &usage[i]
			}
		}
		if current != nil {
			break
		}
		_, _ = conn.ExecContext(ctx, `SELECT 1`)
	}
	if current == nil || current.AvgBytesPerFile == nil || *current.AvgBytesPerFile <= 0 {
		t.Fatalf("expected temp files with an average size for testdb, got %+v", current)
	}
	if TempFileStatus(*current) != StatusWarning {
		t.Error("temp files should be a warning")
	}

	queries, err := FetchTopTempQueries(ctx, testDB, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, query := range queries {
		if strings.Contains(query.Query, "temp-spill-test") {
			found = query.TempBlksWritten > 0 && query.TempBytesWritten == query.TempBlksWritten*8192
		}
	}
	if !found {
		t.Errorf("the spilling sort should top the temp writers: %+v", queries)
	}
}

func TestFetchReplicationSlots_RawBytesAndStatus(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	if _, err := testDB.Exec(`SELECT pg_create_physical_replication_slot('raw_bytes_slot', true)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testDB.Exec(`SELECT pg_drop_replication_slot('raw_bytes_slot')`) })
	slots, err := FetchReplicationSlots(context.Background(), testDB)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range slots {
		if slot.SlotName != "raw_bytes_slot" {
			continue
		}
		if slot.WALLagBytes == nil || slot.WALStatus == "" {
			t.Errorf("expected retained bytes and wal_status, got %+v", slot)
		}
		if ReplicationSlotStatus(slot) != StatusWarning {
			t.Errorf("an inactive slot should be a warning, got %s", ReplicationSlotStatus(slot))
		}
		return
	}
	t.Fatal("slot not listed")
}
