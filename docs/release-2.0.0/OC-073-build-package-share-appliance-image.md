# OC-073 — Build, package and share the all-in-one image

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 3 | OC-072 | Not started |

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
| `make appliance-test` | `container-structure-test` (`deploy/appliance/structure-test.yaml`: entrypoint, tini, `odb version`, labels, non-root app user) plus the OC-072 bats smoke suite |

- All targets show in `make help`. `dist/` is git-ignored.
- An archive name always carries the version and arch, so a customer can tell which file they have. `docker load` restores the tag `omni-data-bus-appliance:<version>`.
- Expected size: about 500–600 MB compressed (Couchbase CE is most of it). `make appliance-save` prints the size; there is no size gate as for the 40 MB regular image.
- Building `ARCH=arm64` on an amd64 machine (or the reverse) uses QEMU through buildx; the target checks `docker buildx ls` and prints how to enable it if the platform is missing.

### Release workflow
- On `v*` tags, the existing release job also builds and pushes `ghcr.io/<owner>/omni-data-bus-appliance:<version>` (both arches), signs it with cosign, attaches an SPDX SBOM, and runs Trivy. Trivy findings in the Couchbase base layers are reported but gated only for HIGH/CRITICAL with a fix available, with an allowlist file `deploy/appliance/trivy-ignore.yaml` reviewed per release.
- The release also attaches both `.tar.gz` archives and their `.sha256` files to the GitHub release, so a customer link is just the release page.

## Acceptance criteria & unit tests

### AC1 — Build for the right CPU
**Given** an Apple-silicon Mac, **when** `make appliance` runs, **then** an arm64 image tagged `omni-data-bus-appliance:<version>` is loaded. **Given** `ARCH=amd64`, **then** the image is amd64 and `docker image inspect` shows it. **Given** both builds, **then** `odb version` inside each prints the same version and commit as `make image` for the same source.

| Test | Location | Verifies |
|---|---|---|
| `appliance_build.bats` › `AC1: native and cross build` | `deploy/appliance/test/appliance_build.bats` | CI job `appliance` (amd64 runner + QEMU) |
| `structure-test.yaml` › `odb version`, labels | `deploy/appliance/structure-test.yaml` | |

### AC2 — Save, transfer and load
**Given** `make appliance-save`, **then** `dist/appliance/` holds the `.tar.gz`, a `.sha256` that `shasum -a 256 -c` accepts, and the run kit. **Given** a clean Docker host with no network, **when** `docker load -i <file>` runs, **then** the tagged image is present and starts healthy (OC-072 AC1).

| Test | Location | Verifies |
|---|---|---|
| `appliance_build.bats` › `AC2: save produces archive, checksum, kit` | `deploy/appliance/test/appliance_build.bats` | |
| `appliance_build.bats` › `AC2: load on a clean daemon (no network)` | same | Docker-in-Docker with `--network none` |

### AC3 — Registry push
**Given** `make appliance-push REGISTRY=localhost:5000`, **then** the registry holds one manifest list with `linux/amd64` and `linux/arm64`, and `docker run` on each arch pulls the matching image.

| Test | Location | Verifies |
|---|---|---|
| `appliance_build.bats` › `AC3: push multi-arch to local registry` | `deploy/appliance/test/appliance_build.bats` | `registry:2` container |

### AC4 — Release publishes the appliance
**Given** a `v*` tag, **then** the release has the signed multi-arch appliance image in GHCR with an SBOM, and both archives with checksums attached to the GitHub release.

| Test | Location | Verifies |
|---|---|---|
| Release workflow dry run (`act` or a `v0.0.0-rc` tag on a fork) | `.github/workflows/release.yml` | `cosign verify`, SBOM present, assets attached |
