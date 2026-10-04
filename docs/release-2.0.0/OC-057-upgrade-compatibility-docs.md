# OC-057 — Upgrade from 1.0, compatibility & documentation

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.0 | P0 | 3 | OC-036, OC-037, OC-055 | Implemented |

## User story
As an **operator running Omni-Data-Bus 1.0**, I want to upgrade to 2.0 in place without losing projects, mappings or job history, and to find clear documentation for the new features, so that my teams can adopt directory targets, relationship mapping, the composer and exported scripts right away.

## Scope
**In:**
- Metadata provisioning version bump: new collections and indexes (`relationships`, `exports`) and new index fields on `jobs` (`origin`).
- Rolling upgrade behavior: 1.0 and 2.0 pods side by side during a Helm upgrade.
- API compatibility: 2.0 is additive under `/api/v1`, and the keyspace aliases stay (OC-036).
- Helm chart values for the directory target volume (OC-038).
- User guide chapters and upgrade notes.

**Out:** downgrade from 2.0 to 1.0 once a mapping has been saved as v2 (documented as unsupported; take a metadata backup first).

## Implementation details

### Provisioning (`internal/store` provisioning, OC-003)
- Version `N+1` creates `relationships` and `exports` with their indexes (`(workspaceId, projectId, createdAt DESC)`), and adds `origin` to the `jobs` list index.
- It is idempotent and runs as the Helm pre-upgrade hook (`odb admin migrate`), as in 1.0.
- No bulk rewrite of mappings: v1 specs upgrade on read and save (OC-037), so the upgrade finishes in seconds regardless of data volume.

### Rolling upgrade
- During the upgrade, 1.0 workers can still claim jobs. A job created by 2.0 that uses a v2-only feature (a directory target, depth > 1, m2m, composed) sets `jobs.minWorkerVersion = "2.0.0"`, and the claim query filters on it, so 1.0 workers skip such jobs. 1.0 pods keep serving 1.0 features, so the service stays available throughout.
- A 1.0 API pod reading a v2 mapping returns `409 MAPPING_NEWER_VERSION` instead of corrupting it on save. This is a patch for 1.0.x (1.0.1), and the upgrade notes require 1.0.1 before 2.0.

### Documentation (`docs/user-guide/`)
- New chapters: *Directory targets* (layouts, file names, lookup by key, volumes in containers), *Relationship mapping* (nested, many-to-many, shapes, large arrays), *Document composer*, *Running migrations outside the tool* (the script, credential modes, exit codes, resuming, importing reports).
- `docs/upgrade/1.0-to-2.0.md`: prerequisites (1.0.1, backup), Helm values diff, rollback notes.
- The README feature list and screenshots are updated.

## Results (2026-10-02)
- **Provisioning, now schema version 6.**
  - Step 4 creates `relationships` (OC-042) and step 5 creates `exports` (OC-055). Both collections are KV-only, so they need no GSI index.
  - Step 6 adds `idx_jobs_claim` on `jobs(status, IFMISSINGORNULL(minWorkerLevel, 0), createdAt, id)` for the version-aware claim loop.
  - Every step is idempotent, and nothing bulk-rewrites mappings: v1 specs upgrade on read and save.
- **Mixed-version workers.**
  - A job that needs a 2.0 worker records `minWorkerVersion: "2.0.0"` and `minWorkerLevel`. That means any of: a directory target, a composed document, a relation, grouped rows or an aggregate (`mapping.RequiresV2`).
  - Workers claim through `ListClaimable(level)`, so older workers skip such jobs. Development builds claim everything.
- **Newer specs.** A mapping whose spec version is newer than the server reads is refused with `409 MAPPING_NEWER_VERSION` instead of being rewritten. 2.0 has the guard for future versions, and the 1.0.1 patch needs the same guard.
- **Helm.**
  - New `exports.image` and `exports.downloadUrl` values set what exported scripts run and download (default: the deployed image and this release's binaries).
  - From OC-056: `worker.memoryLimitMB` (`OC_MEMORY_LIMIT_MB`).
  - The directory-target volume values come from OC-038.
- **Docs.**
  - [`docs/user-guide/`](../user-guide/README.md) has four chapters: directory targets, relationship mapping, the document composer, and running migrations outside the tool.
  - [`docs/upgrade/1.0-to-2.0.md`](../upgrade/1.0-to-2.0.md) covers the upgrade.
  - The README documentation map links both.
  - In-app **?** links sit next to the Target step's directory option, the Embed dialog, the Composer and the Export page (`HelpLink`, `lib/helpLinks.ts`).

### Open
- **The 1.0.1 patch** can't be built in this repository, which carries 2.0. On the 1.0 line it must backport two things: the claim filter (`minWorkerLevel` and `ListClaimable`, so 1.0.1 workers skip 2.0 jobs) and the `MAPPING_NEWER_VERSION` guard. The upgrade notes require 1.0.1 before 2.0.
- The README screenshots still show 1.0.

### Changes from the plan
- The 1.0 fixture isn't a bucket dump. `TestProvision_AC1_UpgradeFrom1_0` and `TestUpgrade_AC2_DataPreserved` (`internal/store/cbstore/upgrade_integration_test.go`) provision a fresh scope with 1.0's steps (1–3), seed it as 1.0 did, then upgrade. Both pass on Couchbase 8.0.3.
- `TestClaim_AC3_MinWorkerVersion` is a unit test (`internal/jobs/claim_test.go`, memstore). The claim query's contract runs on Couchbase as `storetest` "Claimable".
- The jobs list didn't need an `origin` index field: no query filters on it. External runs are listed with the rest.

## Acceptance criteria & unit tests

### AC1 — Provisioning upgrade
**Given** a metadata bucket provisioned by 1.0, **when** `odb admin migrate` runs, **then** the new collections and indexes exist, `system.schema` shows the new version, and a second run makes no changes.

| Test | Location | Verifies |
|---|---|---|
| `TestProvision_AC1_UpgradeFrom1_0` *(integration)* | `internal/store/provision_integration_test.go` | Starts from a 1.0 fixture bucket dump |

### AC2 — Data preserved
**Given** a 1.0 fixture with projects, mappings (with embeds) and completed jobs, **then** after the upgrade all are listed and readable, and re-running a 1.0 mapping produces identical documents (OC-037 AC2).

| Test | Location | Verifies |
|---|---|---|
| `TestUpgrade_AC2_DataPreserved` *(integration)* | `internal/store/upgrade_integration_test.go` | |

### AC3 — Mixed-version workers
**Given** a 2.0 job with a directory target and a 1.0.1 worker polling, **then** the 1.0.1 worker never claims it, while 1.0-compatible jobs are still claimed by either version.

| Test | Location | Verifies |
|---|---|---|
| `TestClaim_AC3_MinWorkerVersion` *(integration)* | `internal/jobs/claim_integration_test.go` | |

### AC4 — Docs complete
**Given** the docs build, **then** each new chapter exists, and each links from the in-app help icon of its feature (Target step, Embed dialog, Composer, Export tab).

| Test | Location | Verifies |
|---|---|---|
| `helpLinks.test.ts` › `AC4: every help link resolves` | `src/lib/helpLinks.test.ts` | |
