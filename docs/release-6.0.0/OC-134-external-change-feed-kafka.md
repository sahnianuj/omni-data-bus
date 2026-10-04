# OC-134 — External change feed: Debezium or GoldenGate through Kafka

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P2 | 5 | OC-119 | Not started |

## User story
As a **data engineer whose company already runs Debezium or Oracle GoldenGate**, I want the tool to consume those change topics instead of mining itself, so that we reuse our approved CDC platform and still get the tool's document building.

## Scope
**In:**
- A `ChangeStream` implementation that reads Kafka topics (pure-Go client, for example `franz-go`) with Debezium's JSON or Avro envelope (schema registry optional), or GoldenGate for Big Data's JSON format, for PostgreSQL or Oracle sources.
- Config: brokers, TLS/SASL, topic per table (pattern), consumer group, format. Positions are Kafka offsets per partition; ack = commit offsets after the low watermark.
- Handoff: the initial load uses the feed's snapshot marker when present, or the source SCN/LSN recorded in the events, with the documented caveat that exactness depends on the feed.
- Keys from the envelope's key and `before`/`after` images (keys and join columns only; values re-read as usual).

**Out:** producing to Kafka.

## Acceptance criteria & unit tests

### AC1 — Debezium feed
**Given** a Debezium PostgreSQL connector writing to a Kafka container, **then** the replication fed from it passes the convergence check.

| Test | Location | Verifies |
|---|---|---|
| `TestKafkaFeed_AC1_Debezium` | `internal/replication/feeds/kafka_integration_test.go` | AC1 |
