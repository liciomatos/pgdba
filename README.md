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
| `-W`, `--password-prompt` | — | — | Prompt for the password without echo (like `psql -W`); overrides any password from flags, env, URL, vault or `~/.pgpass` |
| `--vault` | — | — | Connect using a named connection stored in the encrypted vault (see [Connection Vault](#connection-vault)) |
| `--slow-ms` | `PG_SLOW_MS` | `1000` | Threshold in ms to classify a query as slow |
| `--mcp` | — | — | Start MCP server mode (HTTP/SSE) |
| `--mcp-port` | — | `8811` | Port for MCP server |

All flags fall back to their environment variable counterpart. If `--password` is not set and `PGPASSWORD` is empty, `~/.pgpass` is consulted automatically.

### Connection Vault

Store named connection URIs in a local file encrypted with a master password
(Argon2id key derivation + AES-256-GCM), so credentials never sit in shell history,
scripts or plaintext config:

```bash
pgdba-cli vault add prod              # prompts for the URI (hidden) and the master password
pgdba-cli vault add staging "postgres://admin:secret@staging-db/app?sslmode=require"
pgdba-cli vault list                  # names + URIs with passwords masked
pgdba-cli vault remove staging
pgdba-cli vault path                  # where the vault file lives

pgdba-cli --vault prod                # connect (prompts for the master password)
pgdba-cli --vault prod -W             # use the stored URI but type the DB password now
```

| Env var | Description |
|---|---|
| `PGDBA_VAULT_PASSWORD` | Master password for non-interactive use (e.g. `--mcp` under a service manager) |
| `PGDBA_VAULT_FILE` | Vault location (default: `<user config dir>/pgdba-cli/vault.json`, e.g. `~/.config/pgdba-cli/vault.json`) |

The vault file is written with `0600` permissions. There is no recovery if the master
password is lost — delete the file and re-add the connections.

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

# Or via the Makefile, against the local dev environment
make mcp-up        # local dev database (mydb), requires `make dev-up` first
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

The server exposes tools covering every TUI screen — slow queries, connections,
autovacuum, replication slots, freeze status, streaming standbys, replication config,
database sizes, temp file usage, memory & checkpoint stats, pub/sub monitoring, and more.
Every tool only runs `SELECT` queries and is annotated `readOnlyHint`/non-destructive,
so MCP clients don't need to treat calls as risky.

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

### Pub/Sub Monitoring

![Pub/Sub](docs/screenshots/pub_sub.svg)

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
| `6` | **Autovacuum** | Tables ranked by dead tuples, with a Status column flagging what's vacuuming right now and a worker-saturation bar vs. `autovacuum_max_workers` | `enter` detail view, `v` VACUUM ANALYZE |
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
| `R` | **Pub/Sub** | Publications and subscriptions with table drill-down and live stats | `tab` switch section, `enter` table detail |
| `T` | **TOAST Tables** | Tables with TOAST heap data — size, dead tuples, cache hit ratio, and the columns causing TOAST storage | `v` vacuum TOAST heap, `enter` parent detail |
| `I` | **Replica Identity** | Tables without a usable replica identity for logical replication — native publications and pglogical replication sets — flagging the ones already replicating UPDATE/DELETE | `p` replicated only, `/` filter; suggested fix shown for the selected row |

All list screens show every row (no top-N cut-off) and scroll with `↑↓`/`pgup`/`pgdn` when the list is taller than the terminal. All list screens support live filtering via `/`.

### Autovacuum Detail

Press `enter` on any row in the Autovacuum screen to open the detail view for that table:

- **Stats** — live/dead tuple counts, table and total size
- **Vacuum History** — last vacuum, autovacuum, analyze, and autoanalyze timestamps with counters
- **Freeze Status** — `relfrozenxid` age with a visual progress bar
- **Parameters & Thresholds** — per-table autovacuum reloptions vs. global defaults, plus
  `Threshold` / `Current` / `Status` columns computed the way PostgreSQL does it (table value
  when set, global otherwise):
  - `autovacuum_vacuum_threshold` row: `threshold + scale_factor × reltuples` vs dead tuples
    (capped by `autovacuum_vacuum_max_threshold` on PG18+)
  - `autovacuum_vacuum_insert_threshold` row: same for inserts since the last vacuum
    (× unfrozen fraction of the table on PG18+; `disabled` when `-1`)
  - `autovacuum_analyze_threshold` row: same for rows modified since the last analyze
  - `*_freeze_max_age` rows: XID/MultiXact age vs the anti-wraparound limit (`FORCED` when
    exceeded; a table value can only lower the global)
  - `*_freeze_table_age` rows: age vs the aggressive-scan limit (capped at 95% of freeze_max_age)
  - `*_freeze_min_age` rows: effective value (capped at 50% of freeze_max_age)
  - the stats line shows `reltuples` and whether autovacuum would process the table now
  - moving the cursor shows the formula behind the selected row as a tip above the footer,
    with each value's source (`table`, `global` or `adjusted`)
- **Precise Bloat** — press `b` to run `pgstattuple` for an exact bloat measurement (full table scan)

### Pub/Sub Monitoring

Press `R` from the main dashboard to open the Pub/Sub screen:

- **Publications** — name, table count, and operation flags (Insert/Update/Delete/Truncate/Via Root)
- **Subscriptions** — name, status (active/down/disabled), subscribed publications, worker PID, received LSN, and error counts
- Press `enter` on any row to open a detail view with per-table sync state, row counts, and DML stats
- Press `tab` to switch between the Publications and Subscriptions sections

### Replica Identity

Press `I` from the main dashboard to find tables that would break logical replication.
UPDATE/DELETE can only be replicated for tables with a usable replica identity:

| Replication | Usable identity | Without one |
|---|---|---|
| Native publications | PK (`DEFAULT`), `USING INDEX`, or `FULL` | UPDATE/DELETE fail **on the publisher**: `cannot update table … because it does not have a replica identity` |
| pglogical replication sets | PK (`DEFAULT`) or `USING INDEX` — **`FULL` is not supported** | `replication_set_add_table` refuses the table, but a PK dropped (or identity changed to `FULL`/`NOTHING`) *after* it was added breaks replication; tables in `default_insert_only` silently don't replicate UPDATE/DELETE |

- Lists permanent user tables whose identity is unusable: `REPLICA IDENTITY DEFAULT` without a
  primary key, `NOTHING`, `USING INDEX` whose index was dropped, or `FULL` on a table in a
  pglogical set. Unlogged/temp tables are never replicated and are skipped
- **critical** — a publication or pglogical set already replicates UPDATE/DELETE for the
  table (publications count directly, via `FOR ALL TABLES` / `FOR TABLES IN SCHEMA`, or
  through a partitioned parent with `publish_via_partition_root`; pglogical counts the
  local node's sets); **warning** — not replicated yet, or INSERT-only
- The **Replicated by** column shows publication names and `pglogical:<set>`; pglogical is
  detected automatically when its catalog exists
- Press `p` to show only replicated tables (e.g. "replicated tables without PK and not
  `REPLICA IDENTITY FULL`"); combines with `/`
- The selected row shows the suggested fix above the footer, cheapest first:
  `REPLICA IDENTITY DEFAULT` when a PK exists, `USING INDEX` when a unique, non-partial index
  on `NOT NULL` columns exists, `FULL` otherwise — or "add a primary key" for pglogical
  tables, since FULL isn't an option there. The screen is read-only — it never runs the DDL
- The Pub/Sub screen (`R`) shows an alert with the critical count, pointing to `I`

### TOAST Tables

Press `T` from the main dashboard to see which tables have data stored in their TOAST heap:

- **Toast Size** — on-disk size of the TOAST relation (`pg_relation_size` on the TOAST table itself)
- **Toast %** — TOAST size as a percentage of the table's total size (main + TOAST + indexes)
- **Dead Tuples** — dead tuples in the TOAST heap, which accumulate independently of the parent table when large columns are updated
- **Cache Hit %** — buffer cache hit ratio for TOAST I/O (`toast_blks_hit / (toast_blks_hit + toast_blks_read)` from `pg_statio_user_tables`)
- **Toast Columns** — comma-separated list of columns whose storage strategy can produce TOAST data (EXTENDED/EXTERNAL/MAIN — PLAIN columns like `int` are excluded)
- Press `v` to `VACUUM` the TOAST heap directly (`VACUUM pg_toast.<toast_table>`) with a confirmation prompt — useful when dead tuples accumulate after heavy updates on large columns
- Press `enter` to open the Autovacuum Detail screen for the parent table

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
# Start the full local environment (primary + replica + logical subscriber)
make dev-up

# Connect to each node
make run          # pg-main (port 5432) — primary, publications, scenarios
make run-replica  # pg-replica (port 5433) — physical streaming standby
make run-sub      # pg-sub (port 5434) — logical subscriber
make run-pglogical # pg-pglogical (port 5435) — pglogical provider node

# Tear down all containers and volumes
make dev-down

# Unit tests only (no Docker required)
cd pgdba-cli && go test ./... -short

# All tests including integration (requires Docker)
cd pgdba-cli && go test ./... -timeout 120s
```

`make dev-up` starts four containers in a single docker-compose stack and seeds all
diagnostic scenarios automatically:

| Container | Port | Role |
|---|---|---|
| `pg-main` | 5432 | Primary — publications, slow queries, lock scenarios |
| `pg-replica` | 5433 | Physical streaming standby from pg-main |
| `pg-sub` | 5434 | Logical subscriber to pg-main's publications |
| `pg-pglogical` | 5435 | pglogical provider node (`postgresql-18-pglogical`) |

The Replica Identity screen (`I`) has ready-made cases on both servers, baked into the
images (`init-db/04_replica_identity.sql`, `docker/pglogical-init.sql`), so there's nothing
to set up by hand:

| Server | Table | Expected |
|---|---|---|
| pg-main, schema `ri_demo` | `events_no_pk` | critical — no PK, in `ri_demo_pub` → `REPLICA IDENTITY FULL` |
| | `customer_codes` | critical — no PK, unique NOT NULL index → `USING INDEX` |
| | `settings_nothing` | critical — PK but `NOTHING` → `REPLICA IDENTITY DEFAULT` |
| | `sessions_index_dropped` | critical — `USING INDEX` whose index was dropped |
| | `metrics_2026` | critical — partition published through its parent (`publish_via_partition_root`) |
| | `audit_insert_only` | warning — INSERT-only publication |
| | `staging_unpublished` | warning — not published |
| | `logs_full`, `products_ok` | not listed — `FULL` / PK are fine natively |
| pg-pglogical, schema `plg_demo` | `orders_pk_dropped` | critical — PK dropped after joining set `default` → add a PK |
| | `invoices_full` | critical — switched to `FULL` (unsupported by pglogical) → `REPLICA IDENTITY DEFAULT` |
| | `payments_full` | critical — switched to `FULL`, unique index still there → `USING INDEX` |
| | `clickstream_insert_only` | warning — set `default_insert_only` |
| | `customers_ok`, `scratch_full` | not listed |

Init scripts only run on empty volumes: after editing one, `make dev-down && make dev-up`.

### Compatibility testing across PostgreSQL versions

pgdba-cli is tested against PostgreSQL 13 through 18. To run the full integration suite
locally against a specific version:

```bash
# Test against a single version
cd pgdba-cli && PGDBA_TEST_PG_VERSION=17-alpine go test ./... -timeout 120s

# Test against every supported version (13-18) locally, one after another
make test-pg-matrix
```

This mirrors the `pg-compat.yml` CI workflow, which runs the same matrix in GitHub Actions
(triggered manually via `workflow_dispatch` or automatically on release-tag pushes, since
running all 6 versions on every push/PR would be too slow for routine development).

## Requirements

- PostgreSQL 13 or later. New major versions are added to the CI compatibility matrix as
  they're released (currently 14–18 fully supported; PostgreSQL 13 is past its community
  end-of-life on 2025-11-13 but pgdba-cli still targets it on a best-effort basis).
- **Slow Queries** and **Query Load** screens require the `pg_stat_statements` extension
- **Precise Bloat** (`b` in Autovacuum Detail) requires the `pgstattuple` extension
