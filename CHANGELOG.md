# Changelog

All notable changes to pgdba-cli are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

[Unreleased]: https://github.com/liciomatos/pgdba/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/liciomatos/pgdba/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/liciomatos/pgdba/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/liciomatos/pgdba/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/liciomatos/pgdba/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/liciomatos/pgdba/releases/tag/v0.1.0
