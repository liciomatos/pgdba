# pgdba-cli

Terminal UI for PostgreSQL DBAs — interactive diagnostics and management directly in the terminal.

## Installation

### Pre-built binary (recommended)

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

All flags fall back to their environment variable counterpart. If `--password` is not set and `PGPASSWORD` is empty, `~/.pgpass` is consulted automatically.

### Examples

```bash
# Via environment variables
export PGHOST=db.example.com PGUSER=admin PGPASSWORD=secret
pgdba-cli --dbname=production

# Custom slow query threshold
pgdba-cli --url="postgres://admin:secret@db.example.com/production" --slow-ms=500
```

## Features

Navigate with `↑↓` or `j/k`. Press `r` to refresh and `q`/`esc` to go back.

From the main dashboard, open each screen with its shortcut key:

| Key | Screen | Description | Actions |
|---|---|---|---|
| `1` | **Slow Queries** | Top queries by average execution time¹ | — |
| `2` | **Long Running Queries** | Active queries running longer than 5 seconds | `k` kill session |
| `3` | **Replication Slots** | Slots and accumulated WAL size | `d` drop slot |
| `4` | **Blocked Queries** | Blocked sessions and their blockers | `t` terminate session, `a` terminate all |
| `5` | **Connections** | Connections by state with % of limit used | — |
| `6` | **Autovacuum** | Tables with most dead tuples | `v` VACUUM ANALYZE |
| `7` | **Index Usage** | Indexes sorted by scan count | `enter` index detail |
| `8` | **Cache Hit Ratio** | Buffer cache hit ratio per table | — |
| `9` | **Users** | Login roles and their privileges | — |
| `0` | **Roles** | Group roles and members | — |
| `p` | **Config** | PostgreSQL parameters (`pg_settings`) | — |
| `s` | **Schema Browser** | Tables and columns by schema | `enter` describe table |
| `e` | **Extensions** | Installed extensions | — |
| `D` | **Switch Database** | Switch database without restarting | `enter` connect |
| `v` | **Version** | PostgreSQL server version | — |

All list screens support live filtering via `/`.

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

## Requirements

- PostgreSQL 13 or later
- **Slow Queries** screen requires the `pg_stat_statements` extension
