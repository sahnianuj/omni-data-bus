# OC-007 — Migration projects, project-owned repositories & guided workflow

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E2 Projects & Source | P0 | 5 | OC-003, OC-004, OC-005, OC-006 | Implemented |

## User story
As a **data engineer**, I want to create a migration project first, then set up its source and target repositories inside it (testing each one as I configure it and storing the credentials on the project once and for all), and then be walked through tables, document design, mapping, and migration, so that a complex migration feels like a clear sequence of steps I can leave and resume without re-entering anything.

## Concepts
- A **project** is the top-level unit of work. It **owns** exactly one **source repository** and one **target repository**: `connections` documents keyed `<projectId>::<role>`, see [architecture §5](00-architecture.md#5-domain-model-metadata-db).
  - Credentials are entered once, tested, sealed (OC-006), and reused by every discovery, preview, keyspace operation, mapping, and job in the project.
  - There is no workspace-level connection library in 1.0.
- A project contains one or more **table mappings**. Each mapping is one source table → one target collection with one document design (`mappings` row, spec in [architecture §6.2](00-architecture.md#62-mapping-spec-mappingsspec-stored-in-mappingsspec)).
- **Jobs** run one or more mappings of a project (OC-023).

This story delivers the project entity, the wizard shell, the step logic, and the dashboard. Table selection and the mapping API (steps 3–4 content) are OC-035. The forms inside the *Source* and *Target* steps are delivered by OC-009 (PostgreSQL) and OC-012 (Couchbase), which plug into the step slots defined here.

## Implementation details

### API
| Method | Path | Notes |
|---|---|---|
| GET | `/projects` | Paged. Includes `{source?:{type,displayName,status}, target?:{…}, mappings, readyMappings, lastJob}` |
| POST | `/projects` | `{name, description?}`. Name unique per workspace. Returns 201; the project starts in `draft` with no repositories |
| GET/PATCH/DELETE | `/projects/{id}` | DELETE cascades to connections (sealed secrets), snapshots, mappings, and jobs. It is blocked while a job is running (409 `PROJECT_HAS_RUNNING_JOB`) and requires typed-name confirmation in the UI |
| GET | `/projects/{id}/connections` | `{source?, target?}` without secrets (connection endpoints are defined in OC-009 / OC-012) |

- Mapping endpoints and mapping status are in OC-035.
- **Project status** is derived: `draft` (a repository is missing or untested) → `configured` (both repositories tested OK) → `ready` (≥ 1 mapping and all mappings ready) → `running` / `completed` / `failed` (from the latest job).

### Wizard (`/projects/{id}` → stepper)
| Step | Route | Content | Completion rule |
|---|---|---|---|
| 1 Source repository | `…/source` | ConnectorPicker, then the PostgreSQL form with **Test connection** and **Save to project** (OC-009). After save: summary card with *Test connection*, *Edit*, and *Explore schema →* | Source saved and last test OK |
| 2 Target repository | `…/target` | Couchbase form with **Test connection** and **Save to project** (OC-012). After save: summary card with capabilities, *Test connection*, and *Edit* | Target saved and last test OK |
| 3 Tables | `…/tables` | Schema tree with checkboxes (multi-select) using the project's stored source, with row estimate and PK badge. A *Target* column per selected table uses KeyspacePicker defaults on the project's stored target: bucket and scope are chosen once, and the collection name defaults to the table name (sanitized, e.g., `Order Details` → `order_details`) and is editable per row | ≥ 1 mapping |
| 4 Design & Map | `…/mappings` | Mapping cards (table → keyspace, score ring, status). Clicking one opens the **Mapping workbench** (OC-015/019) | All mappings `ready` |
| 5 Review | `…/review` | Summary of source, target, and each mapping (fields mapped, key template, sample doc), job options (OC-023), and pre-flight results. Pre-flight re-tests **both stored repositories** automatically | Pre-flight passes |
| 6 Migrate | `…/jobs/{jobId}` | Job monitor (OC-028) | — |

- **Step states:** ✓ complete, ● current, ○ upcoming, ⚠ attention (a repository test failed, or a mapping is stale), ⊘ locked.
  - Steps 3–6 are **locked until both repositories are saved and tested**, with the tooltip "Set up and test the source and target first".
  - After that every step can be opened freely, except that *Migrate* stays disabled until Review passes.
- **Project header:** the name (inline editable), repository chips `PostgreSQL · shop@db1 ●` and `Couchbase · cb.abc.cloud ●` (clicking one opens its step), a "Saved" indicator, and a *…* menu with Rename and Delete.
- **Autosave** everywhere. Leaving mid-way and returning resumes at the first incomplete step.

### New project flow
- *New project* (Projects page, Dashboard, or ⌘K) opens a small dialog for name and optional description. It creates the project and routes to step 1.
- **Empty states:** Projects page — "Create your first migration project" with a 3-step illustration (Connect → Map → Migrate). The Source step before configuration shows a friendly explainer: "Credentials are encrypted and stored with this project. You'll only enter them once."

### Projects list & dashboard
- `/projects`: cards or table (toggle) showing name, source → target (logos and short names, with a StatusDot for each repository's last test), mapping count, last run status with a progress sparkline, and updated time. Filter by status.
- `/` Dashboard:
  - Stat tiles: projects, running jobs, docs migrated (30 days), repositories healthy / total.
  - "Continue where you left off" (the 3 most recently updated projects).
  - Running jobs with live mini progress bars (SSE).
  - Recent failures.

## Acceptance criteria & unit tests

### AC1 — Create project first
**Given** an editor, **when** they create a project with a unique name, **then** it is persisted in `draft` with no repositories and the user lands on step 1 (Source repository). **Given** a duplicate name in the workspace, **then** a 409 `PROJECT_NAME_TAKEN`. **Given** a viewer, **then** a 403.

| Test | Location | Verifies |
|---|---|---|
| `TestProjectsService_AC1_Create` | `internal/projects/service_test.go` | |
| `TestProjectsService_AC1_DuplicateName` | same | |
| `NewProjectDialog.test.tsx` › `AC1: creates and routes to source step` | `src/features/projects/NewProjectDialog.test.tsx` | |

### AC2 — Repositories are set up inside the project, with a test button in each
**Given** a new project, **then** the Source and Target steps each render a connection form with a **Test connection** button and a **Save to project** button. After save, each shows a summary card whose *Test connection* re-tests the stored credentials. Steps 3–6 stay locked until both repositories are saved and their last test is OK.

| Test | Location | Verifies |
|---|---|---|
| `ProjectWizard.test.tsx` › `AC2: source and target steps render form with test button` | `src/features/projects/ProjectWizard.test.tsx` | |
| `ProjectWizard.test.tsx` › `AC2: later steps locked until both repositories tested OK` | same | |
| `TestProjectStatus_AC2_DraftUntilBothTested` | `internal/projects/status_test.go` | draft → configured transition |

### AC3 — Credentials stored once and reused project-wide
**Given** a project whose repositories are saved, **when** the user browses tables, opens the keyspace picker, previews documents, or starts a job, **then** no credential prompt appears and the server resolves the project's stored connections.

| Test | Location | Verifies |
|---|---|---|
| `TestProjectContext_AC3_ResolvesStoredConnections` | `internal/projects/context_test.go` | Resolver returns source and target sessions from the connections repo and sealer fakes |

### AC4 — Project status derivation
**Given** a project with no repositories, or with a repository that is untested or failed → `draft`. **Given** both repositories saved with last test OK → `configured`. (Statuses driven by mappings and jobs, `ready`/`running`/`completed`, arrive with OC-035 and OC-023.)

| Test | Location | Verifies |
|---|---|---|
| `TestProjectStatus_AC4_Table` | `internal/projects/status_test.go` | |

### AC5 — Stepper state & navigation
**Given** a project with source and target tested, 2 mappings, and 1 stale, **then** steps 1–2 show ✓, step 4 shows ⚠, and *Migrate* is disabled. **Given** the source's last test failed, **then** step 1 shows ⚠ and the header chip turns red. Revisiting the project opens the first incomplete step.

| Test | Location | Verifies |
|---|---|---|
| `deriveSteps.test.ts` › `AC5: computes step states incl. locked and attention` | `src/features/projects/deriveSteps.test.ts` | Pure function |
| `ProjectWizard.test.tsx` › `AC5: resumes at first incomplete step` | `src/features/projects/ProjectWizard.test.tsx` | |

### AC6 — Autosave
**Given** edits, **then** a PUT is sent 800 ms after the last change (debounced), edits made while a request is in flight are coalesced into one follow-up save, and the header shows "Saving…", then "Saved · just now". On network error it shows "Not saved — retry".

| Test | Location | Verifies |
|---|---|---|
| `useAutosave.test.ts` › `AC6: debounces and coalesces` | `useAutosave.test.ts` | Fake timers |
| `useAutosave.test.ts` › `AC6: error state and retry` | same | |

### AC7 — Delete project removes its credentials
**Given** a running job, **then** DELETE returns 409. Otherwise, after typed-name confirmation, the project, its connections (sealed secrets), snapshots, mappings, and jobs are deleted, and an audit entry `project.delete` is written.

| Test | Location | Verifies |
|---|---|---|
| `TestProjectsService_AC7_DeleteBlockedWhenRunning` | `internal/projects/service_test.go` | |
| `TestProjectsService_AC7_DeleteCascadesAndAudits` | same | Repo fake asserts cascade call; audit event captured |

### AC8 — Dashboard & projects list
**Given** data, **then** the stat tiles show correct aggregates (projects, repositories healthy / total) and the project cards show both repository chips with health. Live job progress bars on the dashboard arrive with OC-028.

| Test | Location | Verifies |
|---|---|---|
| `TestDashboardStats_AC8_Aggregates` | `internal/projects/dashboard_test.go` | |
| `ProjectsPage.test.tsx` › `AC8: cards show source/target chips with status` | `src/features/projects/ProjectsPage.test.tsx` | |
| `Dashboard.test.tsx` › `AC8: renders tiles` | `src/features/dashboard/Dashboard.test.tsx` | |

## Implementation notes (as built)
- Mapping creation, the Tables step and mapping status moved to **OC-035**; dashboard live job bars to **OC-028**. Steps 3–6 show a "Coming next" placeholder until those stories land.
- Project optimistic concurrency uses the project's `version` (PATCH sends the version read; 412 on mismatch).
- `JobGuard` blocks deleting a project (and changing its repositories) while a migration runs; until OC-023 it always reports "no job".
