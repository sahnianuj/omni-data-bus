# Relationship mapping

Related rows become part of a document: an order with its customer and its items, each item with its product.

## Embedding a related table

On the canvas, open **Embed** on a table that has a foreign key to or from the mapped table (or a [virtual relationship](#virtual-relationships)).

| Relation | Result |
|---|---|
| **Many-to-one** (the mapped row points at it) | An object: `customer: {…}`. Embedding replaces the foreign-key field: the key lives in the object |
| **One-to-many** (rows point at the mapped row) | An array: `items: [{…}, …]`, ordered and limited as you choose |
| **Many-to-many** (through a junction table) | An array of the far table's rows; junction columns can be mapped too |
| **Lookup only** | No object: pick columns of the related row as flat fields (`customerName`) |

Relations nest: embed `products` inside `items` to get `items[].product`. Depth is limited to 4 by default (`OC_MAX_RELATION_DEPTH`, at most 6).

## Array options

- **Shape:** objects (default), scalars (one column's values: `tags: ["a", "b"]`) or keyed (an object keyed by a column).
- **Filter** rows (`status = 'paid'`), **order** them, and **limit** them (default 1,000).
- **Aggregates:** count, sum, average, minimum, maximum, first or last over the related rows, with or without the array.
- **Overflow** past the limit: `truncate` (default), `fail` the row, or `split` into extra documents.
- **Empty:** an empty array (default) or no field.

## Large arrays

Relations are read in batches — one query per batch of parent rows, never one per row. For one-to-many relations whose parents have thousands of rows, the **sorted-merge** strategy streams both tables in key order instead (chosen automatically, or set it on the relation). `OC_RELATION_MEMORY_MB` (default 256) caps the related rows held per batch; a batch that would exceed it is split.

## Virtual relationships

When the database has no foreign key, define one: Design & map → **Relationships** → *Add*. Pick the two tables and the columns that match; **Detect** samples the data and suggests the cardinality. Virtual relationships are used like real ones and travel with exported migrations.

## Checking the result

**Preview** shows real documents with their related rows (up to 50 per parent) and the largest document's size, so overflows show up before the migration.
