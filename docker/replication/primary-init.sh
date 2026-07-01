#!/bin/bash
set -e

# Allow replication connections from the replica container.
echo "host replication postgres 0.0.0.0/0 trust" >> "$PGDATA/pg_hba.conf"
echo "host all postgres 0.0.0.0/0 trust"         >> "$PGDATA/pg_hba.conf"

# Create a logical replication slot so the WAL-lag monitoring screens have data.
psql -U postgres -d testdb <<'SQL'
SELECT pg_create_logical_replication_slot('test_logical_slot', 'pgoutput');
SELECT pg_create_physical_replication_slot('test_physical_slot');

-- Seed with some rows so autovacuum and freeze screens show real data.
CREATE TABLE IF NOT EXISTS orders (
    id         serial PRIMARY KEY,
    customer   text NOT NULL,
    amount     numeric(10,2),
    created_at timestamptz DEFAULT now()
);

INSERT INTO orders (customer, amount)
SELECT 'Customer ' || i, (random() * 1000)::numeric(10,2)
FROM generate_series(1, 10000) i;

-- Trigger some dead tuples
UPDATE orders SET amount = amount * 1.1 WHERE id % 5 = 0;
DELETE FROM orders WHERE id % 7 = 0;
SQL
