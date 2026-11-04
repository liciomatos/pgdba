package util

import (
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

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

// RecoveryConflictStatus warns when queries on this standby were canceled by WAL replay.
func RecoveryConflictStatus(d RecoveryConflictDatabase) Status {
	if d.Total > 0 {
		return StatusWarning
	}
	return StatusOK
}

// ReplayStatus rates a standby's replay state; a primary is always ok. Lag counts only
// while received WAL is waiting to be replayed (the replay timestamp also ages while
// the primary is idle), with the StandbyLagStatus thresholds. A paused replay or a
// missing / non-streaming WAL receiver is a warning.
func ReplayStatus(r RecoveryConflicts) Status {
	if !r.InRecovery {
		return StatusOK
	}
	status := StatusOK
	if r.ReplayBacklogBytes != nil && *r.ReplayBacklogBytes > 0 {
		status = StandbyLagStatus(*r.ReplayBacklogBytes, r.ReplayLagSeconds)
	}
	if r.ReplayPaused != nil && *r.ReplayPaused {
		status = worst(status, StatusWarning)
	}
	if r.WALReceiver == nil || r.WALReceiver.Status != "streaming" {
		status = worst(status, StatusWarning)
	}
	return status
}

// DatabaseHealthStatus rates one database's pg_stat_database counters:
//   - critical: any data checksum failure (storage corruption);
//   - warning: any deadlock, ≥ 5% of ≥ 1,000 transactions rolled back, or ≥ 10% of
//     session time spent idle in transaction (PG14+: locks and snapshots held for nothing).
func DatabaseHealthStatus(d DatabaseHealth) Status {
	if d.ChecksumFailures != nil && *d.ChecksumFailures > 0 {
		return StatusCritical
	}
	status := StatusOK
	if d.Deadlocks > 0 {
		status = StatusWarning
	}
	if d.RollbackPct != nil && *d.RollbackPct >= 5 && d.XactCommit+d.XactRollback >= 1000 {
		status = StatusWarning
	}
	if pct := d.IdleInTransactionPct(); pct != nil && *pct >= 10 {
		status = StatusWarning
	}
	return status
}

// SequenceStatus rates how much of a sequence's range (capped by its column type) is
// used: warning from 75%, critical from 90%.
func SequenceStatus(s OwnedSequence) Status {
	switch {
	case s.PctUsed == nil:
		return StatusOK
	case *s.PctUsed >= 90:
		return StatusCritical
	case *s.PctUsed >= 75:
		return StatusWarning
	}
	return StatusOK
}

// UpsertTableStatus is the worst of the table's HOT status (TableChurnStatus) and its
// owned sequences' exhaustion status.
func UpsertTableStatus(t UpsertTable) Status {
	status := TableChurnStatus(t.Churn)
	for _, sequence := range t.Sequences {
		status = worst(status, SequenceStatus(sequence))
	}
	return status
}

// upsertBurnRatio is the values-per-live-row ratio above which a sequence is reported as
// burned by conflicting upserts.
const upsertBurnRatio = 2

// UpsertTableHints explains what to change on an upsert target table.
func UpsertTableHints(t UpsertTable) []string {
	hints := []string{}
	if hint := TableChurnHint(t.Churn); hint != "" {
		hints = append(hints, hint)
	}
	if t.UpdateSharePct != nil && *t.UpdateSharePct >= 50 && t.IndexCount > 3 && TableChurnStatus(t.Churn) != StatusOK {
		hints = append(hints, fmt.Sprintf("most upserts end as updates and the table has %d indexes: each non-HOT update writes all of them", t.IndexCount))
	}
	for _, sequence := range t.Sequences {
		if sequence.ValuesPerLiveRow != nil && *sequence.ValuesPerLiveRow >= upsertBurnRatio {
			hints = append(hints, fmt.Sprintf("%s used %.1f values per live row: every attempted upsert row calls nextval() even when it updates", sequence.Name, *sequence.ValuesPerLiveRow))
		}
		if SequenceStatus(sequence) != StatusOK {
			hints = append(hints, fmt.Sprintf("%s has used %.1f%% of its %s range — plan a move to bigint", sequence.Name, *sequence.PctUsed, sequence.ColumnType))
		}
	}
	return hints
}

// UpsertStatementHint flags DO UPDATE statements without a WHERE clause: they write a
// new row version (and WAL, and possibly every index) even when nothing changed.
func UpsertStatementHint(s UpsertStatement) string {
	if s.Action == "update" && !s.UpdateHasWhere {
		return "DO UPDATE has no WHERE: unchanged rows are rewritten anyway — add WHERE (t.col1, t.col2) IS DISTINCT FROM (EXCLUDED.col1, EXCLUDED.col2)"
	}
	return ""
}

// LockTableStatus rates how full the shared lock table is (non-fast-path locks over
// max_locks_per_transaction × (max_connections + max_prepared_transactions)): warning
// from 50%, critical from 80% — when it fills up, lock requests fail with "out of
// shared memory".
func LockTableStatus(summary LockSummary) Status {
	switch {
	case summary.UsagePct >= 80:
		return StatusCritical
	case summary.UsagePct >= 50:
		return StatusWarning
	}
	return StatusOK
}

// OperationProgressStatus warns while an operation sits in a "waiting for …" phase:
// CREATE INDEX / REINDEX CONCURRENTLY waiting for older transactions (or lockers) to
// finish makes no progress until they do.
func OperationProgressStatus(p OperationProgress) Status {
	if strings.HasPrefix(p.Phase, "waiting") {
		return StatusWarning
	}
	return StatusOK
}

// SLRUStatus warns when an SLRU cache read ≥ 1,000 pages from disk with a hit ratio
// below 90%: its buffers are too small for the workload (PG17+: the *_buffers
// settings), typically Subtrans with many subtransactions or MultiXact with many
// row locks / foreign keys.
func SLRUStatus(c SLRUStats) Status {
	if c.BlksRead >= 1000 && c.HitPct != nil && *c.HitPct < 90 {
		return StatusWarning
	}
	return StatusOK
}

// SubtransactionStatus warns for a session whose subtransaction cache overflowed
// (more than 64 open subtransactions, e.g. SAVEPOINTs or PL/pgSQL EXCEPTION blocks in a
// loop): every snapshot then has to consult pg_subtrans.
func SubtransactionStatus(b SubtransactionBackend) Status {
	if b.Overflowed {
		return StatusWarning
	}
	return StatusOK
}

// MissingFKIndexStatus: every unindexed foreign key is a warning; critical when the
// referencing table is ≥ 1 GB and the referenced table gets updates or deletes (each
// one scans that table).
func MissingFKIndexStatus(m MissingFKIndex) Status {
	if m.TableSizeBytes >= 1<<30 && m.ReferencedWrites > 0 {
		return StatusCritical
	}
	return StatusWarning
}

// SeqScanTableStatus warns about large tables (heap ≥ 100 MB) read by ≥ 100 sequential
// scans averaging ≥ 100,000 rows: likely queries missing an index.
func SeqScanTableStatus(t SeqScanTable) Status {
	if t.HeapBytes >= 100<<20 && t.SeqScan >= 100 && t.AvgRowsPerScan >= 100_000 {
		return StatusWarning
	}
	return StatusOK
}

// ColumnStatsHints suggests index and statistics changes from one column's statistics.
func ColumnStatsHints(table TableColumnStats, column ColumnStats) []string {
	hints := []string{}
	switch {
	case !column.Readable:
		return append(hints, "statistics hidden: this role has no SELECT privilege on the column")
	case !column.HasStats:
		return append(hints, "no statistics: run ANALYZE "+table.Table)
	}
	bigTable := table.Reltuples >= 100_000
	if column.EstimatedDistinct != nil && *column.EstimatedDistinct > 0 && *column.EstimatedDistinct <= 10 && bigTable && column.LeadsIndex {
		hints = append(hints, fmt.Sprintf("only ~%.0f distinct values lead an index: a btree on it rarely beats a seq scan — prefer a partial index (WHERE %s = …) for the rare value, or put a more selective column first", *column.EstimatedDistinct, column.Name))
	}
	if column.Correlation != nil && math.Abs(*column.Correlation) >= 0.9 && bigTable {
		hints = append(hints, fmt.Sprintf("physical order follows %s (correlation %.2f): range filters on it suit a small BRIN index", column.Name, *column.Correlation))
	}
	if column.NullFrac != nil && *column.NullFrac >= 0.8 && column.InIndex {
		hints = append(hints, fmt.Sprintf("%.0f%% NULL and indexed: a partial index WHERE %s IS NOT NULL is much smaller", *column.NullFrac*100, column.Name))
	}
	return hints
}

// BloatEstimateStatus rates a statistics-based bloat estimate: critical from 50% and
// 100 MB of bloat, warning from 30% and 10 MB. Not-applicable estimates are ok.
func BloatEstimateStatus(b BloatEstimate) Status {
	switch {
	case b.NotApplicable:
		return StatusOK
	case b.BloatPct >= 50 && b.BloatBytes >= 100<<20:
		return StatusCritical
	case b.BloatPct >= 30 && b.BloatBytes >= 10<<20:
		return StatusWarning
	}
	return StatusOK
}

// DisabledTriggerStatus: a disabled internal (foreign key) trigger means the constraint
// is not enforced — critical; any other disabled trigger is a warning.
func DisabledTriggerStatus(t DisabledTrigger) Status {
	if t.Internal {
		return StatusCritical
	}
	return StatusWarning
}

// PartitionedTableStatus warns when the default partition holds ≥ 10% of a partitioned
// table of at least 100 MB: partitions for that data are missing.
func PartitionedTableStatus(t PartitionedTable) Status {
	if t.DefaultPartitionBytes != nil && t.SizeBytes >= 100<<20 && float64(*t.DefaultPartitionBytes) >= 0.1*float64(t.SizeBytes) {
		return StatusWarning
	}
	return StatusOK
}

// HBARuleStatus rates a pg_hba.conf line: critical for a line with an error, trust
// over the network, or cleartext password over a connection that may not be SSL;
// warning for trust on the local socket or loopback, cleartext password over hostssl,
// and md5 (deprecated in favor of scram-sha-256).
func HBARuleStatus(rule HBARule) Status {
	onThisMachine := rule.Type == "local" || isLoopbackAddress(rule.Address)
	switch {
	case rule.Error != "":
		return StatusCritical
	case rule.AuthMethod == "trust" && !onThisMachine:
		return StatusCritical
	case rule.AuthMethod == "password" && !onThisMachine && rule.Type != "hostssl":
		return StatusCritical
	case rule.AuthMethod == "trust", rule.AuthMethod == "password", rule.AuthMethod == "md5":
		return StatusWarning
	}
	return StatusOK
}

// ConnectionEncryptionStatus warns about network connections without SSL.
func ConnectionEncryptionStatus(c ConnectionEncryption) Status {
	if !c.SSL && c.ClientAddr != "local" {
		return StatusWarning
	}
	return StatusOK
}

// isLoopbackAddress reports whether a pg_hba address ("127.0.0.1/255.255.255.255",
// "::1/…", "localhost") only matches connections from this machine.
func isLoopbackAddress(address string) bool {
	host, _, _ := strings.Cut(address, "/")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
