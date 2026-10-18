# Document composer

The composer designs the document first and binds tables to it — useful when you already know the target JSON (an API contract, an existing Couchbase collection) or when one document draws on several tables.

## Starting from a design

Design & map → **Composer**. Start from the generated design, or **Import** a JSON sample, an NDJSON file or a JSON Schema — and let the tool propose the whole mapping from it ([Start from the customer's design](design-first.md)). The imported fields become the *contract*; the composer keeps example values next to fields (they are never written) and offers to keep fields that look constant as constants.

## Binding tables

- The **driving table** gives one document per row. With **Group rows** it gives one document per distinct value of the group-by columns (an order header built from order lines).
- Each object or array in the design is **bound** to a relation of the driving table (or of another bound node). **Suggestions** propose bindings by name, type fit and cardinality.
- An object bound to a one-to-many relation takes its **first** row; an array bound to a many-to-one relation **wraps** it in a one-item array.
- **Auto-map** fills the fields of a node from its table's columns; drag columns onto fields for the rest.

## Conformance to an imported design

Fields of the imported design that you don't map, and fields you add beyond it, are listed in the **Reference** box with one-click fixes. By default they are information only; turn on **Strict** to make them errors that block migrations and exports.

## Preview and validation

Preview shows composed documents from sample rows, per-node row counts and the worst case (the largest document). Problems are listed in the **Issues** panel; each mapping card in the list says how many things are left to fix.
