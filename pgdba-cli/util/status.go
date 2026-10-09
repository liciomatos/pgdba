package util

import "time"

// Status is the severity of one diagnostic row or response. The values match the
// SeverityColor levels, so a screen can color a cell straight from it, and the MCP
// reports String(). Each check's thresholds live in one *Status function here so the
// TUI colors and the MCP "status" field can never disagree.
type Status int

const (
	StatusOK       Status = 0
	StatusWarning  Status = 1
	StatusCritical Status = 2
)

func (s Status) String() string {
	switch s {
	case StatusWarning:
		return "warning"
	case StatusCritical:
		return "critical"
	}
	return "ok"
}

// worst returns the most severe of the given statuses.
func worst(statuses ...Status) Status {
	result := StatusOK
	for _, status := range statuses {
		result = max(result, status)
	}
	return result
}

// XminHolderStatus rates something holding back the xmin horizon. Vacuum can't remove
// rows deleted after the horizon, and freezing can't advance past it, so an old holder
// causes bloat and eventually wraparound pressure. Thresholds are relative to
// autovacuum_freeze_max_age (the age at which anti-wraparound vacuums are forced):
// warning from 5% of it (10M XIDs at the 200M default) or a transaction open for over an
// hour, critical from 25% (50M XIDs).
func XminHolderStatus(xminAge int64, transactionAgeSeconds *int64, freezeMaxAge int64) Status {
	if freezeMaxAge <= 0 {
		freezeMaxAge = 200_000_000
	}
	switch {
	case float64(xminAge) >= 0.25*float64(freezeMaxAge):
		return StatusCritical
	case float64(xminAge) >= 0.05*float64(freezeMaxAge):
		return StatusWarning
	case transactionAgeSeconds != nil && *transactionAgeSeconds >= 3600:
		return StatusWarning
	}
	return StatusOK
}

// VacuumProgressStatus rates a running vacuum: an anti-wraparound vacuum means
// autovacuum fell behind on freezing (warning), and more than one index pass means
// the dead-tuple memory (maintenance_work_mem / autovacuum_work_mem) filled up and every
// index had to be scanned again (warning).
func VacuumProgressStatus(progress VacuumProgress) Status {
	if progress.IsWraparound || progress.IndexVacuumCount > 1 {
		return StatusWarning
	}
	return StatusOK
}

// AutovacuumTableStatus rates a table in the autovacuum list:
//   - critical: dead tuples at ≥ 2× the autovacuum trigger, or a per-table cost
//     override that makes its vacuum ≥ 10× slower than the global setting;
//   - warning: dead tuples past the trigger (vacuum overdue), a cost override slower
//     than the global setting, or autovacuum disabled for the table.
func AutovacuumTableStatus(table AutovacuumTable) Status {
	status := StatusOK
	if table.TriggerRatio != nil {
		switch {
		case *table.TriggerRatio >= 2:
			status = worst(status, StatusCritical)
		case *table.TriggerRatio >= 1:
			status = worst(status, StatusWarning)
		}
	}
	if table.Cost.SlowerThanGlobal {
		status = worst(status, StatusWarning)
		if table.Cost.SlowdownFactor == nil || *table.Cost.SlowdownFactor >= 10 {
			status = worst(status, StatusCritical)
		}
	}
	if !table.AutovacuumEnabled {
		status = worst(status, StatusWarning)
	}
	return status
}

// CacheHitStatus rates an instance-level cache hit ratio (heap, index or database):
// critical below 90%, warning below 99% — an OLTP working set should be served from
// shared_buffers almost entirely. nil (nothing read yet) is ok.
func CacheHitStatus(pct *float64) Status {
	switch {
	case pct == nil:
		return StatusOK
	case *pct < 90:
		return StatusCritical
	case *pct < 99:
		return StatusWarning
	}
	return StatusOK
}

// TableCacheHitStatus rates one table's cache hit ratio, more leniently than the
// instance ratio because single tables (bulk loads, rarely read history) legitimately
// read from disk: critical below 70%, warning below 90%.
func TableCacheHitStatus(pct *float64) Status {
	switch {
	case pct == nil:
		return StatusOK
	case *pct < 70:
		return StatusCritical
	case *pct < 90:
		return StatusWarning
	}
	return StatusOK
}

// unusedIndexMinBytes keeps tiny never-scanned indexes (empty tables) from being flagged.
const unusedIndexMinBytes = 1 << 20

// IndexUsageStatus rates an index: critical when INVALID (a failed CREATE INDEX
// CONCURRENTLY: maintained on every write, never used), warning when it looks redundant
// or has never been scanned and is larger than 1 MB.
func IndexUsageStatus(idx IndexUsage) Status {
	switch {
	case !idx.IsValid:
		return StatusCritical
	case len(idx.RedundantWith) > 0, idx.IdxScan == 0 && idx.IndexSizeBytes > unusedIndexMinBytes:
		return StatusWarning
	}
	return StatusOK
}

// churnMinUpdates is the update count from which a low HOT ratio is worth acting on.
const churnMinUpdates = 10_000

