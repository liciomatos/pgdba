-- pg-pglogical: pglogical provider node with Replica Identity screen (key I) scenarios.
-- pglogical needs a PRIMARY KEY or REPLICA IDENTITY USING INDEX for UPDATE/DELETE and
-- does not support REPLICA IDENTITY FULL. replication_set_add_table() refuses such
-- tables, so the broken cases are created the way they happen in production: the table
-- is added while healthy and changed afterwards.

CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE EXTENSION IF NOT EXISTS pglogical;

-- The DSN is only stored here (subscribers would use it); the local socket is enough.
SELECT pglogical.create_node(node_name := 'pgdba_provider', dsn := 'dbname=mydb user=postgres');

CREATE SCHEMA IF NOT EXISTS plg_demo;

-- critical: had a PK when added to "default", PK dropped afterwards → add a PK again
CREATE TABLE plg_demo.orders_pk_dropped (id int PRIMARY KEY, customer text, amount numeric);

-- critical: has a PK but was switched to REPLICA IDENTITY FULL after being added
--   → FULL is valid natively but not for pglogical. Fix: REPLICA IDENTITY DEFAULT
CREATE TABLE plg_demo.invoices_full (id int PRIMARY KEY, total numeric);

-- critical: no PK, added with REPLICA IDENTITY USING INDEX, switched to FULL afterwards
--   → the unique index is still there. Fix: REPLICA IDENTITY USING INDEX payments_reference_key
CREATE TABLE plg_demo.payments_full (reference text NOT NULL, amount numeric);
CREATE UNIQUE INDEX payments_reference_key ON plg_demo.payments_full (reference);
ALTER TABLE plg_demo.payments_full REPLICA IDENTITY USING INDEX payments_reference_key;

-- warning: no PK, in "default_insert_only" — UPDATE/DELETE are silently not replicated
CREATE TABLE plg_demo.clickstream_insert_only (clicked_at timestamptz NOT NULL DEFAULT now(), url text);

-- not listed: healthy table in "default"
CREATE TABLE plg_demo.customers_ok (id int PRIMARY KEY, name text);

-- not listed: no PK + FULL is fine as long as it isn't in a pglogical set
CREATE TABLE plg_demo.scratch_full (note text);
ALTER TABLE plg_demo.scratch_full REPLICA IDENTITY FULL;

INSERT INTO plg_demo.orders_pk_dropped SELECT g, 'Customer ' || g, g * 10 FROM generate_series(1, 50) g;
INSERT INTO plg_demo.invoices_full SELECT g, g * 99.9 FROM generate_series(1, 50) g;
INSERT INTO plg_demo.payments_full SELECT 'PAY-' || g, g * 5 FROM generate_series(1, 50) g;
INSERT INTO plg_demo.clickstream_insert_only (url) SELECT '/page/' || g FROM generate_series(1, 50) g;
INSERT INTO plg_demo.customers_ok SELECT g, 'Customer ' || g FROM generate_series(1, 20) g;

SELECT pglogical.replication_set_add_table('default', 'plg_demo.orders_pk_dropped');
SELECT pglogical.replication_set_add_table('default', 'plg_demo.invoices_full');
SELECT pglogical.replication_set_add_table('default', 'plg_demo.payments_full');
SELECT pglogical.replication_set_add_table('default', 'plg_demo.customers_ok');
SELECT pglogical.replication_set_add_table('default_insert_only', 'plg_demo.clickstream_insert_only');

-- Break them after the fact, as an ALTER/migration would.
ALTER TABLE plg_demo.orders_pk_dropped DROP CONSTRAINT orders_pk_dropped_pkey;
ALTER TABLE plg_demo.invoices_full REPLICA IDENTITY FULL;
ALTER TABLE plg_demo.payments_full REPLICA IDENTITY FULL;
