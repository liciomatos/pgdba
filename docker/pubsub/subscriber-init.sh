#!/bin/bash
set -e

psql -U postgres -d sales <<'SQL'
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- Mirror schema must exist on the subscriber before the subscription syncs data.
CREATE TABLE orders (
    id         serial PRIMARY KEY,
    customer   text NOT NULL,
    product    text NOT NULL,
    quantity   int  NOT NULL DEFAULT 1,
    amount     numeric(10,2),
    status     text NOT NULL DEFAULT 'pending',
    created_at timestamptz DEFAULT now()
);

CREATE TABLE inventory (
    sku        text PRIMARY KEY,
    product    text NOT NULL,
    stock      int  NOT NULL DEFAULT 0,
    updated_at timestamptz DEFAULT now()
);

-- Subscribe to both publications on the publisher.
-- The publisher container is reachable as "publisher" within the Docker network.
CREATE SUBSCRIPTION orders_sub
    CONNECTION 'host=publisher port=5432 user=postgres password=postgres dbname=sales'
    PUBLICATION orders_pub;

CREATE SUBSCRIPTION inventory_sub
    CONNECTION 'host=publisher port=5432 user=postgres password=postgres dbname=sales'
    PUBLICATION inventory_pub;
SQL
