# OC-082 — Embedding a foreign key removes the key field

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P1 | 2 | OC-042, OC-064, OC-071 | Implemented |

## User story
As a **data engineer** embedding the row a foreign key refers to (an order line's `productId` → a `product` object), I want the foreign-key field removed from the document once the object is embedded, so that the key appears once, as the embedded object's own primary key, and not twice.

## Background (2026-10-02)
- **Canvas** (embed a related table, many-to-one): the server moved the key into the new object and unmapped the foreign-key field, but left the field in the design, empty. Documents carried `productId: null` next to `product.productId`, or the field showed as unmapped.
- **Composer** ("Embed products here…", OC-064): the dialog offered *replace* or *keep alongside*, but defaulted to *alongside* when the field was in the imported design (OC-071). Its default column list also left out the key column, so *replace* dropped the key from the document entirely.

## Scope
**In:**
- **Canvas:** a many-to-one embed removes the foreign-key field(s) whose only mapping copied the key column as is. Fields that combine the key with other columns, or carry other mappings or aggregates, stay (unmapped from the key, as before).
- **Composer:** the dialog defaults to *replace* everywhere, and with *replace* the key column is ticked, so it lives in the embedded object. *Keep alongside* remains as an option, and then the key column is left out of the object, as before.
- When the removed field is in the imported design, nothing is blocked: the Reference box lists it as a missing design field (info unless strict, OC-075) with its usual restore fix, and the Composer dialog says so.

**Out:** many-to-one lookups (values only, no object), which keep the key field; one-to-many and many-to-many embeds, which don't replace a key; collapsing an object back to its id (OC-064 AC3), which is unchanged.

## Acceptance criteria & unit tests

### AC1 — Canvas removes the key field
**Given** `orders.customer_id` mapped to root `customerId`, **when** I embed `customer` many-to-one through `customer_id`, **then** root `customerId` is gone, `customer.customerId` is mapped from the embedded row, and the other root fields and mappings are kept. A field combining `customer_id` with another column stays.

### AC2 — Composer replaces by default and keeps the key in the object
**Given** "Embed products here…" on `productId`, **then** *replace* is selected (also when `productId` is in the imported design) and `product_id` is among the ticked columns; submitting removes `productId` and adds `product.productId`. Choosing *keep alongside* unticks the key column.

| Test | Location | Verifies |
|---|---|---|
| `TestAddEmbed_ManyToOne_MovesKeyIntoObject` (updated) | `backend/internal/automap/embeds_test.go` | AC1 |
| `TestAddEmbed_KeyFieldWithOtherMappingsStays_OC082` | `backend/internal/automap/embeds_test.go` | AC1 |
| `Composer.test.tsx` › `OC-064 AC1: embed products here` (updated) | `web/src/features/composer/Composer.test.tsx` | AC2 |
| `Composer.test.tsx` › `OC-071 AC3: issue icons, keep the key, reference fixes` (updated) | same | AC2 (imported design) |
