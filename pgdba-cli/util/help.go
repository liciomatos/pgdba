package util

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HelpTopicProvider is implemented by every screen that has a "?" help page. The
// navigator opens HelpModel for the child's topic; the text lives in screenHelp below
// so all user documentation is in one place.
type HelpTopicProvider interface {
	HelpTopic() string
}

// HelpItem is a "term — explanation" pair: a column, an indicator or a key.
type HelpItem struct {
	Term        string
	Explanation string
}

// ScreenHelp is a man-page style description of one screen.
type ScreenHelp struct {
	Title   string
	Purpose string
	Columns []HelpItem // what each column / metric means
	Keys    []HelpItem // screen-specific keys; navigation and "?" are added by the renderer
	Reading []string   // how to interpret the values, thresholds behind the colors, caveats
	Source  string     // catalog views / functions the data comes from
}

// commonKeys are shown on every help page after the screen's own keys.
var commonKeys = []HelpItem{
	{"↑ ↓", "move the selection (k / j also work)"},
	{"pgup pgdn", "page up / down; home / end jump to the first / last row"},
	{"r", "refresh — data is a snapshot taken when the screen opened"},
	{"?", "this help (q, esc or ? close it)"},
	{"q esc", "back to the previous screen"},
}

var screenHelp = map[string]ScreenHelp{
	"dashboard": {
		Title:   "Dashboard — instance health at a glance",
		Purpose: "Start screen. Summarizes activity, storage and server health of the connected database in one view, colored by severity, so you know which detail screen to open first.",
		Columns: []HelpItem{
			{"Connections / Heap cache hit", "bars: sessions used vs max_connections (running out blocks new logins); share of user-table heap block reads served from shared_buffers — yellow < 99%, red < 90% (the same ratio Memory & Checkpoint Stats shows)."},
			{"Commit rate", "share of transactions that commit instead of rolling back."},
			{"Active / Wait events", "sessions running a query now; sessions waiting on something (lock, I/O, …) — yellow ≥ 5, red ≥ 20."},
			{"Blocked queries", "sessions waiting on a lock held by another session — red when > 0 (key 4)."},
			{"Long-running (>60s)", "non-idle sessions running for more than a minute; yellow ≥ 1, red ≥ 5 (key 2)."},
			{"Slow queries", "pg_stat_statements entries whose mean time exceeds --slow-ms; yellow > 5, red > 20 (key 1). N/A without pg_stat_statements — the reason is listed under Warnings."},
			{"Dead tuples", "total across user tables; yellow > 10k, red > 100k (key 6)."},
			{"Temp files / Invalid indexes / DB size", "temp spill since stats reset (key t); indexes left INVALID by a failed CREATE INDEX CONCURRENTLY (key 7); database size (key S)."},
			{"Slot WAL retained", "WAL kept by the slot that retains the most vs max_slot_wal_keep_size — yellow ≥ 50%, red ≥ 80%; yellow when the limit is -1 (unlimited: a stalled slot can fill the disk) (key 3)."},
			{"Slots at risk", "slot count; wal_status unreserved (about to be invalidated, yellow) / lost (invalidated — the consumer must be rebuilt, red); PG18+: longest idle slot vs idle_replication_slot_timeout, yellow at ≥ 80%."},
			{"Archiving", "pg_stat_archiver: ok, off, or FAILING (red) — while archiving fails, WAL can't leave pg_wal."},
			{"Replication", "worst lag of physical standbys and of logical subscribers fed by this server — yellow > 64 MB / 10 s, red > 1 GB / 60 s; this server's subscriptions with apply/sync errors (PG15+) and conflicts (PG18+), yellow when > 0."},
			{"Freeze", "oldest database's XID age toward the 2.1B wraparound limit — yellow > 7.1%, red > 8.6% (key f)."},
			{"up … • stats since …", "server uptime and when this database's cumulative statistics were last reset, on the connection line — every counter on this screen accumulates since then (\"never reset\": since the cluster was created or its last crash recovery)."},
			{"Warnings", "parts of the dashboard that could not be read (missing extension, permission denied on a view, outdated pg_stat_statements). Each part is read on its own, so one failure never hides the rest."},
		},
		Keys: []HelpItem{
			{"1-0", "slow queries, long running, replication slots, blocked queries, connections, autovacuum, index usage, cache hit, users, roles"},
			{"p s e D L w f", "config, schema browser, extensions, switch database, query load, wait events, freeze monitor"},
			{"S t m R T I", "database sizes, temp files, memory, pub/sub, TOAST tables, replica identity"},
			{"X V H O", "xmin horizon (who holds back vacuum), vacuum progress, table churn / HOT updates, I/O & WAL"},
			{"q ctrl+c", "quit pgdba"},
		},
		Reading: []string{
			"Green = fine, yellow = look at it, red = act now. Every number links to a screen via the key shown in the footer.",
			"Counters such as dead tuples and temp files are cumulative since the last stats reset, not per second.",
			"On short terminals the blank lines between sections are dropped so everything fits.",
		},
		Source: "pg_stat_activity, pg_stat_database, pg_stat_user_tables, pg_index, pg_replication_slots, pg_stat_archiver, pg_stat_replication, pg_stat_subscription_stats (PG15+), pg_settings, pg_database, pg_stat_statements",
	},
	"slow_queries": {
		Title:   "Slow Queries (key 1) — statements with a high average execution time",
		Purpose: "Finds the statements that are slow *each time they run* (mean time above the --slow-ms threshold, default 1000 ms), the usual candidates for EXPLAIN and indexing.",
		Columns: []HelpItem{
			{"Query ID", "pg_stat_statements queryid — stable across calls with different literals."},
			{"Query", "normalized text ($1, $2 for literals), truncated; enter shows it in full."},
			{"Calls", "times executed since pg_stat_statements was last reset."},
			{"Total / Mean / Stddev (ms)", "total time, average per call, and its spread. A high stddev means the plan or data volume varies between calls."},
			{"Rows", "total rows returned or affected."},
		},
		Keys: []HelpItem{
			{"enter", "show the full query text"},
			{"/", "filter rows by text"},
		},
		Reading: []string{
			"Mean (ms) is yellow above the threshold and red above twice the threshold.",
			"Slow ≠ expensive: a 2 s query run twice a day matters less than a 50 ms query run a million times — use Query Load (L) for total cost.",
			"Requires the pg_stat_statements extension (shared_preload_libraries + CREATE EXTENSION). Any extension version works: before 1.8 the columns are total_time/mean_time.",
			"A yellow ⚠ line means the extension is older than the version this server ships (common after a major upgrade): run ALTER EXTENSION pg_stat_statements UPDATE.",
		},
		Source: "pg_stat_statements (mean_exec_time, or mean_time before 1.8, > --slow-ms)",
	},
	"long_running": {
		Title:   "Long Running Queries (key 2) — sessions busy for too long",
		Purpose: "Shows client sessions whose current statement started more than 5 seconds ago: stuck queries, runaway reports, and 'idle in transaction' sessions that hold locks and block vacuum. Background processes (autovacuum, walsenders) are left out.",
		Columns: []HelpItem{
			{"PID", "backend process id (use it with pg_cancel_backend / pg_terminate_backend)."},
			{"User / Application", "who is running it and from which client (application_name)."},
			{"State", "active = executing; idle in transaction = transaction open but doing nothing (holds locks and the xmin horizon)."},
			{"Duration (s)", "time since query_start; yellow > 10 s, red > 60 s."},
			{"Query", "current or last statement; enter shows it in full."},
		},
		Keys: []HelpItem{
			{"enter", "show the full query text"},
			{"k", "terminate the selected backend (pg_terminate_backend) — asks y/n first"},
			{"/", "filter rows by text"},
		},
		Reading: []string{
			"Terminating rolls back the session's open transaction. Prefer asking the owner first; there is no 'cancel only' action here.",
			"Long 'idle in transaction' sessions prevent vacuum from removing dead rows everywhere, not just in the tables they touched.",
		},
		Source: "pg_stat_activity (backend_type = 'client backend', state <> 'idle' and now() - query_start > 5 s)",
	},
	"replication_slots": {
		Title:   "Replication Slots (key 3) — WAL retained by physical and logical slots",
		Purpose: "Lists replication slots and how much WAL each one forces the server to keep. An inactive or lagging slot keeps WAL forever and can fill the disk.",
		Columns: []HelpItem{
			{"Slot Name / Plugin / Type", "slot identity; plugin (e.g. pgoutput) for logical slots; physical or logical."},
			{"Active", "a consumer is connected — green true, red false."},
			{"WAL Lag", "WAL retained for this slot (current LSN − restart_lsn)."},
			{"Safe WAL", "how much more WAL can be written before the slot is invalidated by max_slot_wal_keep_size; empty when unlimited."},
			{"Failover / Synced", "PG17+: slot is synchronized to standbys for failover / this is a synced copy."},
			{"Inactive Since", "PG18+: when the slot was last released by its consumer."},
		},
		Keys: []HelpItem{
			{"d", "drop the selected slot (pg_drop_replication_slot) — asks y/n first"},
			{"S", "streaming standbys (pg_stat_replication)"},
			{"p", "replication-related configuration"},
		},
		Reading: []string{
			"Dropping a slot used by a standby or subscriber breaks it: the consumer can't resume and must be rebuilt / re-synced.",
			"Inactive + growing WAL Lag is the classic disk-full cause. Set max_slot_wal_keep_size as a safety net.",
		},
		Source: "pg_replication_slots, pg_current_wal_lsn()",
	},
	"record_locks": {
		Title:   "Blocked Queries (key 4) — who is waiting on whom",
		Purpose: "Pairs every session waiting on a lock with the session that blocks it, so you can find the root blocker of a lock queue.",
		Columns: []HelpItem{
			{"Blocked PID / User / App", "the waiting session."},
			{"Blocking PID / User / App", "the session holding the lock it needs."},
			{"Blocked / Blocking Statement", "their current statements, truncated; enter shows both in full."},
		},
		Keys: []HelpItem{
			{"enter", "show both statements in full"},
			{"t", "terminate the blocking session of the selected row — asks y/n first"},
			{"a", "terminate every blocking session listed — asks y/n first"},
			{"/", "filter rows by text"},
		},
		Reading: []string{
			"A PID that appears as Blocking in many rows (and is not itself blocked) is the root of the queue — terminating it releases the rest.",
			"Blockers are often 'idle in transaction' sessions: an application that opened a transaction and never committed.",
		},
		Source: "pg_stat_activity + pg_blocking_pids()",
	},
	"connections": {
		Title:   "Connections (key 5) — sessions by state",
		Purpose: "Counts sessions per state against max_connections to spot connection exhaustion and leaks, and shows who opens them and which sessions sit idle in a transaction.",
		Columns: []HelpItem{
			{"State", "active, idle, idle in transaction, idle in transaction (aborted), …"},
			{"Count", "number of sessions in that state."},
			{"used / max", "sessions vs max_connections — yellow from 70%, red from 90%."},
			{"Top applications / users / clients", "the 5 application_names, users and client addresses with the most client connections."},
			{"Oldest transaction / Longest idle in transaction", "age of the oldest open transaction; the session idle in a transaction the longest — yellow from 5 minutes, red from 1 hour (it holds locks and the xmin horizon)."},
		},
		Keys: []HelpItem{{"/", "filter rows by text"}},
		Reading: []string{
			"Many 'idle' sessions usually means an application pool larger than needed — consider PgBouncer.",
			"'idle in transaction' should be near zero; set idle_in_transaction_session_timeout to cap it.",
			"superuser_reserved_connections are kept free for administrators, so ordinary users hit the limit earlier.",
		},
		Source: "pg_stat_activity (state, application_name, usename, client_addr, state_change, xact_start), max_connections",
	},
	"autovacuum": {
		Title:   "Autovacuum Monitor (key 6) — dead tuples and vacuum activity",
		Purpose: "Ranks tables by dead tuples and shows, live, whether (auto)vacuum is working on them — the starting point for bloat and vacuum-tuning work.",
		Columns: []HelpItem{
			{"Status", "auto vacuum / manual vacuum = a vacuum is running on the table right now; idle = nothing running."},
			{"Dead Tuples / Dead %", "rows deleted or updated but not yet vacuumed; Dead % yellow > 10, red > 30."},
			{"Trigger", "dead tuples as a share of the autovacuum trigger (autovacuum_vacuum_threshold + scale_factor × reltuples, with the table's overrides): ≥ 100% = a vacuum is due (yellow), ≥ 200% = overdue (red)."},
			{"Size", "total size including indexes and TOAST."},
			{"Throttle", "global = the table uses the global cost settings; otherwise its override as cost_limit/cost_delay, plus xN when that makes its vacuum N times slower than the global settings (yellow; red from x10 or when the global setting is unthrottled)."},
			{"Last Autovacuum / Autovacs", "when autovacuum last finished on it and how many times since stats reset."},
			{"Workers bar", "running autovacuum workers vs autovacuum_max_workers; all busy = tables are waiting their turn."},
		},
		Keys: []HelpItem{
			{"enter", "Autovacuum Detail: parameters, computed thresholds, freeze status, bloat"},
			{"v", "VACUUM ANALYZE the selected table — asks y/n first"},
			{"/", "filter rows by text"},
		},
		Reading: []string{
			"High Dead % with an old Last Autovacuum means autovacuum isn't keeping up: open the detail to see the table's thresholds.",
			"Dead tuples are estimates from the statistics collector; use 'b' in the detail for an exact pgstattuple measurement.",
			"Autovacuum's speed limit is cost_limit / cost_delay: with the PG14+ vacuum_cost_page_miss of 2, 200/20ms allows ~39 MB/s of uncached reads, 600/2ms ~1.1 GB/s. The global autovacuum_vacuum_cost_* settings fall back to vacuum_cost_* when -1.",
			"Tables with their own cost settings are left out of the cost balancing between workers, so a slow override stays slow even when it's the only vacuum running.",
		},
		Source: "pg_stat_user_tables, pg_class (reloptions, reltuples), pg_settings, pg_stat_progress_vacuum, pg_stat_activity",
	},
	"autovacuum_detail": {
		Title:   "Autovacuum Detail — one table's vacuum triggers and freeze status",
		Purpose: "Explains *when* autovacuum will process this table: every autovacuum parameter (table override vs global default) with the threshold PostgreSQL computes from it and how close the table is.",
		Columns: []HelpItem{
			{"Live / Dead / Modified / Reltuples", "row counters; Reltuples is the planner estimate the thresholds scale with."},
			{"Autovacuum due", "YES when autovacuum would pick the table up now (or an anti-wraparound vacuum is forced)."},
			{"XID age bar", "age(relfrozenxid) vs the effective autovacuum_freeze_max_age."},
			{"Table value / Global default", "reloption set on the table (marked *) or (inherited) from postgresql.conf."},
			{"Threshold / Current / Status", "computed trigger vs current value: DUE = autovacuum will run, FORCED = anti-wraparound vacuum, aggressive = next vacuum scans all unfrozen pages."},
		},
		Keys: []HelpItem{
			{"↑ ↓", "move the selection; the line above the footer shows the formula behind the selected parameter"},
			{"b", "exact bloat with pgstattuple (full table scan; needs the extension)"},
			{"v", "VACUUM ANALYZE this table — asks y/n first"},
		},
		Reading: []string{
			"Vacuum threshold = autovacuum_vacuum_threshold + scale_factor × reltuples (capped by autovacuum_vacuum_max_threshold on PG18+). Insert and analyze thresholds follow the same pattern.",
			"A table value always wins over the global one, except autovacuum_freeze_max_age, which a table can only lower.",
			"Big tables with the default scale_factor (0.2) wait for 20% of their rows to die — lower it per table instead of globally.",
		},
		Source: "pg_stat_user_tables, pg_class (reloptions, reltuples, relfrozenxid), pg_settings, pgstattuple()",
	},
	"index_usage": {
		Title:   "Index Usage (key 7) — which indexes are used, and how",
		Purpose: "Ranks indexes by wasted space — size × (1 − their share of the table's index scans) — so a big index that serves few of its table's scans comes first, and flags invalid and possibly redundant indexes. Indexes backing PRIMARY KEY / UNIQUE constraints are hidden until you press c.",
		Columns: []HelpItem{
			{"Status", "INVALID (red) = a failed CREATE INDEX CONCURRENTLY left it behind: maintained on writes, never used for reads — drop and recreate it; redundant (yellow) = see the Redundant column; unused (yellow) = never scanned and larger than 1 MB; ok."},
			{"Scans", "index scans started since stats reset."},
			{"Share", "this index's share of the scans of all indexes on its table (- when none was scanned)."},
			{"Tup/Scan", "index entries returned per scan (idx_tup_read / idx_scan); high values mean a poorly selective index."},
			{"Size", "on-disk size of the index."},
			{"Redundant", "possible redundancy with another index of the same table: dup of (same columns, same order), prefix of (its columns lead a longer btree index, which can serve the same searches), same cols as (same columns in another order)."},
		},
		Keys: []HelpItem{
			{"enter", "Index Detail: columns, type, unique/primary flags"},
			{"c", "show / hide indexes that back PRIMARY KEY, UNIQUE or EXCLUDE constraints"},
			{"/", "filter rows by text"},
			{"↑ ↓", "move the selection; the line above the footer shows the full index name and its redundancies"},
		},
		Reading: []string{
			"0 scans is a removal candidate only after a full business cycle since the last stats reset — and only on this server: replicas keep their own counters.",
			"Never drop PRIMARY KEY / UNIQUE indexes because of 0 scans: they enforce constraints. A unique index is never called redundant because of a non-unique one.",
			"Redundancy is a hint: operator classes, collations, INCLUDE columns, partial predicates and expressions aren't compared (partial and expression indexes are skipped).",
		},
		Source: "pg_stat_user_indexes, pg_index, pg_attribute",
	},
	"index_detail": {
		Title:   "Index Detail — structure of one index",
		Purpose: "Shows the definition of the selected index: access method, flags and the columns in key order.",
		Columns: []HelpItem{
			{"Type", "access method (btree, hash, gin, gist, brin, …)."},
			{"Unique / Primary / Valid", "constraint flags and whether the index is usable."},
			{"# / Column / Type / Nullable", "key columns in order; (expression) for expression indexes."},
		},
		Reading: []string{
			"Column order matters for btree: an index on (a, b) helps filters on a, or a and b — not on b alone.",
		},
		Source: "pg_index, pg_class, pg_am, pg_attribute",
	},
	"cache_hit": {
		Title:   "Cache Hit Ratio (key 8) — reads served from shared_buffers",
		Purpose: "Per-table share of block reads found in PostgreSQL's buffer cache, for the table heap and its indexes.",
		Columns: []HelpItem{
			{"Heap Read / Heap Hit", "blocks read from outside shared_buffers vs found in it."},
			{"Cache Hit % / Idx Hit %", "hit / (hit + read); yellow < 90%, red < 70%; N/A when the table had no reads."},
		},
		Keys: []HelpItem{{"/", "filter rows by text"}},
		Reading: []string{
			"A 'read' may still come from the OS page cache, so a low ratio isn't always disk I/O — but it is extra work.",
			"Low ratios on big, hot tables suggest shared_buffers is small for the working set; on rarely-read tables they're expected.",
			"The instance-wide heap, index and database ratios are on the dashboard and Memory & Checkpoint Stats (key m); the MCP check_cache_hit tool can also measure them over an interval (delta_seconds).",
		},
		Source: "pg_statio_user_tables",
	},
	"users": {
		Title:   "Users (key 9) — login roles and their privileges",
		Purpose: "Audits roles that can log in: superusers, replication rights, connection limits and password expiry.",
		Columns: []HelpItem{
			{"Super", "superuser — bypasses every permission check (red)."},
			{"CreateDB / CreateRole", "can create databases / roles."},
			{"Replication", "can start replication connections (yellow)."},
			{"ConnLimit / Valid Until", "per-role connection cap (-1 = unlimited); password expiry."},
			{"Member Of", "roles whose privileges this user inherits."},
		},
		Keys:    []HelpItem{{"/", "filter rows by text"}},
		Reading: []string{"Applications should never connect as a superuser; keep the list of red rows as short as possible."},
		Source:  "pg_roles (rolcanlogin), pg_auth_members",
	},
	"roles": {
		Title:   "Roles (key 0) — group roles and membership",
		Purpose: "Lists group roles (roles that can't log in, excluding the built-in pg_* roles) with their attributes and members, to understand how privileges are granted through groups. Login roles are in Users (9).",
		Columns: []HelpItem{
			{"Super / Inherit / CreateRole / CreateDB", "role attributes; Inherit = members get the role's privileges automatically."},
			{"Members", "roles that are members of this one."},
		},
		Keys:   []HelpItem{{"/", "filter rows by text"}},
		Source: "pg_roles, pg_auth_members",
	},
	"pg_config": {
		Title:   "Config Parameters (key p) — server settings",
		Purpose: "Browse and search every setting with its current value and where it was set.",
		Columns: []HelpItem{
			{"Setting / Unit", "current value; the unit applies to it (e.g. 8kB × 16384 = 128 MB)."},
			{"Category", "group in the documentation."},
			{"Source", "default, configuration file, command line, database, user, session… — tells you where to change it."},
		},
		Keys:    []HelpItem{{"/", "filter by name, value or description"}},
		Reading: []string{"Settings marked as needing a restart only take effect after one; others need SELECT pg_reload_conf()."},
		Source:  "pg_settings",
	},
	"schema_browser": {
		Title:   "Schema Browser (key s) — tables and their columns",
		Purpose: "Lists user tables with size and estimated rows; describe a table to see its columns, types and defaults.",
		Columns: []HelpItem{
			{"Est. Rows", "planner estimate (pg_class.reltuples), refreshed by VACUUM / ANALYZE — not an exact count."},
			{"Column view", "type, length, nullability and default of each column."},
		},
		Keys: []HelpItem{
			{"enter", "describe the selected table"},
			{"q esc r", "from the column view, back to the table list"},
			{"/", "filter rows by text"},
		},
		Source: "information_schema.tables, information_schema.columns, pg_class (size, reltuples)",
	},
	"extensions": {
		Title:   "Extensions (key e) — installed extensions",
		Purpose: "Shows which extensions are installed in this database, their version and schema.",
		Keys:    []HelpItem{{"/", "filter rows by text"}},
		Reading: []string{
			"Several pgdba features depend on extensions: pg_stat_statements (slow queries, query load) and pgstattuple (precise bloat).",
			"Extensions are per database: switch databases (D) to check others.",
		},
		Source: "pg_extension",
	},
	"databases": {
		Title:   "Switch Database (key D) — reconnect to another database",
		Purpose: "Lists the databases on this server; pick one to reconnect pgdba to it with the same credentials.",
		Keys: []HelpItem{
			{"enter", "connect to the selected database"},
			{"/", "filter rows by text"},
		},
		Reading: []string{"Most statistics (tables, indexes, extensions) are per database; server-wide ones (connections, replication) are not."},
		Source:  "pg_database",
	},
	"query_load": {
		Title:   "Query Load (key L) — statements by total cost",
		Purpose: "Ranks statements by total execution time, i.e. by how much of the server's time they consume overall — the best place to start optimizing.",
		Columns: []HelpItem{
			{"Calls / Total / Avg (ms)", "executions, cumulative time (yellow > 5 s, red > 60 s), average per call."},
			{"Buffer (MB)", "shared buffers hit + read — how much data the statement touches."},
			{"Temp (MB)", "data spilled to temp files (sorts / hashes larger than work_mem)."},
			{"Load", "share of the instance's total execution time."},
		},
		Keys: []HelpItem{
			{"enter", "show the full query text"},
			{"/", "filter rows by text"},
		},
		Reading: []string{
			"A cheap query called very often can top this list: caching or batching it may beat any index.",
			"Requires pg_stat_statements (any version; before 1.8 the column is total_time); counters accumulate since pg_stat_statements_reset().",
			"A yellow ⚠ line means the extension is outdated for this server: run ALTER EXTENSION pg_stat_statements UPDATE.",
		},
		Source: "pg_stat_statements",
	},
	"wait_events": {
		Title:   "Wait Events (key w) — what active sessions are waiting on",
		Purpose: "Samples active sessions 10 times over one second and groups them by wait event, averaged into active sessions (AAS) — a quick answer to 'why is the database slow right now?'. Idle sessions are left out.",
		Columns: []HelpItem{
			{"Type", "CPU = running, not waiting (green); Lock = row/table locks (red); IO, LWLock, BufferPin = storage and internal contention (yellow); Client = waiting for the application inside a transaction."},
			{"Event / AAS / Distribution", "specific wait event; average number of sessions in it per sample (the RDS Performance Insights unit); share of all sampled sessions. The line above the table gives the total, which equals the sum of the AAS column."},
		},
		Reading: []string{
			"Sessions in state 'idle' (waiting for their client's next query, Client:ClientRead) and background processes idling in their main loop (wait type Activity) are excluded — they used to dominate a single snapshot without saying anything.",
			"One second of samples is still short: refresh (r) a few times, or use the MCP check_wait_events tool, which samples for 20 s by default.",
			"Many Lock waits → Blocked Queries (4). Many IO waits → check Cache Hit (8) and Temp Files (t).",
		},
		Source: "pg_stat_activity (wait_event_type, wait_event)",
	},
	"freeze": {
		Title:   "Freeze Monitor (key f) — transaction ID wraparound risk",
		Purpose: "Tracks how close databases and tables are to transaction ID wraparound. PostgreSQL stops accepting writes near 2.1 billion XIDs to protect data, so old relfrozenxid ages must be frozen in time.",
		Columns: []HelpItem{
			{"Top bar", "current database's age(datfrozenxid) as % of the ~2.1B shutdown limit; yellow > 7.1%, red > 8.6%."},
			{"XID Age / MXI Age", "age(relfrozenxid) and mxid_age(relminmxid) of each table."},
			{"% Freeze", "XID age vs autovacuum_freeze_max_age, where autovacuum forces a freeze; yellow > 50%, red > 75%."},
		},
		Keys: []HelpItem{
			{"f", "VACUUM FREEZE the selected table — asks y/n first"},
			{"D", "switch database (each database has its own ages)"},
		},
		Reading: []string{
			"Over 100% means an anti-wraparound autovacuum should be running; if it isn't finishing, check long transactions (2) and replication slots (3) holding the xmin horizon.",
			"VACUUM FREEZE on a large table is I/O heavy — prefer quiet hours.",
		},
		Source: "pg_database (datfrozenxid), pg_class (relfrozenxid, relminmxid), pg_settings",
	},
	"database_sizes": {
		Title:   "Database Sizes (key S) — space per database and tablespace",
		Purpose: "Shows the size of every database and tablespace on the server, for capacity planning and finding what is growing.",
		Source:  "pg_database_size(), pg_tablespace_size()",
	},
	"temp_files": {
		Title:   "Temp File Usage (key t) — sorts and hashes spilled to disk",
		Purpose: "Per-database count and volume of temporary files written since the last stats reset. Temp files mean operations didn't fit in work_mem.",
		Columns: []HelpItem{
			{"Temp Files / Temp Size", "files created and bytes written; non-zero counts are yellow."},
			{"Avg / File", "temp bytes per file: files around 1 GB mean single sorts or hashes far above work_mem."},
			{"Stats Reset", "when the counters started — compare volumes over the same period; 'never reset' = since the cluster was created or its last crash recovery."},
			{"Top temp writers", "the 5 statements that wrote the most temp data (pg_stat_statements temp_blks_written × block_size), with their call count."},
		},
		Reading: []string{
			"The Top temp writers list names the statements to fix; Query Load (L, Temp column) and log_temp_files give more context.",
			"Raise work_mem per role or session for heavy reports instead of globally: it applies per sort/hash node, per connection.",
		},
		Source: "pg_stat_database (temp_files, temp_bytes, stats_reset), pg_stat_statements (temp_blks_written)",
	},
	"xmin_horizon": {
		Title:   "Xmin Horizon (key X) — what keeps vacuum from cleaning up",
		Purpose: "Lists everything holding back the xmin horizon, oldest first: sessions with an open snapshot or transaction, standbys with hot_standby_feedback, replication slots and prepared transactions. Vacuum can't remove rows deleted after the horizon, and freezing can't advance past it — the usual cause of bloat that vacuum doesn't fix and of freeze ages that keep growing.",
		Columns: []HelpItem{
			{"Source", "session (a backend), replica (a standby's hot_standby_feedback, via its walsender), slot (replication slot xmin / catalog_xmin), prepared (two-phase transaction waiting for COMMIT/ROLLBACK PREPARED)."},
			{"ID", "pid, standby application name, slot name, or prepared transaction gid."},
			{"Xmin Age", "transactions since the holder's xmin (or its own XID); the oldest row defines the horizon, shown in the summary line."},
			{"Xact Age", "time since the transaction started (sessions and prepared transactions)."},
			{"State", "session state; slots show active/inactive, (catalog only) when the slot only holds catalog_xmin — that blocks cleanup of system catalogs, not user tables."},
		},
		Keys: []HelpItem{
			{"enter", "show the session's full query"},
			{"/", "filter rows by text"},
		},
		Reading: []string{
			"Status is relative to autovacuum_freeze_max_age: warning from 5% of it (10M XIDs at the 200M default) or a transaction open for more than an hour, critical from 25% (50M).",
			"Fix by source: end or terminate the session (Long Running Queries, key 2); for a replica, end its long queries or turn hot_standby_feedback off; drop or advance an unused slot (key 3); COMMIT PREPARED / ROLLBACK PREPARED a forgotten prepared transaction.",
			"Walsenders are listed once, as replica, not again as sessions. The horizon is per cluster: holders in other databases affect this one too.",
		},
		Source: "pg_stat_activity (backend_xmin, backend_xid), pg_stat_replication (backend_xmin), pg_replication_slots (xmin, catalog_xmin), pg_prepared_xacts",
	},
	"vacuum_progress": {
		Title:   "Vacuum Progress (key V) — VACUUMs running right now",
		Purpose: "Every manual VACUUM and autovacuum running in the cluster, with its phase and progress, to answer 'is vacuum working on that table, and how far is it?'.",
		Columns: []HelpItem{
			{"Kind", "manual, auto, or wraparound — an autovacuum started to prevent XID wraparound: it can't be skipped, doesn't yield to lock requests, and means freezing fell behind (red)."},
			{"Phase", "scanning heap → vacuuming indexes → vacuuming heap → cleaning up indexes → truncating heap → performing final cleanup."},
			{"Scanned / Vacuumed", "share of the table's heap blocks scanned / vacuumed so far."},
			{"Idx Passes", "index vacuum passes so far (PG17+: indexes processed / total in this pass). More than 1 means the dead-tuple memory filled up and every index had to be scanned again — raise maintenance_work_mem / autovacuum_work_mem."},
			{"Dead Memory", "dead tuples collected / capacity (PG ≤ 16, in tuples) or dead-tuple memory used / available (PG17+, in bytes)."},
			{"Duration", "time since the vacuum's transaction started."},
		},
		Reading: []string{
			"Status is warning for anti-wraparound vacuums and for more than one index pass.",
			"Vacuums in other databases are listed too; their table shows as (oid N) because it can only be resolved from inside that database.",
			"A vacuum that stays in the same phase for long is usually throttled by its cost settings — see the Throttle column of the Autovacuum screen (key 6).",
		},
		Source: "pg_stat_progress_vacuum, pg_stat_activity",
	},
	"table_churn": {
		Title:   "Table Churn (key H) — writes and HOT updates per table",
		Purpose: "Ranks tables by updates since the last stats reset and shows how many were HOT (heap-only tuple) updates. A HOT update writes no index entries and its old version is pruned without vacuum; a non-HOT update writes every index of the table and leaves dead index entries behind.",
		Columns: []HelpItem{
			{"Inserts / Updates / Deletes", "rows written since the stats reset (pg_stat_user_tables n_tup_*)."},
			{"HOT %", "share of updates that were HOT."},
			{"New-page Upd", "PG16+: updates whose new row version had to go to another page — the same page was full, so the update couldn't be HOT."},
			{"Fillfactor", "the table's fillfactor; (def) = not set, i.e. 100: pages are packed full, leaving no room for HOT updates."},
			{"Dead / Size", "dead tuples waiting for vacuum; total size including indexes and TOAST."},
		},
		Keys: []HelpItem{{"/", "filter rows by text"}},
		Reading: []string{
			"Status is warning for tables with ≥ 10,000 updates of which less than 50% were HOT. The line above the footer suggests the fix for the selected table.",
			"With fillfactor 100, lowering it to 80–90 (ALTER TABLE … SET (fillfactor = 85), effective for new pages; VACUUM FULL / pg_repack to rewrite) leaves room on each page for HOT updates.",
			"If fillfactor already leaves room and HOT stays low, the updates change indexed columns — those can never be HOT; consider whether every index on them is needed.",
			"Bloat estimates (pgstattuple_approx) are available through the MCP check_table_churn tool with include_bloat.",
		},
		Source: "pg_stat_user_tables (n_tup_ins/upd/del/hot_upd, n_tup_newpage_upd PG16+), pg_class (reloptions)",
	},
	"io_wal": {
		Title:   "I/O & WAL (key O) — who reads and writes, and how much WAL",
		Purpose: "WAL volume and its full-page-image share (pg_stat_wal, PG14+), and buffer I/O split by backend type, object and context (pg_stat_io, PG16+), to tell vacuum, checkpointer, background writer and query I/O apart.",
		Columns: []HelpItem{
			{"WAL / records / full-page images", "WAL generated since the reset; % of records that are full-page images (written on the first change to each page after a checkpoint)."},
			{"buffers full", "times a backend had to write WAL itself because wal_buffers was full."},
			{"Relation writes by", "share of data-block writes done by each backend type: checkpointer and background writer are expected; client backends writing a lot means they evict dirty buffers themselves."},
			{"Backend Type / Object / Context", "who did the I/O, on what (relation, temp relation, wal on PG18+), and in which context: normal, vacuum (vacuum's ring buffer), bulkread, bulkwrite."},
			{"Reads / Read, Writes / Written, Extends, Hits, Evictions, Fsyncs", "operations and bytes since the pg_stat_io reset."},
		},
		Reading: []string{
			"Full-page images is yellow from 30% of records: checkpoints are frequent for this write pattern — raise max_wal_size / checkpoint_timeout, or enable wal_compression.",
			"WAL buffers full is yellow above 0.1% of records: wal_buffers is too small for the write bursts.",
			"Relation writes turn yellow when client backends do ≥ 25% of the writes in the normal context — tune the background writer (bgwriter_lru_maxpages / bgwriter_delay) or shared_buffers.",
			"On PG13 neither view exists; PG14–15 show only the WAL part.",
		},
		Source: "pg_stat_wal (PG14+), pg_stat_io (PG16+), pg_settings",
	},
	"memory_stats": {
		Title:   "Memory & Checkpoint Stats (key m) — buffers and write activity",
		Purpose: "Memory-related settings, the buffer cache hit ratios and checkpoint / background writer counters — SQL-only, so it works against remote servers.",
		Columns: []HelpItem{
			{"Memory settings", "shared_buffers, effective_cache_size, work_mem, maintenance_work_mem, wal_buffers, huge_pages."},
			{"Cache hit — heap / index / database", "heap = table blocks of user tables, index = their index blocks (pg_statio_user_tables), database = every block this database read, catalogs and TOAST included (pg_stat_database). Yellow < 99%, red < 90%. The dashboard's heap cache hit is the same number."},
			{"Checkpoints timed vs requested", "requested checkpoints are forced by WAL volume; many of them mean max_wal_size is too small."},
			{"Buffers backend", "PG ≤ 16: pages written by backends themselves — high values mean the background writer can't keep up (not available on PG17+)."},
		},
		Reading: []string{
			"PG17+ reads checkpoint counters from pg_stat_checkpointer instead of pg_stat_bgwriter.",
			"Settings, cache hit and checkpoints are read independently; a part that fails is shown as a yellow ⚠ line and the rest still loads.",
		},
		Source: "pg_settings, pg_stat_database, pg_stat_bgwriter, pg_stat_checkpointer (PG17+)",
	},
	"pub_sub": {
		Title:   "Pub/Sub (key R) — logical replication publications and subscriptions",
		Purpose: "Shows this server's publications (what it sends) and subscriptions (what it receives), with subscription health.",
		Columns: []HelpItem{
			{"Publications: Tables / All", "number of tables; FOR ALL TABLES."},
			{"Insert / Update / Delete / Truncate", "which operations are published."},
			{"Via Root", "publish_via_partition_root: partition changes are published as the parent table."},
			{"Subscriptions: Status", "active = apply worker running (green); down = enabled but no worker (red); disabled."},
			{"Received LSN / Last Receive", "how far the subscriber has received and when it last heard from the publisher."},
			{"Errors", "apply + table sync errors (PG15+); red when > 0."},
		},
		Keys: []HelpItem{
			{"tab", "switch between the Publications and Subscriptions sections"},
			{"enter", "tables of the selected publication / subscription"},
		},
		Reading: []string{
			"A red alert next to Publications means replicated tables have no usable replica identity — UPDATE/DELETE on them fail. Press I.",
		},
		Source: "pg_publication, pg_publication_tables, pg_subscription, pg_stat_subscription, pg_stat_subscription_stats (PG15+)",
	},
	"pub_table_detail": {
		Title:   "Publication Tables — what one publication sends",
		Purpose: "Tables included in the publication, with column lists, row filters and their size in rows.",
		Columns: []HelpItem{
			{"Columns / Row Filter", "PG15+ column lists and WHERE filters; empty = all columns / all rows."},
			{"Live / Dead Rows", "row counters of the published table."},
		},
		Source: "pg_publication_tables, pg_stat_user_tables",
	},
	"sub_table_detail": {
		Title:   "Subscription Tables — sync state of each subscribed table",
		Purpose: "Shows where each table of the subscription is in its initial synchronization and the DML applied to it.",
		Columns: []HelpItem{
			{"Sync State", "initialize → copying data → finished → synchronized → ready (green). Tables stuck before ready are not replicating changes yet."},
			{"INS / UPD / DEL", "rows inserted / updated / deleted on this subscriber's copy."},
		},
		Source: "pg_subscription_rel, pg_stat_user_tables",
	},
	"toast_tables": {
		Title:   "TOAST Tables (key T) — out-of-line storage of large values",
		Purpose: "Tables whose large values (text, jsonb, bytea…) live in a TOAST table, with its size, bloat and cache behavior — TOAST is vacuumed separately and often forgotten.",
		Columns: []HelpItem{
			{"Toast Size / Toast %", "size of the TOAST table and its share of the table's total; yellow > 20%, red > 50%."},
			{"Dead Tuples", "dead rows in the TOAST table; yellow > 10k, red > 100k."},
			{"Cache Hit %", "TOAST block reads served from shared_buffers; yellow < 95%, red < 80%."},
			{"Toast Columns", "columns whose storage (EXTENDED / EXTERNAL / MAIN) can produce TOAST data."},
		},
		Keys: []HelpItem{
			{"enter", "Autovacuum Detail of the parent table"},
			{"v", "VACUUM the TOAST table — asks y/n first"},
			{"/", "filter rows by text"},
		},
		Reading: []string{"Frequent updates of a large column rewrite its TOAST chunks every time: consider splitting that column into its own table."},
		Source:  "pg_class (reltoastrelid), pg_stat_all_tables, pg_statio_user_tables, pg_attribute",
	},
	"replication_config": {
		Title:   "Replication Config — settings that affect replication",
		Purpose: "Replication-related parameters with a hint about each value's implication (red = risk, yellow = needs attention, green = OK).",
		Columns: []HelpItem{
			{"Context", "postmaster = needs a restart; sighup = reload is enough."},
			{"Hint", "what the current value implies for replication."},
		},
		Keys:   []HelpItem{{"/", "filter rows by text"}},
		Source: "pg_settings",
	},
	"replication_standbys": {
		Title:   "Streaming Standbys — physical replicas connected to this server",
		Purpose: "Each connected standby (walsender) with its lag, to check replicas are keeping up.",
		Columns: []HelpItem{
			{"State / Sync", "streaming, catchup…; sync / quorum = commits wait for it, async = they don't."},
			{"Write / Flush / Replay Lag", "time until the standby wrote, flushed and applied recent WAL."},
			{"Lag Bytes", "WAL not yet replayed by the standby."},
		},
		Keys: []HelpItem{{"k", "terminate the selected walsender — the standby reconnects; asks y/n first"}},
		Reading: []string{
			"With a synchronous standby, its lag is added to every commit on the primary.",
			"Replay lag that keeps growing usually means long queries on the standby conflict with replay (hot_standby_feedback / max_standby_streaming_delay).",
		},
		Source: "pg_stat_replication",
	},
	"replica_identity": {
		Title:   "Replica Identity (key I) — tables that break logical replication",
		Purpose: "Finds tables whose UPDATE/DELETE can't be replicated because they have no usable replica identity — with native publications they fail on the publisher; with pglogical they can't be replicated.",
		Columns: []HelpItem{
			{"Identity", "pg_class.relreplident: default (uses the PK), nothing, index, full."},
			{"Problem", "no PK, NOTHING, index dropped (USING INDEX whose index is gone), FULL (pglogical)."},
			{"Replicated by", "publications and pglogical:<set> that include the table — directly, FOR ALL TABLES, FOR TABLES IN SCHEMA or through a partitioned parent."},
			{"Status", "critical = UPDATE/DELETE are already replicated for it (broken today); warning = not replicated yet, or INSERT-only."},
		},
		Keys: []HelpItem{
			{"p", "show only replicated tables (toggle)"},
			{"/", "filter rows by text, e.g. /no PK"},
			{"↑ ↓", "move the selection; the line above the footer shows the suggested fix for the selected table"},
		},
		Reading: []string{
			"Native publications accept a PK, REPLICA IDENTITY USING INDEX or FULL. pglogical accepts only a PK or USING INDEX — FULL is not supported.",
			"Fix order: REPLICA IDENTITY DEFAULT if a PK exists, USING INDEX on a unique NOT NULL index, FULL otherwise (more WAL, and the subscriber may scan the table per change).",
			"The screen is read-only: copy the suggested ALTER TABLE and run it yourself.",
		},
		Source: "pg_class, pg_index, pg_publication_tables, pg_partition_ancestors(), pglogical.replication_set(_table) when installed",
	},
	"version": {
		Title:   "Server Version",
		Purpose: "Full PostgreSQL version string of the connected server.",
		Source:  "version()",
	},
}

