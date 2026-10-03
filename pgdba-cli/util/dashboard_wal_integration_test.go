package util

import (
	"context"
	"testing"
	"time"
)

func TestFetchWALReplicationStatus(t *testing.T) {
	if testing.Short() || skipIntegration {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	if _, err := testDB.ExecContext(ctx,
		`SELECT pg_create_physical_replication_slot('dash_wal_slot', true)`); err != nil {
		t.Fatal(err)
	}
	defer testDB.ExecContext(ctx, `SELECT pg_drop_replication_slot('dash_wal_slot')`)
	// Generate some WAL so the slot retains a measurable amount.
	if _, err := testDB.ExecContext(ctx, `
		CREATE TABLE dash_wal_t AS SELECT g, md5(g::text) FROM generate_series(1, 20000) g;
		DROP TABLE dash_wal_t;`); err != nil {
		t.Fatal(err)
	}

	status, err := FetchWALReplicationStatus(ctx, testDB)
	if err != nil {
		t.Fatal(err)
	}
	if status.SlotCount < 1 || status.MaxRetainedBytes <= 0 || status.MaxRetainedSlot == "" {
		t.Errorf("slot retention not measured: %+v", status)
	}
	if status.MaxSlotWALKeepBytes != nil {
		t.Errorf("default max_slot_wal_keep_size is -1 (unlimited), got %d", *status.MaxSlotWALKeepBytes)
	}
	if status.ArchiveMode != "off" || status.ArchiveFailingNow {
		t.Errorf("test server has archiving off: %+v", status)
	}
	if (pgMajorVersion() >= 18) != (status.IdleSlotTimeoutSeconds != nil) {
		t.Errorf("idle_replication_slot_timeout presence wrong for PG%d", pgMajorVersion())
	}

	if _, err := testDB.ExecContext(ctx, `ALTER SYSTEM SET max_slot_wal_keep_size = '1GB'`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		testDB.ExecContext(ctx, `ALTER SYSTEM RESET max_slot_wal_keep_size`)
		testDB.ExecContext(ctx, `SELECT pg_reload_conf()`)
	}()
	if _, err := testDB.ExecContext(ctx, `SELECT pg_reload_conf()`); err != nil {
		t.Fatal(err)
	}
	// pg_reload_conf() only signals: each backend applies the new value at its next
	// command boundary, so a pooled connection may still see the old one briefly.
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err = FetchWALReplicationStatus(ctx, testDB)
		if err != nil {
			t.Fatal(err)
		}
		if status.MaxSlotWALKeepBytes != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if status.MaxSlotWALKeepBytes == nil || *status.MaxSlotWALKeepBytes != 1<<30 {
		t.Fatalf("max_slot_wal_keep_size should read 1 GB in bytes, got %v", status.MaxSlotWALKeepBytes)
	}
	if pct := status.SlotRetentionPct(); pct == nil || *pct <= 0 || *pct >= 100 {
		t.Errorf("retention %% of 1 GB should be between 0 and 100, got %v", pct)
	}
}
