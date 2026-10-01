# OC-068 — Move mappings when the target kind changes

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E8 Directory Target | P1 | 3 | OC-036, OC-038 | Implemented |

## User story
As a **data engineer**, I want to switch a project's target between a directory and Couchbase after I've created mappings, so that I don't have to delete and rebuild my mappings to try the other target.

## Background (2026-10-01)
The user changed a project's target from a directory to Couchbase and got `409 TARGET_KIND_IN_USE`: "Clear their locations or delete them first." OC-036 AC3 blocks the change on purpose, because a folder means nothing to Couchbase. But the app can't clear or edit a mapping's location. Locations are only set when the Tables step creates a mapping. So the only way out was to delete every mapping and lose its work.

## Scope
**In:**
- `PUT /projects/{id}/connections/target` takes an optional `relocate`. With a kind change, it moves every mapping whose location is of the old kind to the new kind, then saves the target:
  - **To Couchbase**: `relocate.bucket` and `relocate.scope` are required. The collection is the old folder's last segment, made a valid collection name (`naming.CollectionName`, the Tables step's rule).
  - **To a directory**: the folder is the old collection name, made a valid folder name.
  - The new location has `createIfMissing: true`, which creates only what doesn't exist yet.
- Each moved mapping is audited as `mapping.relocate` with its old and new locations.
- Without `relocate`, the 409 stays (OC-036 AC3). Its detail now points to the Target step's move option instead of asking to clear locations.
- Target step: on 409 `TARGET_KIND_IN_USE`, a *Move mappings* dialog lists each mapping with its old and new location. For Couchbase it asks for a bucket and scope (the Tables step's picker). *Move and save* re-saves with `relocate`.

**Out:** editing one mapping's location after it's created. A job that already ran keeps its output where it went.

## Acceptance criteria & unit tests

### AC1 — Directory to Couchbase
**Given** mappings in folders `order_details/` and `customer/`, **when** the target is saved as Couchbase with `relocate {bucket: shop, scope: sales}`, **then** the target is Couchbase and the mappings write to `shop.sales.order_details` and `shop.sales.customer` with `createIfMissing`.

| Test | Location | Verifies |
|---|---|---|
| `TestSave_RelocateToCouchbase_OC068_AC1` | `backend/internal/connections/handlers_test.go` | |

### AC2 — Couchbase to a directory
**Given** a mapping in `shop.sales.order_details`, **when** the target is saved as a directory with `relocate {}`, **then** the mapping writes to folder `order_details/`.

| Test | Location | Verifies |
|---|---|---|
| `TestSave_RelocateToDirectory_OC068_AC2` | same | |

### AC3 — Still guarded
**Given** mappings with locations, **when** the kind changes without `relocate`, **then** `409 TARGET_KIND_IN_USE` and nothing changes. **When** `relocate` to Couchbase has no bucket or scope, **then** `422 LOCATION_INVALID` and nothing changes.

| Test | Location | Verifies |
|---|---|---|
| `TestSave_RelocateGuard_OC068_AC3` | same | |

### AC4 — Move from the Target step
**Given** the save answers `TARGET_KIND_IN_USE`, **then** the *Move mappings* dialog lists the mappings with their new locations, and *Move and save* re-saves with `relocate`.

| Test | Location | Verifies |
|---|---|---|
| `RelocateDialog.test.tsx` › `OC-068 AC4: move and save` | `web/src/features/connections/RelocateDialog.test.tsx` | |
| `RelocateDialog.test.tsx` › `OC-068: new locations mirror the server` | same | |

## Implementation notes (2026-10-01)
- `connections.Service.Save` replaces `kindChangeAllowed` with `kindChange`. It returns the moves, each checked with `mapping.ValidateLocation` for the new kind. Nothing is written until every move and the connection itself are valid. Then the mappings are updated, then the connection is upserted. If the upsert fails after the moves, re-saving with `relocate` finishes the job, because mappings already of the new kind are skipped.
- `relocated` takes the old location's leaf name: the collection, or the folder's last segment. It falls back to the source table. That name goes through `naming.CollectionName` or `naming.FolderName`. `SanitizeCollectionName` moved from `mappings` to `naming.CollectionName`. `naming.FolderName` drops `%`, which folders don't allow, and caps the name at 64 bytes. New directory mappings in the Tables step and on the server use it too.
- The new Couchbase connection isn't saved when the dialog opens, so its buckets can't be browsed. The dialog takes the bucket and scope as text, with scope prefilled as `_default`. Preflight catches a bucket that doesn't exist.
- Web: `useSaveConnection` sends `relocate` and refreshes the mapping list after a move. `RepositoryStep` opens `RelocateDialog` on a target `TARGET_KIND_IN_USE`, and a failed move shows its error inside the dialog. The stale, unused `targetKinds.inUse` string was removed.
- Audit: the new action `mapping.relocate` has metadata `{projectId, from, to}`.
