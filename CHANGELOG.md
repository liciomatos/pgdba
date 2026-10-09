# Changelog

All notable changes to pgdba-cli are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- **Query replicas from the MCP server**: `--replica name=URL` / `--replica-vault
  name=entry` (repeatable) add servers, and every tool accepts `target` (`primary` by
  default); `meta.target`/`meta.role` tell which server answered. `check_index_usage`
  gains `compare: true`, listing each index's scans on the primary and on every replica
  side by side with `unused_everywhere`. Replicas must run the primary's major version.
- **Table Churn screen (`H`) and `check_table_churn` MCP tool**: inserts, updates, deletes
  and HOT updates per table with `hot_pct`, `n_tup_newpage_upd` (PG16+) and fillfactor;
  update-heavy tables (≥ 10k updates) with < 50% HOT get a warning and a fix hint. The MCP
  tool can add a `pgstattuple_approx` bloat estimate (`include_bloat`), skipping tables
  above `bloat_max_table_bytes`.
- **I/O & WAL screen (`O`) and `check_io` / `check_wal` MCP tools**: `pg_stat_io` (PG16+) by
  backend type, object and context with bytes and who writes relation blocks, and
  `pg_stat_wal` (PG14+) with full-page-image %, bytes per record/second and the settings
  that drive them. Older servers get `{"status": "unsupported"}` instead of an error.
- **Xmin Horizon screen (`X`) and `check_xmin_horizon` MCP tool**: everything holding back
  the xmin horizon — sessions with an open snapshot or XID, standbys via
  `hot_standby_feedback`, replication slots (`xmin` / `catalog_xmin`) and prepared
  transactions — oldest first, marking the holder that defines the horizon. Catalog-only
  slots are flagged and don't count for user tables. Status is relative to
  `autovacuum_freeze_max_age` (warning ≥ 5% or a transaction open > 1 h, critical ≥ 25%).
- **Vacuum Progress screen (`V`) and `check_vacuum_progress` MCP tool**: every running
  VACUUM/autovacuum in the cluster with phase, % of heap scanned/vacuumed, index passes,
  dead-tuple memory (tuples up to PG16, bytes on PG17+), duration, and anti-wraparound
  vacuums flagged.
- **`check_server_info` MCP tool** and a richer `meta` block in every MCP response: role,
  postmaster start time, uptime, `stats_reset` of the database / bgwriter / checkpointer
  (PG17+) counters with seconds since each reset (a `note` explains a never-reset `null`),
  and the pg_stat_statements version. The dashboard shows "stats since …" on its
  connection line.
- **Autovacuum screen: Trigger and Throttle columns** — dead tuples as a share of the
  table's autovacuum trigger, and per-table cost overrides with how many times slower than
  the global settings they make its vacuum. `check_autovacuum` returns the overrides, the
  effective `cost_delay`/`cost_limit` (with the `-1` fallback to `vacuum_cost_*`
  resolved), throughput in cost units/s and max uncached read MB/s, `slowdown_factor`,
  `trigger_ratio`, `mod_since_analyze`, size in bytes and a `status`.

