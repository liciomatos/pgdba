package util

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"
)

// NoRowLimit passed as the limit argument of a Fetch* function returns every row
// (LIMIT NULLIF(0, 0) = LIMIT NULL = LIMIT ALL). TUI screens use it so the table
// scrolls through the full list; MCP handlers keep a finite default limit.
const NoRowLimit = 0

// pgMajorVersion returns the major version number of the connected PostgreSQL server.
func pgMajorVersion() int {
	v := config.Config.Version
	if idx := strings.Index(v, "."); idx > 0 {
		n, _ := strconv.Atoi(v[:idx])
		return n
	}
	n, _ := strconv.Atoi(v)
	return n
}

// DashboardResult holds all metrics shown on the main dashboard.
type DashboardResult struct {
	Host                       string
	Database                   string
	UsedConnections            int
	MaxConnections             int
	ConnectionPct              float64
	ActiveQueries              int
	BlockedQueries             int
	SlowQueryCount             *int // nil when pg_stat_statements is unavailable (see SlowQueryUnavailableReason)
	SlowQueryUnavailableReason string
	SlowThresholdMS            int
	CacheHitRatio              *float64 // nil when no table I/O data exists
	DeadTuples                 int64
	InvalidIndexes             int
	ReplicationSlots           int
	FreezeOldestDB             string
	FreezeOldestDBAge          int64
	FreezePctToward            float64
	LongRunningCount           int        // queries running > 60 seconds
	WaitEventCount             int        // non-idle queries with an active wait event
	TempFilesBytes             int64      // temp_bytes from pg_stat_database for current db
	DBSizePretty               string     // pg_size_pretty(pg_database_size(current_database()))
	CommitPct                  *float64   // 100*xact_commit/(xact_commit+xact_rollback), nil if no activity
	UptimeSeconds              int64      // EXTRACT(EPOCH FROM (now() - pg_postmaster_start_time()))
	StatsReset                 *time.Time // pg_stat_database.stats_reset of the current db; nil = never reset
	// Warnings lists the parts that failed ("part: error"); their fields keep zero values.
	Warnings []string
}

