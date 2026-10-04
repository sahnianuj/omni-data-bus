# OC-073 — Build, package and share the all-in-one image

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 3 | OC-072 | Implemented |

## User story
As a **solutions engineer**, I want `make` targets that build the all-in-one image for the customer's CPU, save it as one compressed file with a checksum, and (optionally) push it to a registry, so that I can send the customer a single file, or a single image name, that they load and run, including on machines with no internet access.

## Scope
**In:**
- `make appliance`, `make appliance-save`, `make appliance-push`, `make appliance-test`.
- Builds for `linux/amd64`, `linux/arm64`, or both.
- A shareable archive: `dist/appliance/omni-data-bus-appliance-<version>-<arch>.tar.gz`, plus `.sha256`, plus the customer run kit from OC-074 next to it.
- The release workflow publishes the appliance image to GHCR alongside the regular image (multi-arch, cosign-signed, SBOM, Trivy gate, same as OC-032).

**Out:** what the customer does with the file (OC-074), installers (`.pkg`, `.msi`), and Docker Desktop extensions.

## Implementation details

### Make targets
| Target | Does |
|---|---|
| `make appliance [ARCH=amd64\|arm64] [VERSION=…]` | `docker buildx build -f deploy/appliance/Dockerfile --platform linux/$(ARCH) --load -t omni-data-bus-appliance:$(VERSION)`. `ARCH` defaults to the build machine's architecture. Passes the same `VERSION`/`COMMIT`/`BUILD_TIME`/`CHANNEL` build args as `make image` |
| `make appliance-save [ARCH=…]` | Runs `appliance` for that arch, then `docker save omni-data-bus-appliance:$(VERSION) \| gzip -6 > dist/appliance/omni-data-bus-appliance-$(VERSION)-$(ARCH).tar.gz`, writes the `.sha256`, copies the OC-074 run kit into `dist/appliance/`, and prints the file sizes and the exact commands to hand over. `ARCH=all` produces one archive per arch (a `docker save` archive holds a single platform) |
| `make appliance-push REGISTRY=… [VERSION=…]` | Multi-arch `buildx build --push` of both platforms as one manifest list, to `$(REGISTRY)/omni-data-bus-appliance:$(VERSION)`. For customers who can pull from a private registry |
| `make appliance-test` | `container-structure-test` (`deploy/appliance/structure-test.yaml`: entrypoint, tini, `odb version`, labels, non-root app user) plus the OC-072 smoke test (`deploy/appliance/test/smoke.sh`) |

- The logic is in `scripts/appliance.sh` (`build`, `save`, `push`); the targets pass `VERSION`/`COMMIT`/`BUILD_TIME`/`CHANNEL`. The appliance Dockerfile copies `odb` from the regular image built for the same platform (`ARG ODB_IMAGE`), so both run the same binary.
- `appliance-push` uses its own `docker-container` builder (`odb-appliance`, host network): Docker Desktop's default builder can't push a multi-platform image. A registry on `localhost` is allowed over plain http.
- All targets show in `make help`. `dist/` is git-ignored.
- An archive name always carries the version and arch, so a customer can tell which file they have. `docker load` restores the tag `omni-data-bus-appliance:<version>`.
- Expected size: about 500–600 MB compressed (Couchbase CE is most of it). `make appliance-save` prints the size; there is no size gate as for the 40 MB regular image.
- Building `ARCH=arm64` on an amd64 machine (or the reverse) uses QEMU through buildx; the target checks `docker buildx ls` and prints how to enable it if the platform is missing.

### Release workflow
- On `v*` tags (and as a dry run on a PR labelled `release-check`), job `appliance` in `.github/workflows/release.yml`: `scripts/appliance.sh save all` builds and archives both CPUs; the structure test and the smoke test run on the amd64 image; Trivy **reports** HIGH/CRITICAL findings (not a gate: almost all are in Couchbase's own image, which we ship unmodified; the regular image's scan stays a gate); on a tag, the multi-arch image is pushed to `ghcr.io/<owner>/omni-data-bus-appliance:<version>` on top of the regular image the `image` job pushed and signed (so that one isn't rebuilt), signed with cosign, with an SPDX SBOM attested.
- The `release` job attaches both archives, their checksums, `run.sh`, `compose.yml` and the customer `README.md` to the GitHub release.

## Acceptance criteria & unit tests

### AC1 — Build for the right CPU
**Given** an Apple-silicon Mac, **when** `make appliance` runs, **then** an arm64 `omni-data-bus-appliance:<version>` is loaded. **Given** `ARCH=amd64`, **then** the image is amd64. Both report the same `odb` version and commit as `make image` for the same source.

| Test | Location | Verifies |
|---|---|---|
| `make appliance`, `make appliance-save ARCH=all` (2026-10-01) | `scripts/appliance.sh` | arm64 native and amd64 under QEMU both built |
| `structure-test.yaml` (11 checks) | `deploy/appliance/structure-test.yaml` | Passed on both images: `odb --version`, CE license, labels, entrypoint, non-root app user |

### AC2 — Save, transfer and load
**Given** `make appliance-save ARCH=all`, **then** `dist/appliance/` holds one archive per CPU (amd64 496 MB, arm64 464 MB), a `.sha256` each that `shasum -a 256 -c` accepts, `run.sh` and `compose.yml` pinned to the version, and the customer `README.md`. **Given** a Docker host without the image, **when** `./run.sh load` runs, **then** it checks the checksum and loads the archive for that host's CPU.

| Test | Location | Verifies |
|---|---|---|
| `docs_commands.sh` › customer guide | `deploy/appliance/test/docs_commands.sh` | Local images deleted first; loaded from the archive (arm64 picked on arm64) |
| Archive contents | manual, 2026-10-01 | Each archive's image config has the right `architecture` |

Not done: loading in a Docker-in-Docker daemon with `--network none`. `docker load` needs no network by design, and the test above starts from no local image.

### AC3 — Registry push
**Given** `make appliance-push REGISTRY=localhost:5055/odb`, **then** the registry holds one index with `linux/amd64` and `linux/arm64` (plus build attestations), and `docker pull` on arm64 gets the arm64 image.

| Test | Location | Verifies |
|---|---|---|
| Push to a throwaway `registry:2` (2026-10-01) | `scripts/appliance.sh push` | `docker buildx imagetools inspect`; pulled arm64 |

### AC4 — Release publishes the appliance
**Given** a `v*` tag, **then** the release has the signed multi-arch appliance image in GHCR with an SBOM, and both archives with checksums and the run kit attached to the GitHub release.

| Test | Location | Verifies |
|---|---|---|
| `actionlint` | `.github/workflows/release.yml` | Clean |
| Dry run on a PR labelled `release-check` | `.github/workflows/release.yml` | **Not run yet** (needs GitHub) |
