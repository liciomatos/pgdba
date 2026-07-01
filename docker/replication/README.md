# Replication Test Environment

PostgreSQL 15 primary + streaming replica for testing WAL, replication slots, and streaming standbys screens.

## Start

```bash
cd docker/replication
docker compose up -d
```

The replica clones the primary automatically via `pg_basebackup` (~10 s first boot).

## Connect

```bash
# Primary (port 5432)
pgdba-cli --host=localhost --port=5432 --user=postgres --password=postgres --dbname=testdb --sslmode=disable

# Replica (port 5433) — read-only
pgdba-cli --host=localhost --port=5433 --user=postgres --password=postgres --dbname=testdb --sslmode=disable
```

## MCP mode

```bash
pgdba-cli --mcp --url "postgres://postgres:postgres@localhost:5432/testdb?sslmode=disable"
```

## Test scenarios

| Screen | Key | What to verify |
|--------|-----|----------------|
| Replication Slots | `3` | `test_logical_slot` and `test_physical_slot` visible; WAL lag column |
| Streaming Standbys | `3` → `s` | `pgdba_replica` standby connected; write/flush/replay lag |
| Replication Config | `3` → `p` | `wal_level=logical` shown with green hint |
| Freeze Monitor | `f` | XID ages for `testdb`; `orders` table in table list |
| Autovacuum | `6` | `orders` with dead tuples from seeding |
| Autovacuum Detail | `6` → Enter | Vacuum history, freeze status, custom params for `orders` |

## Stop

```bash
docker compose down -v   # -v removes volumes (fresh state on next up)
```
