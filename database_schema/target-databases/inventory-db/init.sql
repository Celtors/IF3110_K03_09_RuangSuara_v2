CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY,
    category_name VARCHAR(100) UNIQUE NOT NULL,
    description TEXT
);

CREATE TABLE IF NOT EXISTS suppliers (
    id INTEGER PRIMARY KEY,
    supplier_code VARCHAR(20) UNIQUE NOT NULL,
    supplier_name VARCHAR(100) NOT NULL,
    email VARCHAR(150)
);

CREATE TABLE IF NOT EXISTS products (
    id INTEGER PRIMARY KEY,
    sku VARCHAR(30) UNIQUE NOT NULL,
    product_name VARCHAR(150) NOT NULL,
    category_id INTEGER NOT NULL REFERENCES categories(id),
    supplier_id INTEGER NOT NULL REFERENCES suppliers(id),
    unit_price NUMERIC(12,2) NOT NULL,
    reorder_level INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS stock_movements (
    id INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES products(id),
    supplier_id INTEGER REFERENCES suppliers(id),
    movement_type VARCHAR(10) NOT NULL
        CHECK (movement_type IN ('IN', 'OUT')),
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    movement_date DATE NOT NULL,
    notes TEXT
);

-- 20 categories
INSERT INTO categories
SELECT
    i,
    'Category ' || i,
    'Description for category ' || i
FROM generate_series(1, 20) AS i
ON CONFLICT DO NOTHING;

-- 30 suppliers
INSERT INTO suppliers
SELECT
    i,
    'SUP' || LPAD(i::TEXT, 4, '0'),
    'Supplier Company ' || i,
    'supplier' || i || '@example.com'
FROM generate_series(1, 30) AS i
ON CONFLICT DO NOTHING;

-- 100 products
INSERT INTO products
SELECT
    i,
    'SKU-' || LPAD(i::TEXT, 4, '0'),
    'Inventory Product ' || i,
    ((i - 1) % 20) + 1,
    ((i - 1) % 30) + 1,
    10000 + (i * 1250),
    5 + (i % 20)
FROM generate_series(1, 100) AS i
ON CONFLICT DO NOTHING;

-- 200 stock movements
INSERT INTO stock_movements
SELECT
    i,
    ((i - 1) % 100) + 1,
    ((i - 1) % 30) + 1,
    CASE WHEN i % 2 = 0 THEN 'IN' ELSE 'OUT' END,
    1 + (i % 50),
    DATE '2026-01-01' + i,
    'Automatically generated movement ' || i
FROM generate_series(1, 200) AS i
ON CONFLICT DO NOTHING;