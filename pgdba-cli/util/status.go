package util

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
