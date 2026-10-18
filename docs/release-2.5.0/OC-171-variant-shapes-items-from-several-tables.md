# OC-171 — Variant shapes: an array or object whose items come from one of several tables

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P1 | 13 | OC-164, OC-166, OC-043 | Implemented |

## User story
As a **data engineer** whose customer design has an array (or an object) whose items take different shapes, each built from a different source table, I want to import those shapes with the document design and have each item built from the table its row belongs to, so that the documents match the customer's variants whatever the domain: payments, activity feeds, addresses, contact methods, line-item types.

## Background (gap found 2026-10-17)
Many designs have **polymorphic** parts. One array or object holds items of several kinds, each with its own fields and usually a discriminator constant:
- an order's `payments[]`: card, gift card, cash, QR, each with its own fields;
- a customer's `contactMethods[]`: email, phone, postal address;
- a ticket's `activity[]`: comments, status changes, attachments;
- an invoice's `lines[]`: product lines, service lines, discount lines;
- an object, such as `primaryInstrument{}`, that is one of several kinds.

In a relational source, a variant is stored in one of three ways. The tool has to support all three, because which one appears depends on the customer's source, not on the design:

| Source pattern | How a row picks its variant | Example |
|---|---|---|
| **A. Exclusive arc** | The item row has several nullable foreign keys, and exactly one is set (often a CHECK constraint) | `collected_payments.{cash_tender_id, credit_card_id, svc_id, scc_id, …}` |
| **B. Discriminator column** | The item row has a type column, and each value has its own detail table, or none | `contact_methods.kind IN ('email','phone','postal')` + `postal_addresses.contact_id` |
| **C. Union of tables** | Each variant is its own table referencing the parent, with no shared item table | `comments.ticket_id`, `status_changes.ticket_id`, `attachments.ticket_id` → `ticket.activity[]` |

On the design side, variants show up as:
- JSON Schema `oneOf` / `anyOf` of objects, usually with a `const` discriminator (`"kind": {"const": "email"}`);
- `$ref`s to separate files;
- a generic `items: {type: object}` that refers to other files in its description, as the fixture's cart does (OC-162).

2.0 can't express any of this:
- an array's items have one shape, from one relation;
- `oneOf`/`anyOf` is read only as "nullable T";
- designs are imported one file at a time;
- discovery doesn't read CHECK constraints.

## Scope
**In:**
- **Variants in the design**:
  - `oneOf` / `anyOf` of object schemas becomes a variant set on that array's items, or on that object. Each variant keeps its `title`, its field order and its discriminator (the property whose `const` differs between variants);
  - `discriminator.propertyName` (the OpenAPI style) is honoured when present;
  - the import dialog accepts several files or a folder. Relative `$ref`s are resolved within the upload. A generic `items: {type: object}` can be given its variants by the user ("Variants of payments[]…") or through a description that names the files;
  - a variant set with a single member is an ordinary object, and nothing changes.
- **Source patterns, discovered from the connected source**:
  - **A:** discovery reads CHECK constraints (PostgreSQL `pg_constraint contype = 'c'`, `pg_get_constraintdef`). It recognises `num_nonnulls(a, b, …) = 1` and the `CASE WHEN x IS NOT NULL THEN 1 ELSE 0 END + … = 1` form as `Table.exclusiveArcs`. Without a constraint, a set of ≥ 2 nullable foreign keys on a child table is a *possible* arc (lower confidence; the user confirms);
  - **B:** a text or enum column whose values match the variants' discriminator constants (enum values from discovery; otherwise distinct values from a sample of up to 1,000 rows of the connected source);
  - **C:** two or more child tables that reference the same parent and whose columns fit different variants.
- **Spec v2**, additive. A relation may carry `variants[]`, one per variant:
  - `{id, match, relation?, itemsFieldId, discriminator?}`;
  - `match` is `notNull(column)` for A, `equals(column, value)` for B, or `source(relationId)` for C;
  - `relation` is the N:1 or 1:1 relation that reads the variant's own table (A, B), or the variant's own 1:N relation (C);
  - for C, the array merges the variants' rows, ordered by a column common to all of them (`created_at`) when there is one, else by variant then key. The order can be edited.
- **The proposal** (OC-163/164): for each variant set in the design, it tries patterns A, B and C from the container's scope. It assigns each design variant to the source variant whose columns fit it best (Hungarian over variants × candidates). The reasons say, for example, "kind=email ↔ contact_methods.kind", "scc_id ↔ variant SCC, fields 9/11", or "union of 3 tables". Unmatched variants are unresolved, with a reason (OC-169).
- **Composer:** a node with variants shows them as child rows (`payments[] · SCC ← sccs`). The Bind dialog gets a *Variants* section to pick the pattern, then add, remove or reassign variants. The Inspector shows each variant's discriminator.
- **Runner:**
  - A and B compile to one read of the item rows, plus one keyed read per variant table, sharing reads as in OC-144;
  - C compiles to one read per variant table, merged per parent.
  - The server and `odb run` produce the same documents.
