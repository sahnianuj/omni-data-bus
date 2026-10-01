-- Grouping sample (release 2.0, OC-063): a detail table without its parent. order_detail has one
-- row per order line and there's no orders table for it, so one document per order means
-- grouping order_detail by order_id; product_id references sales.products (embedded with OC-064).

CREATE TABLE sales.order_detail (
    type          text    NOT NULL,
    order_id      bigint  NOT NULL,
    line_no       integer NOT NULL,
    product_id    integer NOT NULL REFERENCES sales.products (product_id),
    quantity      integer NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

-- 500 orders with 1–4 lines each.
INSERT INTO sales.order_detail
SELECT 'order_detail', o, l, 1 + (o * 7 + l * 3) % 50, 1 + (o + l) % 5
FROM generate_series(1, 500) AS o, generate_series(1, 4) AS l
WHERE l <= 1 + o % 4;

ANALYZE sales.order_detail;
