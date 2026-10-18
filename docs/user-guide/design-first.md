# Start from the customer's design

When the customer already has the JSON documents they want (a JSON Schema, or sample documents), you don't have to build the mapping field by field. Import their design, and the tool proposes the whole mapping from your connected source: which table the documents come from, which related table fills each embedded object and array, and which column, conversion or constant fills each field. You then review the proposal on the customer's own structure and keep what's right.

The proposal always reads the project's **source connection**: the schema it discovered, with its keys, foreign keys, unique keys, CHECK constraints and enum values. No DDL file is involved.

## Importing the design

There are two ways in:

- **New mapping from a design…** on the Mappings page. Paste or choose the design. The tool ranks the tables its documents could come from, each with the reasons why (the fields that match, the containers it can fill, the document ID pattern, the name). Pick one, choose where the documents go, and **Create and propose**.
- **Import JSON design…** in an existing mapping's Composer, with **Propose the mapping from the source** checked. It's on by default when the mapping is still empty.

A design split across files is imported in one go. Choose several files, or **Choose a folder…**. The shallowest file is the document, and the others are the files its `$ref`s point to, or the item designs of an array whose items the document doesn't describe (for example `payments` items described in `payments/*.schema.json`). See [Variant shapes](#variant-shapes).

## What the proposal decides

- **The driving table:** the table whose fields, relationships and name fit the whole design best. A key pattern in the design's description ("Document ID: cart::{uuid}") also sets the document key (`cart::{id}`).
- **Every object and array:** a related table in either direction, at any depth up to the relationship depth limit.
  - A table the parent references fills an object (`partner`).
  - A table that references the parent fills an array (`items[]`), or an object through its first row (`customer`).
  - A one-to-one table fills an object directly.
  - An array over a single related row holds that one item (`appliedRewards[]`).
  - An object whose fields are the parent's own columns needs no table (`pricing`).
- **Every field:**
  - a column of its row, or of a one-to-one or many-to-one table next to it, with no nested object (`savedAt` from `saved_orders`);
  - with the conversion the types need, such as dollar amounts into whole cents (× 100), or a number into its display text (`"3.95"`);
  - a constant (`"type": "cart"`), a combined name (`first_name` + `last_name`), a total over child rows (`sum` of the payments' tips), or a value the column can't hold that a one-to-one table decides (`status` is `"saved"` when a saved order exists).
- **What the design says:** descriptions are read as evidence.
  - "sourced from saved_orders.saved_at" names the column.
  - "Not populated" marks a field left empty on purpose: nothing is written for it, and it counts as done.
  - `const` is a constant, and `enum` lists the values the preview checks.

Each proposal has a confidence: **high**, **medium** or **low**. A medium or low one needs a look, and so does an unresolved field.

## Reviewing and fine-tuning

The proposal opens in **review**. The Document tree is the customer's design, with what's proposed on each row and a confidence chip. The banner counts what was proposed, what needs review, what's not populated and what's unresolved.

- Filter the tree with **Needs review**, **Unresolved**, **Not populated** or **Edited**. Press `n` and `p` to jump between the rows to look at.
- Select a row: the Inspector's **Proposal** panel says why it was proposed and what else would fit.
  - Use another table, or a candidate column.
  - Mark the field not populated.
  - Add a virtual relationship when nothing reaches the right table.
  - **Take back** the row.
- Change anything with the usual editors: the Inspector, the Bind dialog, or dragging columns. Changed rows show **edited**, and **Propose again** fills what's still missing around your edits.
- The Sample pane shows the documents as they'd be written, while you review.
- **Apply** keeps everything as one undo step. **Apply high only** drops the rows below high confidence first. **Discard** leaves the mapping as it was.
- **Coverage sheet** downloads a CSV with one row per field and what the proposal says about it, to agree the mapping with the customer.

### Unresolved fields

An unresolved row says why:

| Reason | What to do |
|---|---|
| No column fits this field | Pick one of the candidates, set a constant, or mark it not populated |
| No relationship reaches a table that fits | Add a virtual relationship (prefilled), then **Propose again** |
| The best column can't convert to this type | Pick another column, or write an expression |
| Deepest level reached | Ask an administrator to raise `OC_MAX_RELATION_DEPTH` (up to 6) |
| Another document's key | The key expression is proposed (`concat('tax_data::', row.id)`): keep it or change it |
| The items have no fields in the design | Import the item designs: see [Variant shapes](#variant-shapes) |

## When the table is already mapped

If the table the design comes from already has a mapping in the project, **New mapping from a design** asks first. It shows the existing mapping: where it writes, its key, how much of it is mapped, and when it was last edited.

- **Update this mapping** opens that mapping's import with the design. **Update design, keep mappings** shows what changes in the design and keeps every field that's still there. The proposal then fills only what's missing.
- **Create a new mapping** is offered when the table only drives another mapping. A project maps each table once. If both mappings would write the same keys to the same place, you're warned: the last migration to run would win. The migration's pre-flight warns about that too.
- **Cancel** changes nothing.

## Variant shapes

Some arrays hold items of several kinds, each with its own fields: a cart's payments (card, gift card, cash…), a customer's contact methods, a ticket's activity. In the design these are a `oneOf` of objects, usually with a field that tells them apart (`"paymentType": {"const": "SCC"}`), or separate files per kind.

The tool recognises the three ways sources store them:

- **One table, one key set per row:** each payment row sets exactly one of `card_id`, `cash_id`… The proposal reads the CHECK constraint that says so. Without one, the nullable foreign keys still work, but the review warns that a row could match none or several.
- **One table with a type column:** `contact_methods.kind` is `email`, `phone` or `postal`, read from the column's values. A kind with its own detail table (`postal_addresses`) reads it.
- **One table per kind:** comments, status changes and attachments each have their own table. The array is all of them, merged in time order.

In the tree, each kind is a row marked **variant** under the array's items. The array's Inspector says which rows are each kind, and what to do with a row of none of them: skip it (counted in the run report), or fail the document. Mappings with variants need workers and `odb` from 2.5 or later.
