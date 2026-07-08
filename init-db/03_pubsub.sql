-- Pub/Sub demo: orders + inventory tables with publications.
-- Subscriptions are created separately via 'make seed' because CREATE SUBSCRIPTION
-- needs the server to have fully started its WAL sender background processes.

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

INSERT INTO orders (customer, product, quantity, amount, status)
SELECT
    'Customer ' || i,
    (ARRAY['Widget A', 'Widget B', 'Gadget X', 'Gadget Y'])[1 + (i % 4)],
    (i % 5) + 1,
    ((i % 100) * 9.99)::numeric(10,2),
    (ARRAY['pending', 'shipped', 'delivered'])[1 + (i % 3)]
FROM generate_series(1, 500) i
ON CONFLICT DO NOTHING;

INSERT INTO inventory (sku, product, stock)
VALUES
    ('WGT-A', 'Widget A', 1200),
    ('WGT-B', 'Widget B', 850),
    ('GDG-X', 'Gadget X', 320),
    ('GDG-Y', 'Gadget Y', 75)
ON CONFLICT DO NOTHING;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'orders_pub') THEN
        CREATE PUBLICATION orders_pub FOR TABLE orders;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'inventory_pub') THEN
        CREATE PUBLICATION inventory_pub FOR TABLE inventory WITH (publish = 'insert, update');
    END IF;
END
$$;
