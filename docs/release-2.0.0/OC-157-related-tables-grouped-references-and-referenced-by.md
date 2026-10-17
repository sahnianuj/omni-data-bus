# OC-157 — Related tables grouped as References and Referenced by, embedded by cardinality

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P1 | 5 | OC-042, OC-043, OC-044, OC-152 | Implemented |

## User story
As a **data engineer** designing a collection in Design & Map, I want the tables I can embed split into **References** (tables this one points to) and **Referenced by** (tables that point to this one), each labelled with its cardinality, so that I can see at a glance what each embed will produce: a single object for 1:1 and N:1, an array of objects for 1:N and M:N.

## Background (2026-10-16)
Discovery's **Relationships** tab already shows a table this way (`TableDetail`, `relationshipGraph.ts`): *References* on the left, *Referenced by* on the right. Design & Map's **Related tables** panel (`RelatedTree`, OC-042 AC6) doesn't:

- **One mixed list.** ↗ parents and ↙ children are interleaved: ↗ first, then ↙, sorted by name (`mapping.Candidates`), and the same happens again at every nested level (`partners` → `↙ cash_drawers`, `↙ partner_sessions`, `↗ roles`, `↗ stores`). The only cue is a small arrow in front of each row, so users read `carts` → 15 related tables as one undifferentiated list.
- **Many-to-many is somewhere else.** Junction candidates (OC-044) sit in a separate "Many-to-many" block below the tree, away from the one-to-many children they're closest to.
- **No cardinality shown.** The row says "~25 rows" or "~12 per parent", but not whether the embed becomes an object or an array. Users only find out from the dialog, or from the preview after embedding.
- **A true 1:1 becomes an array of one.** For a database foreign key, `relations.Build` and `mapping.Candidates` always create ↗ many-to-one on the referencing side and ↙ **one-to-many** on the referenced side. When the child's FK columns are unique (`cart_totals.cart_id` is its primary key, or has a unique constraint), each cart has at most one `cart_totals` row, but the embed still writes `cart_totals: [ {…} ]`. Virtual 1:1 relationships already get it right: `Build` makes the back hop many-to-one (`case CardinalityOneToOne: bk = EmbedManyToOne`). Real foreign keys don't, even though the snapshot has what's needed (`PrimaryKey`, `UniqueConstraints`, `Indexes[].Unique`, `Column.IsUnique`).

## Behaviour: what each relationship embeds as

| Group | Cardinality (from this table) | When | Embeds as | Relation `kind` |
|---|---|---|---|---|
| **References** ↗ | **N:1** | This table's FK columns aren't unique | Object (`store{…}`) | `many-to-one` |
| **References** ↗ | **1:1** | This table's FK columns are unique | Object | `many-to-one` |
| **Referenced by** ↙ | **1:N** | The other table's FK columns aren't unique | Array of objects (`cart_items[]`) | `one-to-many` |
| **Referenced by** ↙ | **1:1** | The other table's FK columns are unique | **Object** (`cart_totals{…}`). The dialog can switch it to an array | `many-to-one`, join from this table's key to the child's FK |
| **Referenced by** ↔ | **M:N** | Junction candidate (OC-044) | Array of objects (`tags[]`) | `many-to-many` |

"Unique" means the FK column set matches the primary key, a unique constraint, a unique non-expression index (in any column order), or a single column with `isUnique`. Virtual relationships (OC-042) keep their stored cardinality: the `from` side is **References** and the `to` side is **Referenced by**.

## UI changes

