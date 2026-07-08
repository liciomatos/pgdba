#!/bin/bash
set -e

# Allow the subscriber container to connect for replication.
echo "host all postgres 0.0.0.0/0 trust" >> "$PGDATA/pg_hba.conf"
echo "host replication postgres 0.0.0.0/0 trust" >> "$PGDATA/pg_hba.conf"

psql -U postgres -d sales <<'SQL'
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- Orders table replicated to the subscriber.
CREATE TABLE orders (
    id         serial PRIMARY KEY,
    customer   text NOT NULL,
    product    text NOT NULL,
    quantity   int  NOT NULL DEFAULT 1,
    amount     numeric(10,2),
    status     text NOT NULL DEFAULT 'pending',
    created_at timestamptz DEFAULT now()
);

-- Inventory table replicated to the subscriber (insert-only).
CREATE TABLE inventory (
    sku        text PRIMARY KEY,
    product    text NOT NULL,
    stock      int  NOT NULL DEFAULT 0,
    updated_at timestamptz DEFAULT now()
);

-- Seed data
INSERT INTO orders (customer, product, quantity, amount, status)
SELECT
    'Customer ' || i,
    (ARRAY['Widget A', 'Widget B', 'Gadget X', 'Gadget Y'])[1 + (i % 4)],
    (i % 5) + 1,
    ((i % 100) * 9.99)::numeric(10,2),
    (ARRAY['pending', 'shipped', 'delivered'])[1 + (i % 3)]
FROM generate_series(1, 500) i;

INSERT INTO inventory (sku, product, stock)
VALUES
    ('WGT-A', 'Widget A', 1200),
    ('WGT-B', 'Widget B', 850),
    ('GDG-X', 'Gadget X', 320),
    ('GDG-Y', 'Gadget Y', 75);

-- Publish both tables; inventory is insert/update only (no delete).
CREATE PUBLICATION orders_pub FOR TABLE orders;
CREATE PUBLICATION inventory_pub FOR TABLE inventory WITH (publish = 'insert, update');
SQL
