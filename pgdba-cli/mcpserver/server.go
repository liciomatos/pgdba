package mcpserver

import (
	"fmt"
	"log"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/liciomatos/pgdba-cli/util"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Serve registers all diagnostic tools and starts the SSE server on the given port.
// The caller is responsible for ensuring config.Config.DB is connected before calling.
//
// Every response uses the same envelope: {"meta": {host, database, server_version,
// role, uptime, stats_reset…},
// "warnings": [parts that failed or need attention], "result": <tool-specific>}.
//
// Every tool takes an optional "target" (the primary or a --replica name) and runs
// against that server; see targets.go.
//
// Every tool is annotated read-only/non-destructive: all handlers only run SELECT
// queries via Fetch*, never Exec or DDL. Without these hints, MCP clients assume
// the worst and treat every tool as potentially destructive.
func Serve(port int) error {
	s := server.NewMCPServer("pgdba", config.Config.Version,
		server.WithToolCapabilities(false),
	)

	// --- tools without parameters ---
	addTool(s, mcp.NewTool("check_dashboard",
		mcp.WithDescription("PostgreSQL health summary: connections, active/blocked queries, cache hit ratio, dead tuples, invalid indexes, replication slots. Each part runs independently: a failing part (e.g. pg_stat_statements missing) is listed in warnings and the rest is still returned; slow_query_count is null with slow_query_unavailable_reason when pg_stat_statements can't be read."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckDashboard)

	addTool(s, mcp.NewTool("check_blocked_queries",
		mcp.WithDescription("Lock waits as a tree, from pg_blocking_pids() (also catches sessions queued behind another waiter, e.g. behind a pending ACCESS EXCLUSIVE). blocked: one row per (waiting session, session it waits for) with lock_type, lock_mode, relation, wait_seconds (PG14+), both transactions' ages, blocking_state (idle in transaction = the blocker is not even running), root_blocker_pid and depth (1 = direct); both statements truncated to 500 characters. root_blockers: heads of the chains with how many sessions wait on each — terminating a root releases its whole chain. lock_table: pg_locks counts by type and mode, advisory locks held, and shared lock table usage vs max_locks_per_transaction × (max_connections + max_prepared_transactions). status: critical while any session waits; lock_table status warning ≥ 50%, critical ≥ 80% (full = out of shared memory)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckBlockedQueries)

	addTool(s, mcp.NewTool("check_connections",
		mcp.WithDescription("Connection count by state and % of max_connections used, plus where client connections come from (top 5 applications, users and client addresses), the 5 sessions idle in transaction the longest (with idle time, transaction age and last query) and the age of the oldest open transaction. status: critical at ≥ 90% of max_connections or a session idle in transaction ≥ 1 h; warning at ≥ 70% or idle in transaction ≥ 5 min."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckConnections)

	addTool(s, mcp.NewTool("check_wait_events",
		mcp.WithDescription("What sessions wait on, sampled: pg_stat_activity is read `samples` times `interval_ms` apart and sessions are averaged per wait event into average active sessions (aas, the Performance Insights unit); total_aas equals the sum of the events' aas. Idle sessions (state idle, e.g. Client:ClientRead) and idle background processes (wait_event_type Activity) are excluded unless include_idle is true. \"CPU\" = running, not waiting. status: Lock critical from 1 aas and warning below; IO, LWLock and BufferPin warning from 1 aas."),
		mcp.WithInteger("samples",
			mcp.Description("Number of pg_stat_activity samples"),
			mcp.DefaultNumber(20),
		),
		mcp.WithInteger("interval_ms",
			mcp.Description("Milliseconds between samples ((samples-1) × interval_ms ≤ 60 s; the call waits that long)"),
			mcp.DefaultNumber(1000),
		),
		mcp.WithBoolean("include_idle",
			mcp.Description("Also count idle sessions and idle background processes (the old single-snapshot view)"),
			mcp.DefaultBool(false),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckWaitEvents)

	addTool(s, mcp.NewTool("check_replication_slots",
		mcp.WithDescription("Replication slots with WAL retained in bytes (wal_lag_bytes, measured from the replay position on a standby), safe_wal_size_bytes, wal_status, slot type, database and active status. status: critical when wal_status is lost or unreserved, warning when the slot is inactive."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckReplicationSlots)

	addTool(s, mcp.NewTool("check_users",
		mcp.WithDescription("Login roles with superuser/createdb/replication flags, connection limit and password expiry. status: critical when the password has expired, warning when it expires within 7 days."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckUsers)

	addTool(s, mcp.NewTool("check_roles",
		mcp.WithDescription("Group roles (non-login) with privilege flags and member list. status is informational (always ok)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckRoles)

	addTool(s, mcp.NewTool("check_extensions",
		mcp.WithDescription("Installed PostgreSQL extensions with installed version, default_version (what this server ships), schema and description. status: warning when the installed version is older than default_version — run ALTER EXTENSION ... UPDATE."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckExtensions)

	// --- tools with optional parameters ---
	addTool(s, mcp.NewTool("check_slow_queries",
		mcp.WithDescription("Top queries by mean execution time from pg_stat_statements (query text truncated to 500 characters, query_truncated marks it). Works with every extension version (pre-1.8 total_time/mean_time columns included) and reports pg_stat_statements_version, with a warning when ALTER EXTENSION pg_stat_statements UPDATE is due. status: warning above threshold_ms, critical above twice it."),
		mcp.WithNumber("threshold_ms",
			mcp.Description("Minimum mean execution time in ms to include a query"),
			mcp.DefaultNumber(1000.0),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSlowQueries)

	addTool(s, mcp.NewTool("check_long_running_queries",
		mcp.WithDescription("Non-idle sessions whose current query runs longer than the given threshold, with user, application, client address, backend type and wait event (query truncated to 500 characters). Client backends only unless include_background is true (autovacuum workers, walsenders, … have no user). status: warning above 10 s, critical above 60 s."),
		mcp.WithInteger("min_duration_seconds",
			mcp.Description("Minimum query duration in seconds"),
			mcp.DefaultNumber(5),
		),
		mcp.WithBoolean("include_background",
			mcp.Description("Also include background processes (autovacuum workers, walsenders, parallel workers, …)"),
			mcp.DefaultBool(false),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckLongRunningQueries)

	addTool(s, mcp.NewTool("check_autovacuum",
		mcp.WithDescription("Tables with the most dead tuples, with each table's autovacuum overrides and effective throttling: cost_delay/cost_limit actually used (table reloption or global, with the -1 fallback to vacuum_cost_* resolved), the resulting throughput in cost units/s and max uncached read MB/s, and how many times slower than the global settings an override makes it (slowdown_factor). trigger_ratio = dead tuples ÷ autovacuum trigger threshold (≥ 1 = vacuum due); analyze_ratio = mod_since_analyze ÷ autoanalyze threshold (≥ 1 = analyze due) and never_analyzed for tables the planner only knows from default estimates — order_by=analyze ranks tables by stale statistics instead of dead tuples. status: critical when trigger_ratio ≥ 2 or an override is ≥ 10× slower than global; warning when trigger_ratio ≥ 1, any override is slower than global, or autovacuum is disabled for the table."),
		mcp.WithString("order_by",
			mcp.Description("dead_tuples (default) or analyze: stale statistics first (never analyzed with modifications, then highest analyze_ratio)"),
			mcp.Enum(util.AutovacuumOrderings...),
			mcp.DefaultString("dead_tuples"),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckAutovacuum)

	addTool(s, mcp.NewTool("check_index_usage",
		mcp.WithDescription("Index usage ranked by wasted space: size × (1 − share of the table's index scans), invalid indexes first. Indexes backing PRIMARY KEY / UNIQUE / EXCLUDE constraints are hidden unless include_constraints is true (redundancy is still checked against them, so a duplicate of the PK is flagged). Each index has tup_read_per_scan, table_scan_share_pct, size in bytes, and redundant_with: possible redundancies with another index of the same table (duplicate = same columns, prefix = its columns lead a longer btree index, same_columns_different_order); partial and expression indexes aren't compared. status: critical when INVALID, warning when possibly redundant or never scanned and > 1 MB. Counters accumulate since meta.stats_reset.database."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return (0 = all)"),
			mcp.DefaultNumber(50),
		),
		mcp.WithBoolean("include_constraints",
			mcp.Description("Also list indexes backing PRIMARY KEY / UNIQUE / EXCLUDE constraints"),
			mcp.DefaultBool(false),
		),
		mcp.WithBoolean("compare",
			mcp.Description("Run on the primary and every --replica and show each index's counters side by side (per_target), with unused_everywhere — standbys keep their own idx_scan, so an index unused on the primary may serve replica reads. Ignores target."),
			mcp.DefaultBool(false),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckIndexUsage)

	addTool(s, mcp.NewTool("check_cache_hit",
		mcp.WithDescription("Buffer cache hit ratios: a summary with heap, index and database ratios — each labeled with its formula, the same numbers check_dashboard and check_memory_stats report — plus per-table heap/index ratios sorted by heap blocks read. Cumulative since the stats reset unless delta_seconds is set: the counters are then read twice that many seconds apart and the ratios cover only that interval. Summary status: critical < 90%, warning < 99%; per table: critical < 70%, warning < 90%."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of tables to return (0 = all)"),
			mcp.DefaultNumber(50),
		),
		mcp.WithNumber("delta_seconds",
			mcp.Description("Measure over this many seconds instead of since the stats reset (0–60; the call waits that long)"),
			mcp.DefaultNumber(0),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckCacheHit)

	addTool(s, mcp.NewTool("check_query_load",
		mcp.WithDescription("Top statements from pg_stat_statements with every counter the installed extension version has (query truncated to 500 characters): calls, rows, total/mean execution ms and load_pct (share of all execution time); plans and total_plan_ms (extension ≥ 1.8, needs pg_stat_statements.track_planning); shared_blks_hit/read/dirtied/written with hit_pct and buffer_mb; blk_read/write_time_ms (needs track_io_timing); temp_blks_read/written, temp_mb and temp_blk_time_ms (≥ 1.10); wal_records/wal_fpi/wal_bytes (≥ 1.8); jit_functions and jit_time_ms (≥ 1.10). Fields the extension version lacks are null. order_by picks the ranking (e.g. shared_blks_read for I/O-heavy statements, shared_blks_dirtied/wal_bytes for write amplification, temp_blks_written for work_mem spills, plan_time, jit_time). delta_seconds samples twice and reports only what ran in that window (load_pct then covers the window) instead of everything since the last reset. Warnings say when track_io_timing or track_planning is off. status: warning when one statement takes ≥ 20% of the execution time, critical from 40%."),
		mcp.WithString("order_by",
			mcp.Description("Ranking column"),
			mcp.Enum(util.StatementOrderings...),
			mcp.DefaultString("total_time"),
		),
		mcp.WithNumber("delta_seconds",
			mcp.Description("Measure over this many seconds instead of since pg_stat_statements_reset() (0–60)"),
			mcp.DefaultNumber(0),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckQueryLoad)

	addTool(s, mcp.NewTool("check_pg_config",
		mcp.WithDescription("PostgreSQL runtime parameters from pg_settings (name, setting, unit, source, category, pending_restart). Without arguments returns only the key tuning parameters (memory, autovacuum, checkpoint, WAL, planner, timeouts, logging) without descriptions, so the output stays small. Paged with limit/offset; total tells how many rows matched."),
		mcp.WithString("filter",
			mcp.Description("Substring to match against parameter name or category (case-insensitive). When set, scope defaults to all."),
			mcp.DefaultString(""),
		),
		mcp.WithString("scope",
			mcp.Description("key = curated tuning parameters (default without filter); modified = changed from the built-in default (source not default/override/client); all = every parameter (default with a filter)"),
			mcp.Enum("key", "modified", "all"),
		),
		mcp.WithBoolean("only_modified",
			mcp.Description("Shortcut for scope=modified"),
			mcp.DefaultBool(false),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of settings to return (0 = no limit)"),
			mcp.DefaultNumber(100),
		),
		mcp.WithInteger("offset",
			mcp.Description("Number of matching settings to skip, for paging"),
			mcp.DefaultNumber(0),
		),
		mcp.WithBoolean("include_description",
			mcp.Description("Include pg_settings.short_desc for each parameter"),
			mcp.DefaultBool(false),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckPgConfig)

	addTool(s, mcp.NewTool("check_schema",
		mcp.WithDescription("Tables in the given schema with estimated row count, size (size_bytes / size_pretty) and column definitions. status is informational (always ok)."),
		mcp.WithString("schema",
			mcp.Description("Schema name to inspect"),
			mcp.DefaultString("public"),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSchema)

	addTool(s, mcp.NewTool("check_autovacuum_detail",
		mcp.WithDescription("Detailed autovacuum statistics for one table: live/dead tuples, vacuum history, freeze status, custom autovacuum parameters vs globals, and the computed autovacuum thresholds (dead-tuple, insert and analyze triggers; XID/MXID anti-wraparound, aggressive-scan, freeze-min-age and failsafe limits) using table reloptions when set and global settings otherwise."),
		mcp.WithString("schema",
			mcp.Description("Schema name"),
			mcp.DefaultString("public"),
		),
		mcp.WithString("table",
			mcp.Description("Table name (required)"),
			mcp.DefaultString(""),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckAutovacuumDetail)

	addTool(s, mcp.NewTool("check_replica_identity",
		mcp.WithDescription("Tables without a usable replica identity for logical replication: no primary key with REPLICA IDENTITY DEFAULT, REPLICA IDENTITY NOTHING, a dropped identity index, or REPLICA IDENTITY FULL on a table in a pglogical replication set (pglogical needs a PK or USING INDEX and does not support FULL). Covers native publications and, when the pglogical extension is installed, the local node's replication sets. Status \"critical\" means a publication/set already replicates UPDATE/DELETE for the table; \"warning\" means it would break once replicated (or only INSERTs are replicated). Includes a suggested fix (REPLICA IDENTITY DEFAULT / USING INDEX / FULL, or adding a primary key for pglogical). Read-only: never runs the fix."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckReplicaIdentity)

	addTool(s, mcp.NewTool("check_server_info",
		mcp.WithDescription("Server identity and statistics context only — the meta block every tool returns: host, database, server_version, role (primary/replica), postmaster start time, uptime, the stats_reset of the database / bgwriter / checkpointer (PG17+) counters with seconds since each reset, and the pg_stat_statements version."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckServerInfo)

	addTool(s, mcp.NewTool("check_xmin_horizon",
		mcp.WithDescription("Everything holding back the xmin horizon (what keeps vacuum from removing dead rows and freezing from advancing), oldest first: sessions with an open snapshot or XID, standbys via hot_standby_feedback, replication slots (xmin/catalog_xmin) and prepared transactions. defines_horizon marks the oldest. status per holder: critical when xmin_age ≥ 25% of autovacuum_freeze_max_age, warning from 5% or a transaction open > 1 h. Empty holders = nothing holds the horizon."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckXminHorizon)

	addTool(s, mcp.NewTool("check_vacuum_progress",
		mcp.WithDescription("VACUUMs running right now (manual and autovacuum, all databases) from pg_stat_progress_vacuum: table, phase, % of heap blocks scanned/vacuumed, index vacuum passes, dead-tuple memory (tuples on PG ≤ 16, bytes on PG17+), duration, and is_wraparound for anti-wraparound autovacuums. status: warning for anti-wraparound or more than one index pass (dead-tuple memory too small). Empty list = no vacuum running."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckVacuumProgress)

	addTool(s, mcp.NewTool("check_table_churn",
		mcp.WithDescription("Write activity per table since the stats reset, ranked by updates: n_tup_ins/upd/del/hot_upd, hot_pct (share of HOT updates), n_tup_newpage_upd (PG16+), fillfactor from reloptions (100 when unset) and sizes in bytes. status: warning when a table has ≥ 10,000 updates and hot_pct < 50, with a hint (lower fillfactor if 100, otherwise updates likely touch indexed columns). include_bloat adds a pgstattuple_approx estimate (dead tuple and free space %) for tables whose heap is ≤ bloat_max_table_bytes — larger tables and servers without the pgstattuple extension get bloat_skipped_reason instead of a full scan."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of tables to return (0 = all)"),
			mcp.DefaultNumber(20),
		),
		mcp.WithBoolean("include_bloat",
			mcp.Description("Estimate bloat with pgstattuple_approx (needs the pgstattuple extension and pg_stat_scan_tables or superuser)"),
			mcp.DefaultBool(false),
		),
		mcp.WithNumber("bloat_max_table_bytes",
			mcp.Description("Skip the bloat estimate for tables whose heap is larger than this (default 10 GiB)"),
			mcp.DefaultNumber(float64(util.DefaultBloatMaxTableBytes)),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckTableChurn)

	addTool(s, mcp.NewTool("check_io",
		mcp.WithDescription("Buffer I/O from pg_stat_io (PG16+) by backend_type, object and context: reads, writes, writebacks, extends, hits, evictions, reuses, fsyncs and bytes, plus who does the relation block writes (relation_writes_by_backend_type) — separating vacuum (context vacuum), checkpointer, background writer and client backends. status: warning when client backends do ≥ 25% of the relation writes in the normal context. On PG13–15 the result is {status: unsupported} with the server version."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckIO)

	addTool(s, mcp.NewTool("check_wal",
		mcp.WithDescription("WAL generation from pg_stat_wal (PG14+): wal_records, wal_fpi, wal_bytes, wal_buffers_full, fpi_pct (full-page images as % of records), bytes per record and per second since the reset, with full_page_writes, wal_compression, checkpoint_timeout, max_wal_size and wal_buffers. status: warning when fpi_pct ≥ 30 (checkpoints too frequent; consider max_wal_size/checkpoint_timeout or wal_compression) or wal_buffers_full exceeds 0.1% of records. On PG13 the result is {status: unsupported}."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckWAL)

	addTool(s, mcp.NewTool("check_freeze_by_database",
		mcp.WithDescription("XID wraparound risk for every database: age of datfrozenxid and percentage toward PostgreSQL shutdown (2.1B limit). status: warning above 7.1%, critical above 8.6%."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckFreezeByDatabase)

	addTool(s, mcp.NewTool("check_freeze_by_table",
		mcp.WithDescription("Top tables by relfrozenxid age with wraparound risk percentage relative to autovacuum_freeze_max_age and total size in bytes. status: warning above 50%, critical above 75% of autovacuum_freeze_max_age."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(50),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckFreezeByTable)

	addTool(s, mcp.NewTool("check_streaming_standbys",
		mcp.WithDescription("Streaming replication standbys from pg_stat_replication with write/flush/replay lag (intervals and *_lag_seconds) and byte lag. status: warning above 64 MB or 10 s of replay lag, critical above 1 GB or 60 s."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckStreamingStandbys)

	addTool(s, mcp.NewTool("check_replication_config",
		mcp.WithDescription("Replication-related pg_settings parameters (wal_level, synchronous_commit, slots, archive, etc.) with contextual hints about risk or misconfiguration. status per parameter follows the hint: critical (e.g. wal_level minimal, full_page_writes off), warning, or ok."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckReplicationConfig)

	addTool(s, mcp.NewTool("check_database_sizes",
		mcp.WithDescription("On-disk size of every database and tablespace (bytes and pretty), plus the total cluster size. status is informational (always ok)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckDatabaseSizes)

	addTool(s, mcp.NewTool("check_temp_files",
		mcp.WithDescription("Temp file spill per database from pg_stat_database (temp_files, temp_bytes, avg_bytes_per_file) with stats_reset (a note explains a never-reset null), plus the 5 statements that wrote the most temp data (top_queries, from pg_stat_statements: temp_blks_written and bytes). High values suggest work_mem is too low for those statements. status: warning for any temp file since the reset."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckTempFiles)

	addTool(s, mcp.NewTool("check_memory_stats",
		mcp.WithDescription("Memory-related config (shared_buffers, work_mem, ...), cluster-wide buffer cache hit ratio, and checkpoint/background writer activity. SQL-only, works against remote servers. Parts that fail are listed in warnings while the rest is still returned. cache_hit carries the heap, index and database ratios with their formulas (same values as check_dashboard / check_cache_hit); delta_seconds measures them over an interval instead of since the stats reset."),
		mcp.WithNumber("delta_seconds",
			mcp.Description("Measure the cache hit ratios over this many seconds instead of since the stats reset (0–60)"),
			mcp.DefaultNumber(0),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckMemoryStats)

	addTool(s, mcp.NewTool("check_publications",
		mcp.WithDescription("Logical replication publications defined on this server with per-operation flags and table count. status is informational (always ok)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckPublications)

	addTool(s, mcp.NewTool("check_subscriptions",
		mcp.WithDescription("Logical replication subscriptions with connection status, received LSN and error counts (PG15+). status: critical when enabled but the apply worker isn't running; warning when disabled or with apply/sync errors."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSubscriptions)

	addTool(s, mcp.NewTool("check_publication_tables",
		mcp.WithDescription("Tables and statistics (live/dead rows, seq/idx scans, last vacuum) for a specific publication. PG15+ also returns column filters and row filters. status is informational (always ok)."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Publication name")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckPublicationTables)

	addTool(s, mcp.NewTool("check_subscription_tables",
		mcp.WithDescription("Per-table sync state and row stats (live rows, INS/UPD/DEL) for a specific subscription. status: warning until the table reaches the ready state."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Subscription name")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSubscriptionTables)

	addTool(s, mcp.NewTool("check_toast_tables",
		mcp.WithDescription("User tables with TOAST data — TOAST size in bytes, toast %, dead tuples, cache hit ratio and last autovacuum on the TOAST heap. status from the TOAST cache hit ratio: critical < 70%, warning < 90%."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckToastTables)

	addTool(s, mcp.NewTool("check_recovery_conflicts",
		mcp.WithDescription("Standby-side replication health — call it with target set to a replica (on a primary every counter is 0 and a warning says so). Per database, pg_stat_database_conflicts: queries canceled because WAL replay needed something they held — confl_snapshot (vacuum on the primary removed rows the query could still see: hot_standby_feedback or max_standby_streaming_delay), confl_lock (ACCESS EXCLUSIVE from DDL, TRUNCATE or vacuum truncation), confl_bufferpin, confl_deadlock, confl_tablespace, confl_active_logicalslot (PG16+). Also the replay state: replay_paused, receive/replay LSN, replay_backlog_bytes (received but not replayed), last_replay_time and replay_lag_seconds (also grows while the primary is idle, so it only means lag when the backlog is > 0) and the pg_stat_wal_receiver row. Related settings are in check_replication_config; the primary's view of its standbys is check_streaming_standbys. status: warning for any conflict; replay_status uses check_streaming_standbys' thresholds (64 MB / 10 s warning, 1 GB / 60 s critical) and warns when replay is paused or no receiver is streaming."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckRecoveryConflicts)

	addTool(s, mcp.NewTool("check_database_health",
		mcp.WithDescription("Error and waste counters from pg_stat_database for every database since its stats_reset: commits, rollbacks and rollback_pct, deadlocks, recovery_conflicts, checksum_failures (null = data checksums disabled) with the last failure time, blk_read/write_time_ms (0 unless track_io_timing is on), tup_returned/fetched/inserted/updated/deleted, and on PG14+ session_time/active_time/idle_in_transaction_time (ms), idle_in_transaction_pct, sessions, sessions_abandoned (client vanished), sessions_fatal, sessions_killed; parallel_workers_to_launch/launched on PG18+. Temp files are in check_temp_files. status: critical for any checksum failure; warning for any deadlock, ≥ 5% rollbacks over ≥ 1,000 transactions, or ≥ 10% of session time idle in transaction."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckDatabaseHealth)

	addTool(s, mcp.NewTool("check_upsert_analysis",
		mcp.WithDescription("INSERT … ON CONFLICT workload. statements: pg_stat_statements entries with ON CONFLICT (calls, rows, time, shared blocks, wal_bytes on extension ≥ 1.8), the conflict_action (update/nothing), and a hint when DO UPDATE has no WHERE (unchanged rows are rewritten anyway). tables: each resolved target table with n_tup_ins/upd/hot_upd, hot_pct, update_share_pct (≈ conflict rate when the upsert is the main writer), fillfactor, dead tuples, index_count (every index a non-HOT update writes), arbiter_indexes (unique/exclusion indexes ON CONFLICT can use, with idx_scan and size) and owned sequences with pct_used of the column type's range and values_per_live_row (every attempted row calls nextval(), so conflicts burn values). Also live speculative_token_waits and active upserts waiting on locks. Target names are resolved with this session's search_path; pass table to analyze one table even without pg_stat_statements. status: worst of the tables (check_table_churn's HOT rule, sequences ≥ 75% warning / ≥ 90% critical) and warning while sessions wait on speculative insertion or locks."),
		mcp.WithString("table",
			mcp.Description("Also analyze this table (schema.table or a search_path name), even when no ON CONFLICT statement targets it"),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of ON CONFLICT statements, by total time (0 = all)"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckUpsertAnalysis)

	addTool(s, mcp.NewTool("check_operation_progress",
		mcp.WithDescription("Long-running maintenance and bulk operations in progress, from the pg_stat_progress_* views: CREATE INDEX / REINDEX (also CONCURRENTLY, with lockers_waiting while it waits for older transactions), ANALYZE, CLUSTER / VACUUM FULL, COPY (PG14+) and base backups — command, relation and index, phase, done/total in unit (blocks, tuples, sample blocks, bytes), pct_done when the total is known, duration_seconds and the statement. Plain VACUUM is in check_vacuum_progress. status: warning while a phase is \"waiting for …\" (no progress until other transactions finish)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckOperationProgress)

	addTool(s, mcp.NewTool("check_slru",
		mcp.WithDescription("Transaction-metadata caches from pg_stat_slru (Xact/commit_timestamp, MultiXactOffset/Member, Subtrans, Notify, Serial, …): blocks hit, read from disk, written, zeroed, flushes, truncates and hit_pct since stats_reset. Disk reads on Subtrans point at sessions with many subtransactions, on MultiXact at heavy row locking / foreign keys. On PG16+ also subtransaction_backends: sessions with open subtransactions (subxact_count) and whether their cache overflowed (> 64, e.g. SAVEPOINT or PL/pgSQL EXCEPTION blocks in a loop), which slows every snapshot on the server. status: warning for a cache with ≥ 1,000 reads and < 90% hits, or an overflowed session."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSLRU)

	addTool(s, mcp.NewTool("check_sequences",
		mcp.WithDescription("Every sequence with the column that uses it (owned by a serial/identity column, or referenced by a DEFAULT nextval()), most used first: data_type, column_type, last_value, max_value capped by the column type (a bigint sequence feeding an integer column stops at 2,147,483,647), increment, cycle and pct_used of the range. last_value is null when the sequence was never called or this role can't read it. status: warning from 75%, critical from 90% — plan the move to bigint before inserts fail."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of sequences (0 = all)"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSequences)

	addTool(s, mcp.NewTool("check_missing_fk_indexes",
		mcp.WithDescription("Foreign keys whose columns are not the leading columns of any valid, non-partial index on the referencing table. Without one, every DELETE or key UPDATE on the referenced table scans the referencing table while holding locks, and joins on the key can't use an index. Each row has the referencing and referenced tables, the columns, the referencing table's size (all partitions) and seq_scan count, referenced_updates_deletes (how often the scan happens) and a CREATE INDEX [CONCURRENTLY] suggestion. Constraints cloned onto partitions are reported once, on the parent. status: warning for each; critical when the referencing table is ≥ 1 GB and the referenced table gets updates or deletes."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of foreign keys (0 = all)"),
			mcp.DefaultNumber(50),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckMissingFKIndexes)

	addTool(s, mcp.NewTool("check_seq_scan_tables",
		mcp.WithDescription("Tables ranked by rows read through sequential scans since the stats reset: seq_scan, seq_tup_read, avg_rows_per_seq_scan, idx_scan, seq_scan_pct, n_live_tup, heap_bytes and last_seq_scan (PG16+). Large tables scanned often with many rows per scan usually serve queries that lack an index — look up the statements with check_query_load (order_by shared_blks_read) and the column statistics with check_column_stats. status: warning for a heap ≥ 100 MB with ≥ 100 seq scans averaging ≥ 100,000 rows."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of tables (0 = all)"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSeqScanTables)

	addTool(s, mcp.NewTool("check_column_stats",
		mcp.WithDescription("The planner's statistics for one table (pg_stats): per column null_frac, n_distinct (> 0 count, < 0 fraction of rows) with estimated_distinct, correlation (physical vs logical order), avg_width_bytes, the 5 most common values (cut to 60 characters) with their frequencies, and whether the column leads or is in an index; plus extended statistics (CREATE STATISTICS) on the table and when it was last analyzed. hints suggest partial indexes for low-cardinality or mostly-NULL indexed columns, BRIN for strongly correlated columns on big tables, and ANALYZE for columns without statistics. Use it to choose index columns and order, and to spot correlated columns that need CREATE STATISTICS."),
		mcp.WithString("table",
			mcp.Required(),
			mcp.Description("Table name: schema.table, or a name on the search_path"),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckColumnStats)

	addTool(s, mcp.NewTool("check_bloat_estimate",
		mcp.WithDescription("Statistics-based bloat estimate for tables and btree indexes (no extension needed): actual size vs the pages the rows should need at the relation's fillfactor, from pg_stats average widths. Needs recent ANALYZE; not_applicable marks rows the method can't estimate (columns without statistics, very wide index entries). For exact table numbers use check_table_churn with include_bloat (pgstattuple). Relations smaller than min_bytes are skipped. status: critical from 50% and 100 MB of bloat, warning from 30% and 10 MB — VACUUM FULL / pg_repack for tables, REINDEX CONCURRENTLY for indexes."),
		mcp.WithNumber("min_bytes",
			mcp.Description("Skip relations smaller than this many bytes"),
			mcp.DefaultNumber(float64(util.DefaultBloatMinBytes)),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of relations (0 = all)"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckBloatEstimate)

	addTool(s, mcp.NewTool("check_schema_hygiene",
		mcp.WithDescription("Schema settings that silently change behavior: disabled_triggers (internal = a foreign key's enforcement trigger, off after DISABLE TRIGGER ALL — the constraint is not enforced), not_valid_constraints (CHECK / FOREIGN KEY added NOT VALID and never validated: old rows unchecked, the planner can't use them — run ALTER TABLE … VALIDATE CONSTRAINT), unlogged_tables (emptied after a crash, absent on replicas), partitioned_tables (partition key, partition count, size, default partition size) and storage_parameters (non-autovacuum reloptions such as fillfactor, toast_tuple_target, parallel_workers). Tables without a primary key are in check_replica_identity. status: critical for a disabled foreign-key trigger; warning for other disabled triggers, NOT VALID constraints, and a default partition holding ≥ 10% of a partitioned table ≥ 100 MB."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSchemaHygiene)

	addTool(s, mcp.NewTool("check_functions",
		mcp.WithDescription("User function statistics from pg_stat_user_functions, by total time: calls, total_time_ms (including functions it calls), self_time_ms and mean_time_ms. Empty with a warning while track_functions is none (the default): set it to pl (PL functions) or all. Functions inlined by the planner (simple SQL functions) are never counted."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of functions (0 = all)"),
			mcp.DefaultNumber(20),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckFunctions)

	addTool(s, mcp.NewTool("check_security",
		mcp.WithDescription("Transport and authentication setup: ssl and password_encryption settings, client connections grouped by user, address and SSL use (pg_stat_ssl, with TLS version), and the pg_hba.conf rules (pg_hba_file_rules; needs superuser or a GRANT — otherwise a warning). Role attributes and password expiry are in check_users. status: critical for an hba line with an error, trust over the network, or cleartext password without hostssl; warning for trust on the local socket, md5 rules, network connections without SSL, and password_encryption other than scram-sha-256."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSecurity)

	addr := fmt.Sprintf(":%d", port)
	sseURL := fmt.Sprintf("http://localhost:%d/sse", port)
	sseServer := server.NewSSEServer(s, server.WithBaseURL(fmt.Sprintf("http://localhost:%d", port)))
	log.Printf("pgdba MCP server listening on %s", addr)
	printClientConfig(sseURL)
	return sseServer.Start(addr)
}

// printClientConfig prints ready-to-copy snippets so a user can wire this server
// into Claude Code without having to look up the URL format themselves.
func printClientConfig(sseURL string) {
	fmt.Printf(`
Configure Claude Code:

  claude mcp add --transport sse pgdba %s --scope project

Or add directly to .mcp.json:

  {
    "mcpServers": {
      "pgdba": {
        "url": "%s"
      }
    }
  }

Verify with: claude mcp list   (or /mcp inside a Claude Code session)

`, sseURL, sseURL)
}
