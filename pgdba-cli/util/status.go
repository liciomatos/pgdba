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
