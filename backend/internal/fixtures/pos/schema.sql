-- Point-of-sale fixture (release 2.5, OC-162): the source behind the design-first mapping
-- stories. A cart document embeds its customer, partner, items (each with modifiers and applied
-- rewards), pricing, saved-order state and payments; each payment is one of five tender tables
-- (an exclusive arc, OC-171). Generic names, synthetic rows; deterministic, so tests can assert
-- on values. Loaded by the dev stack on first start, by `make dev-seed`, and by the integration
-- tests (fixtures/pos.Apply), which skip it when pos.carts already exists.

-- Re-runnable: fixtures.Apply reloads when a schema is missing (it drops what's there first).
DROP SCHEMA IF EXISTS pos, crm, support CASCADE;
CREATE SCHEMA pos;

CREATE TYPE pos.cart_status AS ENUM ('open', 'payment_pending', 'closed', 'abandoned', 'voided', 'queued');
CREATE TYPE pos.collection_type AS ENUM ('IN_STORE', 'DRIVE_THRU', 'CURBSIDE', 'OUTDOOR');
CREATE TYPE pos.consumption_type AS ENUM ('ConsumeInStore', 'ConsumeOutOfStore');
CREATE TYPE pos.entry_method AS ENUM ('msrSwipe', 'manualEntry', 'swipe', 'scanned');

CREATE TABLE pos.stores (
    number      text PRIMARY KEY,
    name        text NOT NULL,
    region      text
);

CREATE TABLE pos.encryption_keys (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pos.partners (
    number          text PRIMARY KEY,
    partner_id      uuid NOT NULL,
    store_number    text REFERENCES pos.stores (number),
    first_name      text NOT NULL,
    last_name       text NOT NULL,
    collection_type pos.collection_type NOT NULL DEFAULT 'IN_STORE',
    register_number integer,
    is_deleted      boolean DEFAULT false,
    CONSTRAINT partners_number_check CHECK (length(number) >= 6)
);

CREATE TABLE pos.carts (
    id                     uuid PRIMARY KEY,
    partner_number         text NOT NULL REFERENCES pos.partners (number),
    store_number           text NOT NULL REFERENCES pos.stores (number) ON DELETE CASCADE,
    total_in_cents         integer NOT NULL,
    total_to_display       numeric(8,2) NOT NULL,
    consumption_type       pos.consumption_type NOT NULL,
    order_notes            text NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    register_number        integer,
    receipt_number         integer,
    discount_in_cents      integer NOT NULL DEFAULT 0,
    subtotal_in_cents      integer NOT NULL DEFAULT 0,
    tax_in_cents           integer NOT NULL DEFAULT 0,
    last_priced_at         timestamptz,
    status                 pos.cart_status NOT NULL DEFAULT 'open',
    closed_at              timestamptz,
    voided_at              timestamptz,
    tax_data               jsonb,
    channel                varchar(20) NOT NULL DEFAULT 'cafe',
    skip_fulfillment       boolean NOT NULL DEFAULT false,
    loyalty_transaction_id text,
    submit_after_tip_abort boolean NOT NULL DEFAULT false,
    CONSTRAINT check_closed_cart_timestamps CHECK ((status = 'closed' AND closed_at IS NOT NULL AND voided_at IS NULL) OR status <> 'closed'),
    CONSTRAINT check_open_cart_timestamps CHECK ((status = 'open' AND closed_at IS NULL AND voided_at IS NULL) OR status <> 'open'),
    CONSTRAINT check_voided_cart_timestamps CHECK ((status = 'voided' AND closed_at IS NOT NULL AND voided_at IS NOT NULL) OR status <> 'voided'),
    CONSTRAINT chk_skip_fulfillment_drive_thru_only CHECK (skip_fulfillment = false OR channel = 'drive_thru')
);

CREATE TABLE pos.applied_rewards (
    id          uuid PRIMARY KEY,
    cart_id     uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    reward_code text NOT NULL,
    description text
);

CREATE TABLE pos.cart_items (
    id                         uuid PRIMARY KEY,
    cart_id                    uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    ask_me_instructions        text,
    base_price                 numeric(8,2) NOT NULL,
    display_index              integer NOT NULL,
    sequence_number            integer NOT NULL DEFAULT 1,
    is_refund                  boolean,
    is_available               boolean,
    is_impulse_item            boolean,
    is_refill                  boolean,
    image_url                  text,
    item_availability_override boolean,
    name                       text NOT NULL,
    price                      numeric(8,2) NOT NULL,
    price_override             numeric(8,2),
    redemption_tier            text,
    sellable_item_id           integer NOT NULL,
    serving_size_name          text NOT NULL,
    applied_reward_id          uuid REFERENCES pos.applied_rewards (id) ON DELETE SET NULL,
    menu_product_id            integer,
    form_code                  text NOT NULL DEFAULT '',
    size_code                  text,
    product_type_id            integer NOT NULL,
    voided                     boolean NOT NULL DEFAULT false,
    price_override_authorized  boolean NOT NULL DEFAULT false,
    discount_details           jsonb
);
CREATE INDEX ON pos.cart_items (cart_id);

CREATE TABLE pos.customizations (
    id                         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_item_id               uuid NOT NULL REFERENCES pos.cart_items (id) ON DELETE CASCADE,
    sequence_number            integer NOT NULL DEFAULT 1,
    item_availability_override boolean,
    name                       text NOT NULL,
    price                      numeric(8,2) NOT NULL,
    price_override             numeric(8,2),
    quantity                   integer NOT NULL,
    redemption_tier            text,
    sellable_item_id           integer NOT NULL,
    menu_product_id            integer,
    form_code                  text NOT NULL DEFAULT '',
    size_code                  text NOT NULL DEFAULT '',
    is_available               boolean,
    voided                     boolean NOT NULL DEFAULT false
);
CREATE INDEX ON pos.customizations (cart_item_id);

-- Not unique per cart: a cart may carry more than one customer row (the design takes the first).
CREATE TABLE pos.customers (
    id                uuid PRIMARY KEY,
    cart_id           uuid REFERENCES pos.carts (id) ON DELETE CASCADE,
    first_name        text,
    last_name         text,
    stars_earned      double precision,
    loyalty_id        text,
    alias             text,
    valid_partner     boolean NOT NULL DEFAULT false,
    partner_number    text,
    partner_name      text,
    partner_status    text,
    eligible_benefits text[],
    tier_number       integer
);
CREATE INDEX ON pos.customers (cart_id);

-- One-to-one with carts: the foreign key is the primary key.
CREATE TABLE pos.saved_orders (
    cart_id               uuid PRIMARY KEY REFERENCES pos.carts (id) ON DELETE CASCADE,
    order_collection_type pos.collection_type NOT NULL DEFAULT 'IN_STORE',
    saved_at              timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pos.svcs (
    id                   uuid PRIMARY KEY,
    cart_id              uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    encryption_key_id    uuid NOT NULL REFERENCES pos.encryption_keys (id) ON DELETE RESTRICT,
    encrypted_svc_number text NOT NULL,
    svc_number_hash      text NOT NULL,
    is_default           boolean NOT NULL DEFAULT false,
    account_id           uuid,
    last_four_digits     varchar(4) NOT NULL,
    card_image           text NOT NULL,
    entry_method         pos.entry_method NOT NULL DEFAULT 'manualEntry',
    balance_in_cents     integer NOT NULL DEFAULT 0,
    authorization_code   text,
    loyalty_id           text,
    CONSTRAINT svcs_last_four_digits_check CHECK (char_length(last_four_digits) = 4)
);

CREATE TABLE pos.sccs (
    id                   uuid PRIMARY KEY,
    cart_id              uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    encryption_key_id    uuid NOT NULL REFERENCES pos.encryption_keys (id) ON DELETE RESTRICT,
    encrypted_scc_number text NOT NULL,
    scc_hash             text NOT NULL,
    card_image           text NOT NULL,
    entry_method         pos.entry_method NOT NULL DEFAULT 'manualEntry'
);

CREATE TABLE pos.credit_cards (
    id                       uuid PRIMARY KEY,
    cart_id                  uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    card_brand               text NOT NULL,
    merchant_id              text NOT NULL,
    auth_code                text NOT NULL,
    presentation_method      text NOT NULL,
    processor_transaction_id text NOT NULL,
    masked_account_number    text NOT NULL,
    account_last_four_digits text,
    card_holder_name         text
);

CREATE TABLE pos.cash_tenders (
    id              uuid PRIMARY KEY,
    cart_id         uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    amount_in_cents integer NOT NULL,
    change_in_cents integer NOT NULL DEFAULT 0,
    currency_iso    text NOT NULL DEFAULT 'USD'
);

CREATE TABLE pos.scan_and_pay_qrs (
    id                uuid PRIMARY KEY,
    cart_id           uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    encryption_key_id uuid NOT NULL REFERENCES pos.encryption_keys (id) ON DELETE RESTRICT,
    qr_code           text NOT NULL,
    account_id        uuid
);

-- One-to-one with carts. Exactly one of the two keys is set (num_nonnulls form, OC-171).
CREATE TABLE pos.associated_scans (
    cart_id                 uuid PRIMARY KEY REFERENCES pos.carts (id) ON DELETE CASCADE,
    svc_id                  uuid REFERENCES pos.svcs (id) ON DELETE RESTRICT,
    scan_and_pay_qr_code_id uuid REFERENCES pos.scan_and_pay_qrs (id) ON DELETE RESTRICT,
    CONSTRAINT associated_scans_check CHECK (num_nonnulls(svc_id, scan_and_pay_qr_code_id) = 1)
);

-- One row per payment of a cart. Exactly one tender key is set (the CASE form, OC-171).
CREATE TABLE pos.collected_payments (
    cart_id                 uuid NOT NULL REFERENCES pos.carts (id) ON DELETE CASCADE,
    cash_tender_id          uuid REFERENCES pos.cash_tenders (id) ON DELETE RESTRICT,
    credit_card_id          uuid REFERENCES pos.credit_cards (id) ON DELETE RESTRICT,
    svc_id                  uuid REFERENCES pos.svcs (id) ON DELETE RESTRICT,
    scan_and_pay_qr_code_id uuid REFERENCES pos.scan_and_pay_qrs (id) ON DELETE RESTRICT,
    scc_id                  uuid REFERENCES pos.sccs (id) ON DELETE RESTRICT,
    charged_amount_in_cents integer NOT NULL,
    tip_in_cents            integer NOT NULL,
    transaction_id          uuid,
    voided_at               timestamptz,
    created_at              timestamptz NOT NULL,
    CONSTRAINT collected_payments_check CHECK ((
        CASE WHEN cash_tender_id IS NOT NULL THEN 1 ELSE 0 END +
        CASE WHEN credit_card_id IS NOT NULL THEN 1 ELSE 0 END +
        CASE WHEN scan_and_pay_qr_code_id IS NOT NULL THEN 1 ELSE 0 END +
        CASE WHEN svc_id IS NOT NULL THEN 1 ELSE 0 END +
        CASE WHEN scc_id IS NOT NULL THEN 1 ELSE 0 END) = 1)
);
CREATE INDEX ON pos.collected_payments (cart_id);

-- ── Rows ─────────────────────────────────────────────────────────────────────
-- Ids are md5-derived UUIDs ('cart' || n), so every run seeds the same keys.
CREATE FUNCTION pos.uid(kind text, n integer) RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT md5(kind || n)::uuid $$;

INSERT INTO pos.stores SELECT lpad(s::text, 5, '1'), 'Store ' || s, (ARRAY['north', 'south', 'east', 'west'])[1 + s % 4] FROM generate_series(1, 5) AS s;
INSERT INTO pos.encryption_keys SELECT pos.uid('key', k), timestamptz '2026-01-01 00:00Z' FROM generate_series(1, 3) AS k;
INSERT INTO pos.partners
SELECT lpad((1000000 + p)::text, 7, '0'), pos.uid('partner', p), lpad((1 + p % 5)::text, 5, '1'),
       (ARRAY['Ana', 'Ben', 'Chloe', 'Dev', 'Eli', 'Fay', 'Gus', 'Hana'])[1 + p % 8],
       (ARRAY['Lopez', 'Ng', 'Okafor', 'Patel', 'Quinn', 'Ross'])[1 + p % 6],
       (ARRAY['IN_STORE', 'DRIVE_THRU', 'CURBSIDE', 'OUTDOOR'])[1 + p % 4]::pos.collection_type, 40 + p % 4, false
FROM generate_series(1, 12) AS p;

-- 200 carts: mostly closed, some open (saved or not), payment pending, voided or abandoned.
INSERT INTO pos.carts (id, partner_number, store_number, total_in_cents, total_to_display, consumption_type, order_notes, created_at,
                       register_number, receipt_number, discount_in_cents, subtotal_in_cents, tax_in_cents, last_priced_at, status,
                       closed_at, voided_at, tax_data, channel, skip_fulfillment, loyalty_transaction_id, submit_after_tip_abort)
SELECT pos.uid('cart', c), lpad((1000000 + 1 + c % 12)::text, 7, '0'), lpad((1 + (1 + c % 12) % 5)::text, 5, '1'),
       0, 0, (ARRAY['ConsumeInStore', 'ConsumeOutOfStore'])[1 + c % 2]::pos.consumption_type,
       CASE WHEN c % 7 = 0 THEN 'Extra hot, please' ELSE '' END,
       timestamptz '2026-07-01 08:00Z' + c * interval '37 minutes', 40 + c % 4, CASE WHEN st IN ('closed', 'voided') THEN 5000 + c END,
       0, 0, 0, timestamptz '2026-07-01 08:05Z' + c * interval '37 minutes', st,
       CASE WHEN st IN ('closed', 'voided') THEN timestamptz '2026-07-01 08:20Z' + c * interval '37 minutes' END,
       CASE WHEN st = 'voided' THEN timestamptz '2026-07-01 08:25Z' + c * interval '37 minutes' END,
       jsonb_build_object('rate', 0.0825), CASE WHEN c % 5 = 0 THEN 'drive_thru' ELSE 'cafe' END, c % 25 = 0, CASE WHEN c % 3 = 0 THEN 'lt-' || c END, false
FROM generate_series(1, 200) AS c,
     LATERAL (SELECT (CASE WHEN c % 10 IN (1, 2) THEN 'open' WHEN c % 10 = 3 THEN 'payment_pending' WHEN c % 29 = 0 THEN 'voided'
                           WHEN c % 31 = 0 THEN 'abandoned' ELSE 'closed' END)::pos.cart_status AS st) s;

-- A reward on every fourth cart; the first item of that cart uses it.
INSERT INTO pos.applied_rewards SELECT pos.uid('reward', c), pos.uid('cart', c), 'STARS-' || (25 * (1 + c % 4)), (25 * (1 + c % 4)) || ' Stars Reward'
FROM generate_series(1, 200) AS c WHERE c % 4 = 0;

-- 1 to 4 items per cart; prices in dollars (the design wants cents).
INSERT INTO pos.cart_items (id, cart_id, base_price, display_index, sequence_number, is_refund, is_available, is_impulse_item, is_refill,
                            image_url, name, price, price_override, sellable_item_id, serving_size_name, applied_reward_id, menu_product_id,
                            form_code, size_code, product_type_id, voided, price_override_authorized)
SELECT pos.uid('item', c * 10 + i), pos.uid('cart', c), p.base, i - 1, i, false, true, i = 4, false,
       'https://img.example/items/' || (5000 + (c + i) % 9) || '.png', p.name, p.base, NULL, 5000 + (c + i) % 9, p.size,
       CASE WHEN i = 1 AND c % 4 = 0 THEN pos.uid('reward', c) END, 100 + (c + i) % 9, 'HOT', p.code, 1 + (c + i) % 3, false, false
FROM generate_series(1, 200) AS c, generate_series(1, 4) AS i,
     LATERAL (SELECT (ARRAY['Latte', 'Mocha', 'Drip Coffee', 'Tea', 'Croissant', 'Muffin', 'Bagel', 'Cold Brew', 'Juice'])[1 + (c + i) % 9] AS name,
                     (ARRAY[4.95, 5.25, 2.45, 2.95, 3.25, 2.75, 2.50, 4.45, 3.95])[1 + (c + i) % 9]::numeric(8,2) AS base,
                     (ARRAY['Tall', 'Grande', 'Venti'])[1 + (c + i) % 3] AS size, (ARRAY['TL', 'GR', 'VT'])[1 + (c + i) % 3] AS code) p
WHERE i <= 1 + c % 4;

-- 0 to 2 modifiers per item.
INSERT INTO pos.customizations (id, cart_item_id, sequence_number, name, price, quantity, sellable_item_id, form_code, size_code, is_available, voided)
SELECT pos.uid('mod', (c * 10 + i) * 10 + m), pos.uid('item', c * 10 + i), m,
       (ARRAY['Oat Milk', 'Extra Shot', 'Vanilla', 'Whipped Cream', 'Decaf'])[1 + (c + i + m) % 5],
       (ARRAY[0.70, 0.90, 0.60, 0.00, 0.00])[1 + (c + i + m) % 5]::numeric(8,2), 1, 15000 + (c + i + m) % 5, 'ADD', 'STD', true, false
FROM generate_series(1, 200) AS c, generate_series(1, 4) AS i, generate_series(1, 2) AS m
WHERE i <= 1 + c % 4 AND m <= (c + i) % 3;

-- Totals from the items and modifiers.
UPDATE pos.carts ct SET subtotal_in_cents = t.sub, discount_in_cents = CASE WHEN r.id IS NOT NULL THEN 100 ELSE 0 END,
       tax_in_cents = round(t.sub * 0.0825)::int,
       total_in_cents = t.sub + round(t.sub * 0.0825)::int - CASE WHEN r.id IS NOT NULL THEN 100 ELSE 0 END,
       total_to_display = ((t.sub + round(t.sub * 0.0825)::int - CASE WHEN r.id IS NOT NULL THEN 100 ELSE 0 END) / 100.0)::numeric(8,2)
FROM (SELECT i.cart_id, (round(sum(i.price) * 100) + coalesce(round(sum(m.price) * 100), 0))::int AS sub
      FROM pos.cart_items i LEFT JOIN (SELECT cart_item_id, sum(price) AS price FROM pos.customizations GROUP BY cart_item_id) m ON m.cart_item_id = i.id
      GROUP BY i.cart_id) t
LEFT JOIN pos.applied_rewards r ON r.cart_id = t.cart_id
WHERE ct.id = t.cart_id;

-- Customers on two carts in three; every 17th cart has a second customer row.
INSERT INTO pos.customers
SELECT pos.uid('customer', c * 2 + k), pos.uid('cart', c), (ARRAY['John', 'Mia', 'Omar', 'Lea', 'Sam'])[1 + c % 5], (ARRAY['Doe', 'Kim', 'Ali', 'Roy'])[1 + c % 4],
       (c % 6) * 5, 'loyalty-' || c, NULL, c % 9 = 0, CASE WHEN c % 9 = 0 THEN lpad((1000000 + 1 + c % 12)::text, 7, '0') END, NULL, NULL,
       CASE WHEN c % 2 = 0 THEN ARRAY['FREE_REFILL'] ELSE ARRAY[]::text[] END, CASE WHEN c % 5 = 0 THEN 2 END
FROM generate_series(1, 200) AS c, generate_series(0, 1) AS k
WHERE c % 3 <> 0 AND (k = 0 OR c % 17 = 0);

-- Open carts ending in 1 are saved orders.
INSERT INTO pos.saved_orders SELECT id, (ARRAY['IN_STORE', 'DRIVE_THRU', 'CURBSIDE', 'OUTDOOR'])[1 + extract(minute FROM created_at)::int % 4]::pos.collection_type, created_at + interval '3 minutes'
FROM pos.carts WHERE status = 'open' AND (extract(epoch FROM created_at - timestamptz '2026-07-01 08:00Z') / 2220)::int % 10 = 1;

-- Payments: one per closed or voided cart, two on every eighth; the tender is chosen by cart.
INSERT INTO pos.sccs SELECT pos.uid('scc', c), pos.uid('cart', c), pos.uid('key', 1 + c % 3), 'enc-scc-' || c, 'sha256-scc-' || c,
       'https://img.example/cards/scc-' || (c % 3) || '.png', (ARRAY['scanned', 'manualEntry'])[1 + c % 2]::pos.entry_method
FROM generate_series(1, 200) AS c WHERE c % 5 = 0;
INSERT INTO pos.svcs SELECT pos.uid('svc', c), pos.uid('cart', c), pos.uid('key', 1 + c % 3), 'enc-svc-' || c, 'sha256-svc-' || c, c % 2 = 0,
       pos.uid('account', c % 40), lpad((1000 + c)::text, 4, '0'), 'https://img.example/cards/svc-' || (c % 3) || '.png',
       'swipe', 2500 + c, 'AUTH' || c, 'loyalty-' || c
FROM generate_series(1, 200) AS c WHERE c % 5 = 1 OR c % 50 = 0;
INSERT INTO pos.credit_cards SELECT pos.uid('cc', c), pos.uid('cart', c), (ARRAY['VISA', 'MC', 'AMEX'])[1 + c % 3], 'm-001', 'A' || c, 'Contactless',
       'ptx-' || c, '************' || lpad((4000 + c)::text, 4, '0'), lpad((4000 + c)::text, 4, '0'), 'Card Holder ' || c
FROM generate_series(1, 200) AS c WHERE c % 5 = 2 OR c % 8 = 0;
INSERT INTO pos.cash_tenders SELECT pos.uid('cash', c), pos.uid('cart', c), 2000, 0, 'USD' FROM generate_series(1, 200) AS c WHERE c % 5 = 3;
INSERT INTO pos.scan_and_pay_qrs SELECT pos.uid('qr', c), pos.uid('cart', c), pos.uid('key', 1 + c % 3), 'qr-' || c, pos.uid('account', c % 40)
FROM generate_series(1, 200) AS c WHERE c % 5 = 4 OR c % 50 = 0;

INSERT INTO pos.collected_payments
SELECT ct.id,
       CASE WHEN c % 5 = 3 THEN pos.uid('cash', c) END, CASE WHEN c % 5 = 2 THEN pos.uid('cc', c) END, CASE WHEN c % 5 = 1 THEN pos.uid('svc', c) END,
       CASE WHEN c % 5 = 4 THEN pos.uid('qr', c) END, CASE WHEN c % 5 = 0 THEN pos.uid('scc', c) END,
       ct.total_in_cents - CASE WHEN c % 8 = 0 THEN 500 ELSE 0 END, CASE WHEN c % 4 = 0 THEN 100 ELSE 0 END, pos.uid('txn', c),
       CASE WHEN ct.status = 'voided' THEN ct.voided_at END, ct.created_at + interval '15 minutes'
FROM generate_series(1, 200) AS c JOIN pos.carts ct ON ct.id = pos.uid('cart', c)
WHERE ct.status IN ('closed', 'voided');
-- The second payment of every eighth cart is by credit card.
INSERT INTO pos.collected_payments (cart_id, credit_card_id, charged_amount_in_cents, tip_in_cents, transaction_id, created_at)
SELECT ct.id, pos.uid('cc', c), 500, 0, pos.uid('txn2', c), ct.created_at + interval '16 minutes'
FROM generate_series(1, 200) AS c JOIN pos.carts ct ON ct.id = pos.uid('cart', c)
WHERE ct.status IN ('closed', 'voided') AND c % 8 = 0 AND c % 5 <> 2;

-- A scan on some open carts.
INSERT INTO pos.associated_scans SELECT ct.id, CASE WHEN c % 50 = 0 THEN pos.uid('svc', c) END, CASE WHEN c % 50 <> 0 THEN pos.uid('qr', c) END
FROM generate_series(1, 200) AS c JOIN pos.carts ct ON ct.id = pos.uid('cart', c)
WHERE c % 50 = 0 OR (c % 5 = 4 AND ct.status <> 'closed');

ANALYZE pos.carts, pos.cart_items, pos.customizations, pos.customers, pos.collected_payments;

-- ── Variant shapes by type column (OC-171 pattern B): crm ───────────────────
CREATE SCHEMA crm;
CREATE TABLE crm.customers (id integer PRIMARY KEY, name text NOT NULL);
CREATE TABLE crm.contact_methods (
    id          integer PRIMARY KEY,
    customer_id integer NOT NULL REFERENCES crm.customers (id),
    kind        text NOT NULL,
    value       text,
    created_at  timestamptz NOT NULL
);
-- The postal kind's detail, one-to-one with its contact method.
CREATE TABLE crm.postal_addresses (
    contact_id integer PRIMARY KEY REFERENCES crm.contact_methods (id),
    street     text NOT NULL,
    city       text NOT NULL,
    zip        text NOT NULL
);
INSERT INTO crm.customers SELECT c, 'Customer ' || c FROM generate_series(1, 30) AS c;
INSERT INTO crm.contact_methods
SELECT c * 10 + m, c, (ARRAY['email', 'phone', 'postal'])[1 + (c + m) % 3],
       CASE (c + m) % 3 WHEN 0 THEN 'c' || c || '@example.com' WHEN 1 THEN '+1 555 01' || lpad((c * 10 + m)::text, 2, '0') END,
       timestamptz '2026-01-01 00:00Z' + (c * 10 + m) * interval '1 hour'
FROM generate_series(1, 30) AS c, generate_series(1, 3) AS m WHERE m <= 1 + c % 3;
-- One kind no design has.
INSERT INTO crm.contact_methods VALUES (9999, 7, 'fax', '+1 555 0999', timestamptz '2026-01-02 00:00Z');
INSERT INTO crm.postal_addresses SELECT id, id || ' Main St', (ARRAY['Lyon', 'Nice', 'Pau'])[1 + id % 3], lpad((id * 7 % 100000)::text, 5, '0')
FROM crm.contact_methods WHERE kind = 'postal';

-- ── Variant shapes as a union of tables (OC-171 pattern C): support ─────────
CREATE SCHEMA support;
CREATE TABLE support.tickets (id integer PRIMARY KEY, title text NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE support.comments (id integer PRIMARY KEY, ticket_id integer NOT NULL REFERENCES support.tickets (id), body text NOT NULL, author text NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE support.status_changes (id integer PRIMARY KEY, ticket_id integer NOT NULL REFERENCES support.tickets (id), from_status text NOT NULL, to_status text NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE support.attachments (id integer PRIMARY KEY, ticket_id integer NOT NULL REFERENCES support.tickets (id), file_name text NOT NULL, size_bytes integer NOT NULL, created_at timestamptz NOT NULL);
INSERT INTO support.tickets SELECT t, 'Ticket ' || t, timestamptz '2026-03-01 09:00Z' + t * interval '1 day' FROM generate_series(1, 20) AS t;
INSERT INTO support.comments SELECT t * 10 + k, t, 'Comment ' || k || ' on ' || t, (ARRAY['ana', 'ben'])[1 + k % 2], timestamptz '2026-03-01 09:00Z' + t * interval '1 day' + (k * 3) * interval '1 minute'
FROM generate_series(1, 20) AS t, generate_series(1, 2) AS k;
INSERT INTO support.status_changes SELECT t * 10 + k, t, (ARRAY['new', 'open'])[k], (ARRAY['open', 'closed'])[k], timestamptz '2026-03-01 09:00Z' + t * interval '1 day' + (k * 3 + 1) * interval '1 minute'
FROM generate_series(1, 20) AS t, generate_series(1, 2) AS k WHERE t % 2 = 0 OR k = 1;
INSERT INTO support.attachments SELECT t * 10, t, 'screenshot-' || t || '.png', 1000 * t, timestamptz '2026-03-01 09:00Z' + t * interval '1 day' + 2 * interval '1 minute'
FROM generate_series(1, 20) AS t WHERE t % 3 = 0;

-- The dev stack's read-only role reads the fixtures too (it deliberately lacks only billing).
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'omni_ro') THEN
    GRANT USAGE ON SCHEMA pos, crm, support TO omni_ro;
    GRANT SELECT ON ALL TABLES IN SCHEMA pos, crm, support TO omni_ro;
  END IF;
END $$;

