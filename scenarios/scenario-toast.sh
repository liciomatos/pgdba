#!/usr/bin/env bash
set -e

CONTAINER=pgdba_main

echo "==> Creating TOAST scenario..."

podman exec -i "$CONTAINER" psql -U postgres -d mydb <<'SQL'
-- Table with a large text column — values >8 KB are stored out-of-line in the TOAST heap.
CREATE TABLE IF NOT EXISTS toast_demo (
    id       serial PRIMARY KEY,
    title    text NOT NULL,
    payload  text NOT NULL,   -- ~16 KB per row → definitely stored in TOAST
    metadata jsonb
);

-- Insert 120 rows; each payload is ~16 KB, guaranteeing TOAST storage.
INSERT INTO toast_demo (title, payload, metadata)
SELECT
    'Record ' || i,
    repeat('Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor. ', 200),
    jsonb_build_object(
        'index', i,
        'tags', '["test","toast","demo"]'::jsonb,
        'description', repeat('x', 800)
    )
FROM generate_series(1, 120) AS i
ON CONFLICT DO NOTHING;

-- First round of updates — creates dead tuples in the TOAST heap for 1/3 of rows.
UPDATE toast_demo
SET payload = repeat('First update — overwriting the original payload content. ', 200)
WHERE id % 3 = 0;

-- Second round — creates more TOAST dead tuples on a different subset.
UPDATE toast_demo
SET payload = repeat('Second update — another generation of dead TOAST tuples. ', 220)
WHERE id % 5 = 0;

-- Read the table to generate I/O stats in pg_statio_user_tables.toast_blks_*.
SELECT count(*), sum(length(payload)) FROM toast_demo;
SQL

echo ""
echo "TOAST scenario ready — 120 rows with ~16 KB payloads each."
echo "Two UPDATE passes created dead tuples in the TOAST heap."
echo "Open pgdba and press T to view TOAST Tables."
echo ""
echo "To clean up: make scenarios-clean"