// HelpModel is the "?" page: a scrollable man-page view of one screen's ScreenHelp.
// It keeps the screen it was opened from and returns to it unchanged.
type HelpModel struct {
	topic    string
	returnTo tea.Model
	viewport viewport.Model
	width    int
	height   int
}

func NewHelpModel(topic string, returnTo tea.Model) tea.Model {
	return HelpModel{topic: topic, returnTo: returnTo, viewport: viewport.New(0, 0)}
}

// ConsumesKey claims every key so global shortcuts don't navigate away while reading.
func (m HelpModel) ConsumesKey(string) bool { return true }

func (m HelpModel) Init() tea.Cmd { return nil }

func (m HelpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		// header (title + connection line) + blank line, then blank line + footer line
		m.viewport.Height = max(msg.Height-lipgloss.Height(m.header())-3, 3)
		m.viewport.SetContent(RenderScreenHelp(m.topic, msg.Width))
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "?":
			return m.returnTo, nil
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m HelpModel) header() string {
	return RenderHeader("Help — " + screenHelpOrFallback(m.topic).Title)
}

func (m HelpModel) View() string {
	scroll := ""
	if !m.viewport.AtTop() || !m.viewport.AtBottom() {
		scroll = fmt.Sprintf(" • %d%%", int(m.viewport.ScrollPercent()*100))
	}
	return m.header() + "\n" + m.viewport.View() + "\n\n" +
		FooterStyle.Render("↑↓ pgup pgdn scroll • q back"+scroll)
}

