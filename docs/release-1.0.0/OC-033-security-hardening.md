# OC-033 — Security hardening (egress control, headers, limits, audit)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E6 Operate & Harden | P0 | 5 | OC-005, OC-006, OC-009, OC-012 | Implemented |

## User story
As a **security reviewer**, I want the application protected against SSRF through user-supplied database hosts, common web attacks, and abuse, and I want sensitive actions audited, so that Omni-Data-Bus can be offered as an online service safely.

## Implementation details

### Egress guard (`internal/netguard`)
Users supply arbitrary hosts, so the server could be used to probe internal networks. Policy:
- **Always denied:** link-local and cloud metadata (`169.254.0.0/16`, `fe80::/10`, `fd00:ec2::254`), `0.0.0.0/8`, multicast, and broadcast.
- **Denied unless `OC_ALLOW_PRIVATE_NETWORKS=true`** (true by default for self-hosted, and should be false for SaaS): loopback (`127.0.0.0/8`, `::1`), RFC1918 (`10/8`, `172.16/12`, `192.168/16`), CGNAT (`100.64/10`), and ULA (`fc00::/7`).
- **Allow and deny lists:** `OC_EGRESS_ALLOW_CIDRS` and `OC_EGRESS_DENY_CIDRS`. Deny wins.
- **Ports:** allowed port ranges are configurable (defaults: 5432–5439, 6432 for PgBouncer, 8091–8097, 11207, 11210, 18091–18097, and 443).
- **Enforcement at dial time** (defeats DNS rebinding): `netguard.Dialer.DialContext` resolves the host, checks **every** resolved IP, and dials the checked IP directly (keeping SNI and ServerName for TLS). pgx uses it through `DialFunc` (with `LookupFunc` also overridden).
- **Couchbase:** gocb doesn't expose a custom dialer for all services. Mitigation:
  1. Pre-resolve and check the bootstrap hosts before `gocb.Connect`.
  2. After bootstrap, fetch the cluster node list from `cluster.Diagnostics()` / the ping report and verify every node address against the policy. On a violation, close the cluster and fail with `EGRESS_DENIED`.
  3. Recommend a NetworkPolicy / egress firewall in the SaaS deployment docs (OC-032) as defense in depth.

  This limitation is documented in the security notes.
- Errors: `EGRESS_DENIED` (422) with the message "Connections to private or internal addresses are disabled on this server." The IP is not echoed, to avoid leaking resolution results.

### HTTP security headers (middleware)
- `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`. Fonts are self-hosted, with no CDN.
- `Strict-Transport-Security: max-age=63072000; includeSubDomains` (when `OC_PUBLIC_URL` is https).
- `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`, and `Cross-Origin-Opener-Policy: same-origin`.

### Limits
- Request body: 1 MB by default. 6 MB for `doc-schemas/infer` (JSON samples) and cert uploads, enforced with `http.MaxBytesReader`.
- **API rate limiting:** per session 20 req/s with a burst of 40; per IP for unauthenticated routes 5 req/s. It returns 429 with `Retry-After`. The login limiter is in OC-005.
- **Expensive operations:** at most 2 concurrent discoveries and 4 previews per workspace, and at most 3 connection tests per session per 10 s.
- SSE: at most 10 concurrent streams per session.
- JSON decoding uses `DisallowUnknownFields` on mutating endpoints (via the generated strict server) and a depth limit (64) for user-supplied JSON documents and samples.

### Injection safety
- All SQL built from user-controlled identifiers goes through `pgx.Identifier.Sanitize()`, and values are always parameters. A **lint test** scans `internal/connectors/source/postgres` for `fmt.Sprintf` used with `SELECT`/`FROM`/`WHERE` strings that don't go through the identifier helper.
- N1QL identifiers use backticks and are validated against the Couchbase name rules (OC-014).
- The expression sandbox is covered in OC-020.

### Audit log completeness
- Actions: auth.*, project.create/update/delete, connection.create/update/delete/test, keyspace.create, mapping.update (throttled to one audit entry per 10 minutes per mapping), job.create/pause/resume/cancel/retry, and report.download.
- `GET /api/v1/audit?cursor=` is visible to owners, with a Settings → Audit log page (table with filters).
- Retention: `OC_AUDIT_RETENTION_DAYS` (default 365), enforced by a cleanup job.

