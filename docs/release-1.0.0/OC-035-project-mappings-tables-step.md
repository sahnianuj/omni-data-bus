# OC-035 — Project mappings API & Tables step

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 5 | OC-007, OC-010, OC-013, OC-015, OC-017 | Implemented |

## User story
As a **data engineer**, I want to pick one or more source tables in my project and get a mapping for each, pre-filled with a generated document design and auto-mapped fields, so that I start the design-and-map step from a high score instead of a blank page.

> Split out of OC-007 so the project skeleton could ship before discovery, document design and auto-mapping existed.

## Implementation details

### API
| Method | Path | Notes |
|---|---|---|
| GET | `/projects/{id}/mappings` | Summary: table, keyspace, status, automapStats |
| POST | `/projects/{id}/mappings` | `{sourceSchema, sourceTable, target:{bucket,scope,collection,createIfMissing}}` creates the mapping with an **auto-generated doc schema and auto-map already applied** (OC-015 generate plus OC-017). Requires both repositories to be saved (422 `PROJECT_REPOSITORIES_INCOMPLETE`) |
| POST | `/projects/{id}/mappings:bulk` | Same, for many tables at once |
| GET/PUT/DELETE | `/projects/{id}/mappings/{mid}` | PUT requires `If-Match` (version) |

- **Mapping status** is computed, not stored: `draft` (validation errors), `ready` (valid; warnings allowed), `stale` (the source snapshot diff touches mapped columns).
- **Project status** gains `ready` (≥ 1 mapping and all mappings ready).
- Mappings are stored in the `mappings` collection (`workspaceId`, `projectId`, `version`), deleted with the project.

### Tables step (`/projects/{id}/tables`)
A schema tree with checkboxes (multi-select) using the project's stored source, with row estimate and PK badge. A *Target* column per selected table uses KeyspacePicker defaults on the project's stored target: bucket and scope are chosen once, and the collection name defaults to the table name (sanitized, e.g., `Order Details` → `order_details`) and is editable per row.

## Acceptance criteria & unit tests

### AC1 — Multi-table selection creates mappings with defaults
**Given** the tables `customer` and `Order Details` selected with bucket `shop` and scope `sales`, **then** two mappings are created with collections `customer` and `order_details`. Each gets a generated doc schema and an auto-map result, and the response includes `automapStats` for each.

| Test | Location | Verifies |
|---|---|---|
| `TestSanitizeCollectionName_AC1` | `internal/mappings/service_test.go` | Spaces, uppercase, leading `_`, length > 251, unicode |
| `TestBulkCreateMappings_AC1_GeneratesSchemaAndAutomap` | `internal/mappings/service_test.go` | Fake generator and automapper called per table |
| `TablesStep.test.tsx` › `AC1: per-row target collection editable` | `src/features/projects/steps/TablesStep.test.tsx` | |

### AC2 — Mappings require saved repositories
**Given** `POST /projects/{id}/mappings` before both repositories exist, **then** 422 `PROJECT_REPOSITORIES_INCOMPLETE`.

| Test | Location | Verifies |
|---|---|---|
| `TestMappingsCreate_AC2_RequiresRepositories` | `internal/mappings/service_test.go` | |

### AC3 — Mapping and project status derivation
**Given** a mapping with no key template → `draft`. **Given** a valid mapping → `ready`. **Given** the latest snapshot diff removing a mapped column → `stale`. **Given** both repositories tested and all mappings ready → project `ready`.

| Test | Location | Verifies |
|---|---|---|
| `TestMappingStatus_AC3_Table` | `internal/mapping/validate_test.go` | |
| `TestProjectStatus_AC3_ReadyWithMappings` | `internal/mappings/service_test.go` | |

### AC4 — Optimistic concurrency
**Given** two tabs editing the same mapping, **when** the second saves with an outdated `If-Match`, **then** 412 `MAPPING_VERSION_CONFLICT` is returned, and the UI shows a conflict banner ("Someone else changed this mapping…") with *Reload* / *Overwrite* options.

| Test | Location | Verifies |
|---|---|---|
| `TestMappingsRepo_AC4_VersionMismatch412` | `internal/mappings/service_test.go` | |
| `useMappingAutosave.test.ts` › `AC4: surfaces conflict and supports overwrite` | `src/features/mapping/useMappingAutosave.test.ts` | |

### AC5 — Add to project mappings from the schema explorer (moved from OC-011 AC7)
**Given** a table selected in the schema explorer without a mapping, **when** the user clicks *+ Add to project mappings*, **then** the Tables step opens with that table preselected and its default collection filled in; the mapping is created from there (`POST …/mappings:bulk`) once the target bucket and scope are chosen.

> Changed during implementation: creating the mapping straight from the explorer would need a target keyspace the user hasn't picked yet, so the button hands off to the Tables step instead. **Given** a mapping already exists for the table, **then** the button reads *Open mapping →* and navigates to its workbench.

| Test | Location | Verifies |
|---|---|---|
| `SchemaExplorerPage.test.tsx` › `AC5: adds mapping and navigates to tables step` | `src/features/discovery/SchemaExplorerPage.test.tsx` | |
| `SchemaExplorerPage.test.tsx` › `AC5: existing mapping shows Open mapping` | `src/features/discovery/SchemaExplorerPage.test.tsx` | |

### AC6 — Deferred target creation is persisted (moved from OC-014 AC5)
**Given** the user picks *Create when migration starts* for a new collection in the Tables step, **then** the mapping stores `targetCreateIfMissing: true` with the planned keyspace, and no SDK create is called until the job's pre-flight (OC-023).

| Test | Location | Verifies |
|---|---|---|
| `TestMappingSpec_AC6_CreateIfMissingPersisted` | `internal/mapping/validate_test.go` | |

