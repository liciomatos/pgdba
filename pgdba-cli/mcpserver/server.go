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
		mcp.WithDescription("Sessions blocked by row-level or relation locks, including the blocking session's statement."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckBlockedQueries)

	addTool(s, mcp.NewTool("check_connections",
		mcp.WithDescription("Connection count by state (active, idle, idle in transaction, …) and percentage of max_connections used."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckConnections)

	addTool(s, mcp.NewTool("check_wait_events",
		mcp.WithDescription("Active wait events grouped by type (Lock, IO, LWLock, CPU, …) with percentage distribution."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckWaitEvents)

	addTool(s, mcp.NewTool("check_replication_slots",
		mcp.WithDescription("Replication slots with WAL accumulation size, slot type, database, and active status."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckReplicationSlots)

	addTool(s, mcp.NewTool("check_users",
		mcp.WithDescription("Login roles with superuser/createdb/replication flags, connection limit, and expiry date."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckUsers)

	addTool(s, mcp.NewTool("check_roles",
		mcp.WithDescription("Group roles (non-login) with privilege flags and member list."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckRoles)

	addTool(s, mcp.NewTool("check_extensions",
		mcp.WithDescription("Installed PostgreSQL extensions with version, schema, and description."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckExtensions)

	// --- tools with optional parameters ---
	addTool(s, mcp.NewTool("check_slow_queries",
		mcp.WithDescription("Top queries by mean execution time from pg_stat_statements. Requires the pg_stat_statements extension; works with every extension version (pre-1.8 total_time/mean_time columns included) and reports pg_stat_statements_version, with a warning when ALTER EXTENSION pg_stat_statements UPDATE is due."),
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
		mcp.WithDescription("Non-idle sessions whose current query runs longer than the given threshold, with user, application, client address, backend type and wait event. Client backends only unless include_background is true (autovacuum workers, walsenders, … have no user)."),
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
		mcp.WithDescription("Tables with the most dead tuples, with each table's autovacuum overrides and effective throttling: cost_delay/cost_limit actually used (table reloption or global, with the -1 fallback to vacuum_cost_* resolved), the resulting throughput in cost units/s and max uncached read MB/s, and how many times slower than the global settings an override makes it (slowdown_factor). trigger_ratio = dead tuples ÷ autovacuum trigger threshold (≥ 1 = vacuum due). status: critical when trigger_ratio ≥ 2 or an override is ≥ 10× slower than global; warning when trigger_ratio ≥ 1, any override is slower than global, or autovacuum is disabled for the table."),
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
		mcp.WithDescription("Top queries by total execution time from pg_stat_statements, with load percentage and buffer/temp usage. Requires pg_stat_statements (any version; pre-1.8 column names handled) and reports pg_stat_statements_version."),
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
		mcp.WithDescription("Tables in the given schema with estimated row count, size, and column definitions."),
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
		mcp.WithDescription("XID wraparound risk for every database: age of datfrozenxid and percentage toward PostgreSQL shutdown (2.1B limit)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckFreezeByDatabase)

	addTool(s, mcp.NewTool("check_freeze_by_table",
		mcp.WithDescription("Top tables by relfrozenxid age with wraparound risk percentage relative to autovacuum_freeze_max_age."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(50),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckFreezeByTable)

	addTool(s, mcp.NewTool("check_streaming_standbys",
		mcp.WithDescription("Streaming replication standbys from pg_stat_replication with write/flush/replay lag and byte lag."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckStreamingStandbys)

	addTool(s, mcp.NewTool("check_replication_config",
		mcp.WithDescription("Replication-related pg_settings parameters (wal_level, synchronous_commit, slots, archive, etc.) with contextual hints about risk or misconfiguration."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckReplicationConfig)

	addTool(s, mcp.NewTool("check_database_sizes",
		mcp.WithDescription("On-disk size of every database and tablespace, plus the total cluster size."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckDatabaseSizes)

	addTool(s, mcp.NewTool("check_temp_files",
		mcp.WithDescription("Temp file spill activity per database from pg_stat_database (temp_files, temp_bytes) since the last stats reset. High values suggest work_mem is too low."),
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
		mcp.WithDescription("Logical replication publications defined on this server with per-operation flags and table count."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckPublications)

	addTool(s, mcp.NewTool("check_subscriptions",
		mcp.WithDescription("Logical replication subscriptions with connection status, received LSN, and error counts (PG15+)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSubscriptions)

	addTool(s, mcp.NewTool("check_publication_tables",
		mcp.WithDescription("Tables and statistics (live/dead rows, seq/idx scans, last vacuum) for a specific publication. PG15+ also returns column filters and row filters."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Publication name")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckPublicationTables)

	addTool(s, mcp.NewTool("check_subscription_tables",
		mcp.WithDescription("Per-table sync state and row stats (live rows, INS/UPD/DEL) for a specific subscription."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Subscription name")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckSubscriptionTables)

	addTool(s, mcp.NewTool("check_toast_tables",
		mcp.WithDescription("List user tables with TOAST data — size, toast %, dead tuples, cache hit ratio, and last autovacuum on the TOAST heap."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	), handleCheckToastTables)

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
