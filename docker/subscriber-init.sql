-- Mirror tables for logical replication from pg-main.
-- Schema must match pg-main exactly; data arrives via orders_sub / inventory_sub subscriptions.
-- Subscriptions are created separately via 'make seed' after both servers are ready.

CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

CREATE TABLE IF NOT EXISTS orders (
    id         serial PRIMARY KEY,
    customer   text NOT NULL,
    product    text NOT NULL,
    quantity   int  NOT NULL DEFAULT 1,
    amount     numeric(10,2),
    status     text NOT NULL DEFAULT 'pending',
    created_at timestamptz DEFAULT now()
);

CREATE TABLE IF NOT EXISTS inventory (
    sku        text PRIMARY KEY,
    product    text NOT NULL,
    stock      int  NOT NULL DEFAULT 0,
    updated_at timestamptz DEFAULT now()
);
