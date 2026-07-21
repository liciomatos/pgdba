#!/usr/bin/env bash
set -e

CONTAINER=pgdba_main

echo "==> Creating TOAST scenario..."

podman exec -i "$CONTAINER" psql -U postgres -d mydb <<'SQL'
DROP TABLE IF EXISTS toast_demo;

-- STORAGE EXTERNAL disables compression, so values > ~2 KB go directly to the TOAST heap.
-- Without this, repeated strings compress to a few hundred bytes and stay inline.
CREATE TABLE toast_demo (
    id       serial PRIMARY KEY,
    title    text NOT NULL,
    payload  text NOT NULL,
    metadata jsonb
);

ALTER TABLE toast_demo ALTER COLUMN payload SET STORAGE EXTERNAL;

-- md5(i::text) produces 32 chars of hex that differ per row → not compressible.
-- 300 repetitions × 32 chars = ~9.6 KB per row, guaranteed to land in TOAST.
INSERT INTO toast_demo (title, payload, metadata)
SELECT
    'Record ' || i,
    repeat(md5(i::text), 300),
    jsonb_build_object('index', i, 'tags', '["test","toast","demo"]'::jsonb)
FROM generate_series(1, 120) AS i;

-- Two UPDATE passes create dead tuples in the TOAST heap on different row subsets.
UPDATE toast_demo
    SET payload = repeat(md5((id + 1000)::text), 300)
    WHERE id % 3 = 0;

UPDATE toast_demo
    SET payload = repeat(md5((id + 2000)::text), 300)
    WHERE id % 5 = 0;

-- Read to populate pg_statio_user_tables.toast_blks_* (Cache Hit % column).
SELECT count(*), sum(length(payload)) FROM toast_demo;
SQL

echo ""
echo "TOAST scenario ready — 120 rows with ~9.6 KB non-compressible payloads."
echo "Two UPDATE passes created dead tuples in the TOAST heap."
echo "Open pgdba and press T to view TOAST Tables."
echo ""
echo "To clean up: make scenarios-clean"