### Related tables panel (Canvas source side, `RelatedTree`)
```
▾ Related tables                                   15   🔗 Virtual…
  ▾ REFERENCES  2
    ↗ partners via partner_number          N:1 · ~25 rows     [Embedded]
      ▾ References
        ↗ roles via role_id                N:1                [Embed]
        ↗ stores via store_number          N:1                [Embed]
      ▾ Referenced by
        ↙ cash_drawers via partner_number  1:N · ~3 per partner  [Embed]
        ↙ partner_sessions via …           1:N · ~40 per partner ⚠ [Embed]
    ↗ stores via store_number              N:1 · ~5 rows      [Embedded]
  ▾ REFERENCED BY  13
    ↙ applied_rewards via cart_id          1:N · ~2 per cart  [Embed]
    ↙ cart_items via cart_id               1:N · ~6 per cart  [Embed]
    ↙ cart_totals via cart_id              1:1                [Embed]
    ↔ tags via cart_tags                   M:N                [Embed]
    …
```
- **Two groups at the top level**, **References** then **Referenced by**, each with a count and a collapse toggle. Both start open; the toggle state lasts while the panel is open (same as OC-152). A group with no rows stays visible with a muted line ("carts doesn't reference other tables." / "No tables reference carts."), so the layout always looks the same. A table with no relationships at all keeps today's single "No foreign keys or virtual relationships…" line.
- **Panel header count** is the total of both groups, as today.
- **Nested levels use the same split.** Under an expanded row, its hops are shown under compact sub-headings, *References* and *Referenced by* (small text, no uppercase, indented with the row). A sub-heading only appears when its group has rows. OC-152's rule is unchanged: an embedded row starts open.
- **Junctions move into Referenced by** as ↔ rows after the ↙ rows, and the separate "Many-to-many" block goes away. Payload columns and confidence stay on the row's second line. As today, junctions are only listed at the top level.
- **Cardinality chip** on each row's second line, before the estimate: `N:1`, `1:1`, `1:N` or `M:N`, with the cardinality as its accessible name ("one-to-many"). The tooltip says what it embeds as ("One stores row each, embedded as an object" / "Several cart_items rows each, embedded as an array"). 1:1 rows show no estimate. The too-many (⚠) and no-index warnings stay as they are.
- **Embed button.** The label and accessible name stay "Embed" (existing tests and e2e specs find it by that name). The shape is in its tooltip and in the row's chip, and the Embedded badge has the same tooltip. Junction rows also show "Embed" now; their accessible name stays "Embed as array of <table>".
- **Order inside a group:** by table name, then by join columns, so rows don't move after an embed.
- **Height and scrolling:** the panel shares the source column with the column list through a horizontal splitter on its top edge: drag it, use ↑/↓ when focused, or double-click to reset (default 320px, remembered in this browser; the column list keeps at least 7rem). It replaces the old fixed `max-h-72`. The list scrolls inside the panel with `overscroll-contain`, so reaching its end doesn't scroll the canvas. Group headings are sticky, so a long Referenced by list (carts has 18) keeps its heading in view.
- **Canvas fix:** the edge drawing between the panes was as tall as all the rows. Its wrapper scrolls horizontally (`overflow-x-auto`, which also makes it scroll vertically), so the whole canvas could be scrolled up, leaving empty space at the bottom. The drawing is now as tall as the canvas.
- **Accessibility:** each group is `role="group"` labelled "References" / "Referenced by" (nested ones "Tables partners references" / "Tables referencing partners"), and the heading button has `aria-expanded`. The cardinality chip has an `aria-label` ("one-to-many"). No axe violations.

### Embed dialog (`EmbedDialog`)
- The path line shows the cardinality: `Path: ↙ cart_totals (1:1)`.
- **Referenced by 1:1:** a new **Embed as** choice, *Object* (default) or *Array*. Object hides the limit, order, filter and overflow controls, as for a many-to-one. Array creates today's `one-to-many` relation.
- **References (N:1, 1:1):** unchanged: Object or Look up (OC-047).
- **1:N and M:N:** unchanged: an array with limit, order, filter and overflow (OC-045/046).

### Out of scope
- The Composer's Sources pane: OC-142 builds its "In this document" tree and should use the same two groups and chips.
- Discovery's Relationships tab: no change (cardinality chips there could follow later).
- Showing junctions below the top level, and a search filter for long lists.
- A browser end-to-end test (embed a 1:1 child from Referenced by, migrate, open the document). The unit tests cover each step: the hop's kind (AC2), the embed request (AC4), and the planner keying the child on its foreign key and keeping one row (AC4). (Descoped 2026-10-16.)

## Logic changes

### Backend
1. **Uniqueness helper.** `schema.(*Table).UniqueKey(cols []string) bool` returns true when `cols` as a set equals `PrimaryKey`, an entry of `UniqueConstraints`, or the columns of an `Indexes[]` entry with `Unique` and no `"expr"` part, or when it's a single column with `IsUnique`. **Partial unique indexes:** discovery used to report them as `unique` (`i.indisunique`), although they don't guarantee one row per key. The index query now reads `i.indisunique AND i.indpred IS NULL`, which also stops a partial index from marking a column `isUnique`. Saved snapshots pick this up when they're next scanned. The seed's golden snapshot is unchanged.
2. **Hops carry direction and cardinality** (`relations.Hop`, `relations.Build`):
   - New fields: `Direction` (`references` | `referencedBy`) and `Cardinality` (`one-to-one` | `many-to-one` | `one-to-many`, as seen from `From`). `Kind` stays the **embed shape**, which is what relations, the runtime and the web already use.
   - FK `t.fk → ref`: forward hop `Kind many-to-one`, `Direction references`, `Cardinality` is `one-to-one` if `t.UniqueKey(fk.Columns)`, otherwise `many-to-one`. Back hop `Direction referencedBy`: if unique, `Kind many-to-one` and `Cardinality one-to-one`, otherwise `Kind one-to-many` and `Cardinality one-to-many`. The join is the same in both cases (`parent = ref column`, `child = fk column`).
   - Virtual relationships: the forward hop is `references` and the back hop is `referencedBy`. Cardinality comes from the record, and the kinds stay as they are today.
   - `add()`'s duplicate check is unchanged (it compares `To`, `Kind` and `Join`).