### Dependency & code scanning
`govulncheck`, `gosec` (via golangci-lint), `pnpm audit --prod` (high severity fails), and CodeQL for Go and TS, all in CI.

## Acceptance criteria & unit tests

### AC1 — Egress policy decisions
**Given** each IP class (metadata, loopback, RFC1918, CGNAT, ULA, public, IPv4-mapped IPv6 `::ffff:10.0.0.1`), with and without `AllowPrivateNetworks` and with the allow and deny CIDR lists, **then** the decision matches the policy table. Metadata is **always** denied.

| Test | Location | Verifies |
|---|---|---|
| `TestPolicy_AC1_Matrix` | `internal/netguard/policy_test.go` | |
| `TestPolicy_AC1_IPv4MappedIPv6Normalized` | same | |
| `TestPolicy_AC1_PortAllowList` | same | |

### AC2 — Dial-time enforcement against DNS rebinding
**Given** a resolver returning `[203.0.113.5, 10.0.0.5]` for a host, with private networks disallowed, **then** the dial is refused. **Given** a resolver that returns a public IP on the check and a private one on a second lookup, **then** the dialer connects only to the checked IP (it never resolves twice).

| Test | Location | Verifies |
|---|---|---|
| `TestDialer_AC2_RejectsIfAnyResolvedIPDenied` | `internal/netguard/dialer_test.go` | Fake resolver |
| `TestDialer_AC2_DialsCheckedIPOnly` | same | Fake dialer records the address |
| `TestPgConfig_AC2_UsesGuardedDialAndLookup` | `internal/connectors/source/postgres/connconfig_test.go` | |

### AC3 — Couchbase node verification
**Given** bootstrap to a public host whose cluster reports a node at `169.254.169.254`, **then** the session is closed and `EGRESS_DENIED` is returned.

| Test | Location | Verifies |
|---|---|---|
| `TestCouchbaseEgress_AC3_NodeListChecked` | `internal/connectors/target/couchbase/egress_test.go` | Fake diagnostics report |

### AC4 — Security headers
**Given** any response, **then** all the headers above are present with the exact values, and HSTS is present only for an https public URL.

| Test | Location | Verifies |
|---|---|---|
| `TestSecurityHeaders_AC4_Present` | `internal/httpapi/security_headers_test.go` | |
| `TestSecurityHeaders_AC4_HSTSOnlyHTTPS` | same | |

### AC5 — Body, rate, and concurrency limits
**Given** a 2 MB body to a 1 MB endpoint, **then** 413. **Given** 41 rapid requests in a session, **then** the 41st gets 429 with `Retry-After`. **Given** a 3rd concurrent discovery in a workspace, **then** 429 `TOO_MANY_CONCURRENT_OPERATIONS`.

| Test | Location | Verifies |
|---|---|---|
| `TestBodyLimit_AC5_413` | `internal/httpapi/limits_test.go` | |
| `TestRateLimit_AC5_SessionBurst` | same | Fake clock |
| `TestOpLimiter_AC5_ConcurrentDiscoveries` | `internal/httpapi/oplimit_test.go` | |
| `TestJSONDepth_AC5_Rejected` | `internal/docschema/infer_test.go` | Depth 65 is rejected |

### AC6 — SQL construction lint
**Given** the postgres connector package, **then** no SQL string is built with `fmt.Sprintf` / `+` containing identifiers unless it goes through `quoteIdent`/`pgx.Identifier`.

| Test | Location | Verifies |
|---|---|---|
| `TestNoUnsafeSQLConstruction_AC6` | `internal/connectors/source/postgres/sqllint_test.go` | `go/ast` walk |
| `FuzzQuoteIdent_AC6` | `internal/connectors/source/postgres/quote_test.go` | Round-trip through the PG parser rules; no unbalanced quotes |

### AC7 — Audit coverage & UI
**Given** each auditable action, **then** exactly one audit record with the actor, action, entity, and non-secret metadata is written. The Audit page lists, filters, and pages them for owners, and editors get 403.

