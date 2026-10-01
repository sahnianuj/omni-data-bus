# OC-065 — Array items read their own row, never repeat the parent's

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 3 | OC-063, OC-049 | Implemented |

## User story
As a **data engineer** grouping `order_detail` into one order document, I want every line in `products[]` to show its own row's values, and the tool to stop me (or fix it for me) when a line field reads the order instead, so that each source row appears exactly once in the array.

## Background (gap found 2026-10-01)
A customer's sample showed `products[]` with the right number of entries (3 for order 1) but all three with line 1's values (`lineNo: 1, productId: 2, quantity: 3`). The cause: inside the group's rows, a field can be mapped from the **driving table's** columns, which hold the group's first row (OC-063). In the source picker those columns are listed first, and the group's rows second. Both groups were labelled `order_detail` until OC-063's label fix ("Rows of this group (order_detail)"). Nothing warned; the transform repeats the parent value in every item by design (that's how `$parent` values work).

## Scope
**In:**
- **Validation** `ITEM_READS_PARENT_ROW` (**error**): a field inside an array bound to rows of the **same table** as its parent scope (the group's rows, or a self relation) reads that parent scope's column. Every item would repeat the parent's value. The issue has a **fix**: *Read it from each row* re-points the source to the array's relation (same column name).
- **Rebase on bind**: binding an array to the group's rows (or to a relation on the parent scope's own table) re-points the descendants' existing mappings from the parent scope's columns to the new relation's.
- **Source picker**: inside such an array, the picker lists *Rows of this group* first. The parent's columns are under "This order's first row (order_detail)", and picking one there asks for confirmation.
- The same rule for Couchbase and directory targets (the documents are the same).

**Out:** arrays bound to *other* tables. Reading a parent value there (e.g. `orderId` on each line from `orders`) is legitimate and stays allowed.

## Implementation details
- `mapping.validateGroup` → `validateItemsReadOwnRow(spec)`: for each relation `r` whose table equals its parent scope's table (`r.Group`, or `r.Table == scopeTable(r.Parent)`), every active mapping on a field in `r`'s scope (`FieldScopes == r.ID`) with a source whose relation is `r.Parent()` (`""` for the root) gets the issue, with `Fix{Kind: "repoint-source", RelationID: r.ID}`.
- `composerOps.bindGroupRows` and `bind` (when the table equals the scope's) call `rebaseSources(spec, relationId)`.
- `ProblemsPanel` and the inspector show the fix button. `ComposerInspector` orders the source groups (own relation first) and relabels the parent group.

## Acceptance criteria & unit tests

### AC1 — The customer's repeated line is caught
**Given** grouped `order_detail` with `products[].lineNo` ← the driving table's `line_no`, **then** validation returns `ITEM_READS_PARENT_ROW` (error) on `products[].lineNo`, and starting a job is `422 MAPPING_INVALID`.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC1_ItemReadsParentRow` | `internal/mapping/validate_group_test.go` | |

### AC2 — One click fixes it
**Given** that issue, **when** *Read it from each row* is applied, **then** the mapping reads `{relation: <group rows>, column: line_no}` and the preview shows lines 1, 2, 3.

| Test | Location | Verifies |
|---|---|---|
| `ProblemsPanel.test.tsx` › `OC-065 AC2: repoint fix` | `src/features/composer/ProblemsPanel.test.tsx` | |
| `TestGroup_AC2_EachRowOnce` | `internal/transform/group_test.go` | |

### AC3 — Binding rebases existing mappings
**Given** `products[]{lineNo, quantity}` mapped from the driving table's columns, **when** the array is bound to the group's rows, **then** both mappings read the group's rows.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-065 AC3: bind rebases` | `src/features/composer/composerOps.test.ts` | |

### AC4 — Other tables are unaffected
**Given** `items[]` bound to `order_items` under `orders` with `orderId` ← `orders.order_id`, **then** no issue is raised.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC4_OtherTableParentValueAllowed` | `internal/mapping/validate_group_test.go` | |

## Implementation notes (2026-10-01)
- Reproduced: with `products[].lineNo` ← the driving table's `line_no` (the group's first row), an order with 3 lines gave `[{lineNo:1},{lineNo:1},{lineNo:1}]`, exactly the customer's sample. `mapping.validateItemsReadOwnRow` now makes it `ITEM_READS_PARENT_ROW` (error) for any array bound to rows of its parent scope's own table (the group's rows or a self relation; not junctions), with `Fix{Kind: "repoint-source", RelationID}`.
- The fix is in the composer's Problems panel (*Read it from each row*, `composerOps.repointSource`) and the canvas Validation panel (`fixes.applyFix`). `bind` and `bindGroupRows` call `composerOps.rebaseSources`, so binding re-points what was mapped from the parent row.
- Source picker: only inside such arrays, the own rows come first and the parent's columns are labelled "The group's first row only (table)". No confirmation dialog when picking one; the validation error covers it.
