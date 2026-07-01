package util

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/liciomatos/pgdba-cli/config"
)

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
	Host             string
	Database         string
	UsedConnections  int
	MaxConnections   int
	ConnectionPct    float64
	ActiveQueries    int
	BlockedQueries   int
	SlowQueryCount   int      // -1 when pg_stat_statements is unavailable
	SlowThresholdMS  int
	CacheHitRatio    *float64 // nil when no table I/O data exists
	DeadTuples       int64
	InvalidIndexes   int
	ReplicationSlots    int
	FreezeOldestDB      string
	FreezeOldestDBAge   int64
	FreezePctToward     float64
}

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
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity`).Scan(&result.UsedConnections); err != nil {
		return result, err
	}
	if err := db.QueryRowContext(ctx, `SELECT setting::int FROM pg_settings WHERE name='max_connections'`).Scan(&result.MaxConnections); err != nil {
		return result, err
	}
	if result.MaxConnections > 0 {
		result.ConnectionPct = float64(result.UsedConnections) / float64(result.MaxConnections) * 100
	}
	if err := db.QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE state = 'active' AND query NOT LIKE '%pg_stat_activity%'),
			count(*) FILTER (WHERE wait_event_type = 'Lock')
		FROM pg_stat_activity`,
	).Scan(&result.ActiveQueries, &result.BlockedQueries); err != nil {
		return result, err
	}
	var slowCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_stat_statements WHERE mean_exec_time > $1`, threshold,
	).Scan(&slowCount); err != nil {
		result.SlowQueryCount = -1
	} else {
		result.SlowQueryCount = slowCount
	}
	var cacheHit sql.NullFloat64
	_ = db.QueryRowContext(ctx, `
		SELECT ROUND(100.0 * sum(heap_blks_hit) / NULLIF(sum(heap_blks_hit)+sum(heap_blks_read),0), 1)
		FROM pg_statio_user_tables`,
	).Scan(&cacheHit)
	if cacheHit.Valid {
		result.CacheHitRatio = &cacheHit.Float64
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(sum(n_dead_tup),0) FROM pg_stat_user_tables`).Scan(&result.DeadTuples); err != nil {
		return result, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_index WHERE NOT indisvalid`).Scan(&result.InvalidIndexes); err != nil {
		return result, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_replication_slots`).Scan(&result.ReplicationSlots); err != nil {
		return result, err
	}
	// Non-fatal: freeze status is informational; ignore errors on older setups.
	_ = db.QueryRowContext(ctx, `
		SELECT datname, age(datfrozenxid),
		       round(age(datfrozenxid)::numeric / 2100000000 * 100, 2)
		FROM pg_database
		WHERE datallowconn
		ORDER BY age(datfrozenxid) DESC
		LIMIT 1
	`).Scan(&result.FreezeOldestDB, &result.FreezeOldestDBAge, &result.FreezePctToward)
	return result, nil
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
	rows, err := db.QueryContext(ctx, `
		SELECT queryid, query, calls, total_exec_time, mean_exec_time, stddev_exec_time, rows
		FROM pg_stat_statements
		WHERE mean_exec_time > $1
		ORDER BY mean_exec_time DESC
		LIMIT $2`, thresholdMS, limit)
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

// LongRunningQuery is an active session whose query has exceeded the minimum duration.
type LongRunningQuery struct {
	PID             int
	Username        string
	ApplicationName string
	State           string
	DurationSeconds float64
	Query           string
}