3. **Level-1 candidates** (`mapping.Candidates`): the same direction, cardinality and kind rule, using the same helper. `rowsPerParent` is 1 for 1:1, and the result is sorted by direction, then table, then join.
4. **API** (`api/openapi.yaml`): add `direction` (`RelationDirection`) and `cardinality` (`RelationCardinality`) to `RelationHop` and `EmbedCandidate`. The server always sets them, but the schema leaves them optional, and the web derives them from `kind` when they're missing (a cached response, or a test fixture). Requests don't change. `web/src/lib/api/schema.d.ts` is regenerated. `JunctionCandidate` needs no change, since the web treats it as Referenced by / M:N.
5. **Relation and runtime: no change needed.** A Referenced by 1:1 embedded as an object is a plain `many-to-one` relation whose `join.child` is the child's FK column, not its key. `automap.addOne` only checks that the join columns exist. The planner (`related.Plans`) queries by `join.child` for any kind and keeps one row for a many-to-one, so the document gets one object (null or omitted without a row). This is the same path virtual 1:1 relationships already use.
6. **No mapping migration.** Existing relations keep their kind. A 1:1 that was already embedded as a `one-to-many` array stays an array.

### Web
1. **Types:** `Node.hop` in `RelatedTree` gains `direction` and `cardinality` from `RelationHop` and `EmbedCandidate` (`toHop`, candidate mapping). `EmbedHop` gains an optional `cardinality`.
2. **Grouping:** `web/src/features/mapping/relatedGroups.ts` has `groupByDirection(nodes)` → `{ references, referencedBy }` (used at the root and for each node's children; junctions are appended to Referenced by at the root), `directionOf(hop)` (the server values or the fallback from `kind`) and the chip table.
3. **Embedded detection** (`relationFor` / `embeddedPath`): for a Referenced by 1:1 hop (`kind many-to-one`), also count an existing relation with `kind one-to-many` and the same table and join (an array embedded earlier) as embedded. Otherwise, mappings made before this story would show "Embed" for something already embedded.
4. **Embed plan:** `embedPath` passes the hop's kind through unchanged, so a 1:1 back hop becomes `many-to-one`. If the dialog's *Embed as → Array* is chosen, the last hop is sent as `one-to-many`.
5. **i18n** (`en.json`, `embed.*`): `references`, `referencedBy`, `referencesOf`, `referencedByOf`, `noReferences`, `noReferencedBy`, `card.*` (chip aria labels), `shapeOf.*` (tooltips), `kindOneToOne`, `embedAs`, `embedAsObject`, `embedAsArray`. `junctions` stays (still used by the composer); `embedArrayShort` is removed.

## Acceptance criteria & unit tests

### AC1 — Two groups, at every level
**Given** `carts` with FKs to `partners` and `stores`, 12 tables referencing it and a `cart_tags` junction, **then** Related tables shows **References (2)**: ↗ partners and ↗ stores, and **Referenced by (13)**: the twelve ↙ rows and ↔ tags via cart_tags. There's no separate Many-to-many block. **Given** partners is embedded, **then** under it *References* lists ↗ roles and ↗ stores, and *Referenced by* lists ↙ cash_drawers and ↙ partner_sessions. **Given** a table with no FKs, **then** References shows "… doesn't reference other tables".

| Test | Location | Verifies |
|---|---|---|
| `relatedGroups.test.ts` › `OC-157 AC1: splits rows by direction, keeping their order` | `web/src/features/mapping/relatedGroups.test.ts` | Pure grouping, ordering |
| `RelatedTree.test.tsx` › `OC-157 AC1/AC3: References and Referenced by, each with cardinality chips` | `web/src/features/mapping/RelatedTree.test.tsx` | Root groups, count, collapse |
| `RelatedTree.test.tsx` › `OC-157 AC1: hops under an embedded row are grouped too, and junctions are Referenced by` | same | Nested groups, junction placement |
| `RelatedTree.test.tsx` › `OC-157 AC1: says when a group is empty` | same | Empty group text |

### AC2 — Cardinality from keys
**Given** `cart_totals.cart_id` is both the primary key and the FK to `carts`, and `cart_items.cart_id` isn't unique, **then** `carts`' hops are ↙ cart_totals `kind many-to-one, cardinality one-to-one, direction referencedBy` and ↙ cart_items `kind one-to-many, cardinality one-to-many`, and `cart_totals`' hop to carts is `cardinality one-to-one, direction references`. **Given** the uniqueness comes from a unique constraint, or from a unique index with its columns in another order, **then** it's also 1:1. **Given** an expression or partial unique index, **then** it isn't.

| Test | Location | Verifies |
|---|---|---|
| `TestUniqueKey` | `backend/internal/schema/schema_test.go` | PK, constraint, index order, expression, single column |
| `TestBuild_OC157_AC2_OneToOneForeignKeys` | `backend/internal/relations/graph_test.go` | Hop kind, cardinality, direction |
| `TestCandidates_OC157_AC2` | `backend/internal/mapping/embeds_test.go` | Level-1 candidates, sort, `rowsPerParent` 1, AC5 `embedId` |
| `relatedGroups.test.ts` › `OC-157 AC2: uses the server direction and cardinality, else derives them from the kind` | `web/src/features/mapping/relatedGroups.test.ts` | Fallback |
| `TestDiscover_PartialUniqueIndexNotUnique` | `backend/internal/connectors/source/postgres/discover_integration_test.go` | Partial vs full unique index (`-tags integration`) |

### AC3 — Chips and shape on every row
**Given** AC2's tables, **then** the rows show `N:1 · ~25 rows`, `1:N · ~6 per row`, `1:1` (no estimate) and `M:N`, each chip named by its cardinality. The Embed button's tooltip gives the shape ("At most one cart_totals row each, embedded as an object").

| Test | Location | Verifies |
|---|---|---|
| `RelatedTree.test.tsx` › `OC-157 AC1/AC3: References and Referenced by, each with cardinality chips` | `web/src/features/mapping/RelatedTree.test.tsx` | Chips, estimates, tooltip |

### AC4 — Embedding produces the right shape
**Given** AC2, **when** I embed ↙ cart_totals with the defaults, **then** a `many-to-one` relation with join `id = cart_id` is created and the preview shows `cart_totals: { … }` (an object, null when the cart has none). **When** I choose *Embed as → Array* instead, **then** it's a `one-to-many` relation with `cart_totals: [ … ]`. **When** I embed ↙ cart_items, ↔ tags or ↗ stores, **then** I get an array, an array and an object, as today.

| Test | Location | Verifies |
|---|---|---|
| `EmbedDialog.test.tsx` › `OC-157 AC4: a one-to-one from the referenced side embeds as an object by default, an array on request` | `web/src/features/mapping/EmbedDialog.test.tsx` | Dialog choice and request |
| `TestPlans_OC157_ReverseOneToOneIsOneObject` | `backend/internal/related/related_test.go` | Queried by the child's FK, one row kept: an object |

### AC5 — Mappings made before this story
**Given** a mapping that embedded `cart_totals` as a `one-to-many` array before this story, **then** the ↙ cart_totals row shows **Embedded**, and the mapping and its output don't change.

| Test | Location | Verifies |
|---|---|---|
| `RelatedTree.test.tsx` › `OC-157 AC5: a 1:1 embedded earlier as an array shows as embedded` | `web/src/features/mapping/RelatedTree.test.tsx` | |

### AC6 — Virtual relationships
**Given** a virtual relationship `carts.loyalty_code → loyalty_accounts.code` with cardinality one-to-one, **then** carts lists it under References as `1:1` with the Virtual badge, and loyalty_accounts lists carts under Referenced by as `1:1`, embedded as an object.

| Test | Location | Verifies |
|---|---|---|
| `TestBuild_OC157_AC6_VirtualDirection` | `backend/internal/relations/graph_test.go` | |

### AC7 — Accessible
**Given** the panel, **then** groups are labelled `role="group"` regions with `aria-expanded` headings, and it has no axe violations.

| Test | Location | Verifies |
|---|---|---|
| `RelatedTree.test.tsx` › `OC-157 AC7: the grouped panel has no axe violations` | `web/src/features/mapping/RelatedTree.test.tsx` | |
| `RelatedTree.test.tsx` › `the pane fills its height, scrolls on its own and is resized from its top edge` | same | Splitter, remembered height |
| `resize-handle.test.tsx` › `a top handle resizes the height…` | `web/src/components/ui/resize-handle.test.tsx` | Vertical drag and keys |

## Risks and open questions
- **Composite FKs** are only 1:1 when the whole column set is unique. A unique index on a subset doesn't count.
- **Changing the default shape:** a user who embeds a 1:1 again after this story gets an object where they used to get an array. That's intended. Existing mappings don't change (AC5).
