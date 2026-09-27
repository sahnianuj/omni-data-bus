# OC-016 — Document key & metadata strategy

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 3 | OC-015 | Implemented |

## User story
As a **data engineer**, I want to define how each document's key is built from source columns and whether a type discriminator field is added, so that keys are unique, predictable, and follow Couchbase conventions (e.g., `customer::42`).

## Implementation details

### Key template (`internal/mapping/keytemplate.go`)
- **Syntax:** literal text plus `{column}` tokens plus optional `{column|fn}` filters. The filters are `lower`, `upper`, `trim`, `pad:N` (left zero-pad), and `hash` (sha1 hex of the value, for long composite values). Special tokens are `{$table}` and `{$uuid}` (UUID v7, only when there is no PK; not idempotent).
- **Default:** `<table>::{pk1}` or `<table>::{pk1}::{pk2}` for a composite PK. For a table without a PK, the default is `{$uuid}`, with the warning `NON_IDEMPOTENT_KEY` ("Re-running will create duplicates; choose unique columns").
- **Compile once** into `[]segment` and evaluate per row, using `strings.Builder` with a preallocated capacity.
- **Validation:**
  - Every token references an existing column.
  - The rendered key is ≤ 250 bytes (Couchbase limit), checked at runtime per row. The designer checks a worst-case estimate for varchar(n).
  - Unique-safety check: the set of referenced columns must be a superset of the PK or of a unique constraint. Otherwise the warning `KEY_MAY_COLLIDE` is raised.
  - NULL in a key column at runtime → per-row error `KEY_NULL_COMPONENT` (dead-lettered, OC-026).

### Type field & metadata
- `typeField: {enabled, name:"type", value:"customer"}` adds a constant string field to every document. It is enabled by default, with the value set to the singular snake_case of the table name. The name must not collide with a designed field (validation error).
- `nullHandling: include | omit` applies to all fields; per-field overrides are allowed in OC-020.
- Optional `expirySec` (document TTL). If it exceeds the collection's maxTTL, the server clamps it, and a warning is shown.

### UI
The *Key & metadata* panel sits at the top of the designer:
- A key template input with token autocomplete: typing `{` opens a column list, and chips render tokens.
- A live preview of the rendered keys for the first 3 preview rows, e.g. `customer::1`, `customer::2`, `customer::3`.
- Badges for *Unique ✓* / *May collide ⚠* / *Not idempotent ⚠*.
- The type field toggle with name and value inputs.
- The null handling radio.
- Document expiry.

## Acceptance criteria & unit tests

### AC1 — Template parsing & rendering
**Given** `order::{customer_id}::{order_no|pad:8}` and a row `{customer_id: 42, order_no: 17}`, **then** it renders `order::42::00000017`. Braces can be escaped as `{{` and `}}`. An unknown filter or an unclosed brace → a parse error with its position.

| Test | Location | Verifies |
|---|---|---|
| `TestParseKeyTemplate_AC1_TokensFiltersLiterals` | `internal/mapping/keytemplate_test.go` | |
| `TestRenderKey_AC1_Values` | `internal/mapping/keytemplate_test.go` | int, string, uuid, timestamp (ISO), bool |
| `TestParseKeyTemplate_AC1_Errors` | `internal/mapping/keytemplate_test.go` | Includes positions |
| `FuzzParseKeyTemplate` | `internal/mapping/keytemplate_test.go` | Go fuzz target; no panics |

### AC2 — Sensible defaults
**Given** a single-column PK table → `customer::{customer_id}`. **Given** a composite PK → `order_item::{order_id}::{line_no}`. **Given** no PK → `{$uuid}` plus the `NON_IDEMPOTENT_KEY` warning.

| Test | Location | Verifies |
|---|---|---|
| `TestDefaultKeyTemplate_AC2_Table` | `internal/mapping/keytemplate_test.go` | |

### AC3 — Uniqueness analysis
**Given** a template using only `email` (unique constraint) → unique. **Given** only `last_name` → `KEY_MAY_COLLIDE`. **Given** a PK subset of a composite PK → `KEY_MAY_COLLIDE`.

| Test | Location | Verifies |
|---|---|---|
| `TestKeyUniqueness_AC3_Table` | `internal/mapping/keytemplate_test.go` | |

### AC4 — Runtime limits
**Given** a rendered key > 250 bytes → `KEY_TOO_LONG`. **Given** a NULL component → `KEY_NULL_COMPONENT`. Both are returned as row-level errors, not panics.

| Test | Location | Verifies |
|---|---|---|
| `TestRenderKey_AC4_TooLong` | `internal/mapping/keytemplate_test.go` | |
| `TestRenderKey_AC4_NullComponent` | `internal/mapping/keytemplate_test.go` | |
| `BenchmarkRenderKey` | `internal/mapping/keytemplate_test.go` | < 300 ns/op, ≤ 1 alloc |

### AC5 — Type field
**Given** `typeField` enabled with name `type` and value `customer`, **then** every document contains `"type":"customer"`. **Given** a designed field already named `type`, **then** validation fails with `TYPE_FIELD_COLLISION`.

| Test | Location | Verifies |
|---|---|---|
| `TestSpecValidate_AC5_TypeFieldCollision` | `internal/mapping/validate_test.go` | |
| `TestDefaultTypeValue_AC5_Singularize` | `internal/naming/naming_test.go` | `customers` → `customer`, `categories` → `category`, `order_items` → `order_item` |

### AC6 — Key editor UX
**Given** the user types `{`, **then** column suggestions appear. Selecting one inserts `{col}`. The preview shows 3 rendered keys and the uniqueness badge.

| Test | Location | Verifies |
|---|---|---|
| `KeyTemplateInput.test.tsx` › `AC6: autocomplete inserts token` | `src/features/mapping/KeyTemplateInput.test.tsx` | |
| `renderKey.test.ts` › `AC6: client renderer matches server fixtures` | `src/features/mapping/renderKey.test.ts` | Shared fixture `testdata/key-templates.json` |
