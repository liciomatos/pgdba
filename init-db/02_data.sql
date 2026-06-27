-- Seed data for pgdba-cli test scenarios

-- Populate test_data with 10000 rows (for index usage screen)
INSERT INTO test_data (name, email, age, status)
SELECT
    'user_' || i,
    'user' || i || '@example.com',
    (random() * 100)::int,
    CASE WHEN i % 3 = 0 THEN 'active'
         WHEN i % 3 = 1 THEN 'inactive'
         ELSE 'pending' END
FROM generate_series(1, 10000) AS i
ON CONFLICT DO NOTHING;

-- Create dead tuples for autovacuum screen:
-- insert 10000 rows then delete half → 5000 dead tuples
TRUNCATE test_bloat;
INSERT INTO test_bloat
SELECT generate_series(1, 10000), md5(random()::text);
DELETE FROM test_bloat WHERE id % 2 = 0;
-- intentionally NOT running VACUUM to keep dead tuples visible

-- Run a few queries so they appear in pg_stat_statements (slow queries screen)
SELECT count(*) FROM test_data WHERE name LIKE '%user_5%';
SELECT avg(age), status FROM test_data GROUP BY status ORDER BY avg DESC;
SELECT d1.name, d2.email
FROM test_data d1
JOIN test_data d2 ON d1.age = d2.age
LIMIT 100;
