-- Sample source database for local development and tests.
-- Covers every PostgreSQL type in the architecture type-normalization table (§6.4),
-- one table without a primary key, an identifier with a space ("Order Details"),
-- and a read-only role (omni_ro) that deliberately lacks SELECT on schema billing.

CREATE EXTENSION IF NOT EXISTS citext;

CREATE SCHEMA shop;
CREATE SCHEMA billing;

CREATE TYPE shop.order_status AS ENUM ('pending', 'paid', 'shipped', 'cancelled');

CREATE TABLE shop.category (
    category_id   smallint PRIMARY KEY,
    name          varchar(80) NOT NULL,
    code          char(4) NOT NULL
);

CREATE TABLE shop.address (
    address_id    integer PRIMARY KEY,
    street        text NOT NULL,
    city          text NOT NULL,
    postal_code   varchar(16),
    country_code  char(2) NOT NULL,
    location_ip   inet,
    network       cidr,
    device_mac    macaddr
);

CREATE TABLE shop.customer (
    customer_id     integer PRIMARY KEY,
    first_name      text NOT NULL,
    last_name       text NOT NULL,
    email_address   citext NOT NULL UNIQUE,
    phone           varchar(32),
    address_id      integer REFERENCES shop.address (address_id),
    is_active       boolean NOT NULL DEFAULT true,
    tags            text[],
    external_ref    uuid,
    birth_date      date,
    created_on      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamp,
    preferences     jsonb,
    avatar          bytea,
    notes           text
);

CREATE TABLE shop.product (
    product_id      bigint PRIMARY KEY,
    category_id     smallint REFERENCES shop.category (category_id),
    sku             varchar(32) NOT NULL UNIQUE,
    title           text NOT NULL,
    price           numeric(12, 2) NOT NULL,
    list_price      money,
    weight_kg       real,
    rating          double precision,
    precise_cost    numeric(38, 10),
    attributes      json,
    spec_sheet      xml,
    search_vector   tsvector,
    open_from       time,
    open_until      timetz,
    warranty        interval,
    size_matrix     integer[]
);

CREATE TABLE shop.orders (
    order_id        bigint PRIMARY KEY,
    customer_id     integer NOT NULL REFERENCES shop.customer (customer_id),
    status          shop.order_status NOT NULL DEFAULT 'pending',
    order_date      timestamptz NOT NULL,
    total           numeric(12, 2) NOT NULL
);

CREATE TABLE shop."Order Details" (
    order_id        bigint NOT NULL REFERENCES shop.orders (order_id),
    line_no         smallint NOT NULL,
    product_id      bigint NOT NULL REFERENCES shop.product (product_id),
    quantity        integer NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

-- No primary key on purpose: exercises the cursor-read fallback and "no resume" warnings.
CREATE TABLE shop.page_view (
    viewed_at       timestamptz NOT NULL,
    path            text NOT NULL,
    session_id      uuid
);

CREATE TABLE billing.invoice (
    invoice_id      bigint PRIMARY KEY,
    order_id        bigint NOT NULL,
    amount          numeric(12, 2) NOT NULL
);
CREATE TABLE billing.payment (payment_id bigint PRIMARY KEY, invoice_id bigint NOT NULL, paid_at timestamptz);
CREATE TABLE billing.refund (refund_id bigint PRIMARY KEY, payment_id bigint NOT NULL, amount numeric(12, 2));

-- ---------------------------------------------------------------- data
INSERT INTO shop.category VALUES (1, 'Books', 'BOOK'), (2, 'Games', 'GAME'), (3, 'Tools', 'TOOL');

INSERT INTO shop.address
SELECT g, g || ' Market Street', (ARRAY['San Jose','Austin','Bangalore','London'])[1 + g % 4],
       lpad((10000 + g)::text, 5, '0'), (ARRAY['US','US','IN','GB'])[1 + g % 4],
       ('10.0.' || (g % 255) || '.' || (g % 200))::inet, '10.0.0.0/16'::cidr, '08:00:2b:01:02:03'::macaddr
FROM generate_series(1, 400) AS g;

INSERT INTO shop.customer
SELECT g,
       (ARRAY['Ava','Liam','Mia','Noah','Zoe','Arjun','Priya','Omar'])[1 + g % 8],
       (ARRAY['Patel','Smith','Garcia','Chen','Kumar','Brown'])[1 + g % 6],
       'customer' || g || '@example.com',
       CASE WHEN g % 5 = 0 THEN NULL ELSE '+1-555-' || lpad(g::text, 4, '0') END,
       1 + g % 400,
       g % 7 <> 0,
       ARRAY['tier-' || (g % 3), 'region-' || (g % 4)],
       md5(g::text)::uuid,
       date '1970-01-01' + (g * 37 % 18000),
       timestamptz '2020-01-01 00:00:00+00' + (g || ' hours')::interval,
       timestamp '2024-06-01 12:00:00' + (g || ' minutes')::interval,
       jsonb_build_object('newsletter', g % 2 = 0, 'language', (ARRAY['en','es','hi'])[1 + g % 3]),
       CASE WHEN g % 10 = 0 THEN decode('89504e470d0a1a0a', 'hex') END,
       CASE WHEN g % 4 = 0 THEN 'Prefers email contact' END
FROM generate_series(1, 2000) AS g;

INSERT INTO shop.product
SELECT g, 1 + g % 3, 'SKU-' || lpad(g::text, 6, '0'), 'Product ' || g,
       round((5 + g % 200 + (g % 100) / 100.0)::numeric, 2), (5 + g % 200)::numeric::money,
       (g % 50) / 10.0, (g % 50) / 10.0 + 0.25, (g * 1.0000000001)::numeric(38, 10),
       json_build_object('color', (ARRAY['red','blue','green'])[1 + g % 3]),
       ('<spec><id>' || g || '</id></spec>')::xml,
       to_tsvector('english', 'Product ' || g || ' description'),
       time '09:00', timetz '17:30+05:30', ((g % 3) || ' years 6 months')::interval,
       ARRAY[g % 10, g % 20, g % 30]
FROM generate_series(1, 500) AS g;

INSERT INTO shop.orders
SELECT g, 1 + g % 2000, (ARRAY['pending','paid','shipped','cancelled'])[1 + g % 4]::shop.order_status,
       timestamptz '2023-01-01 00:00:00+00' + (g || ' minutes')::interval,
       round((10 + g % 500 + (g % 100) / 100.0)::numeric, 2)
FROM generate_series(1, 10000) AS g;

INSERT INTO shop."Order Details"
SELECT o, l, 1 + (o * l) % 500, 1 + (o + l) % 5
FROM generate_series(1, 10000) AS o, generate_series(1, 3) AS l;

INSERT INTO shop.page_view
SELECT timestamptz '2024-01-01 00:00:00+00' + (g || ' seconds')::interval, '/products/' || (g % 500), md5((g % 900)::text)::uuid
FROM generate_series(1, 5000) AS g;

INSERT INTO billing.invoice SELECT g, g, 10 + g % 90 FROM generate_series(1, 500) AS g;

ANALYZE;

-- ---------------------------------------------------------------- roles
-- Read-only role for Omni-Data-Bus. It can read schema shop but NOT schema billing,
-- so "Test connection" shows the privileges warning with a GRANT hint.
CREATE ROLE omni_ro LOGIN PASSWORD 'omni_ro';
GRANT CONNECT ON DATABASE shop TO omni_ro;
GRANT USAGE ON SCHEMA shop TO omni_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA shop TO omni_ro;
GRANT USAGE ON SCHEMA billing TO omni_ro;
