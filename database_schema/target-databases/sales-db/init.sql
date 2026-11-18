CREATE TABLE IF NOT EXISTS customers (
    id INTEGER PRIMARY KEY,
    customer_code VARCHAR(20) UNIQUE NOT NULL,
    full_name VARCHAR(100) NOT NULL,
    email VARCHAR(150) UNIQUE NOT NULL,
    phone VARCHAR(20)
);

CREATE TABLE IF NOT EXISTS orders (
    id INTEGER PRIMARY KEY,
    order_number VARCHAR(30) UNIQUE NOT NULL,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    order_date DATE NOT NULL,
    status VARCHAR(20) NOT NULL,
    total_amount NUMERIC(12,2) NOT NULL
);

CREATE TABLE IF NOT EXISTS order_items (
    id INTEGER PRIMARY KEY,
    order_id INTEGER NOT NULL REFERENCES orders(id),
    line_number INTEGER NOT NULL,
    product_sku VARCHAR(30) NOT NULL,
    product_name VARCHAR(150) NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price NUMERIC(12,2) NOT NULL,
    UNIQUE (order_id, line_number)
);

CREATE TABLE IF NOT EXISTS payments (
    id INTEGER PRIMARY KEY,
    order_id INTEGER UNIQUE NOT NULL REFERENCES orders(id),
    payment_date DATE NOT NULL,
    payment_method VARCHAR(30) NOT NULL,
    amount NUMERIC(12,2) NOT NULL,
    payment_status VARCHAR(20) NOT NULL
);

-- 100 customers
INSERT INTO customers
SELECT
    i,
    'CUS' || LPAD(i::TEXT, 4, '0'),
    'Customer ' || i,
    'customer' || i || '@example.com',
    '0812' || LPAD(i::TEXT, 8, '0')
FROM generate_series(1, 100) AS i
ON CONFLICT DO NOTHING;

-- 100 orders
INSERT INTO orders
SELECT
    i,
    'ORD-' || LPAD(i::TEXT, 6, '0'),
    i,
    DATE '2026-01-01' + i,
    'COMPLETED',
    30000 + (i * 200)
FROM generate_series(1, 100) AS i
ON CONFLICT DO NOTHING;

-- 200 order items: two items per order
INSERT INTO order_items
SELECT
    (i * 2) - 1,
    i,
    1,
    'SKU-' || LPAD(i::TEXT, 4, '0'),
    'Product ' || i,
    1,
    10000 + (i * 100)
FROM generate_series(1, 100) AS i
ON CONFLICT DO NOTHING;

INSERT INTO order_items
SELECT
    (i * 2),
    i,
    2,
    'SKU-' || LPAD((i + 100)::TEXT, 4, '0'),
    'Product ' || (i + 100),
    1,
    20000 + (i * 100)
FROM generate_series(1, 100) AS i
ON CONFLICT DO NOTHING;

-- 100 payments
INSERT INTO payments
SELECT
    i,
    i,
    DATE '2026-01-02' + i,
    CASE
        WHEN i % 3 = 0 THEN 'BANK_TRANSFER'
        WHEN i % 3 = 1 THEN 'CREDIT_CARD'
        ELSE 'E_WALLET'
    END,
    30000 + (i * 200),
    'SUCCESS'
FROM generate_series(1, 100) AS i
ON CONFLICT DO NOTHING;