func screenHelpOrFallback(topic string) ScreenHelp {
	if help, ok := screenHelp[topic]; ok {
		return help
	}
	return ScreenHelp{Title: topic, Purpose: "No help written for this screen yet."}
}

// RenderScreenHelp formats a topic as a man page wrapped to width.
func RenderScreenHelp(topic string, width int) string {
	help := screenHelpOrFallback(topic)
	const indent = 4
	textWidth := max(width-indent-1, 20)
	renderHeading := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render
	renderTerm := lipgloss.NewStyle().Bold(true).Foreground(ColorWhite).Render
	paragraph := lipgloss.NewStyle().Width(textWidth).MarginLeft(indent)

	var b strings.Builder
	section := func(name string) { b.WriteString("\n" + renderHeading(name) + "\n") }
	items := func(entries []HelpItem) {
		termWidth := 0
		for _, entry := range entries {
			termWidth = max(termWidth, lipgloss.Width(entry.Term))
		}
		termWidth = min(termWidth, textWidth/3)
		for _, entry := range entries {
			term := lipgloss.NewStyle().Width(termWidth).Render(renderTerm(entry.Term))
			explanation := lipgloss.NewStyle().Width(max(textWidth-termWidth-2, 10)).Render(entry.Explanation)
			row := lipgloss.JoinHorizontal(lipgloss.Top, term, "  ", explanation)
			b.WriteString(lipgloss.NewStyle().MarginLeft(indent).Render(row) + "\n")
		}
	}

	section("PURPOSE")
	b.WriteString(paragraph.Render(help.Purpose) + "\n")
	if len(help.Columns) > 0 {
		section("WHAT YOU SEE")
		items(help.Columns)
	}
	section("KEYS")
	// A screen may refine a common key (e.g. what ↑ ↓ also shows); list it only once.
	keys := append([]HelpItem{}, help.Keys...)
	screenTerms := map[string]bool{}
	for _, key := range help.Keys {
		screenTerms[key.Term] = true
	}
	for _, key := range commonKeys {
		if !screenTerms[key.Term] {
			keys = append(keys, key)
		}
	}
	items(keys)
	if len(help.Reading) > 0 {
		section("HOW TO READ IT")
		for _, line := range help.Reading {
			bullet := lipgloss.JoinHorizontal(lipgloss.Top, "• ", lipgloss.NewStyle().Width(textWidth-2).Render(line))
			b.WriteString(lipgloss.NewStyle().MarginLeft(indent).Render(bullet) + "\n")
		}
	}
	if help.Source != "" {
		section("DATA SOURCE")
		b.WriteString(paragraph.Render(help.Source) + "\n")
	}
	return strings.TrimPrefix(b.String(), "\n")
}

