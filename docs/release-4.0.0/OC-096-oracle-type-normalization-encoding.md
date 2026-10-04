# OC-096 — Oracle type normalization and value encoding

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 5 | OC-093, OC-095 | Not started |

## User story
As a **data engineer**, I want Oracle column types turned into sensible JSON (dates with their time, big numbers without losing digits, LOBs, JSON columns as JSON), so that my documents are correct without hand-writing a transform for every column.

## Scope
**In:**
- `NormalizeType` for Oracle per [architecture §6](00-architecture.md#6-oracle-type-normalization-jsontype), filling `jsonType`, `dataType`, `udtName` (Oracle type name) and warnings in the snapshot.
- Driver value → neutral encode value conversion (OC-093) for every type in §6, including LOB streaming within the size guard, `TIMESTAMP WITH TIME ZONE` (UTC or keep offset), and `INTERVAL` as an ISO-8601 duration.
- Oracle-specific encoding options in the mapping's type options: `dateAs` (`timestamp` default, `utc`, `date`), `numberAs` (`auto` default, `string`, `number`), `charTrim` (default on).
- `IS JSON` detection: a `CLOB`/`VARCHAR2`/`BLOB` with an `IS JSON` check constraint becomes `any` and is embedded as JSON; an invalid value is dead-lettered with `INVALID_JSON`.
- `''`-is-null: no special handling is needed for reads, but the docs and the field inspector explain why an Oracle string field is never `""`.

**Out:** automap suggestions based on types (OC-098).

## Acceptance criteria & unit tests

### AC1 — Type table
**Given** the seed's `type_matrix` table with one column per row of §6 and edge values (max/min `NUMBER(38)`, `NUMBER` with 20 decimals, `DATE` at midnight and not, all time zone kinds, empty `CLOB`, 25 MB `CLOB`, `BLOB`, `INTERVAL`s, `JSON`, `BOOLEAN`), **when** a mapping with every column is previewed, **then** each value encodes exactly as in the golden file `testdata/oracle_types.golden.json`.

### AC2 — Precision kept
**Given** `NUMBER(38)` value `12345678901234567890123456789012345678`, **then** with `numberAs: auto` it is written as a string and a field warning says why. **Given** `NUMBER(10,2)` `12.30`, **then** it is the JSON number `12.3`.

### AC3 — DATE options
**Given** `DATE '2026-03-01 14:05:09'`, **then** the default writes `"2026-03-01T14:05:09"`; `utc` writes `"2026-03-01T14:05:09Z"`; `date` is refused at validation when a sampled value has a non-zero time (`DATE_HAS_TIME`).

| Test | Location | Verifies |
|---|---|---|
| `TestNormalize_AC1_TypeTable` | `internal/connectors/source/oracle/types_test.go` | AC1 (normalization) |
| `TestEncode_AC1_Golden` | `internal/connectors/source/oracle/encode_integration_test.go` | AC1 (values) |
| `TestEncode_AC2_Precision` | same | AC2 |
| `TestEncode_AC3_DateOptions` | same + `internal/mapping/validate_test.go` | AC3 |
