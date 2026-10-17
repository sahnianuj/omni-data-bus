# Computed fields

A computed field takes its value from the document's **other fields** instead of a source column: `fullName` from `firstName` and `lastName`, a `label` per order line, a reference such as `ORD-1001`. It's worked out after the rest of the document is built, so it reads values after their transforms, inside embedded objects and in array items.

## Setting a field's value

Select a field in the Canvas, the Composer or the Table view. The Inspector's **Value source** has three choices:

| Choice | The field gets |
|---|---|
| **Source** | Mapped source columns, through the field's transforms (as before) |
| **Fixed** | The same value in every document (a string, number, boolean, null, or JSON) |
| **Template** | A value built from other fields, like `${doc.firstName} ${doc.lastName}` |

Switching a mapped field away from **Source** asks first; **Undo** brings the mapping back. **ƒx** next to **Add field** adds a new field that is already a template. Computed fields show **ƒx** in the trees.

## Writing a template

A template is text with `${…}` values in it. Inside `${…}`, name a value; nothing else (no calls or arithmetic). Typing `${` offers the names, and a `.` after one offers its fields, with their types.

| Name | Is | Example |
|---|---|---|
| `doc` | The whole document | `${doc.customer.name}` |
| `self` | The object holding the field: the document, an embedded object, or the current array item | `${self.qty}` in `items[]` |
| `parent` | The object holding `self` | `${parent.orderId}` from an item |
| `index` | The item's position in its array, from 0 | `${index}` |
| `row` | The source row, every column, for fields at the top of the document | `${row.created_by}` |

Examples:

```text
${doc.firstName} ${doc.lastName}            → "Ada Lovelace"
ORD-${doc.orderId}                          → "ORD-1001"
${index}: ${self.sku} ${self.product.name}  → "0: A-1 Pen"        (in items[])
${doc.items[0].sku}                         → the first item's sku
${doc.customer["last name"]}                → a field whose name has a space
\${not a value}                             → the text "${not a value}"
```

- A missing or null value is written as nothing. Tick **Null if any value is missing** to make the whole field null instead (and left out when the mapping omits nulls).
- A value that is an object or an array is written as its JSON text.
- The field's own transforms (Canvas) run on the result, e.g. **upper**.
- Under the editor, the value for each sample document shows as you type; ◀ ▶ step through them. Errors show there too, and the Inspector says what the field uses and which computed fields use it.

## When the document changes

Renaming or moving a field rewrites every template that reads it, in the same undo step (`${self.qty}` becomes `${doc.qty}` if `qty` moves out of the array). Removing a field that a template reads leaves the template as written and marks it with *unknown field* until you fix it, so a value never silently turns empty. Auto-map never maps a column onto a computed field, and re-running it keeps them.

## Order, checks and limits

- A computed field that reads another runs after it. Two fields that read each other are an error (*Computed fields read each other: a → b → a*).
- A misspelled field (*isn't a field of the document design*), `parent` at the top, `index` outside an array, `row` inside a relation, or a call or operator inside `${…}` is reported with its line and column, and the mapping isn't ready until it's fixed.
- A value over 1 MB fails the row, unless the field is set to write null or leave the field out instead.
- The document key stays source-based: templates can't be used in the key.
- Exports and `odb run` compute the same values as the server, and a 1.0 worker never runs a mapping with computed fields.

JavaScript expressions (`doc.email.substring(0, doc.email.indexOf("@"))`) come in a later release.