### Changed
- **Index Usage ranks by wasted space** (size × (1 − share of the table's index scans),
  invalid indexes first) and flags possible redundancies — duplicates, btree prefixes and
  the same columns in another order — checked against every index of the table, so a
  plain index duplicating the primary key is caught. Indexes backing PK/UNIQUE/EXCLUDE
  constraints are hidden by default (`c` in the TUI, `include_constraints` in the MCP).
  New share-of-scans and tuples-per-scan columns; `check_index_usage` returns sizes in
  bytes, `redundant_with` and a `status`.
- **One cache hit formula everywhere**: the dashboard, Memory & Checkpoint Stats and
  `check_cache_hit` read the same source and report heap, index and database ratios, each
  labeled with its formula (the dashboard's heap-only ratio and Memory Stats' all-blocks,
  all-databases ratio used to disagree). `check_cache_hit` and `check_memory_stats` accept
  `delta_seconds` to measure over an interval. JSON fields renamed: `cache_hit_ratio` →
  `heap_hit_pct` (dashboard) / `cache_hit` object (memory stats).
- **MCP responses use a single envelope** — every tool now returns
  `{"meta": {host, database, server_version}, "warnings": [...], "result": ...}` instead of
  a bare array/object. Clients that parsed the old shape must read `result`.
- **`check_pg_config` output is small by default**: without arguments it returns only the
  key tuning parameters (memory, autovacuum, checkpoint, WAL, planner, timeouts, logging)
  without descriptions. New `scope` (`key`/`modified`/`all`), `only_modified`, `limit`,
  `offset` and `include_description` parameters; responses carry `total`/`returned`.
- **Long Running Queries lists client backends only** by default; background processes
  (autovacuum, walsenders) are opt-in via the MCP `include_background` parameter. Responses
  add client address, backend type and wait event.
- **Dashboard and Memory & Checkpoint Stats tolerate partial failures**: each part is read
  independently, and a failing one (missing extension, permission denied) is listed as a
  warning — a yellow line in the TUI, `warnings` in the MCP — instead of failing the whole
  screen. `slow_query_count` is `null` with `slow_query_unavailable_reason` instead of `-1`.

### Fixed
- **Slow Queries, Query Load and the dashboard failed on pg_stat_statements < 1.8**
  (`column "total_exec_time" does not exist`), common after a major upgrade without
  `ALTER EXTENSION ... UPDATE`. The extension version is detected and the pre-1.8
  `total_time`/`mean_time` columns are used; a warning suggests the update when the
  extension is older than the server's. The view is also schema-qualified, so an extension
  installed outside the `search_path` works.
- **`check_long_running_queries` / Blocked Queries crashed on NULL user names**
  (`converting NULL to string is unsupported`) when a background process such as an
  autovacuum worker was involved.

## [0.7.0] - 2026-10-03

### Added
- **Replica Identity screen (`I`)**: lists tables without a usable replica identity for
  logical replication — no PK with `REPLICA IDENTITY DEFAULT`, `NOTHING`, a dropped identity
  index, or `FULL` on a table in a pglogical replication set (pglogical needs a PK or
  `USING INDEX`; FULL isn't supported). Covers native publications (directly, `FOR ALL
  TABLES`, `FOR TABLES IN SCHEMA`, or via a partitioned parent) and, when installed, the
  local node's pglogical sets. Tables already replicating UPDATE/DELETE are flagged
  critical; `p` toggles replicated tables only. The selected row shows the suggested fix
  (`REPLICA IDENTITY DEFAULT` / `USING INDEX` / `FULL`, or adding a PK for pglogical);
  read-only. The Pub/Sub screen (`R`) alerts with the critical count, and the new
  `check_replica_identity` MCP tool returns the same data.
- **Dashboard: WAL & Replication section** — WAL retained by the most demanding slot vs
  `max_slot_wal_keep_size` (warns when it's unlimited), slots `unreserved` / `lost` and, on
  PG18+, the longest idle slot vs `idle_replication_slot_timeout`; archiver status
  (red while archiving fails); worst lag of physical standbys and of logical subscribers,
  and this server's subscription errors (PG15+) and conflicts (PG18+). Works on standbys.
- **Autovacuum Detail now computes the autovacuum thresholds**: the parameters table gains
  `Threshold` / `Current` / `Status` columns — dead-tuple, insert and analyze triggers
  (`base + scale_factor × reltuples`) and XID/MultiXact anti-wraparound, aggressive-scan and
  freeze-min-age limits — using the table reloption when set and the global setting
  otherwise, with PostgreSQL's own caps applied (freeze_max_age reloption can only lower the
  global; freeze_table_age ≤ 95% and freeze_min_age ≤ 50% of freeze_max_age).
  Version-aware: `autovacuum_vacuum_max_threshold` and the `relallfrozen`-scaled insert
  threshold on PG18+. The stats line now shows `reltuples` and whether autovacuum is due.
  The formula behind the selected parameter is shown as a footer tip while navigating.
  The `check_autovacuum_detail` MCP tool returns the same computation (plus the PG14+
  failsafe limits) as `thresholds`.
- **`?` help on every screen**: a scrollable, man-page style page per screen — purpose,
  what each column/metric means, screen keys plus the common ones, how to read the values
  (including the exact thresholds behind each color) and the catalog views it reads.
  `q`/`esc`/`?` return to the same screen with its state intact; global shortcuts are
  disabled while reading. Every footer and the dashboard show `? help`, and a test fails if
  a screen is added without a help page.
- **Encrypted connection vault**: `pgdba-cli vault add|list|remove|path` stores named
  connection URIs in a local file encrypted with a master password (Argon2id +
  AES-256-GCM, `0600` permissions); connect with `pgdba-cli --vault <name>`. The master
  password can be supplied via `PGDBA_VAULT_PASSWORD` for non-interactive use (e.g.
  `--mcp`), and the file location overridden with `PGDBA_VAULT_FILE`.
- **`-W` / `--password-prompt`**: prompts for the database password without echo, like
  `psql -W`. The typed password overrides any password from `--password`, `PGPASSWORD`,
  `--url`, the vault, or `~/.pgpass`.
- **Dev environment**: new `pg-pglogical` service (port 5435, `make run-pglogical`) and
  Replica Identity scenarios baked into the pg-main and pg-pglogical images, so
  `make dev-up` comes with every case the `I` screen distinguishes.

### Changed
- **Dashboard layout**: uptime moved to the connection line and the replication slot count
  into the new WAL & Replication section. The dashboard now measures its own height and,
  on short terminals, drops the blank lines between sections so everything fits in 24 rows.

### Fixed
- Passwords (and other connection values) containing spaces, quotes or backslashes now
  work with individual connection flags — values are quoted in the libpq key=value string.
- **List screens no longer stop at the top 20/50 rows**: Index Usage, Autovacuum, Slow
  Queries, Long Running Queries, Query Load, Cache Hit, Freeze Monitor and TOAST Tables now
  list every row and scroll as a single list. MCP tools keep their `limit` parameter.
- **Index Usage no longer truncates table/index names when the terminal has room**: the
  Schema/Table/Index/Columns columns size to their longest value and share the terminal
  width (the widest gives way first on narrow terminals); counters and Size take exactly
  their content width (Size used to cut `8192 bytes` to `8192 by…`). The selected index's
  full `schema.index on schema.table (columns)` is shown above the footer.
- Footers of Slow Queries, Index Usage, Long Running Queries, Record Locks and Query Load
  listed `/ filter` twice.
- Memory & Checkpoint Stats, Freeze Monitor and Temp Files rendered 1–2 lines taller than
  the terminal, which pushed the header off the top of the screen.
- Autovacuum Detail parameters `autovacuum_freeze_min_age` / `autovacuum_freeze_table_age`
  showed an empty global default (their GUCs are `vacuum_freeze_min_age` /
  `vacuum_freeze_table_age`); the list now also includes `autovacuum_enabled`, the insert
  thresholds, the MultiXact freeze ages and (PG18+) `autovacuum_vacuum_max_threshold`.
- **Development**: `make dev-up` works against a remote container engine (e.g. Podman
  Desktop's machine from WSL) — init scripts are baked into images instead of bind-mounted.
- **Development**: integration tests no longer leak a PostgreSQL container per run when
  testcontainers' Ryuk reaper is disabled (Podman) — `TestMain` now terminates it.

## [0.6.0] - 2026-09-18

### Added
- **Autovacuum Monitor (`6`) now shows live activity**: a new Status column
  cross-references `pg_stat_progress_vacuum` (joined with `pg_stat_activity`) against
  the dead-tuple ranking, flagging a table as "auto vacuum", "manual vacuum", or "idle"
  depending on whether something is vacuuming it right now. A summary bar above the
  table shows autovacuum worker saturation (`RunningWorkers / autovacuum_max_workers`),
  colored red when all workers are busy. An earlier draft shipped this as a separate
  "Autovacuum Activity" screen (`A`); it was folded into `6` instead since the two
  screens overlapped almost entirely (same base query, same columns) — one screen now
  covers both "which tables need vacuuming" and "what's vacuuming right now".

### Fixed
- `make test-pg-matrix` was silently reusing the first PostgreSQL version's cached test
  result for every other version in the loop, since `go test`'s result cache doesn't
  invalidate on `PGDBA_TEST_PG_VERSION` changes read via `os.Getenv`. Added `-count=1` so
  each version genuinely runs against its own container.

## [0.5.0] - 2026-07-15

### Added
- **TOAST Tables screen** (`T`): lists user tables that have actual TOAST data on disk,
  sorted by TOAST heap size. Columns: Toast Size, Toast % of total, Dead Tuples (in the
  TOAST heap), Cache Hit %, Last Autovacuum, and Toast Columns (comma-separated column
  names whose storage strategy is EXTENDED/EXTERNAL/MAIN — i.e. the columns that can
  produce TOAST data). Color rules: Toast % >50% red / >20% yellow; Dead Tuples >100K
  red / >10K yellow; Cache Hit % <80% red / <95% yellow.
- **VACUUM on TOAST heap** (`v`): press `v` on any row to run
  `VACUUM pg_toast.<toast_relname>` with a `y/n` confirmation dialog. Pressing `enter`
  opens the Autovacuum Detail screen for the parent table.
- `scenario-toast` (`make scenario-toast`): creates `toast_demo` table with 120 rows of
  ~9.6 KB non-compressible payloads (`STORAGE EXTERNAL` + `md5` content) and two UPDATE
  passes to generate dead tuples in the TOAST heap. Added to `scenario-all` / cleaned
  up by `make scenarios-clean`.
- MCP tool `check_toast_tables` exposes the same data as the TUI screen (read-only).

## [0.4.0] - 2026-07-08

### Added
- **Dashboard sections**: reorganized into three labeled panels — Activity (active queries,
  blocked, wait events, long-running >60 s, slow queries, commit rate), Storage (dead tuples,
  temp files, invalid indexes, DB size), and Server (replication slots, uptime) — with a
  two-column layout and htop-style section dividers.
- **Six new dashboard metrics**: long-running query count, wait-event count, temp-files bytes
  for the current database, DB size (`pg_size_pretty`), commit ratio, and server uptime
  (formatted as `Xd Xh Xm`). All fetched as lightweight aggregates on existing round-trips.
- **Unified dev environment** (`make dev-up`): single command starts pg-main (primary +
  publications), pg-replica (physical streaming standby), and pg-sub (logical subscriber) in
  one docker-compose stack. Replaces the previous separate replication/pubsub targets.
- `make run-sub` target to connect pgdba-cli to the logical subscriber (port 5434).
- **Pub/Sub layout improvements**: adaptive height allocation gives the populated section
  most of the terminal; both Publications and Subscriptions sections now stretch the Name
  column (col 0) for visual consistency.
- Unit tests for `PubSubModel` keyboard navigation: Tab section switching, Tab no-op when
  target section is empty, Enter drill-down, Q to menu.

### Fixed
- `t` key in Blocked Queries screen was intercepted by the navigator and routed to Temp
  Files instead of triggering backend termination — added `ConsumesKey("t")` to
  `RecordLocksModel`.
- `pg_stat_replication` LSN columns (`sent_lsn`, `write_lsn`, `flush_lsn`, `replay_lsn`)
  can be NULL while a standby is in the `startup` state, causing a scan error — wrapped in
  `COALESCE(..., '')`.
- `pg_publication.pubgencols` changed type from `bool` to `char` (`'n'`/`'a'`) in PG18,
  causing a driver scan error — added a PG18 gate that casts the column to bool via
  `(pubgencols != 'n')`.
- `make dev-up` hanging at slot creation: `pg_create_logical_replication_slot` blocked while
  subscription workers held open transactions during initial table sync — moved `test_slot`
  creation to init time (before any subscriptions exist).
- Self-subscription deadlock (`CREATE SUBSCRIPTION` competing with its own internal
  `CREATE_REPLICATION_SLOT` on the same server) — subscriptions are now created on the
  dedicated pg-sub container, eliminating the feedback loop that also caused continuous dead
  tuples on the orders and inventory tables.

## [0.3.0] - 2026-07-01

### Added
- MCP Server mode (`--mcp`/`--mcp-port`): exposes every diagnostic as a read-only tool over
  SSE for LLM clients (Claude Code, Claude Desktop), printing ready-to-use `claude mcp add`
  and `.mcp.json` config on startup.
- Freeze Monitor: XID wraparound risk by database and top tables, with a `VACUUM FREEZE` action.
- Autovacuum Detail: live/dead tuples, vacuum history, freeze status, per-table vs. global
  autovacuum settings, and optional `pgstattuple` precise bloat measurement.
- Streaming Replication: Standbys screen (lag, kill walsender) and Replication Config screen
  (contextual hints on risky settings).
- Database Sizes, Temp Files, and Memory & Checkpoint Stats screens — SQL-only diagnostics
  that work against remote servers with no OS-level access required.
- `make mcp-up`/`mcp-up-repl` targets and a replication test environment
  (`docker/replication/`) for local testing.
- `/release` skill (`.claude/skills/release/SKILL.md`) with the full release checklist.

### Fixed
- PostgreSQL 13–18 compatibility: fixed crashes on PG14 (`two_phase` was gated for the wrong
  version) and PG17+ (`pg_stat_bgwriter`'s checkpoint counters moved to
  `pg_stat_checkpointer`). Replication Slots now also surfaces `failover`/`synced` (PG17+)
  and `inactive_since` (PG18+) when the connected server supports them.
- Navigator key conflicts between global shortcuts and screen-local actions (Freeze Monitor,
  Replication Slots).
- Freeze monitor UX and table selection color.

## [0.2.0] - 2026-06-28
### Added
- Homebrew tap and Scoop bucket distribution for the release binary.

## [0.1.0] - 2026-06-28
### Added
- Initial release: TUI dashboard with slow queries, connections, autovacuum, index usage,
  locks, replication slots, users/roles, schema browser, extensions, query load, and wait
  events screens.
- PostgreSQL connection via URI or individual flags, with `~/.pgpass` support.
- Cross-platform release automation (GoReleaser) for linux/darwin/windows.

[Unreleased]: https://github.com/liciomatos/pgdba/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/liciomatos/pgdba/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/liciomatos/pgdba/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/liciomatos/pgdba/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/liciomatos/pgdba/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/liciomatos/pgdba/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/liciomatos/pgdba/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/liciomatos/pgdba/releases/tag/v0.1.0
