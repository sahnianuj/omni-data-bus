# Omni-Data-Bus developer commands. Run `make help` for the list.
SHELL := /bin/bash
COMPOSE := docker compose -f deploy/compose/dev.yml
GOLANGCI_VERSION := v2.14.0
# Omni-Data-Bus needs no cgo; disabling it gives static binaries and avoids broken local C toolchains.
export CGO_ENABLED := 0
# The race detector is the exception: it needs cgo (and a C compiler), so race runs turn it back on.
RACE_TEST := CGO_ENABLED=1 go test -race
# The release is the VERSION file (OC-172): itself on the commit tagged v<VERSION>, else <VERSION>-dev,
# so even an untagged build says which release it is. The build is the short commit, plus the CI
# run number (CI_RUN) when there is one: 7f6cea2+418.
RELEASE := $(shell cat VERSION 2>/dev/null || echo 0.0.0)
VERSION ?= $(shell if [ "$$(git describe --tags --exact-match 2>/dev/null)" = "v$(RELEASE)" ]; then echo $(RELEASE); else echo $(RELEASE)-dev; fi)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD ?= $(COMMIT)$(if $(CI_RUN),+$(CI_RUN))
DIRTY ?= $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo true || echo false)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# release for a tag (v2.0.0), rc for a pre-release tag (v2.0.0-rc.1), dev otherwise (OC-058).
CHANNEL ?= $(shell v='$(VERSION)'; if [[ $$v =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$$ ]]; then echo release; \
	elif [[ $$v =~ ^v?[0-9]+\.[0-9]+\.[0-9]+-(rc|beta|alpha) ]]; then echo rc; else echo dev; fi)
IMAGE ?= omni-data-bus:$(VERSION)
BUILDINFO := omnidatabus/internal/buildinfo
LDFLAGS := -s -w -X $(BUILDINFO).version=$(VERSION) -X $(BUILDINFO).commit=$(COMMIT) \
	-X $(BUILDINFO).buildTime=$(BUILD_TIME) -X $(BUILDINFO).channel=$(CHANNEL) \
	-X $(BUILDINFO).buildID=$(BUILD) -X $(BUILDINFO).dirty=$(DIRTY)
# The same stamp for `go run` in development (OC-172), so the footer shows 2.5.0-dev · build <commit>.
DEV_LDFLAGS := $(filter-out -s -w,$(LDFLAGS))
DEV_ENV = ODB_LDFLAGS="$(DEV_LDFLAGS)" VITE_ODB_VERSION=$(VERSION)
HELM_IMAGE := alpine/helm:3.18.4
HELM_UNITTEST_IMAGE := helmunittest/helm-unittest:latest

.PHONY: help setup dev dev-force dev-stack dev-stack-down dev-reset run-backend run-web gen check-gen lint test test-backend test-budgets test-web test-integration test-integration-ce test-bench coverage build image image-test helm-test e2e e2e-smoke bench bench-gate bench-smoke index appliance appliance-save appliance-push appliance-test dist verify-dist notices notices-check test-scripts verify-version dev-seed propose-score

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

setup: ## Install Go modules, golangci-lint and web dependencies
	cd backend && go mod download
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	cd web && npm ci

dev: ## Start the dev stack, the API on :8080 and the web app on :5173 (FORCE=1 rescans saved schemas)
	FORCE=$(FORCE) $(DEV_ENV) ./scripts/dev.sh

dev-force: ## make dev, rescanning each source's saved schema when it's next opened
	FORCE=1 $(DEV_ENV) ./scripts/dev.sh

dev-stack: ## Start Couchbase 8.0.3 + sample source PostgreSQL and wait until healthy
	$(COMPOSE) up -d --wait couchbase source-pg
	$(COMPOSE) up couchbase-init --exit-code-from couchbase-init

dev-stack-down: ## Stop the dev stack (data is kept)
	$(COMPOSE) down

dev-reset: ## Stop the dev stack and delete its data volumes
	$(COMPOSE) down -v

dev-seed: ## (Re)load the point-of-sale fixture schema "pos" into the dev source PostgreSQL (OC-162)
	$(COMPOSE) exec -T source-pg psql -v ON_ERROR_STOP=1 -q -U shop_owner -d shop -c "DROP SCHEMA IF EXISTS pos CASCADE"
	$(COMPOSE) exec -T source-pg psql -v ON_ERROR_STOP=1 -q -U shop_owner -d shop < backend/internal/fixtures/pos/schema.sql

propose-score: ## Score the design-first proposal on the pos fixture and write the G10 result (OC-162, OC-173; needs make dev-stack)
	@mkdir -p bench/results/2.5.0
	cd backend && OC_IT_PG_HOST=localhost OC_IT_PG_PORT=5434 OC_PROPOSE_SCORE_OUT=$(CURDIR)/bench/results/2.5.0/propose-quality.json \
		go test -tags integration -count=1 -run 'TestProposeScore|TestProposeGate' -v ./internal/automap/ | grep -v '^=== RUN'

