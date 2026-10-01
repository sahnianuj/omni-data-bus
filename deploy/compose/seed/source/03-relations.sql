-- Relationship mapping sample (release 2.0, OC-042 – OC-046): an order with items[] holding each
-- product (and its brand), a many-to-many junction with payload (order_items), a pure junction
-- with a composite key (order_tags), and a join without a foreign key for a virtual
-- relationship (orders.customer_code → customers.code).
--
-- order_tags' key leads with tag_id, so looking tags up by order_id has no index: the Related
-- tables panel shows index advice for it.

CREATE SCHEMA sales;

CREATE TABLE sales.customers (
    customer_id   integer PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    name          text NOT NULL,
    email         text
);

CREATE TABLE sales.brands (
    brand_id      integer PRIMARY KEY,
    name          text NOT NULL,
    country       char(2)
);

CREATE TABLE sales.products (
    product_id    integer PRIMARY KEY,
    brand_id      integer REFERENCES sales.brands (brand_id),
    sku           text NOT NULL UNIQUE,
    name          text NOT NULL,
    list_price    numeric(10, 2) NOT NULL
);

CREATE TABLE sales.orders (
    order_id      bigint PRIMARY KEY,
    customer_code text NOT NULL,          -- no foreign key: define a virtual relationship
    ordered_at    timestamptz NOT NULL,
    status        text NOT NULL
);

CREATE TABLE sales.order_items (
    order_item_id bigint PRIMARY KEY,
    order_id      bigint NOT NULL REFERENCES sales.orders (order_id),
    line_no       integer NOT NULL,
    product_id    integer NOT NULL REFERENCES sales.products (product_id),
    quantity      integer NOT NULL,
    unit_price    numeric(10, 2) NOT NULL,
    line_total    numeric(12, 2) NOT NULL,
    status        text NOT NULL
);
CREATE INDEX order_items_order_idx ON sales.order_items (order_id, line_no);

CREATE TABLE sales.tags (
    tag_id        integer PRIMARY KEY,
    label         text NOT NULL
);

CREATE TABLE sales.order_tags (
    tag_id        integer NOT NULL REFERENCES sales.tags (tag_id),
    order_id      bigint NOT NULL REFERENCES sales.orders (order_id),
    PRIMARY KEY (tag_id, order_id)
);

INSERT INTO sales.customers
SELECT i, 'C-' || lpad(i::text, 4, '0'), 'Customer ' || i, 'customer' || i || '@example.com'
FROM generate_series(1, 100) AS i;

INSERT INTO sales.brands VALUES
    (1, 'Acme', 'US'), (2, 'Globex', 'DE'), (3, 'Initech', 'US'), (4, 'Umbrella', 'GB'), (5, 'Hooli', 'IN');

INSERT INTO sales.products
SELECT i, 1 + i % 5, 'SKU-' || lpad(i::text, 3, '0'), 'Product ' || i, round((2 + (i * 37 % 200) / 4.0)::numeric, 2)
FROM generate_series(1, 50) AS i;

INSERT INTO sales.orders
SELECT i, 'C-' || lpad((1 + i % 100)::text, 4, '0'), timestamptz '2026-01-01 09:00+00' + i * interval '7 hours',
       (ARRAY['pending', 'paid', 'shipped', 'cancelled'])[1 + i % 4]
FROM generate_series(1001, 1200) AS i;

-- 1 to 6 lines per order (order 1007 has 25, to show limits and overflow).
INSERT INTO sales.order_items
SELECT row_number() OVER (ORDER BY o.order_id, l.n), o.order_id, l.n, 1 + (o.order_id * 7 + l.n * 13) % 50,
       1 + (o.order_id + l.n) % 4, p.list_price, p.list_price * (1 + (o.order_id + l.n) % 4),
       CASE WHEN (o.order_id + l.n) % 9 = 0 THEN 'cancelled' ELSE 'active' END
FROM sales.orders o
CROSS JOIN LATERAL generate_series(1, CASE WHEN o.order_id = 1007 THEN 25 ELSE 1 + o.order_id % 6 END) AS l(n)
JOIN sales.products p ON p.product_id = 1 + (o.order_id * 7 + l.n * 13) % 50;

INSERT INTO sales.tags VALUES (1, 'gift'), (2, 'express'), (3, 'b2b'), (4, 'returned'), (5, 'priority');

INSERT INTO sales.order_tags
SELECT 1 + (o.order_id + k) % 5, o.order_id
FROM sales.orders o CROSS JOIN generate_series(0, 1) AS k
WHERE o.order_id % 3 <> 0
ON CONFLICT DO NOTHING;

ANALYZE sales.customers, sales.brands, sales.products, sales.orders, sales.order_items, sales.tags, sales.order_tags;

GRANT USAGE ON SCHEMA sales TO omni_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA sales TO omni_ro;
