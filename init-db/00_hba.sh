#!/bin/bash
set -e

# Allow the replica and logical subscription workers to connect for replication.
echo "host replication postgres 0.0.0.0/0 trust" >> "$PGDATA/pg_hba.conf"
echo "host all         postgres 0.0.0.0/0 trust" >> "$PGDATA/pg_hba.conf"

# Reload pg_hba so the rules are active for subsequent init scripts in this session.
psql -U postgres -d mydb -c "SELECT pg_reload_conf();"

# Physical slot consumed by the replica via primary_slot_name.
# test_slot: demo inactive logical slot visible in the Replication Slots screen.
psql -U postgres -d mydb <<'SQL'
SELECT pg_create_physical_replication_slot('test_physical_slot');
SELECT pg_create_logical_replication_slot('test_slot', 'pgoutput');
SQL
