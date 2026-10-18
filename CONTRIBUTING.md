# Contributing to Omni-Data-Bus

## Progress must always be visible

Each release's `docs/release-<version>/INDEX.md` is generated from the header table of each story file (`docs/release-<version>/OC-NNN-*.md`). It must reflect reality at every point, not only at the end of a release.

- When work **starts** on a story, set its **Status** to `In progress`.
- When every AC of a story has a passing named test, set it to `Implemented`.
- A story that is dropped is set to `Descoped` (keep the file and ID; say why in the file).
- After **any** status, points, priority, title or dependency change, regenerate in the same change:
  `python3 scripts/update-story-index.py docs/release-<version>` (`make index` does release 1.0.0).
- A new story takes the next free `OC-NNN` and is added to a phase in `scripts/update-story-index.py`.
- Never edit INDEX.md by hand.

## Stories and tests

- Pick the next story from the current release's INDEX.md and respect **Depends on**.
- Every story AC has at least one test that names the story and AC: `TestExport_OC182_AC1_Files` in Go, `it('OC-182 AC4: …')` in the web app. The architecture's Definition of Done ([release 1.0](docs/release-1.0.0/00-architecture.md)) applies to every release.
- The REST API's contract is [`api/openapi.yaml`](api/openapi.yaml): change it with the handler, then `make gen` (CI's `make check-gen` fails when the TypeScript types are stale, and a contract test fails when a route isn't in the spec).
- When a story adds or changes user-facing behaviour, update its chapter in the [user guide](docs/user-guide/README.md) (the app's **?** icons link there), and [getting started](docs/getting-started.md), [development](docs/development.md) or [deployment](docs/deployment.md) when they're affected. Keep `README.md` short: overview, install, quick start, links.
- When a story is finished, add an **Implementation notes** section: deviations from the original text, and the tests for each AC.

## Dependencies and licences

Omni-Data-Bus is licensed under Apache-2.0 (`LICENSE`, copyright and author in `NOTICE`); source files carry no licence header. When you add, remove or upgrade a Go module or a web runtime dependency, run `make notices` and commit the updated `THIRD_PARTY_NOTICES.md` (and the copies it keeps in step for the server and the Helm chart). CI (`make notices-check`) fails when the file is stale or a shipped dependency's licence isn't in `scripts/licence-policy.json`; changing that list needs the code owner's approval.

CI also scans the whole git history for secrets (gitleaks). A test or development value it flags is reviewed and listed, with the reason, in `.gitleaksignore`; never commit a real secret.

## Commands

```bash
make setup          # Go modules, golangci-lint, web dependencies
make dev            # dev stack + API on :8080 + web on :5173
make test           # Go (race, coverage gate) and web unit tests
make test-integration
make lint           # golangci-lint, eslint, tsc
make gen            # regenerate TypeScript API types after an api/openapi.yaml change
make notices        # regenerate THIRD_PARTY_NOTICES.md after a dependency change
make e2e            # Playwright end-to-end suite against the built image
make help           # every target
```

Commit messages follow Conventional Commits (`feat:`, `fix:`, `docs:` …); lefthook checks them and formats staged Go and TypeScript. Open a PR with the [template](.github/pull_request_template.md).

## Releasing

1. The release is the `VERSION` file (for example `2.5.0`). Tag the commit `v<VERSION>` (`v2.5.0`, or `v2.5.0-rc.1`); the release workflow fails when the tag doesn't match `VERSION`.
2. Push the tag. `.github/workflows/release.yml` runs the tests and the benchmark gate, then builds and publishes:
   - the multi-arch image to GHCR (signed with cosign, with an SBOM and a Trivy gate);
   - the Helm chart;
   - the all-in-one image archives;
   - the odb binaries: the macOS one signed and notarized, the Windows one Authenticode-signed, all in `SHA256SUMS` signed with cosign.

   It then attaches everything to the GitHub release. The signing secrets live in the `release` environment (OC-185, OC-186). A pull request labelled `release-check` runs the same workflow as a dry run that publishes and signs nothing.
3. The release checklist at the end of each release's INDEX.md lists what must be true first.
