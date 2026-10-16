# OC-150 — See and change the bucket and scope each mapping writes to

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.0 | P1 | 3 | OC-013, OC-036, OC-068 | Implemented |

## User story
As a **data engineer** editing a project's Couchbase target, I want to see which bucket and scope my mappings write to and move them, so that a cluster change or a mistyped bucket doesn't leave mappings writing to different places without my noticing.

## Background (2026-10-15)
Bucket and scope are chosen per mapping in the Tables step and can't be changed afterwards in the UI (the API already accepts a new target on save). The Target step showed only the connection. In a trial, two mappings wrote to a mistyped bucket `tar` and one to `target`; the only way to see it was the run review.

## Scope
**In:**
- The Target step of a Couchbase project shows **Where your mappings write**: mappings grouped by bucket and scope, each with its source table (linking to the mapping) and collection. Shown while viewing and while editing the connection.
- When mappings write to more than one bucket/scope pair, a warning says how many.
- Editors can **Change** a group: the bucket → scope picker of the Tables step (live buckets and scopes, "+ New" by permission) moves every mapping in the group, keeping each collection name; missing collections are created when the migration starts.
- Saves go through the existing mapping PUT (with If-Match), one mapping at a time.

**Out:** an all-or-nothing move (a failure part-way leaves the earlier mappings moved; the card then shows where each one is); moving directory-target mappings; changing a single mapping's collection here.

## Acceptance criteria & unit tests

### AC1 — Where mappings write
**Given** mappings in `travel-sample.inventory` and `tar.oe`, **then** the card lists both groups with their tables and collections and warns about 2 pairs.

### AC2 — Move a group
**Given** the `tar.oe` group of two mappings, **when** I pick `travel-sample` / `inventory` and confirm, **then** both mappings are saved with that bucket and scope and their own collection names.

| Test | Location | Verifies |
|---|---|---|
| `MappingLocations.test.tsx` › `shows the bucket and scope each mapping writes to…` | `web/src/features/connections/MappingLocations.test.tsx` | AC1 |
| `MappingLocations.test.tsx` › `moves a group to another bucket and scope…` | same | AC2 |
