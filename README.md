# Omni-Data-Bus

Omni-Data-Bus is a web application that migrates data from relational databases into Couchbase. You create a project, connect a **source** (PostgreSQL) and a **target** (Couchbase Capella, self-managed Couchbase, or a folder of JSON files), design the JSON documents you want, and press **Migrate**: rows are streamed, transformed and written with a live progress bar, then verified. The same migration can be exported and run anywhere with the `odb` command.

## What it does

- **Documents, not rows:** design each document from a table, a JSON sample or schema, or the customer's own design, which proposes the whole mapping from the connected source. Embed related tables in either direction, at any depth, and add computed fields.
- **Explainable auto-mapping:** a deterministic matcher maps fields and says why ("*10 of 23 fields automatically mapped*"). You finish the rest on a canvas or in the document composer, and preview real documents before migrating.
- **Safe migrations:** snapshot-consistent parallel reads, batching, retries, a dead-letter log, pause and resume, crash recovery, and verification with a downloadable report.
- **Run it anywhere:** an export is `config.json` (where) and `mappings.json` (what), run by the same `odb preflight` and `odb run` on macOS, Windows or Linux, with or without Docker, then imported back as an external run.
- **Credentials stay secret:** connections are tested once and stored encrypted (AES-256-GCM envelope encryption). The app never shows a secret again, and logs redact them.

## Install

Each [GitHub release](https://github.com/sahnianuj/omni-data-bus/releases) has the container image, the Helm chart, the all-in-one image archives and the `odb` runner with `SHA256SUMS`.

| You want | Use |
|---|---|
| A customer or a laptop without Couchbase | The **all-in-one image** (app + Couchbase Server CE): [deploy/appliance/README.md](deploy/appliance/README.md) |
| Production with Capella or your cluster | The image `ghcr.io/sahnianuj/omni-data-bus:<version>` with [Docker Compose](deploy/compose/README.md) or the [Helm chart](deploy/helm/omni-data-bus/README.md) |
| To run an exported migration | The `odb` runner below; [running migrations outside the tool](docs/user-guide/running-outside-the-tool.md) |

```bash
sha256sum -c SHA256SUMS --ignore-missing    # macOS: shasum -a 256 -c SHA256SUMS --ignore-missing
tar -xzf odb_<version>_<machine>.tar.gz     # Windows: Expand-Archive odb_<version>_windows_amd64.zip
odb --version
```

| Your computer | File |
|---|---|
| macOS, Intel or Apple silicon (signed and notarized) | `odb_<version>_darwin_universal.tar.gz` |
| Linux x86-64 | `odb_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | `odb_<version>_linux_arm64.tar.gz` |
| Windows x86-64 (signed) | `odb_<version>_windows_amd64.zip` |
| Windows ARM64 (signed) | `odb_<version>_windows_arm64.zip` |

From a clone of the repository:

```bash
make build      # backend/bin/odb, with the web app embedded
make image      # the container image
make dist       # odb for every machine, with SHA256SUMS, in dist/odb/
```

## Quick start

With Docker running (or a Capella cluster in `.env`, see the [development guide](docs/development.md)):

```bash
make setup      # Go modules, golangci-lint, web dependencies
make dev        # dev stack + API on :8080 + web on http://localhost:5173
```

Open http://localhost:5173 and create an account, then a project. The dev stack's sample source is PostgreSQL on `localhost:5434`, database `shop`, read-only user `omni_ro` / `omni_ro`; its sandbox target is Couchbase at `couchbase://localhost` (`Administrator` / `password`). [Getting started](docs/getting-started.md) walks through a whole migration.

## Commands

```
odb                                         start the server (OC_ROLE or --role all | api | worker)
odb preflight | run | resume | status       run an exported migration, in its folder
odb verify | inspect | help
odb version [--json]                        the release and build (also --version)
odb admin gen-key | provision [--print] | rotate-secrets
odb healthcheck                             probe a running server's /readyz
odb bundle validate | inspect | diff        check a mappings.json
```

Exit codes of the runner commands: 0 completed, 2 completed with errors, 3 failed, 4 preflight or configuration error, 5 cancelled (`odb resume` continues). `odb help` lists the files and settings. `make help` lists the developer commands.

## Documentation

| Document | For |
|---|---|
| [Getting started](docs/getting-started.md) | Step-by-step walkthrough of one migration, from sign-up to export |
| [User guide](docs/user-guide/README.md) | Directory targets, relationship mapping, the composer, design-first mapping, computed fields, running migrations outside the tool (the app's **?** icons link here) |
| [Development guide](docs/development.md) | Architecture, local setup and the dev stack, configuration reference, the metadata store, testing, make targets, troubleshooting |
| [Deployment guide](docs/deployment.md) | Binary, image, Docker Compose, Kubernetes, the all-in-one image, the odb release binaries, monitoring, security model |
| Upgrading | [1.0 → 2.0](docs/upgrade/1.0-to-2.0.md), [2.0 → 2.5](docs/upgrade/2.0-to-2.5.md) |
| [API](api/openapi.yaml) | The REST API contract (OpenAPI) |
| Architecture | [1.0](docs/release-1.0.0/00-architecture.md) (domain model, conventions, NFRs, Definition of Done), [2.0](docs/release-2.0.0/00-architecture.md), [2.5](docs/release-2.5.0/00-architecture.md) |
| Release plans | INDEX.md of [1.0](docs/release-1.0.0/INDEX.md), [2.0](docs/release-2.0.0/INDEX.md), [2.5](docs/release-2.5.0/INDEX.md) ([release notes](docs/release-2.5.0/RELEASE-NOTES.md)), [3.0](docs/release-3.0.0/INDEX.md) and later: user stories and progress |
| [Benchmarks](bench/README.md) | Performance benchmark and release gates |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to build, test and change the project, and release it |

## Project structure

| Path | Contents |
|---|---|
| [backend/cmd/odb/](backend/cmd/odb/) | The `odb` binary: the server, the runner commands (`preflight`, `run`…) and admin commands |
| [backend/internal/](backend/internal/) | The Go packages: `httpapi`, `auth`, `projects`, `connections`, `connectors` (sources and targets), `discovery`, `mapping`, `automap`, `transform`, `pipeline`, `jobs`, `export`, `headless` (the runner), `store` (the Couchbase metadata store)… |
| [web/](web/) | The React app (Vite): `src/features/` per screen, `tests/e2e/` (Playwright) |
| [api/openapi.yaml](api/openapi.yaml) | The API contract; the web app's types are generated from it |
| [deploy/](deploy/) | `docker/` (image), `compose/` (dev stack, production), `helm/` (chart), `appliance/` (all-in-one image), `observability/` (dashboard, alerts) |
| [bench/](bench/) | Benchmark harness, release gates and results |
| [scripts/](scripts/) | Dev and release scripts, the story index, third-party notices and the licence policy |
| [docs/](docs/) | Guides, user guide, upgrade notes, and per release: architecture, user stories (`OC-NNN-*.md`), INDEX.md |

Build, test and documentation conventions: [CONTRIBUTING.md](CONTRIBUTING.md) and the [Makefile](Makefile).

## Licence

Omni-Data-Bus is licensed under the [Apache License 2.0](LICENSE). Copyright 2026 Couchbase, Inc.; author: Anuj Sahni, Principal Cloud & Solutions Architect; see [NOTICE](NOTICE). The open-source packages it includes, and their licences, are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The all-in-one image also bundles Couchbase Server Community Edition under Couchbase's own licence, so that image as a whole isn't Apache-2.0 alone. Couchbase is a trademark of Couchbase, Inc.
