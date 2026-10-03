package util

import (
	"strings"
	"testing"
	"time"
)

func int64Pointer(value int64) *int64       { return &value }
func float64Pointer(value float64) *float64 { return &value }

func TestSlotRetentionMetric(t *testing.T) {
	if got := slotRetentionMetric(WALReplicationStatus{}); got.level != 3 || got.value != "no replication slots" {
		t.Errorf("no slots: %+v", got)
	}
	unlimited := WALReplicationStatus{SlotCount: 1, MaxRetainedBytes: 2 << 30, MaxRetainedSlot: "standby1"}
	if got := slotRetentionMetric(unlimited); got.level != 1 || !strings.Contains(got.value, "unlimited") || !strings.Contains(got.value, "standby1") {
		t.Errorf("unlimited max_slot_wal_keep_size must warn and name the slot: %+v", got)
	}
	limited := WALReplicationStatus{SlotCount: 1, MaxRetainedBytes: 40 << 30, MaxRetainedSlot: "s", MaxSlotWALKeepBytes: int64Pointer(50 << 30)}
	if got := slotRetentionMetric(limited); got.level != 2 || !strings.Contains(got.value, "(80%)") {
		t.Errorf("40 of 50 GB should be red at 80%%: %+v", got)
	}
	limited.MaxRetainedBytes = 30 << 30
	if got := slotRetentionMetric(limited); got.level != 1 {
		t.Errorf("60%% should be yellow: %+v", got)
	}
	limited.MaxRetainedBytes = 1 << 30
	if got := slotRetentionMetric(limited); got.level != 0 {
		t.Errorf("2%% should be green: %+v", got)
	}
}

func TestSlotRiskMetric(t *testing.T) {
	status := WALReplicationStatus{SlotCount: 3}
	if got := slotRiskMetric(status); got.level != 0 || got.value != "3 slots • 0 unreserved • 0 lost" {
		t.Errorf("healthy: %+v", got)
	}
	status.SlotsUnreserved = 1
	if got := slotRiskMetric(status); got.level != 1 {
		t.Errorf("unreserved should warn: %+v", got)
	}
	status.SlotsLost = 1
	if got := slotRiskMetric(status); got.level != 2 {
		t.Errorf("lost should be critical: %+v", got)
	}
	idle := WALReplicationStatus{SlotCount: 1, LongestIdleSeconds: int64Pointer(85000), LongestIdleSlot: "old_slot",
		IdleSlotTimeoutSeconds: int64Pointer(86400)}
	if got := slotRiskMetric(idle); got.level != 1 || !strings.Contains(got.value, "old_slot") || !strings.Contains(got.value, "idle timeout") {
		t.Errorf("idle close to idle_replication_slot_timeout should warn: %+v", got)
	}
	idle.IdleSlotTimeoutSeconds = int64Pointer(0)
	if got := slotRiskMetric(idle); got.level != 0 || strings.Contains(got.value, "timeout") {
		t.Errorf("disabled timeout (0) shouldn't be shown or warn: %+v", got)
	}
}

func TestArchivingMetric(t *testing.T) {
	if got := archivingMetric(WALReplicationStatus{ArchiveMode: "off"}); got.level != 3 || got.value != "off" {
		t.Errorf("off: %+v", got)
	}
	failedAt := time.Now().Add(-10 * time.Minute)
	failing := WALReplicationStatus{ArchiveMode: "on", ArchiveFailingNow: true, FailedCount: 7, LastFailedTime: &failedAt}
	if got := archivingMetric(failing); got.level != 2 || !strings.Contains(got.value, "FAILING") || !strings.Contains(got.value, "7 failures") {
		t.Errorf("failing: %+v", got)
	}
	archivedAt := time.Now().Add(-30 * time.Second)
	ok := WALReplicationStatus{ArchiveMode: "on", ArchivedCount: 12, FailedCount: 1, LastArchivedTime: &archivedAt}
	if got := archivingMetric(ok); got.level != 0 || !strings.HasPrefix(got.value, "ok") {
		t.Errorf("an old failure followed by successes is ok: %+v", got)
	}
}

func TestReplicationMetric(t *testing.T) {
	if got := replicationMetric(WALReplicationStatus{}); got.level != 3 {
		t.Errorf("nothing to report: %+v", got)
	}
	standby := WALReplicationStatus{StandbyCount: 2, MaxStandbyLagBytes: int64Pointer(2 << 30), MaxReplayLagSeconds: float64Pointer(1)}
	if got := replicationMetric(standby); got.level != 2 || !strings.Contains(got.value, "2 standby(s)") {
		t.Errorf("2 GB behind should be critical: %+v", got)
	}
	logical := WALReplicationStatus{SubscriptionCount: 1, SubscriptionErrors: int64Pointer(0), SubscriptionConflicts: int64Pointer(3)}
	if got := replicationMetric(logical); got.level != 1 || !strings.Contains(got.value, "3 conflicts") {
		t.Errorf("conflicts should warn: %+v", got)
	}
	publisher := WALReplicationStatus{StandbyCount: 1, MaxStandbyLagBytes: int64Pointer(0), LogicalSenderCount: 2, MaxLogicalLagBytes: int64Pointer(100 << 20)}
	if got := replicationMetric(publisher); got.level != 1 || !strings.Contains(got.value, "1 standby(s)") || !strings.Contains(got.value, "2 logical subscriber(s)") {
		t.Errorf("standbys and logical subscribers must be counted separately: %+v", got)
	}
	pg14 := WALReplicationStatus{SubscriptionCount: 1}
	if got := replicationMetric(pg14); got.level != 0 || strings.Contains(got.value, "errors") {
		t.Errorf("without stats (PG13/14) show only the count: %+v", got)
	}
}
