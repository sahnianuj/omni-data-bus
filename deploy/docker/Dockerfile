# syntax=docker/dockerfile:1.7
# Omni-Data-Bus image: one static binary (API + worker) with the web app embedded.
#
#   docker build -f deploy/docker/Dockerfile --build-arg VERSION=1.0.0 --build-arg COMMIT=$(git rev-parse --short HEAD) -t omni-data-bus .
#   docker buildx build --platform linux/amd64,linux/arm64 -f deploy/docker/Dockerfile ...   # multi-arch
ARG NODE_VERSION=24.18.0
ARG GO_VERSION=1.26.8

# ── 1. Web app ────────────────────────────────────────────────────────────────
# Built on the build platform: the output is architecture-independent.
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY api/openapi.yaml /src/api/openapi.yaml
COPY web/ ./
# The SPA embeds the release it was built for, to notice a server upgrade under an open tab (OC-058).
ARG VERSION=dev
# Source maps (~5 MB) stay out of the image: they'd be embedded in the binary and break the 40 MB budget.
RUN VITE_ODB_VERSION=${VERSION} npm run build && find dist -name '*.map' -delete

# ── 2. Binary ─────────────────────────────────────────────────────────────────
# Cross-compiled for the target platform; no cgo, so the result runs on distroless/static.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
# UTC build time and release channel (OC-058); the release workflow derives BUILD_TIME from
# SOURCE_DATE_EPOCH (the commit time) so rebuilding a tag gives the same binary.
ARG BUILD_TIME=""
ARG CHANNEL=""
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
COPY --from=web /src/web/dist ./internal/httpapi/webdist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags embedspa -trimpath \
      -ldflags "-s -w -X omnidatabus/internal/buildinfo.version=${VERSION} -X omnidatabus/internal/buildinfo.commit=${COMMIT} \
        -X omnidatabus/internal/buildinfo.buildTime=${BUILD_TIME} -X omnidatabus/internal/buildinfo.channel=${CHANNEL}" \
      -o /out/odb ./cmd/odb \
 && mkdir -p /out/data/odb-out

# ── 3. Runtime ────────────────────────────────────────────────────────────────
# Only the binary and CA certificates; runs as uid 65532. Probes live in compose and Kubernetes
# (distroless has no shell for a HEALTHCHECK).
FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=""
# The release workflow passes the repository URL (links the GHCR package to the repo).
ARG SOURCE_URL=""
LABEL org.opencontainers.image.title="Omni-Data-Bus" \
      org.opencontainers.image.description="Migrate PostgreSQL tables into Couchbase documents" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.source="${SOURCE_URL}"
COPY --from=build /out/odb /usr/local/bin/odb
# Directory targets (OC-038) write under /data; a volume mounted at /data/odb-out takes this
# folder's owner, so the server (uid 65532) can write there.
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV OC_HTTP_ADDR=:8080 \
    OC_METRICS_ADDR=:9090 \
    OC_LOG_FORMAT=json
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/odb"]
