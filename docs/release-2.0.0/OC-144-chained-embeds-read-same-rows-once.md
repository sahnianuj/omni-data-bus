# OC-144 — Chained embeds read the same rows once

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P1 | 2 | OC-043, OC-046 | Implemented |

> **Trimmed (2026-10-16):** hierarchy embeds, the embed size hint, the chained golden parity fixture and the G9 benchmark gate moved to OC-161 (release 3.0).

## User story
As a **data engineer** who embeds the same table in several places of a document (`items[].product` and `featuredProduct`; billing and shipping `address → country`), I want those rows read once per batch, so that rich documents don't cost a query per place they appear.

## Background
The resolver (OC-043) reads relations depth by depth with one query per relation per batch, de-duplicating join keys within that relation. The same table embedded in several places is read once **per relation**, even for the same keys in the same batch.

## Scope
**In:**
- **Shared many-to-one reads per batch:** many-to-one relations at the same depth that read the same table with the same join columns and order, keeping one row per key, with no filter, junction or key range, share one query for the batch, with the union of their key values and columns. Each relation gets the rows of its own parents' keys; a shared row may carry a column only the other relation projects, which no document reads, so the documents are identical with and without sharing.
- One-to-many and many-to-many relations, and many-to-one relations with a filter, keep their own reads.

**Out:** everything moved to OC-161; sharing across depths or across batches.

## Implementation details
- `internal/related` (`resolve.go`): `units` groups each depth's plans by `shareKey`; `lookupShared` issues one batched query with the union of keys and projections and hands each relation the rows of its own keys. Memory accounting (`OC_RELATION_MEMORY_MB`) counts the shared rows once. `Resolver.NoShare` turns it off (tests).

## Acceptance criteria & unit tests

### AC1 — Shared reads, same documents
**Given** `items[].product`-style and `featuredProduct`-style relations both from `products` by `product_id` at the same depth, **then** each batch issues one `products` query, and the documents equal those built with sharing disabled. **Given** one of them has a filter, **then** two queries.

| Test | Location | Verifies |
|---|---|---|
| `TestResolver_OC144_AC1_SharedManyToOneReads` | `backend/internal/related/related_test.go` | One query with the union of keys and columns, same rows as unshared, a filter reads alone |
| `TestParity_AC3_GoldenMappings` | `backend/internal/headless/parity_integration_test.go` | Real PostgreSQL: server job and `odb run` still byte-identical |
