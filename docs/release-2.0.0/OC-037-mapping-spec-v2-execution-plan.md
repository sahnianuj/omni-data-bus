# OC-037 — Mapping spec v2 (relation tree) & shared execution plan

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E7 Foundations 2.0 | P0 | 5 | OC-021, OC-022, OC-025 | Not started |

## User story
As a **developer on Omni-Data-Bus**, I want one mapping model that can express nested and many-to-many relations, compiled into one execution plan used by the migration pipeline, the preview and the headless runner, so that relationship mapping (OC-043 – OC-046), the composer (OC-047) and exported scripts (OC-051) all build identical documents.

## Scope
**In:**
- `specVersion: 2` with `relations[]` (a tree through `parentId`), `aggregates[]` and `mode`, as defined in [architecture §6.1](00-architecture.md#61-mapping-spec-v2-mappingspec).
- A lossless upgrade from v1 (`embeds[]`) on read, rewritten as v2 on the next save.
- `internal/plan`: `Compile(spec, snapshot) → ExecutionPlan`, used by the pipeline, the preview and `odb run`.
- The 1.0 `embed` behavior reproduced exactly by v2 relations at depth 1.

**Out:** the new relation kinds and shapes themselves. The model accepts them, but the resolver supports them only as each story lands (OC-043 depth, OC-044 many-to-many, OC-045 shapes, OC-046 strategies). Until then, validation rejects them with `RELATION_UNSUPPORTED`.

## Implementation details

### Model (`internal/mapping/spec_v2.go`)
- `Relation{ID, ParentID, Kind, Table, Join, Via, TargetFieldID, LookupOnly, Shape, OrderBy, Limit, Filter, Overflow, Strategy, Empty}`.
- `SourceRef{Relation, Column, From}`. JSON input also accepts `embed` as an alias for `relation`, which is how 1.0 clients and old bundles send it.
- `Spec.Version()` returns 1 when `specVersion` is absent.

### Upgrade (`internal/mapping/upgrade.go`)
- `UpgradeV1(spec) Spec`: each embed → a relation with `parentId: null`, the same ID, kind, join, orderBy, limit and empty. `sources[].embed` → `relation`. Location from the 1.0 target refs (OC-036).
- The upgrade runs in the repository `Get`. The document is written back only when the user saves it, so a read never bumps the CAS and invalidates the ETag.
- It is idempotent: `UpgradeV1(UpgradeV1(x)) == UpgradeV1(x)`.

### Execution plan (`internal/plan`)
```go
type ExecutionPlan struct {
    Root      TableRef
    Columns   []string            // root columns actually needed (projection pushdown)
    Relations []RelationStep      // topologically ordered: parents before children
    Program   *transform.Program  // compiled field programs per scope
    Key       *mapping.KeyTemplate
    Hash      string              // sha256 of canonical plan, recorded on jobs and bundles
}
type RelationStep struct {
    Relation mapping.Relation
    Depth    int
    Columns  []string            // child columns needed by mappings, joins and order-by
    Strategy string              // resolved: batched | sorted-merge (OC-046)
}
```
- Projection pushdown: only columns referenced by mappings, keys, joins, order-bys, filters and aggregates are selected. 1.0 selected every mapped column of the root and embeds; this now covers relations at every depth.
- The pipeline's `ResolveEmbeds` stage becomes `ResolveRelations(plan)`. The preview (OC-022) and `odb run` (OC-051) call the same function.
- `Plan.Hash` goes on `jobs.options.planHash` and on bundles, which is how the runner parity test (OC-056) compares runs.

### Validation (extends OC-022)
- `parentId` references an existing relation, and there are no cycles.
- Depth ≤ `OC_MAX_RELATION_DEPTH`.
- Every `sources[].relation` exists and is in scope for the field (scope rule in the architecture doc; enforced fully in OC-047).
- Kinds not yet supported by the resolver → `RELATION_UNSUPPORTED`.

## Acceptance criteria & unit tests

### AC1 — v1 upgrade is lossless
**Given** every 1.0 mapping fixture in `testdata/specs/v1/*.json`, **when** upgraded, **then** it matches `testdata/specs/v2/*.json`, and the upgrade is idempotent.

| Test | Location | Verifies |
|---|---|---|
| `TestUpgradeV1_AC1_Golden` | `internal/mapping/upgrade_test.go` | |
| `TestUpgradeV1_AC1_Idempotent` | same | |

### AC2 — Byte-identical documents after upgrade
**Given** the 1.0 golden migration (customer + address m2o + orders o2m), **when** run from the upgraded spec, **then** every document is byte-identical to the 1.0 output.

| Test | Location | Verifies |
|---|---|---|
| `TestPlan_AC2_V1ParityGolden` *(integration)* | `internal/pipeline/plan_parity_integration_test.go` | |

### AC3 — Reads don't bump the version
**Given** a stored v1 mapping, **when** it is fetched twice, **then** the ETag is unchanged. **When** it is saved, **then** it is stored as v2.

| Test | Location | Verifies |
|---|---|---|
| `TestMappingsRepo_AC3_UpgradeOnReadNoWrite` *(integration)* | `internal/store/mappings_integration_test.go` | |

### AC4 — Plan compiler
**Given** a spec with relations `a` (root child) and `b` (child of `a`), **then** the plan orders `a` before `b`, and each step's columns include only the needed columns. **Given** a cycle, **then** `RELATION_CYCLE`.

| Test | Location | Verifies |
|---|---|---|
| `TestCompile_AC4_TopologicalOrder` | `internal/plan/compile_test.go` | |
| `TestCompile_AC4_ProjectionPushdown` | same | |
| `TestCompile_AC4_CycleRejected` | same | |
| `TestCompile_AC4_HashStable` | same | Map iteration order doesn't change the hash |

### AC5 — Client accepts both versions
**Given** the web client loads a v1 mapping from an older cached response, **then** it upgrades it with the TS mirror of `UpgradeV1`, which matches the Go fixtures.

| Test | Location | Verifies |
|---|---|---|
| `upgradeSpec.test.ts` › `AC5: matches server fixtures` | `src/features/mapping/upgradeSpec.test.ts` | Shared `testdata/specs` |
