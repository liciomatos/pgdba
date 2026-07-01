package mcpserver

import (
	"fmt"
	"log"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Serve registers all diagnostic tools and starts the SSE server on the given port.
// The caller is responsible for ensuring config.Config.DB is connected before calling.
func Serve(port int) error {
	s := server.NewMCPServer("pgdba", config.Config.Version,
		server.WithToolCapabilities(false),
	)

	// --- tools without parameters ---
	s.AddTool(mcp.NewTool("check_dashboard",
		mcp.WithDescription("PostgreSQL health summary: connections, active/blocked queries, cache hit ratio, dead tuples, invalid indexes, replication slots."),
	), handleCheckDashboard)

	s.AddTool(mcp.NewTool("check_blocked_queries",
		mcp.WithDescription("Sessions blocked by row-level or relation locks, including the blocking session's statement."),
	), handleCheckBlockedQueries)

	s.AddTool(mcp.NewTool("check_connections",
		mcp.WithDescription("Connection count by state (active, idle, idle in transaction, …) and percentage of max_connections used."),
	), handleCheckConnections)

	s.AddTool(mcp.NewTool("check_wait_events",
		mcp.WithDescription("Active wait events grouped by type (Lock, IO, LWLock, CPU, …) with percentage distribution."),
	), handleCheckWaitEvents)

	s.AddTool(mcp.NewTool("check_replication_slots",
		mcp.WithDescription("Replication slots with WAL accumulation size, slot type, database, and active status."),
	), handleCheckReplicationSlots)

	s.AddTool(mcp.NewTool("check_users",
		mcp.WithDescription("Login roles with superuser/createdb/replication flags, connection limit, and expiry date."),
	), handleCheckUsers)

	s.AddTool(mcp.NewTool("check_roles",
		mcp.WithDescription("Group roles (non-login) with privilege flags and member list."),
	), handleCheckRoles)

	s.AddTool(mcp.NewTool("check_extensions",
		mcp.WithDescription("Installed PostgreSQL extensions with version, schema, and description."),
	), handleCheckExtensions)

	// --- tools with optional parameters ---
	s.AddTool(mcp.NewTool("check_slow_queries",
		mcp.WithDescription("Top queries by mean execution time from pg_stat_statements. Requires the pg_stat_statements extension."),
		mcp.WithNumber("threshold_ms",
			mcp.Description("Minimum mean execution time in ms to include a query"),
			mcp.DefaultNumber(1000.0),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
	), handleCheckSlowQueries)

	s.AddTool(mcp.NewTool("check_long_running_queries",
		mcp.WithDescription("Active queries running longer than the given threshold."),
		mcp.WithInteger("min_duration_seconds",
			mcp.Description("Minimum query duration in seconds"),
			mcp.DefaultNumber(5),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
	), handleCheckLongRunningQueries)

	s.AddTool(mcp.NewTool("check_autovacuum",
		mcp.WithDescription("Tables with the most dead tuples, last vacuum/analyze timestamps, and autovacuum count."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
	), handleCheckAutovacuum)

	s.AddTool(mcp.NewTool("check_index_usage",
		mcp.WithDescription("Index scan statistics sorted by scan count (ascending). Highlights invalid and unused indexes."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(50),
		),
	), handleCheckIndexUsage)

	s.AddTool(mcp.NewTool("check_cache_hit",
		mcp.WithDescription("Buffer cache hit ratio per table (heap and index blocks), sorted by heap blocks read descending."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(50),
		),
	), handleCheckCacheHit)

	s.AddTool(mcp.NewTool("check_query_load",
		mcp.WithDescription("Top queries by total execution time from pg_stat_statements, with load percentage and buffer/temp usage. Requires pg_stat_statements."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(20),
		),
	), handleCheckQueryLoad)

	s.AddTool(mcp.NewTool("check_pg_config",
		mcp.WithDescription("PostgreSQL runtime parameters from pg_settings. Optionally filter by name or category substring."),
		mcp.WithString("filter",
			mcp.Description("Substring to match against parameter name or category (case-insensitive). Empty returns all."),
			mcp.DefaultString(""),
		),
	), handleCheckPgConfig)

	s.AddTool(mcp.NewTool("check_schema",
		mcp.WithDescription("Tables in the given schema with estimated row count, size, and column definitions."),
		mcp.WithString("schema",
			mcp.Description("Schema name to inspect"),
			mcp.DefaultString("public"),
		),
	), handleCheckSchema)

	s.AddTool(mcp.NewTool("check_autovacuum_detail",
		mcp.WithDescription("Detailed autovacuum statistics for one table: live/dead tuples, vacuum history, freeze status, and custom autovacuum parameters vs globals."),
		mcp.WithString("schema",
			mcp.Description("Schema name"),
			mcp.DefaultString("public"),
		),
		mcp.WithString("table",
			mcp.Description("Table name (required)"),
			mcp.DefaultString(""),
		),
	), handleCheckAutovacuumDetail)

	s.AddTool(mcp.NewTool("check_freeze_by_database",
		mcp.WithDescription("XID wraparound risk for every database: age of datfrozenxid and percentage toward PostgreSQL shutdown (2.1B limit)."),
	), handleCheckFreezeByDatabase)

	s.AddTool(mcp.NewTool("check_freeze_by_table",
		mcp.WithDescription("Top tables by relfrozenxid age with wraparound risk percentage relative to autovacuum_freeze_max_age."),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of rows to return"),
			mcp.DefaultNumber(50),
		),
	), handleCheckFreezeByTable)

	s.AddTool(mcp.NewTool("check_streaming_standbys",
		mcp.WithDescription("Streaming replication standbys from pg_stat_replication with write/flush/replay lag and byte lag."),
	), handleCheckStreamingStandbys)

	s.AddTool(mcp.NewTool("check_replication_config",
		mcp.WithDescription("Replication-related pg_settings parameters (wal_level, synchronous_commit, slots, archive, etc.) with contextual hints about risk or misconfiguration."),
	), handleCheckReplicationConfig)

	addr := fmt.Sprintf(":%d", port)
	sseServer := server.NewSSEServer(s, server.WithBaseURL(fmt.Sprintf("http://localhost:%d", port)))
	log.Printf("pgdba MCP server listening on %s", addr)
	return sseServer.Start(addr)
}
