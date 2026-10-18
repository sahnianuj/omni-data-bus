# Release 2.5.0 — Architecture notes

Release 2.5.0 builds on the 2.0.0 architecture ([../release-2.0.0/00-architecture.md](../release-2.0.0/00-architecture.md)): mapping spec v2, the composer, the relationship graph, the portable runner. The domain model, conventions, NFRs and the global Definition of Done are unchanged. This page lists what 2.5.0 adds.

## Design-first mapping

In 2.0 a customer's design is imported (OC-059), then the user asks for binding suggestions (OC-060), accepts them, and runs auto-map. Each step works on what the previous one left, and the user only sees the result at the end.

2.5.0 turns those steps into one **proposal**. It's computed on the server against the **connected source**, reviewed in the composer, and applied in one step:

```
customer design (JSON Schema / samples, one or more files)
        │  docschema.Import (OC-059) + design hints (OC-166)
        ▼
   imported design ──────────────┐
                                 │   source snapshot from discovery (tables, keys, FKs,
                                 │   unique and CHECK constraints, enums, comments, row counts)
                                 ▼   + relationship graph and hops (OC-042, OC-141)
                      automap.Propose (OC-163)
                        1. driving table           (OC-163)
                        2. containers, top-down    (OC-164, OC-171)
                        3. root fields from 1:1    (OC-165)
                        4. fields + conversions    (OC-167)
                                 │
                                 ▼
                     Proposal {driving, bindings[], fields[], unresolved[], coverage}
                                 │  review & fine-tune in the composer (OC-168, OC-169)
                                 │  existing mapping of the driving table? ask first (OC-170)
                                 ▼
                     mapping spec v2 (one undo step / one new version)
```

- **The source is always the connected source.** The proposal reads the project's source connection through discovery (`schema.Snapshot`) and the relationship graph, exactly as the composer does. Uploaded DDL files aren't an input. When discovery lacks something the proposal needs, discovery is extended: CHECK constraints, so OC-171 can recognise exclusive arcs.
- **Deterministic, explainable.** It uses no ML and no outbound calls, like OC-017 and OC-060. Every proposed binding and field carries a score, a band (high, medium, low) and the reasons behind it.
- **Proposals aren't applied until the user says so.** `POST /mappings/{id}/propose` is read-only (viewer role). Applying it goes through the composer's own operations (`bind`, `setMapping`, `setConstant`, …), so it's one undo step, and saving creates one new mapping version.
- **Same spec, same runner.** A proposal produces an ordinary spec v2. Nothing new reaches the execution plan except the variant arrays of OC-171, which are compiled like any other relation.

## Fixtures

The reference case for every story is a **point-of-sale cart**. A cart document embeds its customer, its partner, its items (each with modifiers and applied rewards), its pricing, its saved-order state and its payments. Each payment is one of several card or tender variants. OC-162 adds:

- a `pos` schema to the dev PostgreSQL seed (`carts`, `cart_items`, `customizations`, `applied_rewards`, `customers`, `partners`, `saved_orders`, `associated_scans`, `collected_payments`, `sccs`, `svcs`, `credit_cards`, `cash_tenders`, `scan_and_pay_qrs`, `stores`, `encryption_keys`) with synthetic rows;
- the cart design and payment-variant designs as JSON Schema under `backend/internal/fixtures/pos/designs/`, generic and with synthetic examples (plus the `crm` and `support` variant fixtures of OC-171);
- the expected mapping (`expected.json`), which tests score proposals against.

Customer files are never committed. The fixtures have the same shape, but generic names and synthetic values.

## New spec fields (additive to spec v2, no version bump)

| Field | Story | Meaning |
|---|---|---|
| `docSchema.fields[].enum` | OC-166 | Allowed values from the design (`enum`). Validated in the preview and conformance checks |
| *(hints)* | OC-166 | Not stored: `docschema.ParseHints` reads `notPopulated`, `sources[]` (`table.column`), `aggregate`, `always` and `keyTemplate` from descriptions on demand |
| `docSchema.fields[].variant` | OC-171 | Marks an object child of an array's items as one of its shapes, with its discriminator (from `oneOf`/`anyOf` or several files) |
| `relations[].variants` | OC-171 | Which source each variant comes from: the key that's set (exclusive arc), a type column's value, or one of several child tables (union) |
| `fieldMappings[].state = "notPopulated"` | OC-166 | Intentionally empty: counted as done, never written |

## Versioning (OC-172)

`VERSION` at the repository root holds the release (`2.5.0`). The build is the commit, plus the CI build number when there is one. Every build path reads both, so the footer always shows a release, even for a dev build with no git tags (`2.5.0-dev · build 7f6cea2`).