func FetchLongRunningQueries(ctx context.Context, db *sql.DB, minDurationSeconds, limit int) ([]LongRunningQuery, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			pid,
			usename,
			application_name,
			state,
			ROUND(EXTRACT(EPOCH FROM (now() - query_start))::numeric, 1) AS duration_seconds,
			COALESCE(query, '') AS query
		FROM pg_stat_activity
		WHERE state != 'idle'
		  AND query_start IS NOT NULL
		  AND now() - query_start > ($1 * interval '1 second')
		ORDER BY duration_seconds DESC
		LIMIT $2`, minDurationSeconds, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []LongRunningQuery
	for rows.Next() {
		var q LongRunningQuery
		if err := rows.Scan(&q.PID, &q.Username, &q.ApplicationName, &q.State, &q.DurationSeconds, &q.Query); err != nil {
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
			blocked_activity.usename,
			blocking_locks.pid,
			blocking_activity.usename,
			blocked_activity.query,
			blocking_activity.query,
			blocked_activity.application_name,
			blocking_activity.application_name
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

// AutovacuumTable holds per-table vacuum statistics from pg_stat_user_tables.
type AutovacuumTable struct {
	SchemaName       string
	TableName        string
	DeadTuples       int64
	LiveTuples       int64
	DeadPct          *float64
	TotalSize        string
	LastVacuum       *time.Time
	LastAnalyze      *time.Time
	LastAutovacuum   *time.Time
	LastAutoanalyze  *time.Time
	AutovacuumCount  int64
}

func FetchAutovacuum(ctx context.Context, db *sql.DB, limit int) ([]AutovacuumTable, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			s.schemaname,
			s.relname,
			s.n_dead_tup,
			s.n_live_tup,
			CASE WHEN s.n_live_tup + s.n_dead_tup = 0 THEN NULL
				 ELSE ROUND(100.0 * s.n_dead_tup / (s.n_live_tup + s.n_dead_tup), 1)
			END AS dead_pct,
			pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size,
			s.last_vacuum,
			s.last_analyze,
			s.last_autovacuum,
			s.last_autoanalyze,
			s.autovacuum_count
		FROM pg_stat_user_tables s
		JOIN pg_class c ON c.oid = s.relid
		ORDER BY s.n_dead_tup DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AutovacuumTable
	for rows.Next() {
		var t AutovacuumTable
		var deadPct sql.NullFloat64
		if err := rows.Scan(
			&t.SchemaName, &t.TableName, &t.DeadTuples, &t.LiveTuples, &deadPct,
			&t.TotalSize,
			&t.LastVacuum, &t.LastAnalyze, &t.LastAutovacuum, &t.LastAutoanalyze,
			&t.AutovacuumCount,
		); err != nil {
			return nil, err
		}
		if deadPct.Valid {
			t.DeadPct = &deadPct.Float64
		}
		results = append(results, t)
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
		LIMIT $1`, limit)
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
		LIMIT $1`, limit)
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
	rows, err := db.QueryContext(ctx, `
		SELECT
			queryid::text,
			query,
			calls,
			ROUND(total_exec_time::numeric) AS total_ms,
			ROUND(mean_exec_time::numeric, 2) AS mean_ms,
			ROUND(((shared_blks_hit + shared_blks_read) * 8.0 / 1024)::numeric, 1) AS buffer_mb,
			ROUND((temp_blks_written * 8.0 / 1024)::numeric, 1) AS temp_mb,
			COALESCE(
				ROUND((100.0 * total_exec_time / NULLIF(SUM(total_exec_time) OVER(), 0))::numeric, 1),
				0
			) AS load_pct
		FROM pg_stat_statements
		ORDER BY total_exec_time DESC
		LIMIT $1`, limit)
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
	SlotName    string
	Plugin      string
	SlotType    string
	Database    *string
	Active      bool
	ActivePID   *int
	WALLag      string
	SafeWALSize *string // PG 13+; nil when slot has no confirmed_flush_lsn
	TwoPhase    bool    // PG 14+
}

func FetchReplicationSlots(ctx context.Context, db *sql.DB) ([]ReplicationSlot, error) {
	twoPhaseExpr := "false AS two_phase"
	if pgMajorVersion() >= 14 {
		twoPhaseExpr = "two_phase"
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
			` + twoPhaseExpr + `
		FROM pg_replication_slots`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ReplicationSlot
	for rows.Next() {
		var s ReplicationSlot
		var walLag, safeWAL sql.NullString
		if err := rows.Scan(
			&s.SlotName, &s.Plugin, &s.SlotType, &s.Database, &s.Active, &s.ActivePID,
			&walLag, &safeWAL, &s.TwoPhase,
		); err != nil {
			return nil, err
		}
		if walLag.Valid {
			s.WALLag = walLag.String
		}
		if safeWAL.Valid {
			s.SafeWALSize = &safeWAL.String
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
	Name        string
	Setting     string
	Unit        string
	Category    string
	Source      string
	Description string
}

// FetchPgConfig returns all (or filtered) rows from pg_settings.
// Pass an empty string for filter to return all settings.
func FetchPgConfig(ctx context.Context, db *sql.DB, filter string) ([]PgSetting, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name,
			   setting,
			   COALESCE(unit, '') AS unit,
			   category,
			   source,
			   COALESCE(short_desc, '') AS description
		FROM pg_settings
		WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR category ILIKE '%' || $1 || '%')
		ORDER BY category, name`, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []PgSetting
	for rows.Next() {
		var s PgSetting
		if err := rows.Scan(&s.Name, &s.Setting, &s.Unit, &s.Category, &s.Source, &s.Description); err != nil {
			return nil, err
		}
		results = append(results, s)
	}
	return results, rows.Err()
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

var autovacuumParamNames = []string{
	"autovacuum_vacuum_scale_factor",
	"autovacuum_vacuum_threshold",
	"autovacuum_analyze_scale_factor",
	"autovacuum_analyze_threshold",
	"autovacuum_vacuum_cost_delay",
	"autovacuum_vacuum_cost_limit",
	"autovacuum_freeze_min_age",
	"autovacuum_freeze_max_age",
	"autovacuum_freeze_table_age",
}

func FetchAutovacuumParams(ctx context.Context, db *sql.DB, schema, table string) ([]AutovacuumParam, error) {
	// Read table's reloptions as a comma-separated string.
	var relopts string
	_ = db.QueryRowContext(ctx, `
		SELECT COALESCE(array_to_string(c.reloptions, ','), '')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2`,
		schema, table,
	).Scan(&relopts)

	tableParams := make(map[string]string)
	for _, opt := range strings.Split(relopts, ",") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		parts := strings.SplitN(opt, "=", 2)
		if len(parts) == 2 {
			tableParams[parts[0]] = parts[1]
		}
	}

	// Read global values from pg_settings.
	rows, err := db.QueryContext(ctx, `
		SELECT name, setting, COALESCE(unit, '')
		FROM pg_settings
		WHERE name IN (
			'autovacuum_vacuum_scale_factor',
			'autovacuum_vacuum_threshold',
			'autovacuum_analyze_scale_factor',
			'autovacuum_analyze_threshold',
			'autovacuum_vacuum_cost_delay',
			'autovacuum_vacuum_cost_limit',
			'autovacuum_freeze_min_age',
			'autovacuum_freeze_max_age',
			'autovacuum_freeze_table_age'
		)
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	globalMap := make(map[string]AutovacuumParam)
	for rows.Next() {
		var name, setting, unit string
		if err := rows.Scan(&name, &setting, &unit); err != nil {
			return nil, err
		}
		globalMap[name] = AutovacuumParam{Name: name, GlobalValue: setting, Unit: unit}
	}

	var results []AutovacuumParam
	for _, name := range autovacuumParamNames {
		p := globalMap[name]
		p.Name = name
		p.TableValue = tableParams[name] // "" if not customized
		results = append(results, p)
	}
	return results, rows.Err()
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
		LIMIT $1`, limit)
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
			sent_lsn::text,
			write_lsn::text,
			flush_lsn::text,
			replay_lsn::text,
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

// CheckpointStats holds pg_stat_bgwriter counters for checkpoint and background writer activity.
type CheckpointStats struct {
	CheckpointsTimed    int64
	CheckpointsReq      int64
	BuffersCheckpoint   int64
	BuffersClean        int64
	MaxwrittenClean     int64
	BuffersBackend      int64
	BuffersBackendFsync int64
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
}

func FetchMemoryStats(ctx context.Context, db *sql.DB) (MemoryStats, error) {
	var stats MemoryStats

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
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var c MemoryConfig
		if err := rows.Scan(&c.Name, &c.Setting, &c.Unit, &c.ShortDesc); err != nil {
			return stats, err
		}
		stats.Configs = append(stats.Configs, c)
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}

	var hit, read int64
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(blks_hit), 0), COALESCE(SUM(blks_read), 0)
		FROM pg_stat_database`,
	).Scan(&hit, &read); err != nil {
		return stats, err
	}
	if total := hit + read; total > 0 {
		stats.CacheHitRatio = float64(hit) / float64(total) * 100
	}

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
		&stats.Checkpoint.BuffersBackend, &stats.Checkpoint.BuffersBackendFsync, &stats.Checkpoint.BuffersAlloc,
		&stats.Checkpoint.StatsReset,
	); err != nil {
		return stats, err
	}

	return stats, nil
}