- **Rows that match no variant** (A or B with an unexpected value, or no key set) are skipped and counted, and the run report lists them (`VARIANT_NO_MATCH`, a warning). With `onNoMatch: fail`, the document fails instead.

**Out:** variants nested inside variants beyond the depth limit (`OC_MAX_RELATION_DEPTH`); other databases' CHECK syntax (Oracle in 4.0); inferring variants from sample documents without a schema (OC-059 samples merge into one shape).

## Implementation details
- `schema.Table.CheckConstraints []Check {name, definition}` and `ExclusiveArcs [][]string`, filled in by `discover.go` (one query per schema, cached with the snapshot). Sampling distinct values for pattern B uses the existing preview reader (OC-007 limits, read-only session).
- `docschema.Field.Variants []Variant {ID, Title, Discriminator {Field, Value}, Fields}`. `jsonschema.go` reads `oneOf`/`anyOf` of objects into it, still treats `[null, T]` as nullable as today, and *Export JSON Schema* writes `oneOf` back. `docschema.ImportBundle(files)` resolves cross-file `$ref`s.
- `mapping.Relation.Variants []RelationVariant {ID, Match {Kind: notNull|equals|source, Column, Value, Relation}, Relation, ItemsFieldID, Discriminator *Constant}`, plus `OnNoMatch`. Validation:
  - A's columns must be an exclusive arc, or the user confirms "rows may match none or several";
  - B's values must be distinct;
  - C's relations must share the parent.
- The bundle and `mappings.json` (OC-158) carry `variants`. Older runners refuse the spec with `SPEC_FEATURE_UNSUPPORTED variants`, like computed fields (OC-140).
- **Fixtures:**
  - pattern A is the `pos` payments (OC-162);
  - B and C get small synthetic schemas in the same seed: `crm.contact_methods` (B) and `support.tickets` with comments, status changes and attachments (C), each with a design under `testdata/design-first/`.

## Acceptance criteria & unit tests

### AC1 — Variants read from the design
**Given** a design whose `contactMethods` items are `oneOf` three objects, each with `kind` set to a different `const`, **then** the imported field has 3 variants with discriminator `kind`, and *Export JSON Schema* writes the same `oneOf`. **Given** `oneOf [null, string]`, **then** the field is a nullable string, as today. **Given** the cart design plus its payment files, **then** `payments[]` has 5 variants with discriminator `paymentType`.

| Test | Location | Verifies |
|---|---|---|
| `TestJSONSchema_OC171_AC1_Variants` | `backend/internal/docschema/jsonschema_test.go` | Includes the nullable regression |
| `TestImportBundle_OC171_AC1_CrossFileRef` | `backend/internal/docschema/import_test.go` | |
| `ImportDialog.test.tsx` › `OC-171 AC1: several files` | `web/src/features/composer/ImportDialog.test.tsx` | |

### AC2 — Each source pattern is found on the connected source
**Then**:
- **A:** `pos.collected_payments.exclusiveArcs` holds its 5 foreign keys, for both CHECK forms;
- **B:** `crm.contact_methods.kind` is proposed as the discriminator, with `email`, `phone` and `postal` matched to the design's variants;
- **C:** `ticket.activity[]` is proposed as the union of `comments`, `status_changes` and `attachments`, ordered by `created_at`.

| Test | Location | Verifies |
|---|---|---|
| `TestDiscover_OC171_AC2_ExclusiveArc` *(integration)* | `backend/internal/connectors/source/postgres/discover_integration_test.go` | |
| `TestPropose_OC171_AC2_Patterns` | `backend/internal/automap/propose_test.go` | A, B and C |

### AC3 — Documents have each variant's shape
**Given** seeded rows for each pattern, **then** every item is in its variant's field order with its discriminator, and union arrays are merged in order. Server jobs and `odb run` produce identical documents.

| Test | Location | Verifies |
|---|---|---|
| `TestPlan_OC171_AC3_Variants` *(integration)* | `backend/internal/plan/compile_integration_test.go` | |
| `TestParity_AC3_GoldenMappings` *(integration)* | `backend/internal/headless/parity_integration_test.go` | Golden mappings `pos_cart`, `crm_contact`, `support_ticket` |

### AC4 — Rows with no variant
**Given** a `contact_methods` row with `kind = 'fax'`, **then** it's skipped and counted as `VARIANT_NO_MATCH` in the run report. With `onNoMatch: fail`, that document goes to the dead-letter log instead.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC171_AC4_NoMatch` | `backend/internal/transform/variants_test.go` | |

### AC5 — Edit variants in the Composer
**When** the user reassigns a variant to another table in the Bind dialog's *Variants* section, **then** the tree shows the new source, the preview follows, and one undo restores it.

