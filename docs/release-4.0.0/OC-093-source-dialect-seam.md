# OC-093 — Source dialects: take the PostgreSQL assumptions out of shared code

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 5 | OC-091 | Not started |

## User story
As an **engineer**, I want discovery, mapping validation, planning, encoding, export and the headless runner to work with any source connector, so that adding Oracle doesn't spread `if oracle` branches through the code and PostgreSQL keeps behaving exactly as it does today.

## Scope
**In:** the refactor in [architecture D3](00-architecture.md#d3--a-dialect-seam-not-if-oracle-branches):
- `source.Dialect` port, provided by each connector: `ParseLiteral`, `KeyKind`, `ByteOrderedKey`, `QuoteIdent`, sample/timeout strategy, display name for messages.
- `mapping/relvalidate.go`: filter values are validated by the source's dialect; messages say "Oracle value" or "PostgreSQL value" from the descriptor.
- `pipeline/partition.go` and `plan/strategy.go` use `KeyKind`/`ByteOrderedKey` instead of PostgreSQL type names.
- `encode`: neutral value cases (decimal string with scale, time of day, interval, bytes). The PostgreSQL connector converts `pgtype` values into them at the edge, so `encode` no longer imports `pgx`.
- `mappings/preview.go`: driver errors are classified by the connector (no `pgconn` import).
- `export/script.go` and the headless runner build their source descriptions and registry from connector descriptors; `odb run` registers every source in the build.
- `ConnectorPicker`: Oracle moves from "Coming soon" to available when the connector is registered (OC-094 registers it).
- A lint test (like OC-003's tenant lint) fails when a package outside `connectors/source/postgres` imports `pgx`, `pgconn` or `pgtype`, except the metadata-free dev seed.

**Out:** the Oracle connector itself (OC-094 onwards).

## Acceptance criteria & unit tests

### AC1 — PostgreSQL unchanged
**Given** the 2.0 golden mappings (OC-056), **when** they run after the refactor, **then** the documents are byte-identical and the plan parity oracle is unchanged.

### AC2 — No PostgreSQL types in shared code
**Given** the repository, **then** the import lint passes, and `grep -ri postgres` outside the PostgreSQL connector finds only comments, descriptors and docs.

### AC3 — Dialect-driven validation
**Given** a fake dialect that rejects `'abc'` for an integer column, **when** a relation filter uses it, **then** validation reports the fake dialect's message.

| Test | Location | Verifies |
|---|---|---|
| `TestParity_AC3_GoldenMappings` (existing) | `internal/pipeline/plan_parity_integration_test.go` | AC1 |
| `TestImports_NoPostgresOutsideConnector` | `internal/connectors/source/imports_test.go` | AC2 |
| `TestRelValidate_AC3_DialectLiteral` | `internal/mapping/relvalidate_test.go` | AC3 |