run-backend: ## Run the Go server (reads .env; without one, uses the local dev stack)
	cd backend && if [ -f ../.env ]; then go run -ldflags "$(DEV_LDFLAGS)" ./cmd/odb; else OC_DEV=true go run -ldflags "$(DEV_LDFLAGS)" ./cmd/odb; fi

run-web: ## Run the Vite dev server
	cd web && VITE_ODB_VERSION=$(VERSION) npm run dev

gen: ## Regenerate TypeScript API types from api/openapi.yaml
	cd web && npm run gen:api

check-gen: gen ## Fail if generated files are stale
	cd backend && go run ./tools/checkgen/cmd/checkgen .. web/src/lib/api/schema.d.ts

lint: ## Lint Go and web code
	cd backend && golangci-lint run ./...
	cd web && npm run lint && npm run typecheck

test: test-backend test-budgets test-web test-bench ## Run all unit tests

test-backend: ## Go unit tests with race detector and coverage gate
	@mkdir -p backend/coverage
	cd backend && $(RACE_TEST) -coverprofile=coverage/cover.out ./...
	cd backend && go run ./tools/covergate/cmd/covergate coverage/cover.out

test-budgets: ## Performance budget tests, uninstrumented (they skip under -race and -cover)
	cd backend && go test -count=1 -run 'Budget' ./...
	cd web && npm run bench

test-web: ## Web unit tests with coverage
	cd web && npm run test:coverage


test-integration-ce: ## Go integration tests against Couchbase Server CE, as bundled in the all-in-one image (needs make dev-stack for PostgreSQL)
	scripts/integration-ce.sh
test-bench: ## Benchmark harness unit tests (bench/, OC-034)
	cd bench && go vet ./... && $(RACE_TEST) ./...

test-integration: ## Go integration tests (needs Docker; uses Couchbase 8.0.3 and PostgreSQL containers)
	@mkdir -p backend/coverage
	cd backend && $(RACE_TEST) -tags=integration -count=1 -coverprofile=coverage/integration.out ./...
	cd backend && go run ./tools/covergate/cmd/covergate --integration coverage/integration.out

build: ## Build the web app and a single Go binary with the SPA embedded
	cd web && VITE_ODB_VERSION=$(VERSION) npm run build
	rm -rf backend/internal/httpapi/webdist && cp -R web/dist backend/internal/httpapi/webdist
	cd backend && CGO_ENABLED=0 go build -tags embedspa -trimpath -ldflags "$(LDFLAGS)" -o bin/odb ./cmd/odb
	cd backend && go test -tags embedspa -count=1 -run TestEmbeddedSPA ./internal/httpapi

dist: ## Build the odb runner for linux/darwin/windows × amd64/arm64 (+ universal macOS with lipo): dist/odb/*.tar.gz, *.zip + SHA256SUMS (OC-051, OC-185, OC-186)
	VERSION=$(VERSION) LDFLAGS="$(LDFLAGS)" scripts/dist.sh

verify-dist: ## Check dist/odb: each binary reports VERSION and carries its licence files; the universal macOS odb has x86_64 and arm64 (OC-185, OC-189)
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	for f in dist/odb/odb_$(VERSION)_windows_*.zip; do \
		for e in odb.exe LICENSE NOTICE THIRD_PARTY_NOTICES.md; do unzip -l "$$f" | grep -q " $$e$$" || { echo "$$f has no $$e"; exit 1; }; done; echo "ok $$f"; \
	done; \
	for f in dist/odb/odb_$(VERSION)_*.tar.gz; do \
		tar -xzf "$$f" -C "$$tmp" odb LICENSE NOTICE THIRD_PARTY_NOTICES.md || { echo "$$f lacks odb or its licence files (OC-189)"; exit 1; }; \
		case "$$f" in \
			*darwin_universal*) archs=$$(lipo -archs "$$tmp/odb"); \
				[ "$$archs" = "x86_64 arm64" ] || { echo "$$f: architectures '$$archs', want 'x86_64 arm64'"; exit 1; }; \
				for a in x86_64 arm64; do arch -$$a "$$tmp/odb" version --json | grep -q '"version": "$(VERSION)"' || { echo "$$f ($$a) doesn't report $(VERSION)"; exit 1; }; done ;; \
			*$$(go env GOOS)_$$(go env GOARCH)*) "$$tmp/odb" version --json | grep -q '"version": "$(VERSION)"' || { echo "$$f doesn't report $(VERSION)"; exit 1; } ;; \
		esac; \
		echo "ok $$f"; \
	done

notices: ## Regenerate THIRD_PARTY_NOTICES.md (and the embedded copies) after a dependency change (OC-189)
	python3 scripts/third-party-notices.py

notices-check: ## Fail when THIRD_PARTY_NOTICES.md is stale or a shipped dependency's licence isn't allowed (OC-189)
	python3 -m unittest scripts/test_third_party_notices.py
	python3 scripts/third-party-notices.py --check

test-scripts: ## migrate.sh checks: shellcheck on every variant and the bats suites (ODB_TEST_BASH=/bin/bash for 3.2)
	cd backend && ODB_REQUIRE_BATS=1 go test -count=1 -run 'TestScriptTemplate|TestMigrateScript_Bats' ./internal/export/

