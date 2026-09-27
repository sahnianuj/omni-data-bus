-- A 1,000,000-row table for streaming, progress-bar and resume tests.
CREATE TABLE shop.big_events (
    event_id    bigint PRIMARY KEY,
    kind        text NOT NULL,
    occurred_at timestamptz NOT NULL,
    payload     jsonb
);

INSERT INTO shop.big_events
SELECT g,
       (ARRAY['click', 'view', 'purchase', 'signup'])[1 + g % 4],
       timestamptz '2024-01-01 00:00:00+00' + (g || ' seconds')::interval,
       jsonb_build_object('n', g, 'even', g % 2 = 0)
FROM generate_series(1, 1000000) AS g;

ANALYZE shop.big_events;
GRANT SELECT ON shop.big_events TO omni_ro;
