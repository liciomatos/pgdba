# Changelog

All notable changes to pgdba-cli are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

[Unreleased]: https://github.com/liciomatos/pgdba/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/liciomatos/pgdba/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/liciomatos/pgdba/releases/tag/v0.1.0