// HelpTopic implementations — one per screen model. TestEveryScreenHasHelp fails if
// a model with a View method is missing here or its topic has no text.
func (m DashboardModel) HelpTopic() string           { return "dashboard" }
func (m SlowQueriesModel) HelpTopic() string         { return "slow_queries" }
func (m LongRunningQueriesModel) HelpTopic() string  { return "long_running" }
func (m ReplicationSlotsModel) HelpTopic() string    { return "replication_slots" }
func (m RecordLocksModel) HelpTopic() string         { return "record_locks" }
func (m ConnectionsModel) HelpTopic() string         { return "connections" }
func (m AutovacuumModel) HelpTopic() string          { return "autovacuum" }
func (m AutovacuumDetailModel) HelpTopic() string    { return "autovacuum_detail" }
func (m IndexUsageModel) HelpTopic() string          { return "index_usage" }
func (m IndexDetailModel) HelpTopic() string         { return "index_detail" }
func (m CacheHitModel) HelpTopic() string            { return "cache_hit" }
func (m UsersModel) HelpTopic() string               { return "users" }
func (m RolesModel) HelpTopic() string               { return "roles" }
func (m PgConfigModel) HelpTopic() string            { return "pg_config" }
func (m SchemaBrowserModel) HelpTopic() string       { return "schema_browser" }
func (m ExtensionsModel) HelpTopic() string          { return "extensions" }
func (m DatabaseSelectorModel) HelpTopic() string    { return "databases" }
func (m QueryLoadModel) HelpTopic() string           { return "query_load" }
func (m WaitEventsModel) HelpTopic() string          { return "wait_events" }
func (m FreezeModel) HelpTopic() string              { return "freeze" }
func (m DatabaseSizeModel) HelpTopic() string        { return "database_sizes" }
func (m TempFilesModel) HelpTopic() string           { return "temp_files" }
func (m MemoryStatsModel) HelpTopic() string         { return "memory_stats" }
func (m PubSubModel) HelpTopic() string              { return "pub_sub" }
func (m PubTableDetailModel) HelpTopic() string      { return "pub_table_detail" }
func (m SubTableDetailModel) HelpTopic() string      { return "sub_table_detail" }
func (m ToastTablesModel) HelpTopic() string         { return "toast_tables" }
func (m ReplicationConfigModel) HelpTopic() string   { return "replication_config" }
func (m ReplicationStandbysModel) HelpTopic() string { return "replication_standbys" }
func (m ReplicaIdentityModel) HelpTopic() string     { return "replica_identity" }
func (m XminHorizonModel) HelpTopic() string         { return "xmin_horizon" }
func (m VacuumProgressModel) HelpTopic() string      { return "vacuum_progress" }
func (m TableChurnModel) HelpTopic() string          { return "table_churn" }
func (m IOWALModel) HelpTopic() string               { return "io_wal" }
func (m VersionModel) HelpTopic() string             { return "version" }