// TableChurnStatus warns about update-heavy tables (≥ 10,000 updates) where less than
// half of the updates are HOT: every non-HOT update writes every index and leaves dead
// index entries for vacuum.
func TableChurnStatus(t TableChurn) Status {
	if t.Updates >= churnMinUpdates && t.HotPct != nil && *t.HotPct < 50 {
		return StatusWarning
	}
	return StatusOK
}

// TableChurnHint suggests the likely fix for a table TableChurnStatus warns about.
func TableChurnHint(t TableChurn) string {
	if TableChurnStatus(t) == StatusOK {
		return ""
	}
	if t.Fillfactor >= 100 {
		return "pages are packed full (fillfactor 100): HOT needs free space on the same page — consider fillfactor 80-90"
	}
	return "fillfactor already leaves room: updates probably change indexed columns, which can never be HOT"
}

// WALStatsStatus warns when full-page images are ≥ 30% of WAL records (checkpoints too
// frequent for the write pattern; consider a larger max_wal_size / checkpoint_timeout
// or wal_compression) or when WAL buffers filled up on more than 0.1% of records
// (wal_buffers too small).
func WALStatsStatus(w WALStats) Status {
	if fpi := w.FPIPct(); fpi != nil && *fpi >= 30 {
		return StatusWarning
	}
	if w.Records > 0 && w.BuffersFull*1000 > w.Records {
		return StatusWarning
	}
	return StatusOK
}

// IOStatsStatus warns when client backends do ≥ 25% of the relation block writes in
// the normal context: they're evicting dirty buffers themselves, which adds write
// latency to queries — the background writer / checkpointer aren't keeping up.
func IOStatsStatus(stats IOStats) Status {
	if stats.ClientBackendWritePct != nil && *stats.ClientBackendWritePct >= 25 {
		return StatusWarning
	}
	return StatusOK
}

// WaitEventStatus rates a sampled wait event by its average active sessions: lock waits
// are critical from 1 AAS (sessions blocked on each other all the time) and a warning
// below; IO, LWLock and BufferPin waits are a warning from 1 AAS.
func WaitEventStatus(eventType string, aas float64) Status {
	switch eventType {
	case "Lock":
		if aas >= 1 {
			return StatusCritical
		}
		if aas > 0 {
			return StatusWarning
		}
	case "IO", "LWLock", "BufferPin":
		if aas >= 1 {
			return StatusWarning
		}
	}
	return StatusOK
}

// ConnectionUsageStatus rates sessions used vs max_connections: critical from 90%,
// warning from 70% — running out blocks new logins.
func ConnectionUsageStatus(usagePct float64) Status {
	switch {
	case usagePct >= 90:
		return StatusCritical
	case usagePct >= 70:
		return StatusWarning
	}
	return StatusOK
}

// IdleInTransactionStatus rates how long a session has been idle in a transaction:
// warning from 5 minutes, critical from 1 hour (it holds locks and the xmin horizon).
func IdleInTransactionStatus(stateAgeSeconds int64) Status {
	switch {
	case stateAgeSeconds >= 3600:
		return StatusCritical
	case stateAgeSeconds >= 300:
		return StatusWarning
	}
	return StatusOK
}

// SlowQueryStatus rates a statement's mean time against the slow-query threshold:
// warning above it, critical above twice it (the Slow Queries screen's colors).
func SlowQueryStatus(meanMS float64, thresholdMS int) Status {
	switch {
	case meanMS > 2*float64(thresholdMS):
		return StatusCritical
	case meanMS > float64(thresholdMS):
		return StatusWarning
	}
	return StatusOK
}

// QueryLoadStatus rates a statement's share of the instance's total execution time:
// warning from 20%, critical from 40% — one statement dominating the load.
func QueryLoadStatus(loadPct float64) Status {
	switch {
	case loadPct >= 40:
		return StatusCritical
	case loadPct >= 20:
		return StatusWarning
	}
	return StatusOK
}

// LongRunningStatus rates how long a statement has been running: warning above 10 s,
// critical above 60 s (the Long Running Queries screen's colors).
func LongRunningStatus(durationSeconds float64) Status {
	switch {
	case durationSeconds > 60:
		return StatusCritical
	case durationSeconds > 10:
		return StatusWarning
	}
	return StatusOK
}

// ReplicationSlotStatus rates a slot: critical when invalidated (wal_status lost) or
// about to be (unreserved); warning when no consumer is connected, since an inactive
// slot keeps WAL forever.
func ReplicationSlotStatus(slot ReplicationSlot) Status {
	switch {
	case slot.WALStatus == "lost", slot.WALStatus == "unreserved":
		return StatusCritical
	case !slot.Active:
		return StatusWarning
	}
	return StatusOK
}

// StandbyLagStatus uses the dashboard's replication thresholds: warning above 64 MB or
// 10 s of replay lag, critical above 1 GB or 60 s.
func StandbyLagStatus(lagBytes int64, replayLagSeconds *float64) Status {
	lagSeconds := 0.0
	if replayLagSeconds != nil {
		lagSeconds = *replayLagSeconds
	}
	switch {
	case lagBytes > 1<<30 || lagSeconds > 60:
		return StatusCritical
	case lagBytes > 64<<20 || lagSeconds > 10:
		return StatusWarning
	}
	return StatusOK
}