verify-version: ## Check odb reports the release from VERSION and its build (OC-172)
	@cd backend && go build -trimpath -ldflags "$(LDFLAGS)" -o $(CURDIR)/.verify-odb ./cmd/odb
	@out=$$(./.verify-odb version --json); rm -f .verify-odb; \
		echo "$$out" | grep -q '"version": "$(VERSION)"' || { echo "odb doesn't report version $(VERSION): $$out"; exit 1; }; \
		echo "$$out" | grep -q '"build": "$(BUILD)"' || { echo "odb doesn't report build $(BUILD): $$out"; exit 1; }; \
		case "$(VERSION)" in $(RELEASE)|$(RELEASE)-*) ;; *) echo "version $(VERSION) isn't release $(RELEASE) from VERSION"; exit 1;; esac; \
		echo "odb $(VERSION) · build $(BUILD)"

image: ## Build the container image (IMAGE=omni-data-bus:<version>)
	docker build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) --build-arg CHANNEL=$(CHANNEL) \
		--build-arg BUILD=$(BUILD) --build-arg DIRTY=$(DIRTY) -t $(IMAGE) .

image-test: image ## Check the image: user, entrypoint, files, commands, size < 40 MB
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$(CURDIR)/deploy/docker:/cfg" \
		gcr.io/gcp-runtimes/container-structure-test:latest test --image $(IMAGE) --config /cfg/structure-test.yaml
	@size=$$(docker image inspect $(IMAGE) --format '{{.Size}}'); echo "image size: $$size bytes"; \
		test $$size -lt 41943040 || (echo "image is larger than 40 MB" && exit 1)

# ─── All-in-one image: app + Couchbase Server CE (OC-072 – OC-074) ──────────────
ARCH ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
APPLIANCE ?= omni-data-bus-appliance
APPLIANCE_ENV = VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_TIME=$(BUILD_TIME) CHANNEL=$(CHANNEL) BUILD=$(BUILD) DIRTY=$(DIRTY) APPLIANCE=$(APPLIANCE)

appliance: ## Build the all-in-one image (app + Couchbase CE) for ARCH=amd64|arm64 (default: this machine)
	$(APPLIANCE_ENV) scripts/appliance.sh build $(ARCH)

appliance-save: ## Package the all-in-one image to share: dist/appliance/*.tar.gz + checksum + run kit (ARCH=…|all)
	$(APPLIANCE_ENV) scripts/appliance.sh save $(ARCH)

appliance-push: ## Push the all-in-one image for amd64 and arm64 to REGISTRY=<registry/namespace>
	$(APPLIANCE_ENV) scripts/appliance.sh push $(REGISTRY)

appliance-test: appliance ## Check the all-in-one image: structure, then a full start/stop/restart smoke test
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$(CURDIR)/deploy/appliance:/cfg" \
		gcr.io/gcp-runtimes/container-structure-test:latest test --image $(APPLIANCE):$(VERSION) --config /cfg/structure-test.yaml
	IMAGE=$(APPLIANCE):$(VERSION) deploy/appliance/test/smoke.sh

helm-test: ## Lint the Helm chart and run its unit tests (via Docker)
	docker run --rm -v "$(CURDIR)/deploy/helm:/apps" -w /apps $(HELM_IMAGE) lint omni-data-bus --strict \
		-f omni-data-bus/tests/required-values.yaml
	# As the host user: the image's own user (1000) can't write tests/__snapshot__ in a CI checkout (1001).
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp -v "$(CURDIR)/deploy/helm:/apps" \
		$(HELM_UNITTEST_IMAGE) omni-data-bus

index: ## Regenerate docs/release-1.0.0/INDEX.md from story statuses
	python3 scripts/update-story-index.py docs/release-1.0.0

e2e: ## Full end-to-end suite (Playwright) against the built image, local only; E2E_BROWSERS=all adds Firefox and WebKit
	./scripts/e2e.sh

e2e-smoke: ## The @smoke end-to-end tests CI runs (golden path, build footer, API helper)
	./scripts/e2e.sh --grep @smoke

DATASET ?= standard
GATE ?= release
BENCH_OUT ?= results
bench: ## Benchmark against a running stack: make bench DATASET=standard GATE=release (see bench/README.md)
	cd bench && go run ./cmd/odbbench --dataset $(DATASET) --gate $(GATE) --out $(BENCH_OUT) $(BENCH_ARGS)

RELEASE ?= 2.0.0
bench-gate: ## Judge the committed reference results against bench/gates-$(RELEASE).json (OC-056)
	cd bench && go run ./cmd/odbbench gate --reference-only --gates gates-$(RELEASE).json --results results/$(RELEASE) $(BENCH_GATE_ARGS)

bench-smoke: ## Nightly smoke benchmark against the e2e stack: integrity gates + 20% regression check
	cd bench && go run ./cmd/odbbench --dataset smoke --gate integrity --regression-window 7 --regression-tolerance 0.2 \
		--pprof http://localhost:16060 --out $(BENCH_OUT) $(BENCH_ARGS)