| Test | Location | Verifies |
|---|---|---|
| `TestAudit_AC7_AllActionsCovered` | `internal/audit/coverage_test.go` | Table of actions → invoke the service → the audit fake records them |
| `TestAuditHandler_AC7_OwnerOnly` | `internal/httpapi/audit_test.go` | |
| `AuditLogPage.test.tsx` › `AC7: renders and filters` | `src/features/settings/AuditLogPage.test.tsx` | |

## Implementation notes
- **Egress** (`internal/netguard`): `Policy` + `Dialer`. `app.Run` installs the process-wide dialer from `OC_ALLOW_PRIVATE_NETWORKS`, `OC_EGRESS_ALLOW_CIDRS`, `OC_EGRESS_DENY_CIDRS` and `OC_EGRESS_PORTS` (new, replaces the default port list). pgx uses `LookupFunc` + `DialFunc` (read sessions copy the config, so bulk reads are guarded too), and TLS still verifies against the host name. Couchbase: the bootstrap hosts are checked before `gocb.Connect` (an unresolvable host is left to the SDK), then every endpoint in `Diagnostics()` after `WaitUntilReady`; the auth prober's HTTP request uses the guarded dialer. A refusal is `EGRESS_DENIED` (422) for either role, with the fixed message and no IP.
- **CSP:** the pre-paint theme script moved from an inline `<script>` to `public/theme-init.js`, so `script-src 'self'` holds without hashes or nonces.
- **Body limits:** a `BodyLimit` middleware (1 MB; 6 MB for `doc-schemas/infer|export`, connection settings and `connections/test`) returns `PAYLOAD_TOO_LARGE` (413); `DecodeJSON` uses the same limit and maps `http.MaxBytesError` to 413.
- **Rate and concurrency limits** are in-memory per node (`httpapi.RateLimiter`, `OpLimiter`, `WindowLimiter`), wired in `app/limits.go` after the session middleware. Discoveries are capped inside `discovery.Service` (2 running per workspace), because async runs outlive their request. New codes: `TOO_MANY_CONCURRENT_OPERATIONS`, `PAYLOAD_TOO_LARGE`; limits reuse `RATE_LIMITED`. The "3 connection tests per 10 s" window applies to tests of unsaved settings (`POST /connections/test`), which can target any host. The UI re-tests a repository right after saving it, so counting stored tests tripped the limit in the normal flow; found by the E2E suite (OC-034).
- **JSON depth:** the generated strict server doesn't exist in this codebase (handlers use `DecodeJSON` with `DisallowUnknownFields`); samples are depth-checked in `docschema.ParseSamples` (a byte scan) and while decoding (`decodeOrderedAt`), which also covers documents sampled from Couchbase.
- **SQL lint** is a `go/ast` walk: `+` chains or `fmt.Sprintf` calls containing SQL text may not read identifier fields (`.Schema`, `.Table`, `.Name`, …) outside `Sanitize`/`quoteIdent`/`tableIdent`. `testdata/sqllint/unsafe.go.txt` proves it catches both forms. `FuzzQuoteIdent_AC6` checks that `quoteIdent` and `pgx.Identifier.Sanitize` produce well-formed identifiers that round-trip.
- **Audit:** `internal/audit` holds the action catalogue, a `Recorder` (throttles `mapping.update` to once per mapping per 10 minutes) and `GET /api/v1/audit` (owner-only, filters `action` exact or prefix, `entityType`, `userId`; keyset cursor). Job actions and report downloads (`?download=1` only) are recorded in the job handlers, so internal composition (a restart cancels and creates) stays one entry per user action. Retention is the document expiry, now `OC_AUDIT_RETENTION_DAYS`, so no cleanup job is needed. The handler test is `TestAuditHandler_AC7_OwnerOnly` in `internal/audit/audit_test.go` (the audit package can't live in `httpapi`). The coverage test checks both directions: every catalogued action is recorded somewhere, and every recorded action is catalogued.
- **Scanning:** CI job `dependency-scan` (govulncheck, `npm audit --omit=dev --audit-level=high`; the repo uses npm, not pnpm) and `.github/workflows/codeql.yml` (Go + TypeScript, security-extended). govulncheck found a reachable issue in `google.golang.org/grpc` v1.82.1 (via the Couchbase SDK); upgraded to v1.83.1.