// UserStatus flags login roles whose password has expired (critical) or expires within
// 7 days (warning).
func UserStatus(validUntil *time.Time, now time.Time) Status {
	switch {
	case validUntil == nil:
		return StatusOK
	case validUntil.Before(now):
		return StatusCritical
	case validUntil.Before(now.Add(7 * 24 * time.Hour)):
		return StatusWarning
	}
	return StatusOK
}

// ExtensionStatus warns when an installed extension is older than the version the
// server ships (ALTER EXTENSION ... UPDATE is due), as happens after major upgrades.
func ExtensionStatus(extension Extension) Status {
	if extension.DefaultVersion != "" && compareExtensionVersions(extension.Version, extension.DefaultVersion) < 0 {
		return StatusWarning
	}
	return StatusOK
}

// TempFileStatus warns about any temp file spill since the stats reset (the Temp Files
// screen highlights the same rows): work_mem was too small for some sort or hash.
func TempFileStatus(usage TempFileUsage) Status {
	if usage.TempFiles > 0 {
		return StatusWarning
	}
	return StatusOK
}

// SubscriptionStatus rates a logical replication subscription: critical when enabled
// but its apply worker isn't running; warning when disabled or with apply/sync errors.
func SubscriptionStatus(subscription Subscription) Status {
	errors := int64(0)
	if subscription.ApplyErrorCount != nil {
		errors += *subscription.ApplyErrorCount
	}
	if subscription.SyncErrorCount != nil {
		errors += *subscription.SyncErrorCount
	}
	switch {
	case subscription.Enabled && subscription.WorkerPID == nil:
		return StatusCritical
	case !subscription.Enabled, errors > 0:
		return StatusWarning
	}
	return StatusOK
}

// SubscriptionTableStatus warns while a table is still being copied or synchronized.
func SubscriptionTableStatus(syncState string) Status {
	if syncState == "ready" {
		return StatusOK
	}
	return StatusWarning
}

// HintLevelStatus maps a replication-config hint level (-1 none, 0 ok, 1 warn, 2 error).
func HintLevelStatus(level int) Status {
	switch level {
	case 2:
		return StatusCritical
	case 1:
		return StatusWarning
	}
	return StatusOK
}

// FreezePctStatus uses the dashboard's database freeze thresholds: warning above 7.1%
// and critical above 8.6% of the 2.1B XID wraparound limit (150M / 180M XIDs).
func FreezePctStatus(pctTowardShutdown float64) Status {
	switch {
	case pctTowardShutdown > 8.6:
		return StatusCritical
	case pctTowardShutdown > 7.1:
		return StatusWarning
	}
	return StatusOK
}

// DashboardStatus is the worst of the dashboard's headline checks: connection usage,
// heap cache hit, blocked sessions (critical), long-running queries (warning), invalid
// indexes (warning) and the oldest database's freeze age.
func DashboardStatus(dashboard DashboardResult) Status {
	status := worst(ConnectionUsageStatus(dashboard.ConnectionPct), CacheHitStatus(dashboard.CacheHitRatio),
		FreezePctStatus(dashboard.FreezePctToward))
	if dashboard.BlockedQueries > 0 {
		status = worst(status, StatusCritical)
	}
	if dashboard.LongRunningCount > 0 || dashboard.InvalidIndexes > 0 {
		status = worst(status, StatusWarning)
	}
	return status
}

// MemoryStatsStatus is the worst of the cache hit ratios, plus a warning when more
// checkpoints were requested (forced by WAL volume) than timed — max_wal_size too small.
func MemoryStatsStatus(stats MemoryStats) Status {
	status := worst(CacheHitStatus(stats.CacheHit.HeapHitPct), CacheHitStatus(stats.CacheHit.IdxHitPct))
	if stats.Checkpoint.CheckpointsReq > stats.Checkpoint.CheckpointsTimed {
		status = worst(status, StatusWarning)
	}
	return status
}

// AutovacuumThresholdsStatus rates one table's autovacuum state: critical when an
// anti-wraparound vacuum or the failsafe is due, warning when autovacuum would pick the
// table up now (vacuum or analyze overdue) or an aggressive scan is due.
func AutovacuumThresholdsStatus(thresholds AutovacuumThresholds) Status {
	failsafeDue := thresholds.XIDFailsafe != nil && thresholds.XIDFailsafe.Due ||
		thresholds.MXIDFailsafe != nil && thresholds.MXIDFailsafe.Due
	switch {
	case thresholds.XIDWraparound.Due, thresholds.MXIDWraparound.Due, failsafeDue:
		return StatusCritical
	case thresholds.AutovacuumDue(), thresholds.XIDAggressive.Due, thresholds.MXIDAggressive.Due:
		return StatusWarning
	}
	return StatusOK
}
