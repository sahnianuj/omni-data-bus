#!/usr/bin/env bash
# Runs CI's checks (.github/workflows/ci.yml) on this machine, so failures show up before a push.
#   scripts/ci-local.sh          # everything CI runs that can run here; the pre-push hook (needs Docker; ~6 min)
#   scripts/ci-local.sh --fast   # the quick checks, while working (~2 min)
#   scripts/ci-local.sh lint e2e # only the named steps (see the list below)
# Every step runs even after a failure; the summary at the end lists what failed and where its log is.
# Not covered here: the real Windows and macOS runners (Windows is compile-checked) and the secret scan.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT=$PWD

FAST_STEPS=(actionlint lint unit-tests windows-compile govulncheck npm-audit notices check-gen web-tests)
FULL_STEPS=(actionlint lint backend-test windows-compile govulncheck npm-audit notices check-gen web
  bench helm runner-compat integration image e2e)

case "${1:-}" in
  --fast) STEPS=("${FAST_STEPS[@]}") ;;
  "") STEPS=("${FULL_STEPS[@]}") ;;
  -h | --help) sed -n '2,7p' "$0"; echo "steps: ${FULL_STEPS[*]} unit-tests web-tests"; exit 0 ;;
  *) STEPS=("$@") ;;
esac

LOGS=${TMPDIR:-/tmp}/odb-ci-local
rm -rf "$LOGS" && mkdir -p "$LOGS"
passed=() failed=() skipped=()

docker_up() { docker info >/dev/null 2>&1; }
skip() { echo "SKIP: $*"; return 99; }

# ── The steps: each mirrors a CI job (named in the comment) ────────────────────────────────
step_actionlint() { # actionlint
  docker_up || { skip "Docker isn't running"; return; }
  docker run --rm -v "$ROOT:/repo" -w /repo rhysd/actionlint:latest
}
step_lint() { # backend-lint, and web's lint + typecheck
  (cd backend && go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(make -s -f ../Makefile print-golangci)" run ./...) &&
    (cd web && npm run lint && npm run typecheck)
}
step_unit-tests() { # backend-test without -race and coverage (fast, uses Go's test cache)
  (cd backend && go test ./...)
}
step_backend-test() { # backend-test
  make test-backend
}
step_windows-compile() { # runner-e2e (windows-2022): the same packages, compiled for Windows
  local pkgs=(./cmd/odb/ ./internal/headless/ ./internal/export/ ./internal/mapping/ ./internal/pathcodec/)
  (cd backend && GOOS=windows GOARCH=amd64 go vet "${pkgs[@]}" && GOOS=windows GOARCH=amd64 go vet -tags integration ./cmd/odb/)
}
step_govulncheck() { # dependency-scan
  (cd backend && go run golang.org/x/vuln/cmd/govulncheck@latest ./...)
}
step_npm-audit() { # dependency-scan
  (cd web && npm audit --omit=dev --audit-level=high)
}
step_notices() { # notices
  make notices-check
}
step_check-gen() { # contract
  make check-gen && git diff --exit-code -- web/src/lib/api/schema.d.ts
}
step_web-tests() { # web, without coverage or the build
  (cd web && npx vitest run)
}
step_web() { # web
  (cd web && npm run lint && npm run typecheck && npm run test:coverage && npm run build)
}
step_bench() { # bench-harness, bench-gate
  make test-bench && make bench-gate BENCH_GATE_ARGS=--allow-missing
}
step_helm() { # helm
  docker_up || { skip "Docker isn't running"; return; }
  make helm-test
}
step_runner-compat() { # runner-compat-2.0
  command -v bats >/dev/null || { skip "bats isn't installed (brew install bats-core)"; return; }
  make test-scripts
}
step_integration() { # backend-integration
  docker_up || { skip "Docker isn't running"; return; }
  make test-integration
}
step_image() { # image-check: build, structure test and size, Trivy as CI runs it (DIRTY=false: CI builds a clean checkout)
  docker_up || { skip "Docker isn't running"; return; }
  make image-test IMAGE=omni-data-bus:ci VERSION=0.0.0-ci DIRTY=false &&
    docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v odb-trivy-cache:/root/.cache/trivy \
      "aquasec/trivy:$TRIVY_VERSION" image --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 omni-data-bus:ci
}
step_e2e() { # e2e: the @smoke Playwright tests against the image from the image step
  docker_up || { skip "Docker isn't running"; return; }
  docker image inspect omni-data-bus:ci >/dev/null 2>&1 || make image IMAGE=omni-data-bus:ci VERSION=0.0.0-ci DIRTY=false || return
  docker tag omni-data-bus:ci omni-data-bus:e2e &&
    (cd web && npx playwright install chromium) &&
    E2E_SKIP_BUILD=1 ./scripts/e2e.sh --grep @smoke
}
# The Trivy that CI's trivy-action pin (v0.36.0) installs.
TRIVY_VERSION=0.70.0

# ── Run them ────────────────────────────────────────────────────────────────────────────
start_all=$SECONDS
for s in "${STEPS[@]}"; do
  if ! declare -f "step_$s" >/dev/null; then
    echo "unknown step: $s (try --help)" >&2
    exit 2
  fi
  printf '▶ %-16s ' "$s"
  t=$SECONDS
  "step_$s" >"$LOGS/$s.log" 2>&1
  rc=$?
  d=$((SECONDS - t))
  if [ $rc -eq 0 ]; then
    echo "ok (${d}s)"
    passed+=("$s")
  elif [ $rc -eq 99 ]; then
    echo "skipped: $(grep -m1 '^SKIP: ' "$LOGS/$s.log" | cut -c7-)"
    skipped+=("$s")
  else
    echo "FAILED (${d}s)"
    tail -n 25 "$LOGS/$s.log" | sed 's/^/    /'
    failed+=("$s")
  fi
done

echo
echo "── ${#passed[@]} passed, ${#failed[@]} failed, ${#skipped[@]} skipped in $(((SECONDS - start_all) / 60))m$(((SECONDS - start_all) % 60))s"
for s in ${failed[@]+"${failed[@]}"}; do echo "   FAILED  $s  →  $LOGS/$s.log"; done
for s in ${skipped[@]+"${skipped[@]}"}; do echo "   skipped $s"; done
[ ${#failed[@]} -eq 0 ]