// FetchDashboard runs every metric independently: one failing part (a missing
// extension, a permission error on one view) becomes a warning instead of hiding the
// rest of the dashboard. It only returns an error when every part failed, which means
// the connection itself is unusable.
func FetchDashboard(ctx context.Context, db *sql.DB, slowThresholdMS int) (DashboardResult, error) {
	threshold := slowThresholdMS
	if threshold <= 0 {
		threshold = 1000
	}
	result := DashboardResult{
		Host:            config.Config.Host,
		Database:        config.Config.DBName,
		SlowThresholdMS: threshold,
	}
	parts, failed := 0, 0
	var firstErr error
	run := func(part string, fetch func() error) {
		parts++
		if err := fetch(); err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			result.Warnings = append(result.Warnings, part+": "+err.Error())
		}
	}

	run("connections", func() error {
		return db.QueryRowContext(ctx, `
			SELECT count(*), (SELECT setting::int FROM pg_settings WHERE name = 'max_connections')
			FROM pg_stat_activity`,
		).Scan(&result.UsedConnections, &result.MaxConnections)
	})
	if result.MaxConnections > 0 {
		result.ConnectionPct = float64(result.UsedConnections) / float64(result.MaxConnections) * 100
	}
	run("activity", func() error {
		return db.QueryRowContext(ctx, `
			SELECT
				count(*) FILTER (WHERE state = 'active' AND query NOT LIKE '%pg_stat_activity%'),
				count(*) FILTER (WHERE wait_event_type = 'Lock'),
				count(*) FILTER (WHERE state = 'active'
				                 AND query_start < NOW() - INTERVAL '60 seconds'
				                 AND pid <> pg_backend_pid()),
				count(*) FILTER (WHERE wait_event_type IS NOT NULL
				                 AND state != 'idle'
				                 AND pid <> pg_backend_pid())
			FROM pg_stat_activity`,
		).Scan(&result.ActiveQueries, &result.BlockedQueries, &result.LongRunningCount, &result.WaitEventCount)
	})
	// A missing pg_stat_statements is a normal setup, not a failure: it is reported
	// through SlowQueryUnavailableReason rather than as a warning.
	pgss, pgssErr := FetchPgStatStatementsInfo(ctx, db)
	switch {
	case pgssErr != nil:
		result.SlowQueryUnavailableReason = pgssErr.Error()
	case !pgss.Installed:
		result.SlowQueryUnavailableReason = "pg_stat_statements is not installed in this database"
	default:
		run("pg_stat_statements", func() error {
			var slowCount int
			if err := db.QueryRowContext(ctx, fmt.Sprintf(
				`SELECT count(*) FROM %s WHERE %s > $1`, pgss.Relation, pgss.MeanTimeColumn), threshold,
			).Scan(&slowCount); err != nil {
				result.SlowQueryUnavailableReason = err.Error()
				return err
			}
			result.SlowQueryCount = &slowCount
			return nil
		})
		if hint := pgss.UpdateHint(); hint != "" {
			result.Warnings = append(result.Warnings, hint)
		}
	}
	run("cache hit", func() error {
		var cacheHit sql.NullFloat64
		if err := db.QueryRowContext(ctx, `
			SELECT ROUND(100.0 * sum(heap_blks_hit) / NULLIF(sum(heap_blks_hit)+sum(heap_blks_read),0), 1)
			FROM pg_statio_user_tables`,
		).Scan(&cacheHit); err != nil {
			return err
		}
		if cacheHit.Valid {
			result.CacheHitRatio = &cacheHit.Float64
		}
		return nil
	})
	run("dead tuples", func() error {
		return db.QueryRowContext(ctx, `SELECT COALESCE(sum(n_dead_tup),0) FROM pg_stat_user_tables`).Scan(&result.DeadTuples)
	})
	run("invalid indexes", func() error {
		return db.QueryRowContext(ctx, `SELECT count(*) FROM pg_index WHERE NOT indisvalid`).Scan(&result.InvalidIndexes)
	})
	run("replication slots", func() error {
		return db.QueryRowContext(ctx, `SELECT count(*) FROM pg_replication_slots`).Scan(&result.ReplicationSlots)
	})
	run("freeze", func() error {
		return db.QueryRowContext(ctx, `
			SELECT datname, age(datfrozenxid),
			       round(age(datfrozenxid)::numeric / 2100000000 * 100, 2)
			FROM pg_database
			WHERE datallowconn
			ORDER BY age(datfrozenxid) DESC
			LIMIT 1
		`).Scan(&result.FreezeOldestDB, &result.FreezeOldestDBAge, &result.FreezePctToward)
	})
	run("database stats", func() error {
		var commitPct sql.NullFloat64
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(temp_bytes, 0),
			       ROUND(100.0 * xact_commit / NULLIF(xact_commit + xact_rollback, 0), 1),
			       stats_reset
			FROM pg_stat_database WHERE datname = current_database()`,
		).Scan(&result.TempFilesBytes, &commitPct, &result.StatsReset); err != nil {
			return err
		}
		if commitPct.Valid {
			result.CommitPct = &commitPct.Float64
		}
		return nil
	})
	run("database size", func() error {
		return db.QueryRowContext(ctx,
			`SELECT pg_size_pretty(pg_database_size(current_database()))`,
		).Scan(&result.DBSizePretty)
	})
	run("uptime", func() error {
		return db.QueryRowContext(ctx,
			`SELECT EXTRACT(EPOCH FROM (NOW() - pg_postmaster_start_time()))::bigint`,
		).Scan(&result.UptimeSeconds)
	})

	if failed == parts {
		return result, firstErr
	}
	return result, nil
}

// PgStatStatementsInfo describes the pg_stat_statements extension installed in the
// current database and which timing columns its version exposes.
//
// The extension's SQL version is independent of the server version: a database that
// was upgraded (pg_upgrade, RDS major upgrade) keeps the old extension version until
// someone runs ALTER EXTENSION ... UPDATE. Version 1.8 (shipped with PG13) renamed
// total_time/mean_time/stddev_time to total_exec_time/mean_exec_time/stddev_exec_time,
// so a PG16 server can still expose the pre-1.8 column names.
type PgStatStatementsInfo struct {
	Installed bool
	Version   string
	// DefaultVersion is what CREATE/ALTER EXTENSION would install on this server
	// (pg_available_extensions.default_version); "" when unknown.
	DefaultVersion string
	// Relation is the schema-qualified, quoted pg_stat_statements view — the extension
	// may live in a schema that isn't on the search_path.
	Relation         string
	TotalTimeColumn  string
	MeanTimeColumn   string
	StddevTimeColumn string
}

// Outdated reports whether the installed extension is older than the version this
// server ships, i.e. ALTER EXTENSION pg_stat_statements UPDATE would upgrade it.
func (info PgStatStatementsInfo) Outdated() bool {
	return info.Installed && info.DefaultVersion != "" && compareExtensionVersions(info.Version, info.DefaultVersion) < 0
}

// UpdateHint is the warning shown when the extension is outdated; "" otherwise.
func (info PgStatStatementsInfo) UpdateHint() string {
	if !info.Outdated() {
		return ""
	}
	return fmt.Sprintf("pg_stat_statements %s is older than the %s this server ships; run ALTER EXTENSION pg_stat_statements UPDATE;",
		info.Version, info.DefaultVersion)
}

// ErrPgStatStatementsMissing is returned by Fetch* functions that need pg_stat_statements
// when the extension isn't installed in the current database.
var ErrPgStatStatementsMissing = fmt.Errorf("pg_stat_statements is not installed in this database " +
	"(CREATE EXTENSION pg_stat_statements; it also requires shared_preload_libraries = 'pg_stat_statements')")

// compareExtensionVersions compares dotted extension versions numerically ("1.7" < "1.10").
// Non-numeric parts compare as 0.
func compareExtensionVersions(left, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for i := 0; i < max(len(leftParts), len(rightParts)); i++ {
		var leftNumber, rightNumber int
		if i < len(leftParts) {
			leftNumber, _ = strconv.Atoi(leftParts[i])
		}
		if i < len(rightParts) {
			rightNumber, _ = strconv.Atoi(rightParts[i])
		}
		if leftNumber != rightNumber {
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		}
	}
	return 0
}

// pgStatStatementsColumns returns the total/mean/stddev timing column names for an
// extension version: the *_exec_time names exist from 1.8 on.
func pgStatStatementsColumns(version string) (total, mean, stddev string) {
	if compareExtensionVersions(version, "1.8") < 0 {
		return "total_time", "mean_time", "stddev_time"
	}
	return "total_exec_time", "mean_exec_time", "stddev_exec_time"
}

// FetchPgStatStatementsInfo reads the installed pg_stat_statements version. A missing
// extension is not an error: Installed is false.
func FetchPgStatStatementsInfo(ctx context.Context, db *sql.DB) (PgStatStatementsInfo, error) {
	var info PgStatStatementsInfo
	var schema string
	err := db.QueryRowContext(ctx, `
		SELECT e.extversion, n.nspname, COALESCE(a.default_version, '')
		FROM pg_extension e
		JOIN pg_namespace n ON n.oid = e.extnamespace
		LEFT JOIN pg_available_extensions a ON a.name = e.extname
		WHERE e.extname = 'pg_stat_statements'`,
	).Scan(&info.Version, &schema, &info.DefaultVersion)
	if err == sql.ErrNoRows {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	info.Installed = true
	info.Relation = pq.QuoteIdentifier(schema) + ".pg_stat_statements"
	info.TotalTimeColumn, info.MeanTimeColumn, info.StddevTimeColumn = pgStatStatementsColumns(info.Version)
	return info, nil
}

// requirePgStatStatements returns the extension info or ErrPgStatStatementsMissing.
func requirePgStatStatements(ctx context.Context, db *sql.DB) (PgStatStatementsInfo, error) {
	info, err := FetchPgStatStatementsInfo(ctx, db)
	if err != nil {
		return info, err
	}
	if !info.Installed {
		return info, ErrPgStatStatementsMissing
	}
	return info, nil
}

// SlowQuery is one row from pg_stat_statements sorted by mean execution time.
type SlowQuery struct {
	QueryID          int64
	Query            string
	Calls            int
	TotalExecTimeMS  float64
	MeanExecTimeMS   float64
	StddevExecTimeMS float64
	Rows             int
}

func FetchSlowQueries(ctx context.Context, db *sql.DB, thresholdMS, limit int) ([]SlowQuery, error) {
	pgss, err := requirePgStatStatements(ctx, db)
	if err != nil {
		return nil, err
	}
	// Column names come from pgStatStatementsColumns (fixed identifiers), never user input.
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT queryid, query, calls, %[1]s, %[2]s, %[3]s, rows
		FROM %[4]s
		WHERE %[2]s > $1
		ORDER BY %[2]s DESC
		LIMIT NULLIF($2::int, 0)`,
		pgss.TotalTimeColumn, pgss.MeanTimeColumn, pgss.StddevTimeColumn, pgss.Relation),
		thresholdMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []SlowQuery
	for rows.Next() {
		var q SlowQuery
		if err := rows.Scan(&q.QueryID, &q.Query, &q.Calls, &q.TotalExecTimeMS, &q.MeanExecTimeMS, &q.StddevExecTimeMS, &q.Rows); err != nil {
			return nil, err
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

// LongRunningQuery is a non-idle session whose current query has exceeded the minimum duration.
type LongRunningQuery struct {
	PID             int
	Username        string // "" for background processes, which have no user
	ApplicationName string
	ClientAddr      string // "" for unix-socket and background connections
	BackendType     string
	State           string
	WaitEventType   string
	WaitEvent       string
	DurationSeconds float64
	Query           string
}

// FetchLongRunningQueries lists sessions whose current query has run longer than
// minDurationSeconds. Only client backends are returned unless includeBackground is
// set: autovacuum workers, walsenders and other background processes have no user and
// would otherwise crowd the list (autovacuum is covered by FetchVacuumProgress).
func FetchLongRunningQueries(ctx context.Context, db *sql.DB, minDurationSeconds, limit int, includeBackground bool) ([]LongRunningQuery, error) {
	// Every text column can be NULL for background processes (usename, client_addr,
	// state, wait_event…), so each one is COALESCEd before scanning into a string.
	rows, err := db.QueryContext(ctx, `
		SELECT
			pid,
			COALESCE(usename, ''),
			COALESCE(application_name, ''),
			COALESCE(client_addr::text, ''),
			COALESCE(backend_type, ''),
			COALESCE(state, ''),
			COALESCE(wait_event_type, ''),
			COALESCE(wait_event, ''),
			ROUND(EXTRACT(EPOCH FROM (now() - query_start))::numeric, 1) AS duration_seconds,
			COALESCE(query, '') AS query
		FROM pg_stat_activity
		WHERE state IS DISTINCT FROM 'idle'
		  AND query_start IS NOT NULL
		  AND now() - query_start > ($1 * interval '1 second')
		  AND pid <> pg_backend_pid()
		  AND ($3 OR backend_type = 'client backend')
		ORDER BY duration_seconds DESC
		LIMIT NULLIF($2::int, 0)`, minDurationSeconds, limit, includeBackground)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []LongRunningQuery
	for rows.Next() {
		var q LongRunningQuery
		if err := rows.Scan(&q.PID, &q.Username, &q.ApplicationName, &q.ClientAddr, &q.BackendType,
			&q.State, &q.WaitEventType, &q.WaitEvent, &q.DurationSeconds, &q.Query); err != nil {
			return nil, err
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

// BlockedQuery represents a session that is blocked by another session's lock.
type BlockedQuery struct {
	BlockedPID          int
	BlockedUser         string
	BlockingPID         int
	BlockingUser        string
	BlockedStatement    string
	BlockingStatement   string
	BlockedApplication  string
	BlockingApplication string
}

func FetchBlockedQueries(ctx context.Context, db *sql.DB) ([]BlockedQuery, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			blocked_locks.pid,
			COALESCE(blocked_activity.usename, ''),
			blocking_locks.pid,
			-- the blocker can be a background process (e.g. autovacuum) with no user
			COALESCE(blocking_activity.usename, ''),
			COALESCE(blocked_activity.query, ''),
			COALESCE(blocking_activity.query, ''),
			COALESCE(blocked_activity.application_name, ''),
			COALESCE(blocking_activity.application_name, '')
		FROM pg_catalog.pg_locks blocked_locks
		JOIN pg_catalog.pg_stat_activity blocked_activity ON blocked_activity.pid = blocked_locks.pid
		JOIN pg_catalog.pg_locks blocking_locks
			ON blocking_locks.locktype = blocked_locks.locktype
			AND blocking_locks.DATABASE IS NOT DISTINCT FROM blocked_locks.DATABASE
			AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation
			AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page
			AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple
			AND blocking_locks.virtualxid IS NOT DISTINCT FROM blocked_locks.virtualxid
			AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid
			AND blocking_locks.classid IS NOT DISTINCT FROM blocked_locks.classid
			AND blocking_locks.objid IS NOT DISTINCT FROM blocked_locks.objid
			AND blocking_locks.objsubid IS NOT DISTINCT FROM blocked_locks.objsubid
			AND blocking_locks.pid != blocked_locks.pid
		JOIN pg_catalog.pg_stat_activity blocking_activity ON blocking_activity.pid = blocking_locks.pid
		WHERE NOT blocked_locks.GRANTED`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []BlockedQuery
	for rows.Next() {
		var q BlockedQuery
		if err := rows.Scan(
			&q.BlockedPID, &q.BlockedUser, &q.BlockingPID, &q.BlockingUser,
			&q.BlockedStatement, &q.BlockingStatement,
			&q.BlockedApplication, &q.BlockingApplication,
		); err != nil {
			return nil, err
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

// ConnectionState is one (state, count) pair from pg_stat_activity.
type ConnectionState struct {
	State string
	Count int
}

// ConnectionsResult holds per-state counts plus the max_connections limit.
type ConnectionsResult struct {
	States     []ConnectionState
	TotalUsed  int
	MaxAllowed int
	UsagePct   float64
}

func FetchConnections(ctx context.Context, db *sql.DB) (ConnectionsResult, error) {
	var result ConnectionsResult
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(state, 'background'), count(*)
		FROM pg_stat_activity
		GROUP BY state
		ORDER BY count(*) DESC`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var s ConnectionState
		if err := rows.Scan(&s.State, &s.Count); err != nil {
			return result, err
		}
		result.States = append(result.States, s)
		result.TotalUsed += s.Count
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	var maxStr string
	if err := db.QueryRowContext(ctx, "SHOW max_connections").Scan(&maxStr); err != nil {
		return result, err
	}
	result.MaxAllowed, _ = strconv.Atoi(maxStr)
	if result.MaxAllowed > 0 {
		result.UsagePct = float64(result.TotalUsed) / float64(result.MaxAllowed) * 100
	}
	return result, nil
}

// AutovacuumCost is the effective vacuum cost-based delay for a table's autovacuum,
// compared with what the global settings would give it.
//
// Autovacuum sleeps cost_delay ms every time it accumulates cost_limit "cost units"
// (vacuum_cost_page_hit/miss/dirty per page), so its maximum speed is
// cost_limit / cost_delay units per millisecond. A per-table override that raises the
// delay or lowers the limit can make a big table's vacuum many times slower.
type AutovacuumCost struct {
	DelayMS     float64
	DelaySource string // SourceTable or SourceGlobal
	Limit       float64
	LimitSource string
	// ThroughputPerSec is cost units per second (limit / delay × 1000); nil when
	// unthrottled (delay 0). MaxReadMBPerSec converts it with vacuum_cost_page_miss
	// into the read rate it allows for pages not in shared_buffers.
	ThroughputPerSec       *float64
	MaxReadMBPerSec        *float64
	GlobalDelayMS          float64
	GlobalLimit            float64
	GlobalThroughputPerSec *float64
	GlobalMaxReadMBPerSec  *float64
	// SlowerThanGlobal is set when the table's overrides throttle it more than the
	// global settings would; SlowdownFactor is global ÷ table throughput (nil when the
	// global setting is unthrottled, i.e. infinitely faster).
	SlowerThanGlobal bool
	SlowdownFactor   *float64
}

// autovacuumCostGlobals are the GUCs effectiveAutovacuumCost reads.
var autovacuumCostGlobals = []string{
	"autovacuum_vacuum_cost_delay", "autovacuum_vacuum_cost_limit",
	"vacuum_cost_delay", "vacuum_cost_limit", "vacuum_cost_page_miss",
}

// effectiveAutovacuumCost resolves the delay/limit autovacuum uses for a table:
// the table reloption when set (and not -1), else autovacuum_vacuum_cost_*, which
// itself falls back to vacuum_cost_* when -1.
func effectiveAutovacuumCost(tableOptions, globals map[string]string) AutovacuumCost {
	parse := func(raw string) (float64, bool) {
		value, err := strconv.ParseFloat(raw, 64)
		return value, err == nil
	}
	globalValue := func(autovacuumName, vacuumName string) float64 {
		if value, ok := parse(globals[autovacuumName]); ok && value >= 0 {
			return value
		}
		value, _ := parse(globals[vacuumName])
		return value
	}
	cost := AutovacuumCost{
		GlobalDelayMS: globalValue("autovacuum_vacuum_cost_delay", "vacuum_cost_delay"),
		GlobalLimit:   globalValue("autovacuum_vacuum_cost_limit", "vacuum_cost_limit"),
		DelaySource:   SourceGlobal,
		LimitSource:   SourceGlobal,
	}
	cost.DelayMS, cost.Limit = cost.GlobalDelayMS, cost.GlobalLimit
	if value, ok := parse(tableOptions["autovacuum_vacuum_cost_delay"]); ok && value >= 0 {
		cost.DelayMS, cost.DelaySource = value, SourceTable
	}
	if value, ok := parse(tableOptions["autovacuum_vacuum_cost_limit"]); ok && value > 0 {
		cost.Limit, cost.LimitSource = value, SourceTable
	}
	pageMiss, _ := parse(globals["vacuum_cost_page_miss"])
	rates := func(delayMS, limit float64) (*float64, *float64) {
		if delayMS <= 0 {
			return nil, nil
		}
		perSecond := limit / delayMS * 1000
		var readMB *float64
		if pageMiss > 0 {
			mb := perSecond / pageMiss * 8192 / (1024 * 1024)
			readMB = &mb
		}
		return &perSecond, readMB
	}
	cost.ThroughputPerSec, cost.MaxReadMBPerSec = rates(cost.DelayMS, cost.Limit)
	cost.GlobalThroughputPerSec, cost.GlobalMaxReadMBPerSec = rates(cost.GlobalDelayMS, cost.GlobalLimit)
	switch {
	case cost.ThroughputPerSec == nil: // unthrottled: never slower
	case cost.GlobalThroughputPerSec == nil:
		cost.SlowerThanGlobal = true
	case *cost.ThroughputPerSec < *cost.GlobalThroughputPerSec:
		cost.SlowerThanGlobal = true
		factor := *cost.GlobalThroughputPerSec / *cost.ThroughputPerSec
		cost.SlowdownFactor = &factor
	}
	return cost
}

// AutovacuumTable holds per-table vacuum statistics from pg_stat_user_tables, with the
// table's autovacuum overrides and how close it is to its next autovacuum.
type AutovacuumTable struct {
	SchemaName      string
	TableName       string
	DeadTuples      int64
	LiveTuples      int64
	DeadPct         *float64
	TotalSize       string
	TotalSizeBytes  int64
	ModSinceAnalyze int64
	LastVacuum      *time.Time
	LastAnalyze     *time.Time
	LastAutovacuum  *time.Time
	LastAutoanalyze *time.Time
	AutovacuumCount int64
	// Overrides are the table's autovacuum_* reloptions (empty when it uses the globals).
	Overrides         map[string]string
	AutovacuumEnabled bool
	Cost              AutovacuumCost
	// TriggerThreshold is the dead-tuple count that triggers autovacuum
	// (base + scale_factor × reltuples, with the table's overrides); TriggerRatio is
	// DeadTuples ÷ TriggerThreshold — ≥ 1 means a vacuum is due. nil when the
	// threshold is 0.
	TriggerThreshold float64
	TriggerRatio     *float64
}

// HasOverrides reports whether the table sets any autovacuum_* reloption.
func (t AutovacuumTable) HasOverrides() bool { return len(t.Overrides) > 0 }

func FetchAutovacuum(ctx context.Context, db *sql.DB, limit int) ([]AutovacuumTable, error) {
	globals, _, err := fetchGlobalSettings(ctx, db, append(slices.Clone(autovacuumThresholdGlobals), autovacuumCostGlobals...))
	if err != nil {
		return nil, err
	}
	majorVersion := pgMajorVersion()
	// pg_class.relallfrozen was added in PG18 (feeds the insert threshold); NULL before.
	relallfrozenColumn := "NULL::bigint"
	if majorVersion >= 18 {
		relallfrozenColumn = "c.relallfrozen::bigint"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT
			s.schemaname,
			s.relname,
			s.n_dead_tup,
			s.n_live_tup,
			CASE WHEN s.n_live_tup + s.n_dead_tup = 0 THEN NULL
				 ELSE ROUND(100.0 * s.n_dead_tup / (s.n_live_tup + s.n_dead_tup), 1)
			END AS dead_pct,
			pg_total_relation_size(c.oid),
			pg_size_pretty(pg_total_relation_size(c.oid)),
			s.n_mod_since_analyze,
			s.n_ins_since_vacuum,
			s.last_vacuum,
			s.last_analyze,
			s.last_autovacuum,
			s.last_autoanalyze,
			s.autovacuum_count,
			COALESCE(c.reloptions, '{}'),
			c.reltuples::float8,
			c.relpages::bigint,
			`+relallfrozenColumn+`,
			age(c.relfrozenxid),
			mxid_age(c.relminmxid)
		FROM pg_stat_user_tables s
		JOIN pg_class c ON c.oid = s.relid
		ORDER BY s.n_dead_tup DESC
		LIMIT NULLIF($1::int, 0)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AutovacuumTable
	for rows.Next() {
		var t AutovacuumTable
		var deadPct sql.NullFloat64
		var reloptions pq.StringArray
		in := autovacuumThresholdInputs{MajorVersion: majorVersion, GlobalSettings: globals}
		if err := rows.Scan(
			&t.SchemaName, &t.TableName, &t.DeadTuples, &t.LiveTuples, &deadPct,
			&t.TotalSizeBytes, &t.TotalSize, &t.ModSinceAnalyze, &in.InsertsSinceVacuum,
			&t.LastVacuum, &t.LastAnalyze, &t.LastAutovacuum, &t.LastAutoanalyze,
			&t.AutovacuumCount,
			&reloptions, &in.Reltuples, &in.Relpages, &in.Relallfrozen, &in.XIDAge, &in.MXIDAge,
		); err != nil {
			return nil, err
		}
		if deadPct.Valid {
			t.DeadPct = &deadPct.Float64
		}
		in.TableOptions = parseReloptions(reloptions)
		in.DeadTuples, in.ModsSinceAnalyze = t.DeadTuples, t.ModSinceAnalyze
		t.Overrides = map[string]string{}
		for key, value := range in.TableOptions {
			if strings.HasPrefix(key, "autovacuum_") {
				t.Overrides[key] = value
			}
		}
		thresholds := computeAutovacuumThresholds(in)
		t.AutovacuumEnabled = thresholds.AutovacuumEnabled
		t.TriggerThreshold = thresholds.DeadTuples.Threshold
		if t.TriggerThreshold > 0 {
			ratio := float64(t.DeadTuples) / t.TriggerThreshold
			t.TriggerRatio = &ratio
		}
		t.Cost = effectiveAutovacuumCost(in.TableOptions, globals)
		results = append(results, t)
	}
	return results, rows.Err()
}

// ToastTable holds TOAST-related stats for a user table that has actual TOAST data on disk.
type ToastTable struct {
	SchemaName      string
	TableName       string
	ToastRelname    string // e.g. "pg_toast_16384" — used for VACUUM, not displayed
	ToastSizeBytes  int64
	ToastSizePretty string
	ToastPct        float64 // 100 * toast_size / total_size; 0 if total == 0
	ToastDeadTuples int64
	BlksRead        int64
	BlksHit         int64
	CacheHitPct     *float64 // nil when no I/O has occurred yet
	LastAutovacuum  *time.Time
	ToastColumns    string // comma-separated columns with TOAST-eligible storage (EXTENDED/EXTERNAL/MAIN)
}

func FetchToastTables(ctx context.Context, db *sql.DB, limit int) ([]ToastTable, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			n.nspname,
			c.relname,
			t.relname AS toast_relname,
			pg_relation_size(t.oid),
			pg_size_pretty(pg_relation_size(t.oid)),
			CASE WHEN pg_total_relation_size(c.oid) > 0
			     THEN ROUND(100.0 * pg_relation_size(t.oid) / pg_total_relation_size(c.oid), 1)
			     ELSE 0 END,
			COALESCE(s.n_dead_tup, 0),
			COALESCE(io.toast_blks_read, 0),
			COALESCE(io.toast_blks_hit, 0),
			CASE WHEN COALESCE(io.toast_blks_hit, 0) + COALESCE(io.toast_blks_read, 0) > 0
			     THEN ROUND(100.0 * io.toast_blks_hit /
			               (io.toast_blks_hit + io.toast_blks_read), 1)
			     ELSE NULL END,
			s.last_autovacuum,
			-- Columns whose storage strategy can produce TOAST data:
			-- e=EXTENDED (compress then out-of-line), x=EXTERNAL (out-of-line only), m=MAIN (compress in-line first).
			-- p=PLAIN (fixed-width types like int) never goes to TOAST and is excluded.
			COALESCE((
				SELECT string_agg(a.attname, ', ' ORDER BY a.attnum)
				FROM pg_attribute a
				WHERE a.attrelid = c.oid
				  AND a.attlen = -1
				  AND a.attstorage IN ('e', 'x', 'm')
				  AND a.attnum > 0
				  AND NOT a.attisdropped
			), '')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class t ON t.oid = c.reltoastrelid
		LEFT JOIN pg_stat_all_tables s ON s.relid = t.oid
		LEFT JOIN pg_statio_user_tables io ON io.relid = c.oid
		WHERE n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
		  AND c.relkind = 'r'
		  AND pg_relation_size(t.oid) > 0
		ORDER BY pg_relation_size(t.oid) DESC
		LIMIT NULLIF($1::int, 0)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []ToastTable{}
	for rows.Next() {
		var tt ToastTable
		var cacheHit sql.NullFloat64
		if err := rows.Scan(
			&tt.SchemaName, &tt.TableName, &tt.ToastRelname,
			&tt.ToastSizeBytes, &tt.ToastSizePretty, &tt.ToastPct,
			&tt.ToastDeadTuples,
			&tt.BlksRead, &tt.BlksHit, &cacheHit,
			&tt.LastAutovacuum,
			&tt.ToastColumns,
		); err != nil {
			return nil, err
		}
		if cacheHit.Valid {
			tt.CacheHitPct = &cacheHit.Float64
		}
		results = append(results, tt)
	}
	return results, rows.Err()
}

// IndexUsage holds per-index usage statistics from pg_stat_user_indexes.
type IndexUsage struct {
	SchemaName   string
	TableName    string
	IndexName    string
	IndexColumns string
	IsValid      bool
	IdxScan      int64
	IdxTupRead   int64
	IdxTupFetch  int64
	IndexSize    string
}

func FetchIndexUsage(ctx context.Context, db *sql.DB, limit int) ([]IndexUsage, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			s.schemaname,
			s.relname AS tablename,
			s.indexrelname AS indexname,
			s.idx_scan,
			s.idx_tup_read,
			s.idx_tup_fetch,
			pg_size_pretty(pg_relation_size(s.indexrelid)) AS index_size,
			i.indisvalid AS is_valid,
			COALESCE((
				SELECT string_agg(a.attname, ', ' ORDER BY x.ord)
				FROM pg_index ix
				JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS x(attnum, ord) ON true
				JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = x.attnum
				WHERE ix.indexrelid = s.indexrelid AND x.attnum > 0
			), '(expression)') AS index_columns
		FROM pg_stat_user_indexes s
		JOIN pg_index i ON i.indexrelid = s.indexrelid
		ORDER BY s.idx_scan ASC
		LIMIT NULLIF($1::int, 0)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []IndexUsage
	for rows.Next() {
		var idx IndexUsage
		if err := rows.Scan(
			&idx.SchemaName, &idx.TableName, &idx.IndexName,
			&idx.IdxScan, &idx.IdxTupRead, &idx.IdxTupFetch,
			&idx.IndexSize, &idx.IsValid, &idx.IndexColumns,
		); err != nil {
			return nil, err
		}
		results = append(results, idx)
	}
	return results, rows.Err()
}

// CacheHitTable holds buffer cache hit statistics from pg_statio_user_tables.
type CacheHitTable struct {
	TableName        string
	HeapBlksRead     int64
	HeapBlksHit      int64
	CacheHitRatio    float64
	IdxBlksRead      int64
	IdxBlksHit       int64
	IdxCacheHitRatio float64
}

func FetchCacheHit(ctx context.Context, db *sql.DB, limit int) ([]CacheHitTable, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			relname,
			heap_blks_read,
			heap_blks_hit,
			CASE WHEN heap_blks_hit + heap_blks_read = 0 THEN 0
				 ELSE ROUND(100.0 * heap_blks_hit / (heap_blks_hit + heap_blks_read), 2)
			END AS cache_hit_ratio,
			idx_blks_read,
			idx_blks_hit,
			CASE WHEN idx_blks_hit + idx_blks_read = 0 THEN 0
				 ELSE ROUND(100.0 * idx_blks_hit / (idx_blks_hit + idx_blks_read), 2)
			END AS idx_cache_hit_ratio
		FROM pg_statio_user_tables
		ORDER BY heap_blks_read DESC
		LIMIT NULLIF($1::int, 0)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []CacheHitTable
	for rows.Next() {
		var t CacheHitTable
		if err := rows.Scan(
			&t.TableName, &t.HeapBlksRead, &t.HeapBlksHit, &t.CacheHitRatio,
			&t.IdxBlksRead, &t.IdxBlksHit, &t.IdxCacheHitRatio,
		); err != nil {
			return nil, err
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

// WaitEvent is one grouped wait event from pg_stat_activity.
type WaitEvent struct {
	EventType string
	Event     string
	Count     int
	Pct       float64
}

func FetchWaitEvents(ctx context.Context, db *sql.DB) ([]WaitEvent, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			COALESCE(wait_event_type, 'CPU') AS event_type,
			COALESCE(wait_event, '-') AS event,
			count(*) AS count,
			COALESCE(
				ROUND(100.0 * count(*) / NULLIF(SUM(count(*)) OVER(), 0), 1),
				0
			) AS pct
		FROM pg_stat_activity
		WHERE state = 'active' OR wait_event IS NOT NULL
		GROUP BY wait_event_type, wait_event
		ORDER BY count DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []WaitEvent
	for rows.Next() {
		var e WaitEvent
		if err := rows.Scan(&e.EventType, &e.Event, &e.Count, &e.Pct); err != nil {
			return nil, err
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

// QueryLoad is one row from pg_stat_statements sorted by total execution time.
type QueryLoad struct {
	QueryID  string
	Query    string
	Calls    int
	TotalMS  float64
	MeanMS   float64
	BufferMB float64
	TempMB   float64
	LoadPct  float64
}

func FetchQueryLoad(ctx context.Context, db *sql.DB, limit int) ([]QueryLoad, error) {
	pgss, err := requirePgStatStatements(ctx, db)
	if err != nil {
		return nil, err
	}
	// Column names come from pgStatStatementsColumns (fixed identifiers), never user input.
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			queryid::text,
			query,
			calls,
			ROUND(%[1]s::numeric) AS total_ms,
			ROUND(%[2]s::numeric, 2) AS mean_ms,
			ROUND(((shared_blks_hit + shared_blks_read) * 8.0 / 1024)::numeric, 1) AS buffer_mb,
			ROUND((temp_blks_written * 8.0 / 1024)::numeric, 1) AS temp_mb,
			COALESCE(
				ROUND((100.0 * %[1]s / NULLIF(SUM(%[1]s) OVER(), 0))::numeric, 1),
				0
			) AS load_pct
		FROM %[3]s
		ORDER BY %[1]s DESC
		LIMIT NULLIF($1::int, 0)`,
		pgss.TotalTimeColumn, pgss.MeanTimeColumn, pgss.Relation), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []QueryLoad
	for rows.Next() {
		var q QueryLoad
		if err := rows.Scan(
			&q.QueryID, &q.Query, &q.Calls,
			&q.TotalMS, &q.MeanMS, &q.BufferMB, &q.TempMB, &q.LoadPct,
		); err != nil {
			return nil, err
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

// ReplicationSlot holds information from pg_replication_slots.
type ReplicationSlot struct {
	SlotName      string
	Plugin        string
	SlotType      string
	Database      *string
	Active        bool
	ActivePID     *int
	WALLag        string
	SafeWALSize   *string // PG 13+; nil when slot has no confirmed_flush_lsn
	TwoPhase      bool    // PG 15+
	Failover      *bool   // PG 17+; logical slots only, nil on older PG
	Synced        *bool   // PG 17+; standby-side physical slots only, nil otherwise
	InactiveSince *string // PG 18+; nil on older PG or when slot is active
}

func FetchReplicationSlots(ctx context.Context, db *sql.DB) ([]ReplicationSlot, error) {
	// two_phase was added to pg_replication_slots in PostgreSQL 15.
	twoPhaseExpr := "false AS two_phase"
	if pgMajorVersion() >= 15 {
		twoPhaseExpr = "two_phase"
	}
	// failover and synced were added in PostgreSQL 17.
	failoverExpr := "NULL::boolean AS failover"
	syncedExpr := "NULL::boolean AS synced"
	if pgMajorVersion() >= 17 {
		failoverExpr = "failover"
		syncedExpr = "synced"
	}
	// inactive_since was added in PostgreSQL 18.
	inactiveSinceExpr := "NULL::text AS inactive_since"
	if pgMajorVersion() >= 18 {
		inactiveSinceExpr = "inactive_since::text"
	}
	query := `
		SELECT
			slot_name,
			COALESCE(plugin, ''),
			slot_type,
			database,
			active,
			active_pid,
			pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS wal_lag,
			pg_size_pretty(safe_wal_size) AS safe_wal_size,
			` + twoPhaseExpr + `,
			` + failoverExpr + `,
			` + syncedExpr + `,
			` + inactiveSinceExpr + `
		FROM pg_replication_slots`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ReplicationSlot
	for rows.Next() {
		var s ReplicationSlot
		var walLag, safeWAL, inactiveSince sql.NullString
		var failover, synced sql.NullBool
		if err := rows.Scan(
			&s.SlotName, &s.Plugin, &s.SlotType, &s.Database, &s.Active, &s.ActivePID,
			&walLag, &safeWAL, &s.TwoPhase, &failover, &synced, &inactiveSince,
		); err != nil {
			return nil, err
		}
		if walLag.Valid {
			s.WALLag = walLag.String
		}
		if safeWAL.Valid {
			s.SafeWALSize = &safeWAL.String
		}
		if failover.Valid {
			s.Failover = &failover.Bool
		}
		if synced.Valid {
			s.Synced = &synced.Bool
		}
		if inactiveSince.Valid {
			s.InactiveSince = &inactiveSince.String
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// User holds a PostgreSQL login role and its privileges.
type User struct {
	RoleName    string
	Superuser   bool
	CreateDB    bool
	CreateRole  bool
	Replication bool
	ConnLimit   int
	ValidUntil  *time.Time
	MemberOf    string
}

func FetchUsers(ctx context.Context, db *sql.DB) ([]User, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.rolname,
			   r.rolsuper,
			   r.rolcreatedb,
			   r.rolcreaterole,
			   r.rolreplication,
			   r.rolconnlimit,
			   r.rolvaliduntil,
			   COALESCE(string_agg(m.rolname, ', ' ORDER BY m.rolname), '') AS member_of
		FROM pg_roles r
		LEFT JOIN pg_auth_members am ON am.member = r.oid
		LEFT JOIN pg_roles m ON m.oid = am.roleid
		WHERE r.rolcanlogin = true
		GROUP BY r.rolname, r.rolsuper, r.rolcreatedb, r.rolcreaterole, r.rolreplication,
				 r.rolconnlimit, r.rolvaliduntil
		ORDER BY r.rolname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.RoleName, &u.Superuser, &u.CreateDB, &u.CreateRole, &u.Replication,
			&u.ConnLimit, &u.ValidUntil, &u.MemberOf,
		); err != nil {
			return nil, err
		}
		results = append(results, u)
	}
	return results, rows.Err()
}

// Role holds a PostgreSQL group role (non-login) and its members.
type Role struct {
	RoleName   string
	Superuser  bool
	Inherit    bool
	CreateRole bool
	CreateDB   bool
	Members    string
}

func FetchRoles(ctx context.Context, db *sql.DB) ([]Role, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.rolname,
			   r.rolsuper,
			   r.rolinherit,
			   r.rolcreaterole,
			   r.rolcreatedb,
			   COALESCE(string_agg(m.rolname, ', ' ORDER BY m.rolname), '') AS members
		FROM pg_roles r
		LEFT JOIN pg_auth_members am ON am.roleid = r.oid
		LEFT JOIN pg_roles m ON m.oid = am.member
		WHERE r.rolcanlogin = false AND r.rolname NOT LIKE 'pg_%'
		GROUP BY r.rolname, r.rolsuper, r.rolinherit, r.rolcreaterole, r.rolcreatedb
		ORDER BY r.rolname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.RoleName, &r.Superuser, &r.Inherit, &r.CreateRole, &r.CreateDB, &r.Members); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Extension holds an installed PostgreSQL extension.
type Extension struct {
	Name        string
	Version     string
	Schema      string
	Description string
}

func FetchExtensions(ctx context.Context, db *sql.DB) ([]Extension, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT e.extname AS name,
			   e.extversion AS version,
			   n.nspname AS schema,
			   COALESCE(ae.comment, '-') AS description
		FROM pg_extension e
		JOIN pg_namespace n ON n.oid = e.extnamespace
		LEFT JOIN pg_available_extensions ae ON ae.name = e.extname
		ORDER BY e.extname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []Extension
	for rows.Next() {
		var e Extension
		if err := rows.Scan(&e.Name, &e.Version, &e.Schema, &e.Description); err != nil {
			return nil, err
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

// PgSetting holds one row from pg_settings.
type PgSetting struct {
	Name           string
	Setting        string
	Unit           string
	Category       string
	Source         string
	Description    string
	PendingRestart bool
}

// PgConfigScope selects which pg_settings rows FetchPgConfig returns.
type PgConfigScope string

const (
	PgConfigScopeAll      PgConfigScope = "all"
	PgConfigScopeKey      PgConfigScope = "key"      // keyPgSettings only
	PgConfigScopeModified PgConfigScope = "modified" // changed from the built-in default
)

// keyPgSettings are the parameters a tuning review looks at first: memory, autovacuum,
// checkpoints/WAL, planner costs, timeouts and logging. Names that don't exist on the
// connected version are simply not matched.
var keyPgSettings = []string{
	// memory
	"max_connections", "shared_buffers", "effective_cache_size", "work_mem", "maintenance_work_mem",
	"autovacuum_work_mem", "temp_buffers", "wal_buffers", "huge_pages", "hash_mem_multiplier",
	// autovacuum
	"autovacuum", "autovacuum_max_workers", "autovacuum_naptime",
	"autovacuum_vacuum_threshold", "autovacuum_vacuum_scale_factor", "autovacuum_vacuum_max_threshold",
	"autovacuum_vacuum_insert_threshold", "autovacuum_vacuum_insert_scale_factor",
	"autovacuum_analyze_threshold", "autovacuum_analyze_scale_factor",
	"autovacuum_vacuum_cost_delay", "autovacuum_vacuum_cost_limit", "vacuum_cost_delay", "vacuum_cost_limit",
	"autovacuum_freeze_max_age", "autovacuum_multixact_freeze_max_age", "vacuum_freeze_min_age",
	"vacuum_freeze_table_age", "vacuum_failsafe_age",
	// checkpoints and WAL
	"checkpoint_timeout", "checkpoint_completion_target", "max_wal_size", "min_wal_size",
	"wal_level", "wal_compression", "full_page_writes", "synchronous_commit", "wal_writer_delay",
	"max_wal_senders", "max_replication_slots", "max_slot_wal_keep_size", "wal_keep_size",
	"hot_standby_feedback", "archive_mode",
	// planner and parallelism
	"random_page_cost", "seq_page_cost", "effective_io_concurrency", "maintenance_io_concurrency",
	"default_statistics_target", "jit", "max_worker_processes", "max_parallel_workers",
	"max_parallel_workers_per_gather", "max_parallel_maintenance_workers",
	// timeouts
	"statement_timeout", "lock_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout",
	"transaction_timeout", "deadlock_timeout",
	// logging and statistics
	"log_min_duration_statement", "log_autovacuum_min_duration", "log_checkpoints", "log_lock_waits",
	"log_temp_files", "track_io_timing", "track_activity_query_size", "shared_preload_libraries",
	"pg_stat_statements.max", "pg_stat_statements.track",
}

// PgConfigOptions filters and pages FetchPgConfig. A zero Scope means all, a zero Limit no limit.
type PgConfigOptions struct {
	Filter string // case-insensitive substring of the name or category
	Scope  PgConfigScope
	Limit  int
	Offset int
}

// PgConfigResult is one page of settings plus the number of rows matching the filters.
type PgConfigResult struct {
	Settings []PgSetting
	Total    int
}

// FetchPgConfig returns the pg_settings rows matching opts.
func FetchPgConfig(ctx context.Context, db *sql.DB, opts PgConfigOptions) (PgConfigResult, error) {
	var result PgConfigResult
	scope := opts.Scope
	if scope == "" {
		scope = PgConfigScopeAll
	}
	// "modified" excludes sources that aren't a DBA's choice: 'override' (computed by
	// the server, e.g. wal_buffers=-1) and 'client' (sent by this very connection).
	const matching = `
		WITH matching AS (
			SELECT name, setting, COALESCE(unit, '') AS unit, category, source,
			       COALESCE(short_desc, '') AS description, pending_restart
			FROM pg_settings
			WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR category ILIKE '%' || $1 || '%')
			  AND ($2 <> 'key' OR name = ANY($3))
			  AND ($2 <> 'modified' OR source NOT IN ('default', 'override', 'client'))
		)`
	args := []any{opts.Filter, string(scope), pq.Array(keyPgSettings)}
	rows, err := db.QueryContext(ctx, matching+`
		SELECT m.*, (SELECT count(*) FROM matching)
		FROM matching m
		ORDER BY category, name
		LIMIT NULLIF($4::int, 0) OFFSET $5`,
		append(args, opts.Limit, max(opts.Offset, 0))...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var s PgSetting
		if err := rows.Scan(&s.Name, &s.Setting, &s.Unit, &s.Category, &s.Source, &s.Description,
			&s.PendingRestart, &result.Total); err != nil {
			return result, err
		}
		result.Settings = append(result.Settings, s)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	// An offset past the last row returns no rows, so the total has to be counted apart.
	if len(result.Settings) == 0 && opts.Offset > 0 {
		err = db.QueryRowContext(ctx, matching+` SELECT count(*) FROM matching`, args...).Scan(&result.Total)
	}
	return result, err
}

// SchemaColumn holds one column from information_schema.columns.
type SchemaColumn struct {
	ColumnName    string
	DataType      string
	MaxLength     string
	IsNullable    string
	ColumnDefault string
}

// SchemaTable holds a table with its columns, used by the MCP schema tool.
type SchemaTable struct {
	SchemaName string
	TableName  string
	SizePretty string
	EstRows    int64
	Columns    []SchemaColumn
}

// FetchSchema returns all user tables for the given schema with their columns.
// It uses a single JOIN query to avoid N+1 round-trips.
func FetchSchema(ctx context.Context, db *sql.DB, schema string) ([]SchemaTable, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			t.table_schema, t.table_name,
			pg_size_pretty(pg_total_relation_size(
				quote_ident(t.table_schema)||'.'||quote_ident(t.table_name)
			)) AS size,
			c.reltuples::bigint AS est_rows,
			COALESCE(col.column_name, ''),
			COALESCE(col.data_type, ''),
			COALESCE(col.character_maximum_length::text, col.numeric_precision::text, '') AS length,
			COALESCE(col.is_nullable, ''),
			COALESCE(col.column_default, '')
		FROM information_schema.tables t
		JOIN pg_class c ON c.relname = t.table_name
		JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = t.table_schema
		LEFT JOIN information_schema.columns col
			ON col.table_schema = t.table_schema AND col.table_name = t.table_name
		WHERE t.table_schema = $1
		  AND t.table_type = 'BASE TABLE'
		ORDER BY t.table_schema, t.table_name, col.ordinal_position`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tableMap := make(map[string]*SchemaTable)
	var tableOrder []string
	for rows.Next() {
		var schemaName, tableName, size, colName, dataType, length, isNullable, colDefault string
		var estRows int64
		if err := rows.Scan(&schemaName, &tableName, &size, &estRows, &colName, &dataType, &length, &isNullable, &colDefault); err != nil {
			return nil, err
		}
		key := schemaName + "." + tableName
		if _, exists := tableMap[key]; !exists {
			tableMap[key] = &SchemaTable{
				SchemaName: schemaName,
				TableName:  tableName,
				SizePretty: size,
				EstRows:    estRows,
			}
			tableOrder = append(tableOrder, key)
		}
		if colName != "" {
			tableMap[key].Columns = append(tableMap[key].Columns, SchemaColumn{
				ColumnName:    colName,
				DataType:      dataType,
				MaxLength:     length,
				IsNullable:    isNullable,
				ColumnDefault: colDefault,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	results := make([]SchemaTable, 0, len(tableOrder))
	for _, key := range tableOrder {
		results = append(results, *tableMap[key])
	}
	return results, nil
}

// --- Autovacuum detail ---

// AutovacuumDetailStats holds per-table vacuum statistics for the detail view.
type AutovacuumDetailStats struct {
	SchemaName        string
	TableName         string
	LiveTuples        int64
	DeadTuples        int64
	ModSinceAnalyze   int64
	VacuumCount       int64
	AutovacuumCount   int64
	AnalyzeCount      int64
	AutoanalyzeCount  int64
	TableSize         string
	TotalSize         string
	ToastAndIndexSize string
	FrozenXIDAge      int64
	MXIDAge           int64
	LastVacuum        *time.Time
	LastAutovacuum    *time.Time
	LastAnalyze       *time.Time
	LastAutoanalyze   *time.Time
}

func FetchAutovacuumDetail(ctx context.Context, db *sql.DB, schema, table string) (AutovacuumDetailStats, error) {
	var d AutovacuumDetailStats
	d.SchemaName = schema
	d.TableName = table
	err := db.QueryRowContext(ctx, `
		SELECT
			s.n_live_tup,
			s.n_dead_tup,
			s.n_mod_since_analyze,
			s.last_vacuum,
			s.last_autovacuum,
			s.last_analyze,
			s.last_autoanalyze,
			s.vacuum_count,
			s.autovacuum_count,
			s.analyze_count,
			s.autoanalyze_count,
			pg_size_pretty(pg_relation_size(c.oid))                               AS table_size,
			pg_size_pretty(pg_total_relation_size(c.oid))                         AS total_size,
			pg_size_pretty(pg_total_relation_size(c.oid) - pg_relation_size(c.oid)) AS toast_and_index_size,
			age(c.relfrozenxid)                                                   AS frozen_xid_age,
			mxid_age(c.relminmxid)                                                AS mxid_age
		FROM pg_stat_user_tables s
		JOIN pg_class c ON c.oid = s.relid
		WHERE s.schemaname = $1 AND s.relname = $2`,
		schema, table,
	).Scan(
		&d.LiveTuples, &d.DeadTuples, &d.ModSinceAnalyze,
		&d.LastVacuum, &d.LastAutovacuum, &d.LastAnalyze, &d.LastAutoanalyze,
		&d.VacuumCount, &d.AutovacuumCount, &d.AnalyzeCount, &d.AutoanalyzeCount,
		&d.TableSize, &d.TotalSize, &d.ToastAndIndexSize,
		&d.FrozenXIDAge, &d.MXIDAge,
	)
	return d, err
}

// AutovacuumParam holds one autovacuum parameter with its table-level and global value.
type AutovacuumParam struct {
	Name        string
	TableValue  string // "" means inherited from global
	GlobalValue string
	Unit        string
}

// autovacuumParamSpec pairs a table reloption with the GUC that supplies its value
// when the table doesn't override it. The names differ for the freeze ages:
// reloption autovacuum_freeze_min_age falls back to GUC vacuum_freeze_min_age.
type autovacuumParamSpec struct {
	Reloption    string
	GlobalName   string
	MinPGVersion int
}

var autovacuumParamSpecs = []autovacuumParamSpec{
	{"autovacuum_enabled", "autovacuum", 0},
	{"autovacuum_vacuum_threshold", "autovacuum_vacuum_threshold", 0},
	{"autovacuum_vacuum_scale_factor", "autovacuum_vacuum_scale_factor", 0},
	// autovacuum_vacuum_max_threshold caps the dead-tuple threshold; added in PG18.
	{"autovacuum_vacuum_max_threshold", "autovacuum_vacuum_max_threshold", 18},
	{"autovacuum_vacuum_insert_threshold", "autovacuum_vacuum_insert_threshold", 0},
	{"autovacuum_vacuum_insert_scale_factor", "autovacuum_vacuum_insert_scale_factor", 0},
	{"autovacuum_analyze_threshold", "autovacuum_analyze_threshold", 0},
	{"autovacuum_analyze_scale_factor", "autovacuum_analyze_scale_factor", 0},
	{"autovacuum_vacuum_cost_delay", "autovacuum_vacuum_cost_delay", 0},
	{"autovacuum_vacuum_cost_limit", "autovacuum_vacuum_cost_limit", 0},
	{"autovacuum_freeze_min_age", "vacuum_freeze_min_age", 0},
	{"autovacuum_freeze_max_age", "autovacuum_freeze_max_age", 0},
	{"autovacuum_freeze_table_age", "vacuum_freeze_table_age", 0},
	{"autovacuum_multixact_freeze_min_age", "vacuum_multixact_freeze_min_age", 0},
	{"autovacuum_multixact_freeze_max_age", "autovacuum_multixact_freeze_max_age", 0},
	{"autovacuum_multixact_freeze_table_age", "vacuum_multixact_freeze_table_age", 0},
}

// parseReloptions turns pg_class.reloptions ({"key=value", …}) into a map.
func parseReloptions(reloptions []string) map[string]string {
	options := make(map[string]string, len(reloptions))
	for _, option := range reloptions {
		if key, value, ok := strings.Cut(option, "="); ok {
			options[key] = value
		}
	}
	return options
}

func fetchReloptions(ctx context.Context, db *sql.DB, schema, table string) (map[string]string, error) {
	var reloptions pq.StringArray
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(c.reloptions, '{}')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2`,
		schema, table,
	).Scan(&reloptions)
	if err != nil {
		return nil, err
	}
	return parseReloptions(reloptions), nil
}

// fetchGlobalSettings returns pg_settings.setting/unit for the given GUC names;
// names that don't exist on the connected version are simply absent.
func fetchGlobalSettings(ctx context.Context, db *sql.DB, names []string) (map[string]string, map[string]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT name, setting, COALESCE(unit, '') FROM pg_settings WHERE name = ANY($1)`,
		pq.Array(names))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	settings := make(map[string]string, len(names))
	units := make(map[string]string, len(names))
	for rows.Next() {
		var name, setting, unit string
		if err := rows.Scan(&name, &setting, &unit); err != nil {
			return nil, nil, err
		}
		settings[name] = setting
		units[name] = unit
	}
	return settings, units, rows.Err()
}

func FetchAutovacuumParams(ctx context.Context, db *sql.DB, schema, table string) ([]AutovacuumParam, error) {
	tableParams, err := fetchReloptions(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	globalNames := make([]string, 0, len(autovacuumParamSpecs))
	for _, spec := range autovacuumParamSpecs {
		globalNames = append(globalNames, spec.GlobalName)
	}
	globalSettings, globalUnits, err := fetchGlobalSettings(ctx, db, globalNames)
	if err != nil {
		return nil, err
	}

	var results []AutovacuumParam
	for _, spec := range autovacuumParamSpecs {
		if pgMajorVersion() < spec.MinPGVersion {
			continue
		}
		results = append(results, AutovacuumParam{
			Name:        spec.Reloption,
			TableValue:  tableParams[spec.Reloption], // "" if not customized
			GlobalValue: globalSettings[spec.GlobalName],
			Unit:        globalUnits[spec.GlobalName],
		})
	}
	return results, nil
}

// --- Autovacuum thresholds ---

// Setting sources reported in ThresholdSetting.Source.
const (
	SourceTable    = "table"    // table reloption
	SourceGlobal   = "global"   // postgresql.conf / ALTER SYSTEM (pg_settings)
	SourceAdjusted = "adjusted" // derived by PostgreSQL from other settings (capped/raised)
)

// ThresholdSetting is one effective autovacuum setting and where it came from.
type ThresholdSetting struct {
	Value  float64
	Source string
}

// RowThreshold is a "base + scale_factor × reltuples" autovacuum trigger, computed
// exactly as autovacuum.c's relation_needs_vacanalyze() does.
type RowThreshold struct {
	Base        ThresholdSetting
	ScaleFactor ThresholdSetting
	// MaxThreshold caps the dead-tuple threshold (PG18+ autovacuum_vacuum_max_threshold).
	// nil when not applicable on this version or disabled with -1.
	MaxThreshold *ThresholdSetting
	// UnfrozenFraction scales the insert threshold by the share of pages not yet
	// all-frozen (PG18+, from pg_class.relallfrozen). nil on PG13–17.
	UnfrozenFraction *float64
	Threshold        float64
	Current          int64
	Due              bool
}

// AgeThreshold compares a relfrozenxid/relminmxid age against one freeze limit.
type AgeThreshold struct {
	Limit   ThresholdSetting
	Current int64
	Due     bool
}

// AutovacuumThresholds is the effective autovacuum decision for one table, mixing
// table reloptions (when set) with global settings (otherwise).
type AutovacuumThresholds struct {
	AutovacuumEnabled       bool
	AutovacuumEnabledSource string
	TrackCounts             bool
	// Reltuples is pg_class.reltuples as autovacuum uses it: -1 ("never vacuumed or
	// analyzed", PG14+) is treated as 0, flagged by ReltuplesUnknown.
	Reltuples        float64
	ReltuplesUnknown bool

	DeadTuples RowThreshold
	Inserts    *RowThreshold // nil when insert-triggered vacuum is disabled (-1)
	Analyze    RowThreshold

	XIDWraparound    AgeThreshold // forced anti-wraparound autovacuum (runs even if autovacuum is off)
	XIDAggressive    AgeThreshold // next VACUUM scans every not-all-frozen page
	XIDFreezeMinAge  ThresholdSetting
	XIDFailsafe      *AgeThreshold // PG14+ vacuum_failsafe_age; nil on PG13
	MXIDWraparound   AgeThreshold
	MXIDAggressive   AgeThreshold
	MXIDFreezeMinAge ThresholdSetting
	MXIDFailsafe     *AgeThreshold // PG14+ vacuum_multixact_failsafe_age; nil on PG13
}

// AutovacuumDue reports whether autovacuum would pick the table up right now.
func (t AutovacuumThresholds) AutovacuumDue() bool {
	if t.XIDWraparound.Due || t.MXIDWraparound.Due {
		return true
	}
	if !t.AutovacuumEnabled || !t.TrackCounts {
		return false
	}
	return t.DeadTuples.Due || (t.Inserts != nil && t.Inserts.Due) || t.Analyze.Due
}

// autovacuumThresholdInputs are the raw catalog/stats values the computation needs,
// separated from the SQL so the formulas can be unit-tested per PostgreSQL version.
type autovacuumThresholdInputs struct {
	MajorVersion       int
	TableOptions       map[string]string
	GlobalSettings     map[string]string
	Reltuples          float64
	Relpages           int64
	Relallfrozen       *int64 // PG18+
	DeadTuples         int64
	InsertsSinceVacuum int64
	ModsSinceAnalyze   int64
	XIDAge             int64
	MXIDAge            int64
}

var autovacuumThresholdGlobals = []string{
	"autovacuum", "track_counts",
	"autovacuum_vacuum_threshold", "autovacuum_vacuum_scale_factor", "autovacuum_vacuum_max_threshold",
	"autovacuum_vacuum_insert_threshold", "autovacuum_vacuum_insert_scale_factor",
	"autovacuum_analyze_threshold", "autovacuum_analyze_scale_factor",
	"autovacuum_freeze_max_age", "vacuum_freeze_min_age", "vacuum_freeze_table_age", "vacuum_failsafe_age",
	"autovacuum_multixact_freeze_max_age", "vacuum_multixact_freeze_min_age",
	"vacuum_multixact_freeze_table_age", "vacuum_multixact_failsafe_age",
}

func FetchAutovacuumThresholds(ctx context.Context, db *sql.DB, schema, table string) (AutovacuumThresholds, error) {
	in := autovacuumThresholdInputs{MajorVersion: pgMajorVersion()}
	// pg_class.relallfrozen was added in PG18 (feeds the insert threshold); NULL before.
	relallfrozenColumn := "NULL::bigint"
	if in.MajorVersion >= 18 {
		relallfrozenColumn = "c.relallfrozen::bigint"
	}
	var reloptions pq.StringArray
	err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(c.reloptions, '{}'),
			c.reltuples::float8,
			c.relpages::bigint,
			`+relallfrozenColumn+`,
			COALESCE(s.n_dead_tup, 0),
			COALESCE(s.n_ins_since_vacuum, 0),
			COALESCE(s.n_mod_since_analyze, 0),
			age(c.relfrozenxid),
			mxid_age(c.relminmxid)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_all_tables s ON s.relid = c.oid
		WHERE n.nspname = $1 AND c.relname = $2`,
		schema, table,
	).Scan(&reloptions, &in.Reltuples, &in.Relpages, &in.Relallfrozen,
		&in.DeadTuples, &in.InsertsSinceVacuum, &in.ModsSinceAnalyze, &in.XIDAge, &in.MXIDAge)
	if err != nil {
		return AutovacuumThresholds{}, err
	}
	in.TableOptions = parseReloptions(reloptions)
	if in.GlobalSettings, _, err = fetchGlobalSettings(ctx, db, autovacuumThresholdGlobals); err != nil {
		return AutovacuumThresholds{}, err
	}
	return computeAutovacuumThresholds(in), nil
}

// parsePGBool accepts PostgreSQL's boolean spellings (on/off, true/false, yes/no, 1/0
// and unique prefixes thereof), as stored verbatim in reloptions.
func parsePGBool(raw string) (bool, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case value == "":
		return false, false
	case value == "on", value == "1", strings.HasPrefix("true", value), strings.HasPrefix("yes", value):
		return true, true
	case value == "of", value == "off", value == "0", strings.HasPrefix("false", value), strings.HasPrefix("no", value):
		return false, true
	}
	return false, false
}

func computeAutovacuumThresholds(in autovacuumThresholdInputs) AutovacuumThresholds {
	setting := func(reloption, globalName string) ThresholdSetting {
		if raw, ok := in.TableOptions[reloption]; ok {
			if value, err := strconv.ParseFloat(raw, 64); err == nil {
				return ThresholdSetting{Value: value, Source: SourceTable}
			}
		}
		value, _ := strconv.ParseFloat(in.GlobalSettings[globalName], 64)
		return ThresholdSetting{Value: value, Source: SourceGlobal}
	}
	// The *_freeze_max_age reloptions can only lower the global limit, never raise it.
	lowerOnly := func(reloption, globalName string) ThresholdSetting {
		global := setting("", globalName)
		table := setting(reloption, globalName)
		if table.Source == SourceTable && table.Value >= 0 && table.Value < global.Value {
			return table
		}
		return global
	}
	ageCheck := func(limit ThresholdSetting, age int64) AgeThreshold {
		return AgeThreshold{Limit: limit, Current: age, Due: float64(age) > limit.Value}
	}

	var result AutovacuumThresholds
	globalEnabled, _ := parsePGBool(in.GlobalSettings["autovacuum"])
	result.AutovacuumEnabled, result.AutovacuumEnabledSource = globalEnabled, SourceGlobal
	if tableEnabled, ok := parsePGBool(in.TableOptions["autovacuum_enabled"]); ok {
		// A table can only switch autovacuum off; with the global launcher off,
		// autovacuum_enabled=true still doesn't run (except anti-wraparound).
		result.AutovacuumEnabled = globalEnabled && tableEnabled
		if !tableEnabled {
			result.AutovacuumEnabledSource = SourceTable
		}
	}
	result.TrackCounts, _ = parsePGBool(in.GlobalSettings["track_counts"])

	result.Reltuples = in.Reltuples
	if result.Reltuples < 0 {
		result.Reltuples = 0
		result.ReltuplesUnknown = true
	}

	dead := RowThreshold{
		Base:        setting("autovacuum_vacuum_threshold", "autovacuum_vacuum_threshold"),
		ScaleFactor: setting("autovacuum_vacuum_scale_factor", "autovacuum_vacuum_scale_factor"),
		Current:     in.DeadTuples,
	}
	dead.Threshold = dead.Base.Value + dead.ScaleFactor.Value*result.Reltuples
	// autovacuum_vacuum_max_threshold caps the dead-tuple threshold for huge tables; PG18+.
	if in.MajorVersion >= 18 {
		if maxThreshold := setting("autovacuum_vacuum_max_threshold", "autovacuum_vacuum_max_threshold"); maxThreshold.Value >= 0 {
			dead.MaxThreshold = &maxThreshold
			dead.Threshold = min(dead.Threshold, maxThreshold.Value)
		}
	}
	dead.Due = float64(dead.Current) > dead.Threshold
	result.DeadTuples = dead

	if insertBase := setting("autovacuum_vacuum_insert_threshold", "autovacuum_vacuum_insert_threshold"); insertBase.Value >= 0 {
		inserts := RowThreshold{
			Base:        insertBase,
			ScaleFactor: setting("autovacuum_vacuum_insert_scale_factor", "autovacuum_vacuum_insert_scale_factor"),
			Current:     in.InsertsSinceVacuum,
		}
		scaledTuples := result.Reltuples
		// PG18+ only counts the not-yet-all-frozen part of the table, so insert-only
		// tables that are mostly frozen don't keep re-triggering vacuums.
		if in.MajorVersion >= 18 {
			unfrozen := 1.0
			if in.Relpages > 0 && in.Relallfrozen != nil && *in.Relallfrozen > 0 {
				unfrozen = 1 - float64(min(*in.Relallfrozen, in.Relpages))/float64(in.Relpages)
			}
			inserts.UnfrozenFraction = &unfrozen
			scaledTuples *= unfrozen
		}
		inserts.Threshold = inserts.Base.Value + inserts.ScaleFactor.Value*scaledTuples
		inserts.Due = float64(inserts.Current) > inserts.Threshold
		result.Inserts = &inserts
	}

	analyze := RowThreshold{
		Base:        setting("autovacuum_analyze_threshold", "autovacuum_analyze_threshold"),
		ScaleFactor: setting("autovacuum_analyze_scale_factor", "autovacuum_analyze_scale_factor"),
		Current:     in.ModsSinceAnalyze,
	}
	analyze.Threshold = analyze.Base.Value + analyze.ScaleFactor.Value*result.Reltuples
	analyze.Due = float64(analyze.Current) > analyze.Threshold
	result.Analyze = analyze

	// vacuum_get_cutoffs() clamps freeze_table_age to 95% and freeze_min_age to 50%
	// of the *global* autovacuum_(multixact_)freeze_max_age, so an oversized setting
	// can't postpone freezing past the point where a wraparound vacuum is forced.
	clampToFraction := func(value ThresholdSetting, globalMaxAge, fraction float64) ThresholdSetting {
		if limit := globalMaxAge * fraction; value.Value > limit {
			return ThresholdSetting{Value: float64(int64(limit)), Source: SourceAdjusted}
		}
		return value
	}
	// The failsafe never triggers below 105% of autovacuum_(multixact_)freeze_max_age.
	raiseToFraction := func(value ThresholdSetting, globalMaxAge, fraction float64) ThresholdSetting {
		if limit := globalMaxAge * fraction; value.Value < limit {
			return ThresholdSetting{Value: float64(int64(limit)), Source: SourceAdjusted}
		}
		return value
	}

	globalXIDMaxAge := setting("", "autovacuum_freeze_max_age").Value
	result.XIDWraparound = ageCheck(lowerOnly("autovacuum_freeze_max_age", "autovacuum_freeze_max_age"), in.XIDAge)
	result.XIDAggressive = ageCheck(clampToFraction(
		setting("autovacuum_freeze_table_age", "vacuum_freeze_table_age"), globalXIDMaxAge, 0.95), in.XIDAge)
	result.XIDFreezeMinAge = clampToFraction(
		setting("autovacuum_freeze_min_age", "vacuum_freeze_min_age"), globalXIDMaxAge, 0.5)

	globalMXIDMaxAge := setting("", "autovacuum_multixact_freeze_max_age").Value
	result.MXIDWraparound = ageCheck(lowerOnly("autovacuum_multixact_freeze_max_age", "autovacuum_multixact_freeze_max_age"), in.MXIDAge)
	result.MXIDAggressive = ageCheck(clampToFraction(
		setting("autovacuum_multixact_freeze_table_age", "vacuum_multixact_freeze_table_age"), globalMXIDMaxAge, 0.95), in.MXIDAge)
	result.MXIDFreezeMinAge = clampToFraction(
		setting("autovacuum_multixact_freeze_min_age", "vacuum_multixact_freeze_min_age"), globalMXIDMaxAge, 0.5)

	// vacuum_failsafe_age / vacuum_multixact_failsafe_age were added in PG14.
	if in.MajorVersion >= 14 {
		xidFailsafe := ageCheck(raiseToFraction(setting("", "vacuum_failsafe_age"), globalXIDMaxAge, 1.05), in.XIDAge)
		mxidFailsafe := ageCheck(raiseToFraction(setting("", "vacuum_multixact_failsafe_age"), globalMXIDMaxAge, 1.05), in.MXIDAge)
		result.XIDFailsafe, result.MXIDFailsafe = &xidFailsafe, &mxidFailsafe
	}
	return result
}

// AutovacuumBloatDetail holds precise bloat information from pgstattuple.
// Returned only when the pgstattuple extension is installed.
type AutovacuumBloatDetail struct {
	TableLen       int64
	TupleCount     int64
	DeadTupleCount int64
	DeadTupleLen   int64
	FreeSpace      int64
	RealBloatPct   float64
}

// FetchAutovacuumBloat runs pgstattuple for the given table.
// Returns nil, nil when the pgstattuple extension is not installed.
func FetchAutovacuumBloat(ctx context.Context, db *sql.DB, schema, table string) (*AutovacuumBloatDetail, error) {
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'pgstattuple')`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	var d AutovacuumBloatDetail
	// format('%I.%I', ...) safely quotes identifiers server-side.
	if err := db.QueryRowContext(ctx, `
		SELECT
			table_len,
			tuple_count,
			dead_tuple_count,
			dead_tuple_len,
			free_space,
			CASE WHEN table_len > 0
			     THEN ROUND((dead_tuple_len + free_space)::numeric / table_len * 100, 2)
			     ELSE 0 END AS real_bloat_pct
		FROM pgstattuple(format('%I.%I', $1::text, $2::text))`,
		schema, table,
	).Scan(&d.TableLen, &d.TupleCount, &d.DeadTupleCount, &d.DeadTupleLen, &d.FreeSpace, &d.RealBloatPct); err != nil {
		return nil, err
	}
	return &d, nil
}

// --- Autovacuum Activity ---

// AutovacuumWorkerSaturation compares configured autovacuum workers against how
// many are currently running, to surface worker starvation.
type AutovacuumWorkerSaturation struct {
	MaxWorkers     int
	RunningWorkers int
}

func FetchAutovacuumWorkerSaturation(ctx context.Context, db *sql.DB) (AutovacuumWorkerSaturation, error) {
	var s AutovacuumWorkerSaturation
	if err := db.QueryRowContext(ctx,
		`SELECT setting::int FROM pg_settings WHERE name = 'autovacuum_max_workers'`,
	).Scan(&s.MaxWorkers); err != nil {
		return s, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'autovacuum worker'`,
	).Scan(&s.RunningWorkers); err != nil {
		return s, err
	}
	return s, nil
}

// VacuumProgress is one running VACUUM (manual or autovacuum) from
// pg_stat_progress_vacuum. Version-specific columns are nullable pointers: nil means
// the column doesn't exist on the connected server.
type VacuumProgress struct {
	PID      int
	Database string
	// SchemaName/TableName are empty for vacuums running in another database, whose
	// relid can't be resolved from this database's pg_class (RelationID is still set).
	SchemaName   string
	TableName    string
	RelationID   int64
	Phase        string
	IsAutovacuum bool
	// IsWraparound is an autovacuum started "to prevent wraparound" — it can't be
	// skipped and doesn't yield to lock requests.
	IsWraparound     bool
	HeapBlksTotal    int64
	HeapBlksScanned  int64
	HeapBlksVacuumed int64
	IndexVacuumCount int64
	IndexesTotal     *int64 // PG17+
	IndexesProcessed *int64 // PG17+
	// Dead-tuple memory: tuple counts up to PG16, bytes from PG17 (TID store).
	DeadTuples        *int64 // PG13–16 num_dead_tuples
	MaxDeadTuples     *int64 // PG13–16 max_dead_tuples
	DeadTupleBytes    *int64 // PG17+ dead_tuple_bytes
	MaxDeadTupleBytes *int64 // PG17+ max_dead_tuple_bytes
	DeadItemIDs       *int64 // PG17+ num_dead_item_ids
	DurationSeconds   *int64
	Query             string
}

// ScannedPct is the share of heap blocks scanned so far; nil before the total is known.
func (p VacuumProgress) ScannedPct() *float64 {
	return blockPct(p.HeapBlksScanned, p.HeapBlksTotal)
}

// VacuumedPct is the share of heap blocks vacuumed so far; nil before the total is known.
func (p VacuumProgress) VacuumedPct() *float64 {
	return blockPct(p.HeapBlksVacuumed, p.HeapBlksTotal)
}

func blockPct(done, total int64) *float64 {
	if total <= 0 {
		return nil
	}
	pct := float64(done) / float64(total) * 100
	return &pct
}

// FetchVacuumProgress lists every running VACUUM in the cluster, longest first.
func FetchVacuumProgress(ctx context.Context, db *sql.DB) ([]VacuumProgress, error) {
	// PG17 replaced the dead-tuple counters with byte counters (the TID store) and
	// added index progress.
	deadColumns := `num_dead_tuples::bigint, max_dead_tuples::bigint, NULL::bigint, NULL::bigint, NULL::bigint, NULL::bigint, NULL::bigint`
	if pgMajorVersion() >= 17 {
		deadColumns = `NULL::bigint, NULL::bigint, dead_tuple_bytes, max_dead_tuple_bytes, num_dead_item_ids, indexes_total, indexes_processed`
	}
	// pg_stat_progress_vacuum covers every database, but relid only resolves through
	// this database's pg_class — hence the LEFT JOIN restricted to current_database().
	// LEFT JOIN pg_stat_activity: a progress row can briefly outlive its activity row.
	rows, err := db.QueryContext(ctx, `
		SELECT
			p.pid,
			COALESCE(p.datname, ''),
			COALESCE(n.nspname, ''),
			COALESCE(c.relname, ''),
			p.relid::bigint,
			p.phase,
			COALESCE(a.backend_type, '') = 'autovacuum worker',
			COALESCE(a.query, '') LIKE '%to prevent wraparound%',
			p.heap_blks_total,
			p.heap_blks_scanned,
			p.heap_blks_vacuumed,
			p.index_vacuum_count,
			`+deadColumns+`,
			CASE WHEN a.xact_start IS NULL THEN NULL
			     ELSE EXTRACT(EPOCH FROM (now() - a.xact_start))::bigint END,
			COALESCE(a.query, '')
		FROM pg_stat_progress_vacuum p
		LEFT JOIN pg_class c ON c.oid = p.relid AND p.datname = current_database()
		LEFT JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_activity a ON a.pid = p.pid
		ORDER BY a.xact_start NULLS LAST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []VacuumProgress{}
	for rows.Next() {
		var progress VacuumProgress
		if err := rows.Scan(
			&progress.PID, &progress.Database, &progress.SchemaName, &progress.TableName, &progress.RelationID,
			&progress.Phase, &progress.IsAutovacuum, &progress.IsWraparound,
			&progress.HeapBlksTotal, &progress.HeapBlksScanned, &progress.HeapBlksVacuumed, &progress.IndexVacuumCount,
			&progress.DeadTuples, &progress.MaxDeadTuples, &progress.DeadTupleBytes, &progress.MaxDeadTupleBytes,
			&progress.DeadItemIDs, &progress.IndexesTotal, &progress.IndexesProcessed,
			&progress.DurationSeconds, &progress.Query,
		); err != nil {
			return nil, err
		}
		results = append(results, progress)
	}
	return results, rows.Err()
}

// --- Xmin horizon ---

// Xmin horizon holder sources reported in XminHolder.Source.
const (
	XminSourceSession  = "session"  // a backend with an open snapshot or assigned XID
	XminSourceReplica  = "replica"  // a standby's hot_standby_feedback, via its walsender
	XminSourceSlot     = "slot"     // a replication slot's xmin / catalog_xmin
	XminSourcePrepared = "prepared" // a two-phase transaction waiting for COMMIT PREPARED
)

// XminHolder is something that keeps the xmin horizon from advancing.
type XminHolder struct {
	Source string
	// ID is the pid (session), application name or pid (replica), slot name, or gid.
	ID          string
	Database    string
	User        string
	State       string
	Application string
	ClientAddr  string
	XminAge     int64
	// CatalogOnly marks a slot holding only catalog_xmin: it blocks cleanup of system
	// catalogs, not of user tables.
	CatalogOnly           bool
	TransactionStart      *time.Time
	TransactionAgeSeconds *int64
	Query                 string
	// DefinesHorizon marks the holder(s) with the oldest xmin among those that aren't
	// catalog-only — the horizon that limits vacuum on user tables.
	DefinesHorizon bool
}

// XminHorizon lists everything holding back the xmin horizon, oldest first.
type XminHorizon struct {
	HotStandbyFeedback bool
	FreezeMaxAge       int64 // autovacuum_freeze_max_age, the reference for XminHolderStatus
	// DatabaseFrozenXIDAge is age(datfrozenxid) of the current database.
	DatabaseFrozenXIDAge int64
	Holders              []XminHolder
}

// FetchXminHorizon finds every session, standby (hot_standby_feedback), replication
// slot and prepared transaction holding back the xmin horizon.
func FetchXminHorizon(ctx context.Context, db *sql.DB) (XminHorizon, error) {
	horizon := XminHorizon{Holders: []XminHolder{}}
	if err := db.QueryRowContext(ctx, `
		SELECT current_setting('hot_standby_feedback')::boolean,
		       current_setting('autovacuum_freeze_max_age')::bigint,
		       (SELECT age(datfrozenxid) FROM pg_database WHERE datname = current_database())`,
	).Scan(&horizon.HotStandbyFeedback, &horizon.FreezeMaxAge, &horizon.DatabaseFrozenXIDAge); err != nil {
		return horizon, err
	}
	// Sessions: backend_xid counts too — a transaction that wrote something holds the
	// horizon with its XID even when it has no snapshot. Walsenders are left out of the
	// session list because their backend_xmin is the standby's feedback, reported once
	// under "replica". client_addr is NULL on unix sockets, so it's COALESCEd on its own.
	rows, err := db.QueryContext(ctx, `
		WITH holders AS (
			SELECT 'session' AS source, a.pid::text AS id, COALESCE(a.datname, '') AS database,
			       COALESCE(a.usename, '') AS username, COALESCE(a.state, '') AS state,
			       COALESCE(a.application_name, '') AS application, COALESCE(a.client_addr::text, '') AS client_addr,
			       greatest(age(a.backend_xmin), age(a.backend_xid))::bigint AS xmin_age,
			       false AS catalog_only, a.xact_start AS transaction_start, COALESCE(a.query, '') AS query
			FROM pg_stat_activity a
			WHERE (a.backend_xmin IS NOT NULL OR a.backend_xid IS NOT NULL)
			  AND a.backend_type IS DISTINCT FROM 'walsender'
			  AND a.pid <> pg_backend_pid()
			UNION ALL
			SELECT 'replica', COALESCE(NULLIF(r.application_name, ''), r.pid::text), '',
			       COALESCE(r.usename, ''), COALESCE(r.state, ''), COALESCE(r.application_name, ''),
			       COALESCE(r.client_addr::text, ''), age(r.backend_xmin)::bigint, false, NULL::timestamptz, ''
			FROM pg_stat_replication r
			WHERE r.backend_xmin IS NOT NULL
			UNION ALL
			SELECT 'slot', s.slot_name::text, COALESCE(s.database::text, ''), '',
			       CASE WHEN s.active THEN 'active' ELSE 'inactive' END, COALESCE(s.plugin::text, ''), '',
			       greatest(age(s.xmin), age(s.catalog_xmin))::bigint, s.xmin IS NULL, NULL::timestamptz, ''
			FROM pg_replication_slots s
			WHERE s.xmin IS NOT NULL OR s.catalog_xmin IS NOT NULL
			UNION ALL
			SELECT 'prepared', p.gid, p.database::text, p.owner::text, 'prepared', '', '',
			       age(p.transaction)::bigint, false, p.prepared, ''
			FROM pg_prepared_xacts p
		)
		SELECT *, CASE WHEN transaction_start IS NULL THEN NULL
		               ELSE EXTRACT(EPOCH FROM (now() - transaction_start))::bigint END
		FROM holders
		ORDER BY xmin_age DESC NULLS LAST, transaction_start NULLS LAST`)
	if err != nil {
		return horizon, err
	}
	defer rows.Close()
	for rows.Next() {
		var holder XminHolder
		if err := rows.Scan(&holder.Source, &holder.ID, &holder.Database, &holder.User, &holder.State,
			&holder.Application, &holder.ClientAddr, &holder.XminAge, &holder.CatalogOnly,
			&holder.TransactionStart, &holder.Query, &holder.TransactionAgeSeconds); err != nil {
			return horizon, err
		}
		horizon.Holders = append(horizon.Holders, holder)
	}
	if err := rows.Err(); err != nil {
		return horizon, err
	}
	// The horizon for user tables is set by the oldest holder that isn't catalog-only:
	// a slot's catalog_xmin only keeps system-catalog rows. Holders come oldest first.
	horizonAge := int64(-1)
	for i := range horizon.Holders {
		holder := &horizon.Holders[i]
		if holder.CatalogOnly {
			continue
		}
		if horizonAge < 0 {
			horizonAge = holder.XminAge
		}
		holder.DefinesHorizon = holder.XminAge == horizonAge
	}
	return horizon, nil
}

// HorizonHolder returns the holder that defines the user-table horizon, or nil when
// nothing (or only catalog-only slots) holds it back.
func (horizon XminHorizon) HorizonHolder() *XminHolder {
	for i := range horizon.Holders {
		if horizon.Holders[i].DefinesHorizon {
			return &horizon.Holders[i]
		}
	}
	return nil
}

// --- Server metadata ---

// ServerMeta describes the server a response came from and when its cumulative
// statistics were last reset, so counters can be read as "since <reset>".
type ServerMeta struct {
	ServerVersion       string
	InRecovery          bool
	ServerTime          time.Time
	PostmasterStartTime time.Time
	UptimeSeconds       int64
	// The stats_reset timestamps are nil when the counters were never reset: they then
	// accumulate since the cluster was created (or since the last crash recovery, which
	// discards statistics).
	DatabaseStatsReset     *time.Time
	BgwriterStatsReset     *time.Time
	CheckpointerStatsReset *time.Time // PG17+; checkpoint counters moved to pg_stat_checkpointer
	// PgStatStatementsVersion is "" when the extension isn't installed in this database.
	PgStatStatementsVersion string
}

func FetchServerMeta(ctx context.Context, db *sql.DB) (ServerMeta, error) {
	var meta ServerMeta
	// pg_stat_checkpointer (and its stats_reset) exists from PG17 on.
	checkpointerReset := "NULL::timestamptz"
	if pgMajorVersion() >= 17 {
		checkpointerReset = "(SELECT stats_reset FROM pg_stat_checkpointer)"
	}
	err := db.QueryRowContext(ctx, `
		SELECT
			current_setting('server_version'),
			pg_is_in_recovery(),
			now(),
			pg_postmaster_start_time(),
			EXTRACT(EPOCH FROM (now() - pg_postmaster_start_time()))::bigint,
			(SELECT stats_reset FROM pg_stat_database WHERE datname = current_database()),
			(SELECT stats_reset FROM pg_stat_bgwriter),
			`+checkpointerReset+`,
			COALESCE((SELECT extversion FROM pg_extension WHERE extname = 'pg_stat_statements'), '')`,
	).Scan(&meta.ServerVersion, &meta.InRecovery, &meta.ServerTime, &meta.PostmasterStartTime,
		&meta.UptimeSeconds, &meta.DatabaseStatsReset, &meta.BgwriterStatsReset,
		&meta.CheckpointerStatsReset, &meta.PgStatStatementsVersion)
	return meta, err
}

// --- Freeze Monitor ---

// FreezeDatabaseStatus is the XID freeze status for one database.
type FreezeDatabaseStatus struct {
	DatabaseName      string
	DBXIDAge          int64
	PctTowardShutdown float64
	DBMXIDAge         int64
	Status            int // 0=ok 1=warn 2=critical
}

func FetchFreezeByDatabase(ctx context.Context, db *sql.DB) ([]FreezeDatabaseStatus, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			datname,
			age(datfrozenxid)                                             AS db_xid_age,
			round(age(datfrozenxid)::numeric / 2100000000 * 100, 2)      AS pct_toward_shutdown,
			age(datminmxid)                                               AS db_mxid_age
		FROM pg_database
		WHERE datallowconn
		ORDER BY age(datfrozenxid) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []FreezeDatabaseStatus
	for rows.Next() {
		var s FreezeDatabaseStatus
		if err := rows.Scan(&s.DatabaseName, &s.DBXIDAge, &s.PctTowardShutdown, &s.DBMXIDAge); err != nil {
			return nil, err
		}
		switch {
		case s.PctTowardShutdown > 8.6:
			s.Status = 2
		case s.PctTowardShutdown > 7.1:
			s.Status = 1
		default:
			s.Status = 0
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// FreezeTableStatus is the XID freeze status for one table.
type FreezeTableStatus struct {
	SchemaName      string
	TableName       string
	XIDAge          int64
	MXIDAge         int64
	FreezeMaxAge    int64
	PctTowardFreeze float64
	TotalSize       string
	LastAutovacuum  *time.Time
	Status          int // 0=ok 1=warn 2=critical
}

func FetchFreezeByTable(ctx context.Context, db *sql.DB, limit int) ([]FreezeTableStatus, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			n.nspname                                                                         AS schema,
			c.relname                                                                         AS table,
			age(c.relfrozenxid)                                                               AS xid_age,
			mxid_age(c.relminmxid)                                                            AS mxid_age,
			(SELECT setting::bigint FROM pg_settings WHERE name = 'autovacuum_freeze_max_age') AS freeze_max_age,
			round(age(c.relfrozenxid)::numeric
				/ NULLIF((SELECT setting::bigint FROM pg_settings WHERE name = 'autovacuum_freeze_max_age'), 0)
				* 100, 1)                                                                      AS pct_toward_freeze,
			pg_size_pretty(pg_total_relation_size(c.oid))                                     AS total_size,
			s.last_autovacuum
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
		WHERE c.relkind = 'r'
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND c.relfrozenxid != 0
		ORDER BY age(c.relfrozenxid) DESC
		LIMIT NULLIF($1::int, 0)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []FreezeTableStatus
	for rows.Next() {
		var t FreezeTableStatus
		var pct sql.NullFloat64
		if err := rows.Scan(
			&t.SchemaName, &t.TableName, &t.XIDAge, &t.MXIDAge,
			&t.FreezeMaxAge, &pct, &t.TotalSize, &t.LastAutovacuum,
		); err != nil {
			return nil, err
		}
		if pct.Valid {
			t.PctTowardFreeze = pct.Float64
		}
		switch {
		case t.PctTowardFreeze > 75:
			t.Status = 2
		case t.PctTowardFreeze > 50:
			t.Status = 1
		default:
			t.Status = 0
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

// --- Streaming Replication ---

// StreamingStandby is one row from pg_stat_replication.
type StreamingStandby struct {
	ApplicationName string
	ClientAddr      string
	State           string
	SyncState       string
	SentLSN         string
	WriteLSN        string
	FlushLSN        string
	ReplayLSN       string
	WriteLag        string
	FlushLag        string
	ReplayLag       string
	LagBytes        int64
	PID             int
}

func FetchStreamingStandbys(ctx context.Context, db *sql.DB) ([]StreamingStandby, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			COALESCE(application_name, ''),
			COALESCE(client_addr::text, ''),
			state,
			sync_state,
			COALESCE(sent_lsn::text, ''),
			COALESCE(write_lsn::text, ''),
			COALESCE(flush_lsn::text, ''),
			COALESCE(replay_lsn::text, ''),
			COALESCE(write_lag::text, ''),
			COALESCE(flush_lag::text, ''),
			COALESCE(replay_lag::text, ''),
			COALESCE(pg_wal_lsn_diff(sent_lsn, replay_lsn), 0) AS lag_bytes,
			pid
		FROM pg_stat_replication
		ORDER BY lag_bytes DESC NULLS LAST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []StreamingStandby
	for rows.Next() {
		var s StreamingStandby
		if err := rows.Scan(
			&s.ApplicationName, &s.ClientAddr, &s.State, &s.SyncState,
			&s.SentLSN, &s.WriteLSN, &s.FlushLSN, &s.ReplayLSN,
			&s.WriteLag, &s.FlushLag, &s.ReplayLag,
			&s.LagBytes, &s.PID,
		); err != nil {
			return nil, err
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// --- Replication Config ---

// ReplicationParam holds one replication-related pg_settings row with a contextual hint.
type ReplicationParam struct {
	Name      string
	Setting   string
	Unit      string
	Context   string
	ShortDesc string
	Hint      string // empty means no hint
	HintLevel int    // -1=none 0=ok 1=warn 2=error
}

// ReplicationCounts holds live counts of walsenders and replication slots.
type ReplicationCounts struct {
	ActiveSenders int
	TotalSlots    int
	ActiveSlots   int
}

func replicationHint(name, setting string) (string, int) {
	switch name {
	case "wal_level":
		switch setting {
		case "minimal":
			return "replication disabled", 2
		case "replica":
			return "streaming OK; logical replication not available", 0
		case "logical":
			return "supports logical replication", 0
		}
	case "full_page_writes":
		if setting == "off" {
			return "risk of data corruption after crash", 2
		}
	case "synchronous_commit":
		if setting == "off" {
			return "risk of data loss on crash (async commit)", 1
		}
	case "wal_log_hints":
		if setting == "off" {
			return "required for pg_rewind; enable when using standby failover", 1
		}
	case "hot_standby_feedback":
		if setting == "on" {
			return "prevents vacuum on primary; may cause table bloat", 1
		}
	case "max_slot_wal_keep_size":
		if setting != "-1" {
			return "slots will be invalidated when WAL grows past this limit", 1
		}
	case "archive_mode":
		if setting == "off" {
			return "WAL archiving disabled; point-in-time recovery unavailable", 0
		}
	}
	return "", -1
}

func FetchReplicationConfig(ctx context.Context, db *sql.DB) ([]ReplicationParam, ReplicationCounts, error) {
	var counts ReplicationCounts
	rows, err := db.QueryContext(ctx, `
		SELECT name, setting, COALESCE(unit, ''), context, COALESCE(short_desc, '')
		FROM pg_settings
		WHERE name IN (
			'wal_level', 'synchronous_commit', 'full_page_writes',
			'wal_log_hints', 'wal_compression',
			'max_wal_senders', 'max_replication_slots',
			'wal_keep_size', 'max_slot_wal_keep_size',
			'hot_standby', 'hot_standby_feedback',
			'wal_sender_timeout', 'wal_receiver_timeout',
			'wal_receiver_status_interval', 'recovery_min_apply_delay',
			'archive_mode', 'archive_command', 'archive_library',
			'restore_command'
		)
		ORDER BY
			CASE name
				WHEN 'wal_level'                   THEN 1
				WHEN 'synchronous_commit'           THEN 2
				WHEN 'full_page_writes'             THEN 3
				WHEN 'wal_log_hints'                THEN 4
				WHEN 'wal_compression'              THEN 5
				WHEN 'max_wal_senders'              THEN 6
				WHEN 'max_replication_slots'        THEN 7
				WHEN 'wal_keep_size'                THEN 8
				WHEN 'max_slot_wal_keep_size'       THEN 9
				WHEN 'hot_standby'                  THEN 10
				WHEN 'hot_standby_feedback'         THEN 11
				WHEN 'wal_sender_timeout'           THEN 12
				WHEN 'wal_receiver_timeout'         THEN 13
				WHEN 'wal_receiver_status_interval' THEN 14
				WHEN 'recovery_min_apply_delay'     THEN 15
				WHEN 'archive_mode'                 THEN 16
				WHEN 'archive_command'              THEN 17
				WHEN 'archive_library'              THEN 18
				WHEN 'restore_command'              THEN 19
				ELSE 99
			END`)
	if err != nil {
		return nil, counts, err
	}
	defer rows.Close()

	var results []ReplicationParam
	for rows.Next() {
		var p ReplicationParam
		if err := rows.Scan(&p.Name, &p.Setting, &p.Unit, &p.Context, &p.ShortDesc); err != nil {
			return nil, counts, err
		}
		p.Hint, p.HintLevel = replicationHint(p.Name, p.Setting)
		results = append(results, p)
	}
	if err := rows.Err(); err != nil {
		return nil, counts, err
	}

	_ = db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM pg_stat_replication),
			(SELECT count(*) FROM pg_replication_slots),
			(SELECT count(*) FROM pg_replication_slots WHERE active_pid IS NOT NULL)
	`).Scan(&counts.ActiveSenders, &counts.TotalSlots, &counts.ActiveSlots)

	return results, counts, nil
}

// --- Database Sizes ---

// DatabaseSize is one row from pg_database with its on-disk size.
type DatabaseSize struct {
	Name       string
	Owner      string
	Encoding   string
	SizeBytes  int64
	SizePretty string
}

// TablespaceSize is one row from pg_tablespace with its on-disk size.
type TablespaceSize struct {
	Name       string
	Location   string
	SizeBytes  int64
	SizePretty string
}

// DatabaseSizeReport aggregates per-database sizes, tablespace sizes, and the cluster total.
type DatabaseSizeReport struct {
	Databases   []DatabaseSize
	Tablespaces []TablespaceSize
	TotalBytes  int64
	TotalPretty string
}

func FetchDatabaseSizes(ctx context.Context, db *sql.DB) (DatabaseSizeReport, error) {
	var report DatabaseSizeReport

	rows, err := db.QueryContext(ctx, `
		SELECT
			d.datname,
			pg_catalog.pg_get_userbyid(d.datdba),
			pg_encoding_to_char(d.encoding),
			pg_database_size(d.datname),
			pg_size_pretty(pg_database_size(d.datname))
		FROM pg_database d
		WHERE NOT d.datistemplate
		ORDER BY pg_database_size(d.datname) DESC`)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DatabaseSize
		if err := rows.Scan(&d.Name, &d.Owner, &d.Encoding, &d.SizeBytes, &d.SizePretty); err != nil {
			return report, err
		}
		report.Databases = append(report.Databases, d)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}

	tsRows, err := db.QueryContext(ctx, `
		SELECT
			spcname,
			COALESCE(pg_tablespace_location(oid), ''),
			pg_tablespace_size(spcname),
			pg_size_pretty(pg_tablespace_size(spcname))
		FROM pg_tablespace
		ORDER BY pg_tablespace_size(spcname) DESC`)
	if err != nil {
		return report, err
	}
	defer tsRows.Close()
	for tsRows.Next() {
		var t TablespaceSize
		if err := tsRows.Scan(&t.Name, &t.Location, &t.SizeBytes, &t.SizePretty); err != nil {
			return report, err
		}
		report.Tablespaces = append(report.Tablespaces, t)
	}
	if err := tsRows.Err(); err != nil {
		return report, err
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(pg_database_size(datname)), 0),
			pg_size_pretty(COALESCE(SUM(pg_database_size(datname)), 0))
		FROM pg_database
		WHERE NOT datistemplate`,
	).Scan(&report.TotalBytes, &report.TotalPretty); err != nil {
		return report, err
	}

	return report, nil
}

// --- Temp File Usage ---

// TempFileUsage is one row from pg_stat_database showing temp file spill activity
// accumulated since the last stats reset.
type TempFileUsage struct {
	Database   string
	TempFiles  int64
	TempBytes  int64
	TempPretty string
	StatsReset string
}

func FetchTempFileUsage(ctx context.Context, db *sql.DB) ([]TempFileUsage, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			datname,
			temp_files,
			temp_bytes,
			pg_size_pretty(temp_bytes),
			COALESCE(stats_reset::text, '')
		FROM pg_stat_database
		WHERE datname IS NOT NULL
		ORDER BY temp_bytes DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []TempFileUsage
	for rows.Next() {
		var t TempFileUsage
		if err := rows.Scan(&t.Database, &t.TempFiles, &t.TempBytes, &t.TempPretty, &t.StatsReset); err != nil {
			return nil, err
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

// --- Memory & Checkpoint Stats ---

// MemoryConfig is one memory-related row from pg_settings.
type MemoryConfig struct {
	Name      string
	Setting   string
	Unit      string
	ShortDesc string
}

// CheckpointStats holds pg_stat_bgwriter/pg_stat_checkpointer counters for checkpoint
// and background writer activity.
//
// On PostgreSQL 17+, checkpoint counters moved from pg_stat_bgwriter to a new
// pg_stat_checkpointer view (CheckpointsTimed/CheckpointsReq/BuffersCheckpoint are
// populated from there). BuffersBackend/BuffersBackendFsync were removed entirely in
// PG17 with no direct replacement (that data moved into pg_stat_io, a differently
// shaped view), so they are nil on PG17+.
type CheckpointStats struct {
	CheckpointsTimed    int64
	CheckpointsReq      int64
	BuffersCheckpoint   int64
	BuffersClean        int64
	MaxwrittenClean     int64
	BuffersBackend      *int64 // nil on PG17+: removed from pg_stat_bgwriter, no direct replacement
	BuffersBackendFsync *int64 // nil on PG17+: removed from pg_stat_bgwriter, no direct replacement
	BuffersAlloc        int64
	StatsReset          string
}

// MemoryStats aggregates memory-related config, the cluster-wide buffer cache hit ratio,
// and checkpoint/background writer activity — all derived from SQL only, so it works
// against remote servers where OS-level CPU/RAM metrics are not reachable.
type MemoryStats struct {
	Configs       []MemoryConfig
	CacheHitRatio float64
	Checkpoint    CheckpointStats
	// Warnings lists the parts that failed ("part: error"); their fields keep zero values.
	Warnings []string
}

// FetchMemoryStats runs its three parts (config, cache hit, checkpoints) independently;
// a failing part becomes a warning. It only returns an error when all of them failed.
func FetchMemoryStats(ctx context.Context, db *sql.DB) (MemoryStats, error) {
	var stats MemoryStats
	var errs []error
	for _, part := range []struct {
		name  string
		fetch func(context.Context, *sql.DB, *MemoryStats) error
	}{
		{"memory settings", fetchMemoryConfigs},
		{"cache hit", fetchClusterCacheHit},
		{"checkpoints", fetchCheckpointStats},
	} {
		if err := part.fetch(ctx, db, &stats); err != nil {
			errs = append(errs, err)
			stats.Warnings = append(stats.Warnings, part.name+": "+err.Error())
		}
	}
	if len(errs) == 3 {
		return stats, errs[0]
	}
	return stats, nil
}

func fetchMemoryConfigs(ctx context.Context, db *sql.DB, stats *MemoryStats) error {
	rows, err := db.QueryContext(ctx, `
		SELECT name, setting, COALESCE(unit, ''), COALESCE(short_desc, '')
		FROM pg_settings
		WHERE name IN (
			'shared_buffers', 'effective_cache_size', 'work_mem',
			'maintenance_work_mem', 'wal_buffers', 'huge_pages'
		)
		ORDER BY
			CASE name
				WHEN 'shared_buffers'        THEN 1
				WHEN 'effective_cache_size'  THEN 2
				WHEN 'work_mem'              THEN 3
				WHEN 'maintenance_work_mem'  THEN 4
				WHEN 'wal_buffers'           THEN 5
				WHEN 'huge_pages'            THEN 6
				ELSE 99
			END`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c MemoryConfig
		if err := rows.Scan(&c.Name, &c.Setting, &c.Unit, &c.ShortDesc); err != nil {
			return err
		}
		stats.Configs = append(stats.Configs, c)
	}
	return rows.Err()
}

func fetchClusterCacheHit(ctx context.Context, db *sql.DB, stats *MemoryStats) error {
	var hit, read int64
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(blks_hit), 0), COALESCE(SUM(blks_read), 0)
		FROM pg_stat_database`,
	).Scan(&hit, &read); err != nil {
		return err
	}
	if total := hit + read; total > 0 {
		stats.CacheHitRatio = float64(hit) / float64(total) * 100
	}
	return nil
}

func fetchCheckpointStats(ctx context.Context, db *sql.DB, stats *MemoryStats) error {
	// pg_stat_checkpointer was split out of pg_stat_bgwriter in PostgreSQL 17; checkpoint
	// counters live there now, while buffers_backend/buffers_backend_fsync have no
	// replacement in either view (see pg_stat_io for backend-level I/O on 17+).
	if pgMajorVersion() >= 17 {
		if err := db.QueryRowContext(ctx, `
			SELECT
				c.num_timed, c.num_requested, c.buffers_written,
				b.buffers_clean, b.maxwritten_clean, b.buffers_alloc,
				COALESCE(c.stats_reset::text, '')
			FROM pg_stat_checkpointer c, pg_stat_bgwriter b`,
		).Scan(
			&stats.Checkpoint.CheckpointsTimed, &stats.Checkpoint.CheckpointsReq,
			&stats.Checkpoint.BuffersCheckpoint,
			&stats.Checkpoint.BuffersClean, &stats.Checkpoint.MaxwrittenClean, &stats.Checkpoint.BuffersAlloc,
			&stats.Checkpoint.StatsReset,
		); err != nil {
			return err
		}
	} else {
		var buffersBackend, buffersBackendFsync int64
		if err := db.QueryRowContext(ctx, `
			SELECT
				checkpoints_timed, checkpoints_req,
				buffers_checkpoint, buffers_clean, maxwritten_clean,
				buffers_backend, buffers_backend_fsync, buffers_alloc,
				COALESCE(stats_reset::text, '')
			FROM pg_stat_bgwriter`,
		).Scan(
			&stats.Checkpoint.CheckpointsTimed, &stats.Checkpoint.CheckpointsReq,
			&stats.Checkpoint.BuffersCheckpoint, &stats.Checkpoint.BuffersClean, &stats.Checkpoint.MaxwrittenClean,
			&buffersBackend, &buffersBackendFsync, &stats.Checkpoint.BuffersAlloc,
			&stats.Checkpoint.StatsReset,
		); err != nil {
			return err
		}
		stats.Checkpoint.BuffersBackend = &buffersBackend
		stats.Checkpoint.BuffersBackendFsync = &buffersBackendFsync
	}
	return nil
}

// Publication represents a row from pg_publication with the table count from pg_publication_tables.
type Publication struct {
	PubName    string
	AllTables  bool
	Insert     bool
	Update     bool
	Delete     bool
	Truncate   bool
	ViaRoot    bool
	GenCols    *bool // PG18+; nil on PG13–17 (column added in PG18 as char 'n'/'a')
	TableCount int
}

// Subscription represents a row from pg_subscription joined with pg_stat_subscription
// (apply worker only) and, on PG15+, pg_stat_subscription_stats.
// Version-specific columns are returned as nullable pointers; nil means the column
// does not exist on this PostgreSQL version.
type Subscription struct {
	SubName         string
	Enabled         bool
	SlotName        string
	Publications    string // array_to_string(subpublications, ', ')
	WorkerPID       *int   // nil when subscription worker is not running
	ReceivedLSN     string
	LastReceiveTime *string // formatted timestamp; nil when never received
	ApplyErrorCount *int64  // PG15+; nil on PG13–14
	SyncErrorCount  *int64  // PG15+; nil on PG13–14
	TwoPhaseState   *string // PG15+: 'd'=disabled 'p'=pending 'e'=enabled
	DisableOnError  *bool   // PG16+
	Failover        *bool   // PG17+
}

// FetchPublications returns all publications defined on the connected server.
// pubgencols was added in PG18 as char ('n'=none, 'a'=all); GenCols is nil on PG13–17.
func FetchPublications(ctx context.Context, db *sql.DB) ([]Publication, error) {
	// The constant must NOT appear in GROUP BY — PostgreSQL rejects non-integer
	// constants there. On PG18+ the real column is included in GROUP BY.
	genColsExpr := "false AS pubgencols"
	genColsGroup := ""
	if pgMajorVersion() >= 18 {
		// PG18 added pubgencols as char ('n'=none, 'a'=all); cast to bool for uniform scanning.
		genColsExpr = "(p.pubgencols != 'n') AS pubgencols"
		genColsGroup = ", p.pubgencols"
	}

	query := `
		SELECT
			p.pubname, p.puballtables, p.pubinsert, p.pubupdate,
			p.pubdelete, p.pubtruncate, p.pubviaroot, ` + genColsExpr + `,
			COUNT(pt.tablename) AS table_count
		FROM pg_publication p
		LEFT JOIN pg_publication_tables pt ON pt.pubname = p.pubname
		GROUP BY p.pubname, p.puballtables, p.pubinsert, p.pubupdate,
		         p.pubdelete, p.pubtruncate, p.pubviaroot` + genColsGroup + `
		ORDER BY p.pubname`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	publications := []Publication{}
	for rows.Next() {
		var pub Publication
		var genCols sql.NullBool
		if err := rows.Scan(
			&pub.PubName, &pub.AllTables, &pub.Insert, &pub.Update,
			&pub.Delete, &pub.Truncate, &pub.ViaRoot, &genCols, &pub.TableCount,
		); err != nil {
			return nil, err
		}
		if genCols.Valid {
			pub.GenCols = &genCols.Bool
		}
		publications = append(publications, pub)
	}
	return publications, rows.Err()
}

// FetchSubscriptions returns all subscriptions on the connected server, joined
// with pg_stat_subscription (apply worker) and, when PG15+, pg_stat_subscription_stats.
// Version-specific columns are returned as nullable pointers; nil means the column
// does not exist on this PostgreSQL version.
func FetchSubscriptions(ctx context.Context, db *sql.DB) ([]Subscription, error) {
	// subtwophasestate added in PG15
	twophaseExpr := "NULL::text AS subtwophasestate"
	if pgMajorVersion() >= 15 {
		twophaseExpr = "s.subtwophasestate::text"
	}

	// subdisableonerr added in PG16
	disableOnErrExpr := "NULL::boolean AS subdisableonerr"
	if pgMajorVersion() >= 16 {
		disableOnErrExpr = "s.subdisableonerr"
	}

	// subfailover added in PG17
	failoverExpr := "NULL::boolean AS subfailover"
	if pgMajorVersion() >= 17 {
		failoverExpr = "s.subfailover"
	}

	// pg_stat_subscription_stats is a PG15+ view; guard the entire join
	statsSelect := "NULL::bigint AS apply_error_count, NULL::bigint AS sync_error_count"
	statsJoin := ""
	if pgMajorVersion() >= 15 {
		statsSelect = "COALESCE(stats.apply_error_count, 0), COALESCE(stats.sync_error_count, 0)"
		statsJoin = "LEFT JOIN pg_stat_subscription_stats stats ON stats.subid = s.oid"
	}

	query := `
		SELECT
			s.subname,
			s.subenabled,
			COALESCE(s.subslotname, '') AS subslotname,
			array_to_string(s.subpublications, ', ') AS publications,
			ss.pid,
			COALESCE(ss.received_lsn::text, ''),
			ss.last_msg_receipt_time::text,
			` + statsSelect + `,
			` + twophaseExpr + `,
			` + disableOnErrExpr + `,
			` + failoverExpr + `
		FROM pg_subscription s
		LEFT JOIN pg_stat_subscription ss ON ss.subid = s.oid AND ss.relid IS NULL
		` + statsJoin + `
		ORDER BY s.subname`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	subscriptions := []Subscription{}
	for rows.Next() {
		var sub Subscription
		var pid sql.NullInt64
		var lastReceive sql.NullString
		var applyErrors, syncErrors sql.NullInt64
		var twophaseState sql.NullString
		var disableOnErr, failover sql.NullBool

		if err := rows.Scan(
			&sub.SubName, &sub.Enabled, &sub.SlotName, &sub.Publications,
			&pid, &sub.ReceivedLSN, &lastReceive,
			&applyErrors, &syncErrors,
			&twophaseState, &disableOnErr, &failover,
		); err != nil {
			return nil, err
		}

		if pid.Valid {
			pidInt := int(pid.Int64)
			sub.WorkerPID = &pidInt
		}
		if lastReceive.Valid {
			sub.LastReceiveTime = &lastReceive.String
		}
		if applyErrors.Valid {
			sub.ApplyErrorCount = &applyErrors.Int64
		}
		if syncErrors.Valid {
			sub.SyncErrorCount = &syncErrors.Int64
		}
		if twophaseState.Valid {
			sub.TwoPhaseState = &twophaseState.String
		}
		if disableOnErr.Valid {
			sub.DisableOnError = &disableOnErr.Bool
		}
		if failover.Valid {
			sub.Failover = &failover.Bool
		}

		subscriptions = append(subscriptions, sub)
	}
	return subscriptions, rows.Err()
}

// PublicationTable holds per-table details for a single publication,
// joined with pg_stat_user_tables for live/dead row counts and vacuum history.
type PublicationTable struct {
	SchemaName string
	TableName  string
	Columns    string // "all columns" or "col1, col2, ..." (PG15+ column filters; older = always "all columns")
	RowFilter  string // "" if none; expression string if row-level filter (PG15+)
	LiveRows   int64
	DeadRows   int64
	SeqScans   int64
	IdxScans   int64
	LastVacuum string // "YYYY-MM-DD HH:MM" or "never"
}

// FetchPublicationTables returns per-table details for the named publication.
// attnames and rowfilter were added to pg_publication_tables in PG15; on older
// versions Columns is always "all columns" and RowFilter is always empty.
func FetchPublicationTables(ctx context.Context, db *sql.DB, pubname string) ([]PublicationTable, error) {
	// attnames (column filter list) and rowfilter added in PG15
	attExpr := "'all columns'"
	filterExpr := "''"
	if pgMajorVersion() >= 15 {
		attExpr = "COALESCE(array_to_string(pt.attnames, ', '), 'all columns')"
		filterExpr = "COALESCE(pt.rowfilter::text, '')"
	}

	query := `
		SELECT
			pt.schemaname,
			pt.tablename,
			` + attExpr + ` AS columns,
			` + filterExpr + ` AS row_filter,
			COALESCE(s.n_live_tup, 0),
			COALESCE(s.n_dead_tup, 0),
			COALESCE(s.seq_scan, 0),
			COALESCE(s.idx_scan, 0),
			CASE
				WHEN GREATEST(s.last_vacuum, s.last_autovacuum) IS NOT NULL
				THEN to_char(GREATEST(s.last_vacuum, s.last_autovacuum), 'YYYY-MM-DD HH24:MI')
				ELSE 'never'
			END AS last_vacuum
		FROM pg_publication_tables pt
		LEFT JOIN pg_stat_user_tables s
			ON s.schemaname = pt.schemaname AND s.relname = pt.tablename
		WHERE pt.pubname = $1
		ORDER BY pt.schemaname, pt.tablename`

	rows, err := db.QueryContext(ctx, query, pubname)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PublicationTable
	for rows.Next() {
		var t PublicationTable
		if err := rows.Scan(
			&t.SchemaName, &t.TableName, &t.Columns, &t.RowFilter,
			&t.LiveRows, &t.DeadRows, &t.SeqScans, &t.IdxScans, &t.LastVacuum,
		); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// SubscriptionTable holds per-table sync state and stats for a single subscription.
type SubscriptionTable struct {
	SchemaName string
	TableName  string
	SyncState  string // "ready", "synchronized", "copying", "finished", "initialize"
	SyncLSN    string // "" if not yet set (initial copy in progress)
	Columns    string // comma-separated non-system column names from pg_attribute
	LiveRows   int64
	InsRows    int64
	UpdRows    int64
	DelRows    int64
}

// FetchSubscriptionTables returns per-table sync state for the named subscription,
// joined with pg_stat_user_tables for row-level insert/update/delete stats.
// pg_subscription_rel is available since PG10 — no version gating needed.
func FetchSubscriptionTables(ctx context.Context, db *sql.DB, subname string) ([]SubscriptionTable, error) {
	query := `
		SELECT
			n.nspname AS schemaname,
			c.relname AS tablename,
			CASE sr.srsubstate
				WHEN 'i' THEN 'initialize'
				WHEN 'd' THEN 'copying'
				WHEN 'f' THEN 'finished'
				WHEN 's' THEN 'synchronized'
				WHEN 'r' THEN 'ready'
				ELSE sr.srsubstate::text
			END AS sync_state,
			COALESCE(sr.srsublsn::text, '') AS sync_lsn,
			COALESCE(attrs.col_names, '') AS columns,
			COALESCE(s.n_live_tup, 0),
			COALESCE(s.n_tup_ins, 0),
			COALESCE(s.n_tup_upd, 0),
			COALESCE(s.n_tup_del, 0)
		FROM pg_subscription_rel sr
		JOIN pg_class c ON c.oid = sr.srrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		-- List non-system, non-dropped columns so the subscriber can see what the table contains.
		LEFT JOIN LATERAL (
			SELECT string_agg(a.attname, ', ' ORDER BY a.attnum) AS col_names
			FROM pg_attribute a
			WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		) attrs ON true
		LEFT JOIN pg_stat_user_tables s
			ON s.schemaname = n.nspname AND s.relname = c.relname
		WHERE sr.srsubid = (SELECT oid FROM pg_subscription WHERE subname = $1)
		ORDER BY n.nspname, c.relname`

	rows, err := db.QueryContext(ctx, query, subname)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SubscriptionTable
	for rows.Next() {
		var t SubscriptionTable
		if err := rows.Scan(
			&t.SchemaName, &t.TableName, &t.SyncState, &t.SyncLSN, &t.Columns,
			&t.LiveRows, &t.InsRows, &t.UpdRows, &t.DelRows,
		); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// --- Dashboard: WAL & replication health ---

// WALReplicationStatus gathers the signals that predict a full pg_wal or a broken
// replica: WAL retained by slots against max_slot_wal_keep_size, slots about to be (or
// already) invalidated, a failing archiver, standby lag and logical replication errors.
type WALReplicationStatus struct {
	SlotCount int
	// MaxRetainedBytes is the WAL kept by the slot that retains the most
	// (current LSN − restart_lsn); MaxRetainedSlot names it. Zero/empty without slots.
	MaxRetainedBytes int64
	MaxRetainedSlot  string
	// MaxSlotWALKeepBytes is max_slot_wal_keep_size in bytes; nil when it's -1
	// (unlimited — a stalled slot can fill the disk).
	MaxSlotWALKeepBytes *int64
	SlotsUnreserved     int // wal_status = 'unreserved': past max_wal_size, about to be invalidated
	SlotsLost           int // wal_status = 'lost': invalidated, the consumer must be rebuilt
	// IdleSlotTimeoutSeconds is idle_replication_slot_timeout (PG18+); nil before PG18,
	// 0 when disabled. LongestIdleSeconds/LongestIdleSlot come from inactive_since (PG18+).
	IdleSlotTimeoutSeconds *int64
	LongestIdleSeconds     *int64
	LongestIdleSlot        string

	ArchiveMode       string // off, on, always
	ArchivedCount     int64
	FailedCount       int64
	LastArchivedTime  *time.Time
	LastFailedTime    *time.Time
	ArchiveFailingNow bool // the last attempt failed (last_failed_time after last_archived_time)

	// Physical standbys and logical subscribers both appear in pg_stat_replication; they
	// are split because "standby lag" and "subscriber lag" mean different things.
	StandbyCount        int
	MaxStandbyLagBytes  *int64   // nil when there are no standbys or lag isn't visible
	MaxReplayLagSeconds *float64 // nil when unknown (idle standby, insufficient privileges)
	// LogicalSenderCount is walsenders streaming this server's publications to subscribers.
	LogicalSenderCount int
	MaxLogicalLagBytes *int64

	SubscriptionCount int
	// SubscriptionErrors is apply + sync errors (PG15+); SubscriptionConflicts sums the
	// confl_* counters (PG18+). nil when the version doesn't provide them.
	SubscriptionErrors    *int64
	SubscriptionConflicts *int64
}

// SlotRetentionPct is MaxRetainedBytes as a percentage of max_slot_wal_keep_size, or
// nil when that limit is unlimited.
func (s WALReplicationStatus) SlotRetentionPct() *float64 {
	if s.MaxSlotWALKeepBytes == nil || *s.MaxSlotWALKeepBytes <= 0 {
		return nil
	}
	pct := float64(s.MaxRetainedBytes) / float64(*s.MaxSlotWALKeepBytes) * 100
	return &pct
}

func FetchWALReplicationStatus(ctx context.Context, db *sql.DB) (WALReplicationStatus, error) {
	var status WALReplicationStatus
	majorVersion := pgMajorVersion()

	// pg_current_wal_lsn() errors during recovery; on a standby, retention is measured
	// against the last replayed position instead.
	const currentLSN = `CASE WHEN pg_is_in_recovery() THEN pg_last_wal_replay_lsn() ELSE pg_current_wal_lsn() END`
	if err := db.QueryRowContext(ctx, `
		SELECT
			count(*),
			COALESCE(max(pg_wal_lsn_diff(`+currentLSN+`, restart_lsn)), 0)::bigint,
			COALESCE((array_agg(slot_name ORDER BY pg_wal_lsn_diff(`+currentLSN+`, restart_lsn) DESC NULLS LAST))[1], ''),
			count(*) FILTER (WHERE wal_status = 'unreserved'),
			count(*) FILTER (WHERE wal_status = 'lost')
		FROM pg_replication_slots`,
	).Scan(&status.SlotCount, &status.MaxRetainedBytes, &status.MaxRetainedSlot,
		&status.SlotsUnreserved, &status.SlotsLost); err != nil {
		return status, err
	}

	if err := db.QueryRowContext(ctx, `
		SELECT CASE WHEN setting::bigint < 0 THEN NULL ELSE pg_size_bytes(setting || unit) END
		FROM pg_settings WHERE name = 'max_slot_wal_keep_size'`,
	).Scan(&status.MaxSlotWALKeepBytes); err != nil {
		return status, err
	}

	// idle_replication_slot_timeout and pg_replication_slots.inactive_since: PG18+.
	if majorVersion >= 18 {
		if err := db.QueryRowContext(ctx, `
			SELECT
				(SELECT setting::bigint FROM pg_settings WHERE name = 'idle_replication_slot_timeout'),
				(SELECT EXTRACT(EPOCH FROM now() - min(inactive_since))::bigint FROM pg_replication_slots WHERE NOT active),
				COALESCE((SELECT slot_name FROM pg_replication_slots WHERE NOT active AND inactive_since IS NOT NULL
				          ORDER BY inactive_since LIMIT 1), '')`,
		).Scan(&status.IdleSlotTimeoutSeconds, &status.LongestIdleSeconds, &status.LongestIdleSlot); err != nil {
			return status, err
		}
	}

	if err := db.QueryRowContext(ctx, `
		SELECT current_setting('archive_mode'), archived_count, failed_count, last_archived_time, last_failed_time,
		       last_failed_time IS NOT NULL
		         AND (last_archived_time IS NULL OR last_failed_time > last_archived_time)
		FROM pg_stat_archiver`,
	).Scan(&status.ArchiveMode, &status.ArchivedCount, &status.FailedCount,
		&status.LastArchivedTime, &status.LastFailedTime, &status.ArchiveFailingNow); err != nil {
		return status, err
	}

	// replay_lsn / replay_lag are NULL for other users' walsenders without pg_read_all_stats.
	// A physical walsender isn't connected to a database (datname IS NULL); a logical one
	// (feeding a subscription) is. For logical senders "replay" is the subscriber's
	// confirmed position.
	if err := db.QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE a.datname IS NULL),
			max(pg_wal_lsn_diff(`+currentLSN+`, r.replay_lsn)) FILTER (WHERE a.datname IS NULL)::bigint,
			max(EXTRACT(EPOCH FROM r.replay_lag)) FILTER (WHERE a.datname IS NULL)::float8,
			count(*) FILTER (WHERE a.datname IS NOT NULL),
			max(pg_wal_lsn_diff(`+currentLSN+`, r.replay_lsn)) FILTER (WHERE a.datname IS NOT NULL)::bigint
		FROM pg_stat_replication r
		LEFT JOIN pg_stat_activity a ON a.pid = r.pid`,
	).Scan(&status.StandbyCount, &status.MaxStandbyLagBytes, &status.MaxReplayLagSeconds,
		&status.LogicalSenderCount, &status.MaxLogicalLagBytes); err != nil {
		return status, err
	}

	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_subscription`).Scan(&status.SubscriptionCount); err != nil {
		return status, err
	}
	// pg_stat_subscription_stats is PG15+; its confl_* conflict counters are PG18+. Summing
	// every confl_* key of the row as JSON keeps working if later versions add more types.
	if majorVersion >= 15 && status.SubscriptionCount > 0 {
		conflicts := "NULL::bigint"
		if majorVersion >= 18 {
			conflicts = `COALESCE(sum((SELECT sum(value::bigint) FROM jsonb_each_text(to_jsonb(s)) WHERE key LIKE 'confl\_%')), 0)::bigint`
		}
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(sum(apply_error_count + sync_error_count), 0)::bigint, `+conflicts+`
			FROM pg_stat_subscription_stats s`,
		).Scan(&status.SubscriptionErrors, &status.SubscriptionConflicts); err != nil {
			return status, err
		}
	}
	return status, nil
}

// --- Replica identity (logical replication readiness) ---

// ReplicaIdentityIssue is a table without a replica identity usable by the logical
// replication that includes (or could include) it. Native publications need a PK,
// USING INDEX or FULL — without one, UPDATE/DELETE fail on the publisher with "cannot
// update table ... because it does not have a replica identity". pglogical is
// stricter: it needs a PK or USING INDEX, and does not support REPLICA IDENTITY FULL.
type ReplicaIdentityIssue struct {
	SchemaName    string
	TableName     string
	HasPrimaryKey bool
	// ReplicaIdentity is pg_class.relreplident spelled out: "default" (uses the PK),
	// "nothing", "index" (USING INDEX whose index was dropped) or "full" (only an issue
	// for tables in a pglogical replication set).
	ReplicaIdentity string
	// Publications lists every publication that includes the table, directly, through
	// FOR ALL TABLES / FOR TABLES IN SCHEMA, or through a partitioned ancestor.
	Publications          []string
	PublishesUpdateDelete bool
	// PglogicalSets lists the local node's pglogical replication sets containing the
	// table; empty when pglogical isn't installed.
	PglogicalSets         []string
	PglogicalUpdateDelete bool
	// CandidateIndex is a unique, valid, non-partial, non-deferrable index on NOT NULL
	// columns that REPLICA IDENTITY USING INDEX would accept; nil when there is none.
	CandidateIndex *string
	TotalSize      string
}

// IsCritical reports whether UPDATE/DELETE on the table can't be replicated today:
// it's in a publication or pglogical replication set that replicates them.
func (issue ReplicaIdentityIssue) IsCritical() bool {
	return issue.PublishesUpdateDelete || issue.PglogicalUpdateDelete
}

// IsReplicated reports whether the table is in any publication or pglogical set.
func (issue ReplicaIdentityIssue) IsReplicated() bool {
	return len(issue.Publications) > 0 || len(issue.PglogicalSets) > 0
}

// SuggestedFix returns the statement that gives the table a usable identity, cheapest
// first: back to DEFAULT when a PK exists, USING INDEX when a suitable unique index
// exists, FULL otherwise — except for pglogical tables, which can't use FULL and need
// a primary key (no single statement can be suggested for that).
func (issue ReplicaIdentityIssue) SuggestedFix() string {
	table := pq.QuoteIdentifier(issue.SchemaName) + "." + pq.QuoteIdentifier(issue.TableName)
	switch {
	case issue.HasPrimaryKey:
		return fmt.Sprintf("ALTER TABLE %s REPLICA IDENTITY DEFAULT;", table)
	case issue.CandidateIndex != nil:
		return fmt.Sprintf("ALTER TABLE %s REPLICA IDENTITY USING INDEX %s;", table, pq.QuoteIdentifier(*issue.CandidateIndex))
	case len(issue.PglogicalSets) > 0:
		return fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (...);  -- pglogical does not support REPLICA IDENTITY FULL", table)
	}
	return fmt.Sprintf("ALTER TABLE %s REPLICA IDENTITY FULL;", table)
}

// pglogicalInstalled checks for pglogical's catalog rather than pg_extension so a
// partially configured node (extension present, no local node yet) still works.
func pglogicalInstalled(ctx context.Context, db *sql.DB) (bool, error) {
	var installed bool
	err := db.QueryRowContext(ctx,
		`SELECT to_regclass('pglogical.replication_set_table') IS NOT NULL
		    AND to_regclass('pglogical.local_node') IS NOT NULL`).Scan(&installed)
	return installed, err
}

// FetchReplicaIdentityIssues lists permanent user tables whose replica identity is
// unusable: DEFAULT without a primary key, NOTHING, USING INDEX whose index no longer
// exists, or FULL when the table is in a pglogical replication set. Tables whose
// UPDATE/DELETE can't be replicated today come first.
func FetchReplicaIdentityIssues(ctx context.Context, db *sql.DB) ([]ReplicaIdentityIssue, error) {
	withPglogical, err := pglogicalInstalled(ctx, db)
	if err != nil {
		return nil, err
	}
	// pglogical's schema only exists when the extension is installed, so its part of
	// the query is added conditionally (referencing it otherwise fails at parse time).
	// Only the local node's sets matter: those are the ones this server provides.
	pglogicalSets := ""
	if withPglogical {
		pglogicalSets = `
			UNION ALL
			SELECT
				rs.set_name::text,
				rs.replicate_update OR rs.replicate_delete,
				rst.set_reloid::oid,
				true
			FROM pglogical.replication_set_table rst
			JOIN pglogical.replication_set rs ON rs.set_id = rst.set_id
			JOIN pglogical.local_node ln ON ln.node_id = rs.set_nodeid`
	}
	// pg_partition_ancestors() returns no rows for a regular table, so the table itself
	// is matched separately; ancestors matter when a publication lists the partition root
	// (publish_via_partition_root) — the leaf partitions still need their own identity.
	rows, err := db.QueryContext(ctx, `
		WITH replicated AS (
			SELECT
				p.pubname::text                                                                   AS name,
				p.pubupdate OR p.pubdelete                                                        AS replicates_update_delete,
				(quote_ident(pt.schemaname) || '.' || quote_ident(pt.tablename))::regclass::oid AS relid,
				false                                                                             AS is_pglogical
			FROM pg_publication_tables pt
			JOIN pg_publication p ON p.pubname = pt.pubname`+pglogicalSets+`
		),
		user_tables AS (
			SELECT
				c.oid,
				n.nspname,
				c.relname,
				c.relreplident,
				EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary)   AS has_pk,
				EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisreplident) AS has_identity_index
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relkind = 'r'
			  AND c.relpersistence = 'p' -- temp and unlogged tables are never replicated
			  AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pglogical')
			  AND n.nspname NOT LIKE 'pg_toast%'
		),
		classified AS (
			SELECT
				t.*,
				COALESCE(pubs.names, '{}')                     AS publications,
				COALESCE(pubs.replicates_update_delete, false) AS publishes_update_delete,
				COALESCE(sets.names, '{}')                     AS pglogical_sets,
				COALESCE(sets.replicates_update_delete, false) AS pglogical_update_delete
			FROM user_tables t
			LEFT JOIN LATERAL (
				SELECT array_agg(DISTINCT r.name ORDER BY r.name) AS names,
				       bool_or(r.replicates_update_delete)       AS replicates_update_delete
				FROM replicated r
				WHERE NOT r.is_pglogical
				  AND (r.relid = t.oid OR r.relid IN (SELECT relid FROM pg_partition_ancestors(t.oid)))
			) pubs ON true
			LEFT JOIN LATERAL (
				SELECT array_agg(DISTINCT r.name ORDER BY r.name) AS names,
				       bool_or(r.replicates_update_delete)       AS replicates_update_delete
				FROM replicated r
				WHERE r.is_pglogical AND r.relid = t.oid
			) sets ON true
		)
		SELECT
			t.nspname,
			t.relname,
			t.has_pk,
			t.relreplident::text,
			t.publications,
			t.publishes_update_delete,
			t.pglogical_sets,
			t.pglogical_update_delete,
			(
				SELECT ic.relname
				FROM pg_index i
				JOIN pg_class ic ON ic.oid = i.indexrelid
				WHERE i.indrelid = t.oid
				  AND i.indisunique AND i.indisvalid AND i.indimmediate
				  AND i.indpred IS NULL
				  AND NOT (0 = ANY (i.indkey::int2[])) -- expression columns aren't allowed
				  AND NOT EXISTS (
					SELECT 1 FROM pg_attribute a
					WHERE a.attrelid = t.oid AND a.attnum = ANY (i.indkey::int2[]) AND NOT a.attnotnull
				  )
				ORDER BY i.indnatts, ic.relname
				LIMIT 1
			),
			pg_size_pretty(pg_total_relation_size(t.oid))
		FROM classified t
		WHERE t.relreplident = 'n'
		   OR (t.relreplident = 'd' AND NOT t.has_pk)
		   OR (t.relreplident = 'i' AND NOT t.has_identity_index)
		   -- pglogical needs an identity *index*; FULL has none
		   OR (t.relreplident = 'f' AND cardinality(t.pglogical_sets) > 0)
		ORDER BY t.publishes_update_delete OR t.pglogical_update_delete DESC,
		         cardinality(t.publications) + cardinality(t.pglogical_sets) DESC,
		         t.nspname, t.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identityNames := map[string]string{"d": "default", "n": "nothing", "i": "index", "f": "full"}
	results := []ReplicaIdentityIssue{}
	for rows.Next() {
		var issue ReplicaIdentityIssue
		var relreplident string
		var publications, pglogicalSets pq.StringArray
		if err := rows.Scan(
			&issue.SchemaName, &issue.TableName, &issue.HasPrimaryKey, &relreplident,
			&publications, &issue.PublishesUpdateDelete, &pglogicalSets, &issue.PglogicalUpdateDelete,
			&issue.CandidateIndex, &issue.TotalSize,
		); err != nil {
			return nil, err
		}
		issue.ReplicaIdentity = identityNames[relreplident]
		issue.Publications = []string(publications)
		issue.PglogicalSets = []string(pglogicalSets)
		results = append(results, issue)
	}
	return results, rows.Err()
}
