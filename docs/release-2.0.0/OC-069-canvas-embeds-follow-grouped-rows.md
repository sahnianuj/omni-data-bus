# OC-069 — Canvas embeds follow the group's rows, and removing a lookup removes its fields

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 2 | OC-043, OC-063 | Implemented |

## User story
As a **data engineer**, I want a product embedded from the Canvas to land inside each order line, next to its `productId`, and I want removing an embed to remove every field it added, so that the document has the product's details per line and no leftover fields.

## Background (2026-10-01)
The user grouped `shop."Order Details"` into one document per order (OC-063). Each order's lines (`lineNo`, `productId`, `quantity`) are in an array. On the Canvas, Related tables lists `product` as a parent of the driving table. *Embed* put it at the document root. But `product_id` differs per line, so the root got only the first line's product. The pipeline already supports a relation nested under the group's rows (`TestGroup_AC1_OrderDetail` embeds `product` under `products[]`). Only the Canvas placed it wrong.

Removing a *values only (lookup)* embed dropped its mappings but left its generated `product…` fields in the target document. A lookup has no field of its own; its columns are fields beside the foreign key.

## Scope
**In:**
- In a grouped mapping, a relation from the driving table whose join uses any column that isn't grouped on is embedded under the group's rows. This covers a many-to-one like `product_id → product` and a one-to-many or junction from a per-row column. The dialog says *under {array}*. A relation joined only on group-by columns stays at the root.
- Removing a relation also removes the fields that only its columns (or its nested relations' columns) fed, unless the imported design (OC-059) defines them.

**Out:** moving relations already embedded at the root. Remove them, then embed them again.

## Acceptance criteria & unit tests

### AC1 — Embed lands in the group's rows
**Given** a mapping grouped by `order_id` with the lines in `items[]`, **when** the user embeds `product` (joined on `product_id`) from Related tables, **then** the relation is nested under the group relation and the dialog says it goes under `items`. **Given** a relation joined on `order_id` only, **then** it embeds at the root.

| Test | Location | Verifies |
|---|---|---|
| `RelatedTree.test.tsx` › `OC-069 AC1: per-row relations nest under the group's rows` | `web/src/features/mapping/RelatedTree.test.tsx` | |
| `RelatedTree.test.tsx` › `OC-069 AC1: a product already in the group's rows shows as embedded` | same | |
| `TestAddEmbed_UnderGroupRows_OC069` | `backend/internal/automap/embeds_test.go` | Object and lookup land in `items[]`, nothing on the root |

### AC2 — Removing a lookup cleans its fields
**Given** a lookup embed that generated `productSku`, `productTitle`…, **when** it's removed, **then** those fields and their mappings are gone, and fields mapped from anything else stay. **Given** an imported design that defines `productSku`, **then** that field stays (unmapped).

| Test | Location | Verifies |
|---|---|---|
| `embeds.test.ts` › `OC-069 AC2: removing a lookup removes its fields` | `web/src/features/mapping/embeds.test.ts` | |

## Implementation notes (2026-10-01)
- `RelatedTree.base(join)` is the group relation's id when the mapping has a group relation and a join column isn't in `root.groupBy`, else the root. `embedPath`, `embeddedPath`, the junction *Embed as array* and `junctionEmbedded` all start from it. The dialog's *under* line names the group's array field.
- No server change was needed. `AddRelation` with `parentId` set to the group relation puts an object, or a lookup's fields, in the array's item object, and the pipeline already resolves it (`TestGroup_AC1_OrderDetail`).
- `removeEmbed` also drops every field whose mappings all read removed relations. A field with another source keeps it, and a path in `docSchema.imported.fields` stays.
- Canvas target rows are laid out as a folder tree, like the Composer's: a fixed chevron slot comes first (blank for leaves), then the state dot and name. So each level's names sit one 16px step right of their parent's (`products` level with `orderId`, then `lineNo`/`product`, then the product's fields).
