# OC-071 — Guided fixes when the document differs from the imported design

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 3 | OC-061, OC-064, OC-070 | Implemented |

## User story
As a **data engineer** mapping to the customer's JSON design, I want every difference from that design to show where it is in the document tree, say in plain words what's wrong, and offer a one-click fix, so that I don't have to guess what `REFERENCE_FIELD_UNMAPPED` or *"Missing: products[].productId"* means.

## Background (2026-10-01)
In a grouped Order Details mapping, the user used *Embed product here* (OC-064) on `products[].productId`. The dialog's default, *Replace*, removed `productId` and put a `product` object in its place. The customer's design has `products[].productId`, so:
- The Problems panel showed `products[].productId · REFERENCE_FIELD_UNMAPPED — is in the imported design but not in this one`. It had no fix, and no icon in the tree, because the field no longer exists.
- The sample's Reference box said `Missing: products[].productId` and `Not in the design: products[].product`. Neither said what to do.

## Scope
**In:**
- *Embed … here* defaults to *Keep productId and add product next to it* when the imported design has the key field, and says why.
- `REFERENCE_FIELD_UNMAPPED` fixes, worked out on the server from the field's scope (the relation filling its parent: here, each line of the group):
  - Field missing → `restore-field`, *"Add productId back (from product_id)"*. It re-creates the field with the imported type, in the imported position, mapped from the scope's column whose name matches (`productId` ↔ `product_id`). With no matching column, *"Add productId back"* adds it unmapped.
  - Field present but unmapped → `map-field`, *"Map from product_id"* when a column matches.
  - The messages say what happened and what to do.
- Document tree: a ⚠ or ⛔ icon sits in front of the name of every node with an issue. Its tooltip holds the messages. An issue on a field that doesn't exist shows on its nearest existing parent (*each item*).
- The sample's Reference box lists each difference as a sentence with its fix: *Add back*, or *Add to design* / *Remove* (OC-070).

**Out:** fixing REFERENCE_TYPE_MISMATCH automatically.

## Acceptance criteria & unit tests

### AC1 — Server-side fixes
**Given** an imported design with `items[].productId` and a design without it, whose items are filled by a relation with column `product_id`, **then** the issue's fix is `restore-field` with that relation and column. **Given** the field exists unmapped, **then** the fix is `map-field`.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_OC071_AC1_RestoreAndMap` | `backend/internal/mapping/validate_reference_test.go` | |

### AC2 — Applying the fixes
**When** `restore-field` is applied, **then** `productId` is back under the item with the imported type, in its imported position, mapped from the relation's `product_id`. **When** `map-field` is applied, **then** the field is mapped.

| Test | Location | Verifies |
|---|---|---|
| `fixes.test.ts` › `OC-071 AC2: restore and map` | `web/src/features/mapping/fixes.test.ts` | |

### AC3 — Where and what
**Given** the missing-field issue, **then** the item row in the document tree shows a warning icon whose label is the message. **Given** a key field in the imported design, **then** *Embed … here* defaults to keeping it. The Reference box lists the missing path with *Add back*.

| Test | Location | Verifies |
|---|---|---|
| `Composer.test.tsx` › `OC-071 AC3: issue icons, keep the key, reference fixes` | `web/src/features/composer/Composer.test.tsx` | Icon and fix, *Keep* default |
| `SamplePane.test.tsx` › `OC-061 AC5: shows the document’s conformance beside the reference` | `web/src/features/composer/SamplePane.test.tsx` | Sentences and *Add back* in the Reference box |

## Implementation notes (2026-10-01)
- Server, `mapping/reference.go`: `matchingColumn` compares the field's name and the column names, ignoring case and `_`/`-`/spaces. It looks in the scope's table: the validation table for the root, or `ctx.Related` for a relation, falling back to the root table when the relation reads it (a group). For a missing field the scope is the parent's own relation when one fills it, else the parent's scope. The fix carries `relationId` and `columns[0]`. The messages now say what to do.
- Web, `fixes.ts`: `restoreField` adds the field under the parent path, after the last sibling that comes before it in `imported.fields`, then maps it with `mapFrom`. `mapFrom` replaces whatever filled the field. The Canvas `ValidationPanel` applies both through its generic fix button. The Composer's `ProblemsPanel` gained buttons for `restore-field` and `map-field`.
- `DocumentTree`: the ⚠/⛔ icon moved in front of the type icon and name, as `role="img"` with the messages as label and tooltip. A `REFERENCE_*` issue whose path has no node attaches to the nearest existing ancestor (`a.b[].c` → `a.b[]` → `a.b` → root).
- `EmbedKeyDialog` defaults to *alongside* when the key field's path is in `imported.fields`, with a note that turns orange if *Replace* is picked.
- `SamplePane` `Conformance`: one sentence per path, with *Add … back*, *Add to design* and *Remove* taken from the matching validation issue. The old `Missing:` / `Not in the design:` / `Other type:` strings are removed.
