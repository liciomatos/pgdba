# pgdba-cli

Terminal UI for PostgreSQL DBAs — interactive diagnostics and management directly in the terminal.

## Installation

### Homebrew (macOS and Linux)

```bash
brew tap liciomatos/tap
brew trust liciomatos/tap
brew install pgdba-cli
```

### Scoop (Windows)

```powershell
scoop bucket add liciomatos https://github.com/liciomatos/scoop-bucket
scoop install pgdba-cli
```

### Pre-built binary

Download the latest release from [Releases](https://github.com/liciomatos/pgdba/releases) for your OS and architecture.

```bash
# Linux (amd64)
tar -xzf pgdba-cli_*_linux_amd64.tar.gz
chmod +x pgdba-cli
sudo mv pgdba-cli /usr/local/bin/
```

### Build from source

```bash
git clone https://github.com/liciomatos/pgdba.git
cd pgdba/pgdba-cli
go build -o pgdba-cli .
```

## Usage

```bash
# Via URI (recommended)
pgdba-cli --url="postgres://user:password@host:5432/dbname?sslmode=disable"

# Via individual flags
pgdba-cli --host=<host> --user=<user> --password=<password> --dbname=<dbname>
```

### Flags

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--url` | `DATABASE_URL` | — | PostgreSQL connection URI (overrides all flags below) |
| `--host` | `PGHOST` | — | Server host |
| `--port` | `PGPORT` | `5432` | Port |
| `--user` | `PGUSER` | `postgres` | Username |
| `--password` | `PGPASSWORD` | — | Password |
| `--dbname` | `PGDATABASE` | `mydb` | Database name |
| `--sslmode` | `PGSSLMODE` | `disable` | SSL mode (`disable`, `require`, `verify-ca`, `verify-full`) |
| `--slow-ms` | `PG_SLOW_MS` | `1000` | Threshold in ms to classify a query as slow |
| `--mcp` | — | — | Start MCP server mode (HTTP/SSE) |
| `--mcp-port` | — | `8811` | Port for MCP server |

All flags fall back to their environment variable counterpart. If `--password` is not set and `PGPASSWORD` is empty, `~/.pgpass` is consulted automatically.

### Examples

```bash
# Via environment variables
export PGHOST=db.example.com PGUSER=admin PGPASSWORD=secret
pgdba-cli --dbname=production

# Custom slow query threshold
pgdba-cli --url="postgres://admin:secret@db.example.com/production" --slow-ms=500
```

## MCP Server Mode

pgdba-cli can run as an [MCP](https://modelcontextprotocol.io) server, exposing all diagnostic
screens as tools callable by LLMs (Claude Code, Claude Desktop).

```bash
# Start the MCP server with DB credentials
pgdba-cli --mcp --url "postgres://user:pass@host/db"

# Or via environment variables (no credentials on the command line)
PGHOST=host PGUSER=user PGPASSWORD=pass PGDATABASE=db pgdba-cli --mcp

# Or via the Makefile, against the local dev/replication test environments
make mcp-up        # local dev database (mydb), requires `make docker-up` first
make mcp-up-repl   # replication test environment (testdb), requires `make replication-up` first
```

On startup, the server prints the `claude mcp add` command and the equivalent `.mcp.json`
snippet for the port it's actually listening on, so you don't have to look them up by hand.

Configure Claude Code (`.mcp.json` in your project — credentials never go here):

```json
{
  "mcpServers": {
    "pgdba": {
      "url": "http://localhost:8811/sse"
    }
  }
}
```

The server exposes 24 tools covering every TUI screen — slow queries, connections,
autovacuum, replication slots, freeze status, streaming standbys, replication config,
database sizes, temp file usage, memory & checkpoint stats, and more. Every tool only
runs `SELECT` queries and is annotated `readOnlyHint`/non-destructive, so MCP clients
don't need to treat calls as risky.

## Screenshots

### Dashboard

![Dashboard](docs/screenshots/dashboard.svg)

### Slow Queries

![Slow Queries](docs/screenshots/slow_queries.svg)

### Index Usage

![Index Usage](docs/screenshots/index_usage.svg)

### Autovacuum Monitor

![Autovacuum](docs/screenshots/autovacuum.svg)

### Autovacuum Detail

![Autovacuum Detail](docs/screenshots/autovacuum_detail.svg)

### Freeze Monitor

![Freeze Monitor](docs/screenshots/freeze.svg)

### Replication Slots

![Replication Slots](docs/screenshots/replication_slots.svg)

### Streaming Standbys

![Streaming Standbys](docs/screenshots/replication_standbys.svg)

### Replication Config

![Replication Config](docs/screenshots/replication_config.svg)

### Database Sizes

![Database Sizes](docs/screenshots/database_sizes.svg)

### Temp Files

![Temp Files](docs/screenshots/temp_files.svg)

### Memory & Checkpoint Stats

![Memory & Checkpoint Stats](docs/screenshots/memory_stats.svg)

### Config Parameters

![Config Parameters](docs/screenshots/config.svg)

<details>
<summary>More screenshots</summary>

### Long Running Queries
![Long Running Queries](docs/screenshots/long_running.svg)

### Blocked Queries
![Blocked Queries](docs/screenshots/blocked_queries.svg)

### Connections
![Connections](docs/screenshots/connections.svg)

### Cache Hit Ratio
![Cache Hit Ratio](docs/screenshots/cache_hit.svg)

### Users
![Users](docs/screenshots/users.svg)

### Roles
![Roles](docs/screenshots/roles.svg)

### Schema Browser
![Schema Browser](docs/screenshots/schema_browser.svg)

### Extensions
![Extensions](docs/screenshots/extensions.svg)

### Query Load
![Query Load](docs/screenshots/query_load.svg)

### Wait Events
![Wait Events](docs/screenshots/wait_events.svg)

</details>

## Features

Navigate with `↑↓` or `j/k`. Press `r` to refresh and `q`/`esc` to go back.

From the main dashboard, open each screen with its shortcut key:

| Key | Screen | Description | Actions |
|---|---|---|---|
| `1` | **Slow Queries** | Top queries by average execution time¹ | — |
| `2` | **Long Running Queries** | Active queries running longer than 5 seconds | `k` kill session |
| `3` | **Replication Slots** | Slots, plugin, WAL lag, and safe WAL size | `d` drop slot, `s` streaming standbys, `p` replication config |
| `4` | **Blocked Queries** | Blocked sessions and their blockers | `t` terminate session, `a` terminate all |
| `5` | **Connections** | Connections by state with % of limit used | — |
| `6` | **Autovacuum** | Tables with most dead tuples and bloat estimate | `enter` detail view, `v` VACUUM ANALYZE |
| `7` | **Index Usage** | Indexes sorted by scan count | `enter` index detail |
| `8` | **Cache Hit Ratio** | Buffer cache hit ratio per table | — |
| `9` | **Users** | Login roles and their privileges | — |
| `0` | **Roles** | Group roles and members | — |
| `p` | **Config** | PostgreSQL parameters (`pg_settings`) | — |
| `s` | **Schema Browser** | Tables and columns by schema | `enter` describe table |
| `e` | **Extensions** | Installed extensions | — |
| `D` | **Switch Database** | Switch database without restarting | `enter` connect |
| `L` | **Query Load** | Top queries by total execution time with load % bar | `enter` full query |
| `w` | **Wait Events** | Active wait events grouped by type with distribution bar | — |
| `f` | **Freeze Monitor** | XID age by database and top tables approaching wrap-around | `f` VACUUM FREEZE selected table |
| `S` | **Database Sizes** | On-disk size of every database and tablespace, plus cluster total | — |
| `t` | **Temp Files** | Temp file spill activity per database (`pg_stat_database`) | — |
| `m` | **Memory & Checkpoint Stats** | Memory-related config, cache hit ratio, checkpoint/bgwriter activity | — |

All list screens support live filtering via `/`.

### Autovacuum Detail

Press `enter` on any row in the Autovacuum screen to open the detail view for that table:

- **Stats** — live/dead tuple counts, table and total size
- **Vacuum History** — last vacuum, autovacuum, analyze, and autoanalyze timestamps with counters
- **Freeze Status** — `relfrozenxid` age with a visual progress bar
- **Custom Parameters** — per-table autovacuum settings vs. global defaults
- **Precise Bloat** — press `b` to run `pgstattuple` for an exact bloat measurement (full table scan)

### Streaming Standbys

Press `s` from the Replication Slots screen to see all connected streaming standbys from
`pg_stat_replication`: write/flush/replay lag, sync mode, and client address. Press `k` to
terminate the walsender for the selected standby.

### Replication Config

Press `p` from the Replication Slots screen for a read-only view of all replication-related
`pg_settings` parameters with contextual hints — highlights parameters that require attention
(e.g., `archive_mode=off`, `wal_log_hints=off`).

### Freeze Monitor

Press `f` from the main dashboard to open the Freeze Monitor:

- **Database XID Status** — age and percentage toward XID wrap-around for every database
- **Top Tables by XID Age** — tables closest to needing a freeze, with `% Freeze` colored
  green/yellow/red by proximity to `autovacuum_freeze_max_age`
- Press `f` on a selected table to run `VACUUM (FREEZE, ANALYZE)` with confirmation

### Database Sizes

Press `S` from the main dashboard for on-disk sizing:

- **Per-database size** — owner, encoding, and size (`pg_database_size`), sorted largest first
- **Total across databases** — sum of every non-template database
- **Tablespaces** — size of each tablespace (`pg_tablespace_size`) and its on-disk location

### Temp Files

Press `t` from the main dashboard to see temp file spill activity per database
(`pg_stat_database.temp_files`/`temp_bytes`, accumulated since the last stats reset).
Non-zero values are highlighted — persistent growth is a sign `work_mem` may be too low
for the workload.

### Memory & Checkpoint Stats

Press `m` from the main dashboard for a SQL-only view of memory health — no OS-level
access required, so it works against remote servers:

- **Memory config** — `shared_buffers`, `effective_cache_size`, `work_mem`,
  `maintenance_work_mem`, `wal_buffers`, `huge_pages`
- **Buffer cache hit ratio** — cluster-wide, from `pg_stat_database` (colored red/yellow/green)
- **Checkpoint & background writer activity** — from `pg_stat_bgwriter`; flags when more
  checkpoints happen on-demand than on schedule (a signal to raise `max_wal_size`)

¹ Requires the `pg_stat_statements` extension. Enable it with:
```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

## Development

```bash
# Start local PostgreSQL via Docker
make docker-up

# Build and connect to the local database
make run

# Unit tests only (no Docker required)
cd pgdba-cli && go test ./... -short

# All tests including integration (requires Docker)
cd pgdba-cli && go test ./... -timeout 120s
```

### Replication test environment

A dedicated Docker Compose setup provides a PostgreSQL 15 primary + streaming replica with
pre-seeded test data (replication slots, dead tuples, logical replication):

```bash
# Start primary (port 5432) + replica (port 5433)
make replication-up

# Connect to primary
pgdba-cli --url "postgres://postgres:postgres@localhost:5432/testdb?sslmode=disable"

# Stop and remove volumes
make replication-down
```

See [`docker/replication/README.md`](docker/replication/README.md) for test scenarios covering
each new screen.

## Requirements

- PostgreSQL 13 or later
- **Slow Queries** and **Query Load** screens require the `pg_stat_statements` extension
- **Precise Bloat** (`b` in Autovacuum Detail) requires the `pgstattuple` extension