| Test | Location | Verifies |
|---|---|---|
| `BindDialog.test.tsx` › `OC-171 AC5` | `web/src/features/composer/BindDialog.test.tsx` | |

### AC6 — Older runners refuse
**Given** a 2.0 `odb`, **then** a 2.5 bundle with variants fails with `SPEC_FEATURE_UNSUPPORTED variants` before reading any data.

| Test | Location | Verifies |
|---|---|---|
| `TestBundle_OC171_AC6_FeatureGate` | `backend/internal/export/bundle_test.go` | |

## Implementation notes (2026-10-17)
- **Design model:** no `variants` list was added to `DocField`. Instead, a variant is an **object child of the item node** marked with `variant {title, field, value}`. Every existing walker (scopes, paths, composer tree, validation, reconcile) already reaches it.
  - Rendering writes the matching variant's fields inline in the item.
  - `docschema.Contract` gives the document's paths (`payments[].tipInCents`, not `payments[].SCC.tipInCents`), once each, optional when only some variants have them.
  - Design paths keep the variant (`payments[].SCC.amountChargedInCents`).
- **Import:**
  - `oneOf` / `anyOf` of objects becomes a variant set: the discriminator is OpenAPI's `discriminator.propertyName`, else the field every variant fixes to a different `const`, and variants are named after their value, else their title. `[null, T]` stays a nullable T.
  - A typeless `{"const": …}` takes its value's type.
  - Schema consts give constant hints inside arrays too, except on a scalar array's item node.
  - `docschema.ImportFiles` (infer `files`) resolves relative `$ref`s across the files, and gives an array with untyped items the files its description names (`payments/*.schema.json`) or that sit in a folder named like it. Unused files are a warning.
  - The composer's import takes several files or a folder: the shallowest file is the document.
  - *Export JSON Schema* writes `oneOf` + `discriminator`, and the discriminator back as `const`.
- **Discovery:** `Table.checkConstraints` and `Table.exclusiveArcs`, both CHECK forms (`schema.ExclusiveArc`).
- **Spec:**
  - `Relation.variants [{fieldId, when, value?}]` and `onNoMatch` (skip | fail) for patterns A and B. A variant's own table is a lookup-only relation under the array's relation.
  - Pattern C binds each variant node to its own one-to-many relation (`RelationContainer` accepts a variant node).
  - Validation: `VARIANT_INVALID`, plus the warning `VARIANT_ARC_UNCONFIRMED` when no CHECK confirms the arc.
- **Runtime (`transform/variants.go`):**
  - a row is the first variant whose rule matches;
  - rows of no variant are skipped with `VARIANT_NO_MATCH` (a warning with the count), or fail the document with `onNoMatch: fail`;
  - union arrays merge their parts by the first order column (proposed: a shared `created_at`).

  The resolver needed no change.
- **Proposal (`automap/propose_variants.go`):** patterns A, B and C are tried from the array's scope.
  - **A:** CHECK-confirmed arcs, or the nullable foreign keys at 0.9.
  - **B:** the column's enum values, or up to 1,000 distinct values sampled from the connected source (`source.DistinctValues`, opened only when needed), plus a one-to-one detail table per variant.
  - **C:** each variant's best one-to-many table, all different.

  Each variant's fields only look up its own table.
- **AC5 as built:** variants are edited in the **Inspector** of the bound array, not in the Bind dialog: for each variant, its column and value, plus "a row of no variant". The tree shows variant nodes with a `variant = "SCC"` marker.
- **AC6:** a bundle exported by 2.5 already carries `odbVersion: ">=2.5.0 <3.0.0"`, so a 2.0 `odb` refuses it before reading data (`CodeVersionUnsupported`). No new code was needed. Jobs using variants get `minWorkerVersion 2.5.0` (`mapping.RequiresV25`), so a 2.0 worker never claims them during an upgrade.
- **Fixtures:** `crm` (pattern B, with one `fax` row of no variant) and `support` (pattern C) are in the same seed, with designs under `fixtures/pos/designs/variants/`. The seed grants `omni_ro` read access to all three fixture schemas.
- **Tests:**
  - `TestExclusiveArc_OC171`, `TestDiscover_OC171_AC1_ExclusiveArc` (integration);
  - `TestJSONSchema_OC171_AC1_Variants`, `TestImportBundle_OC171_AC1_CrossFileRef`;
  - `TestPropose_OC171_Arc`, `TestPropose_OC171_TypeColumn` and `TestPropose_OC171_Union`: integration tests with real documents, covering AC2–AC4;
  - `TestBundle_OC171_AC6_FeatureGate`, `TestRequiresV25_OC171`;
  - `Variants.test.tsx` (AC1 import, AC5 edit and undo).
- **Not yet:** the parity golden mappings for the three patterns. OC-173 adds `pos_cart`.

