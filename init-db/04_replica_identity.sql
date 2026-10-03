-- Replica Identity screen (key I) demo: one table per case the screen distinguishes.
-- Kept in its own schema and publication so the existing orders/inventory pub/sub demo
-- (and pg-sub's subscriptions) are untouched. pglogical cases live in pg-pglogical.

CREATE SCHEMA IF NOT EXISTS ri_demo;

-- critical: no PK, REPLICA IDENTITY DEFAULT, published with UPDATE/DELETE
--   → UPDATE/DELETE fail on this server today. Fix: REPLICA IDENTITY FULL
CREATE TABLE ri_demo.events_no_pk (event_time timestamptz NOT NULL DEFAULT now(), payload text);

-- critical: no PK but a unique index on NOT NULL columns
--   → Fix: REPLICA IDENTITY USING INDEX customer_codes_code_key
CREATE TABLE ri_demo.customer_codes (code text NOT NULL, customer text);
CREATE UNIQUE INDEX customer_codes_code_key ON ri_demo.customer_codes (code);

-- critical: has a PK, but REPLICA IDENTITY NOTHING → Fix: REPLICA IDENTITY DEFAULT
CREATE TABLE ri_demo.settings_nothing (key text PRIMARY KEY, value text);
ALTER TABLE ri_demo.settings_nothing REPLICA IDENTITY NOTHING;

-- critical: REPLICA IDENTITY USING INDEX whose index was dropped afterwards
CREATE TABLE ri_demo.sessions_index_dropped (token text NOT NULL, user_name text);
CREATE UNIQUE INDEX sessions_token_key ON ri_demo.sessions_index_dropped (token);
ALTER TABLE ri_demo.sessions_index_dropped REPLICA IDENTITY USING INDEX sessions_token_key;
DROP INDEX ri_demo.sessions_token_key;

-- critical: partition with no PK, published through its partitioned parent
--   (publish_via_partition_root) — pg_publication_tables only lists the parent
CREATE TABLE ri_demo.metrics (recorded_at date NOT NULL, value numeric) PARTITION BY RANGE (recorded_at);
CREATE TABLE ri_demo.metrics_2026 PARTITION OF ri_demo.metrics FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');

-- warning: no PK, but its publication is INSERT-only, so nothing fails today
CREATE TABLE ri_demo.audit_insert_only (logged_at timestamptz NOT NULL DEFAULT now(), message text);

-- warning: no PK and not published yet (would break once added to ri_demo_pub)
CREATE TABLE ri_demo.staging_unpublished (line text);

-- not listed: no PK, but REPLICA IDENTITY FULL is valid for native publications
CREATE TABLE ri_demo.logs_full (logged_at timestamptz NOT NULL DEFAULT now(), message text);
ALTER TABLE ri_demo.logs_full REPLICA IDENTITY FULL;

-- not listed: has a PK (the healthy baseline)
CREATE TABLE ri_demo.products_ok (id int PRIMARY KEY, name text);

INSERT INTO ri_demo.events_no_pk (payload) SELECT 'event ' || g FROM generate_series(1, 50) g;
INSERT INTO ri_demo.customer_codes SELECT 'C' || g, 'Customer ' || g FROM generate_series(1, 50) g;
INSERT INTO ri_demo.settings_nothing VALUES ('theme', 'dark'), ('lang', 'pt-BR');
INSERT INTO ri_demo.sessions_index_dropped SELECT md5(g::text), 'user' || g FROM generate_series(1, 20) g;
INSERT INTO ri_demo.metrics SELECT date '2026-01-01' + g, g FROM generate_series(0, 99) g;
INSERT INTO ri_demo.audit_insert_only (message) SELECT 'audit ' || g FROM generate_series(1, 20) g;
INSERT INTO ri_demo.staging_unpublished SELECT 'line ' || g FROM generate_series(1, 20) g;
INSERT INTO ri_demo.logs_full (message) SELECT 'log ' || g FROM generate_series(1, 20) g;
INSERT INTO ri_demo.products_ok SELECT g, 'Product ' || g FROM generate_series(1, 20) g;

CREATE PUBLICATION ri_demo_pub FOR TABLE
    ri_demo.events_no_pk, ri_demo.customer_codes, ri_demo.settings_nothing,
    ri_demo.sessions_index_dropped, ri_demo.logs_full, ri_demo.products_ok;
CREATE PUBLICATION ri_demo_root_pub FOR TABLE ri_demo.metrics WITH (publish_via_partition_root = true);
CREATE PUBLICATION ri_demo_insert_pub FOR TABLE ri_demo.audit_insert_only WITH (publish = 'insert');
