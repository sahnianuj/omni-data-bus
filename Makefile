# Omni-Data-Bus developer commands. Run `make help` for the list.
SHELL := /bin/bash
COMPOSE := docker compose -f deploy/compose/dev.yml
GOLANGCI_VERSION := v2.14.0
# Omni-Data-Bus needs no cgo; disabling it gives static binaries and avoids broken local C toolchains.
export CGO_ENABLED := 0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# release for a tag (v2.0.0), rc for a pre-release tag (v2.0.0-rc.1), dev otherwise (OC-058).
CHANNEL ?= $(shell v='$(VERSION)'; if [[ $$v =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$$ ]]; then echo release; \
	elif [[ $$v =~ ^v?[0-9]+\.[0-9]+\.[0-9]+-(rc|beta|alpha) ]]; then echo rc; else echo dev; fi)
IMAGE ?= omni-data-bus:$(VERSION)
BUILDINFO := omnidatabus/internal/buildinfo
LDFLAGS := -s -w -X $(BUILDINFO).version=$(VERSION) -X $(BUILDINFO).commit=$(COMMIT) \
	-X $(BUILDINFO).buildTime=$(BUILD_TIME) -X $(BUILDINFO).channel=$(CHANNEL)
HELM_IMAGE := alpine/helm:3.18.4
HELM_UNITTEST_IMAGE := helmunittest/helm-unittest:latest

.PHONY: help setup dev dev-stack dev-stack-down dev-reset run-backend run-web gen check-gen lint test test-backend test-budgets test-web test-integration test-bench coverage build image image-test helm-test e2e bench bench-smoke index

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

setup: ## Install Go modules, golangci-lint and web dependencies
	cd backend && go mod download
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	cd web && npm ci

dev: ## Start the dev stack, the API on :8080 and the web app on :5173
	./scripts/dev.sh

dev-stack: ## Start Couchbase 8.0.3 + sample source PostgreSQL and wait until healthy
	$(COMPOSE) up -d --wait
	$(COMPOSE) up couchbase-init --exit-code-from couchbase-init

dev-stack-down: ## Stop the dev stack (data is kept)
	$(COMPOSE) down

dev-reset: ## Stop the dev stack and delete its data volumes
	$(COMPOSE) down -v

run-backend: ## Run the Go server (reads .env; without one, uses the local dev stack)
	cd backend && if [ -f ../.env ]; then go run ./cmd/odb; else OC_DEV=true go run ./cmd/odb; fi

run-web: ## Run the Vite dev server
	cd web && npm run dev

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
	cd backend && go test -race -coverprofile=coverage/cover.out ./...
	cd backend && go run ./tools/covergate/cmd/covergate coverage/cover.out

test-budgets: ## Performance budget tests, uninstrumented (they skip under -race and -cover)
	cd backend && go test -count=1 -run 'Budget' ./...
	cd web && npm run bench

test-web: ## Web unit tests with coverage
	cd web && npm run test:coverage

test-bench: ## Benchmark harness unit tests (bench/, OC-034)
	cd bench && go vet ./... && go test -race ./...

test-integration: ## Go integration tests (needs Docker; uses Couchbase 8.0.3 and PostgreSQL containers)
	@mkdir -p backend/coverage
	cd backend && go test -race -tags=integration -count=1 -coverprofile=coverage/integration.out ./...
	cd backend && go run ./tools/covergate/cmd/covergate --integration coverage/integration.out

build: ## Build the web app and a single Go binary with the SPA embedded
	cd web && VITE_ODB_VERSION=$(VERSION) npm run build
	rm -rf backend/internal/httpapi/webdist && cp -R web/dist backend/internal/httpapi/webdist
	cd backend && CGO_ENABLED=0 go build -tags embedspa -trimpath -ldflags "$(LDFLAGS)" -o bin/odb ./cmd/odb
	cd backend && go test -tags embedspa -count=1 -run TestEmbeddedSPA ./internal/httpapi

image: ## Build the container image (IMAGE=omni-data-bus:<version>)
	docker build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) --build-arg CHANNEL=$(CHANNEL) -t $(IMAGE) .

image-test: image ## Check the image: user, entrypoint, files, commands, size < 40 MB
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$(CURDIR)/deploy/docker:/cfg" \
		gcr.io/gcp-runtimes/container-structure-test:latest test --image $(IMAGE) --config /cfg/structure-test.yaml
	@size=$$(docker image inspect $(IMAGE) --format '{{.Size}}'); echo "image size: $$size bytes"; \
		test $$size -lt 41943040 || (echo "image is larger than 40 MB" && exit 1)

helm-test: ## Lint the Helm chart and run its unit tests (via Docker)
	docker run --rm -v "$(CURDIR)/deploy/helm:/apps" -w /apps $(HELM_IMAGE) lint omni-data-bus --strict \
		-f omni-data-bus/tests/required-values.yaml
	docker run --rm -v "$(CURDIR)/deploy/helm:/apps" $(HELM_UNITTEST_IMAGE) omni-data-bus

index: ## Regenerate docs/release-1.0.0/INDEX.md from story statuses
	python3 scripts/update-story-index.py docs/release-1.0.0

e2e: ## End-to-end suite (Playwright) against the built image; E2E_BROWSERS=all adds Firefox and WebKit
	./scripts/e2e.sh

DATASET ?= standard
GATE ?= release
BENCH_OUT ?= results
bench: ## Benchmark against a running stack: make bench DATASET=standard GATE=release (see bench/README.md)
	cd bench && go run ./cmd/odbbench --dataset $(DATASET) --gate $(GATE) --out $(BENCH_OUT) $(BENCH_ARGS)

bench-smoke: ## Nightly smoke benchmark against the e2e stack: integrity gates + 20% regression check
	cd bench && go run ./cmd/odbbench --dataset smoke --gate integrity --regression-window 7 --regression-tolerance 0.2 \
		--pprof http://localhost:16060 --out $(BENCH_OUT) $(BENCH_ARGS)
