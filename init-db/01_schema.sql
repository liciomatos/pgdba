-- Test schema for pgdba-cli scenarios

-- For autovacuum screen (dead tuples demo)
CREATE TABLE IF NOT EXISTS test_bloat (
    id SERIAL PRIMARY KEY,
    data TEXT
);

-- For index usage screen (unused indexes demo)
CREATE TABLE IF NOT EXISTS test_data (
    id SERIAL PRIMARY KEY,
    name TEXT,
    email TEXT,
    age INT,
    status TEXT
);
CREATE INDEX IF NOT EXISTS idx_test_data_age    ON test_data(age);
CREATE INDEX IF NOT EXISTS idx_test_data_email  ON test_data(email);
CREATE INDEX IF NOT EXISTS idx_test_data_status ON test_data(status);

-- For blocked queries screen (lock scenario)
CREATE TABLE IF NOT EXISTS test_locks (
    id  INT PRIMARY KEY,
    val TEXT
);
INSERT INTO test_locks VALUES (1, 'unlocked') ON CONFLICT DO NOTHING;
