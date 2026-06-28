package util

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/liciomatos/pgdba-cli/config"
)

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
	ReplicationSlots int
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
	LastVacuum       *time.Time
	LastAnalyze      *time.Time
	LastAutovacuum   *time.Time
	LastAutoanalyze  *time.Time
	AutovacuumCount  int64
}

func FetchAutovacuum(ctx context.Context, db *sql.DB, limit int) ([]AutovacuumTable, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			schemaname,
			relname,
			n_dead_tup,
			n_live_tup,
			CASE WHEN n_live_tup + n_dead_tup = 0 THEN NULL
				 ELSE ROUND(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 1)
			END AS dead_pct,
			last_vacuum,
			last_analyze,
			last_autovacuum,
			last_autoanalyze,
			autovacuum_count
		FROM pg_stat_user_tables
		ORDER BY n_dead_tup DESC
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
	SlotName  string
	SlotType  string
	Database  *string
	Active    bool
	ActivePID *int
	WALLag    string
}

func FetchReplicationSlots(ctx context.Context, db *sql.DB) ([]ReplicationSlot, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			slot_name,
			slot_type,
			database,
			active,
			active_pid,
			pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS wal_lag
		FROM pg_replication_slots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ReplicationSlot
	for rows.Next() {
		var s ReplicationSlot
		if err := rows.Scan(&s.SlotName, &s.SlotType, &s.Database, &s.Active, &s.ActivePID, &s.WALLag); err != nil {
			return nil, err
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
