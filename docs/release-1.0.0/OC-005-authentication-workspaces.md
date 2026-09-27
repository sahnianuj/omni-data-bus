# OC-005 — Authentication, sessions & workspaces

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 Foundation | P0 | 8 | OC-003, OC-004 | Implemented |

## User story
As a **user**, I want to sign up and sign in securely and have my connections and projects private to my workspace, so that database credentials and migration work are protected.

## Scope
**In:** email and password sign-up and sign-in, server-side sessions, CSRF protection, sign-out, a personal workspace created at sign-up, workspace roles (`owner`, `editor`, `viewer`) enforced on the API, a profile page, and a login rate limit.
**Out (post-1.0):** SSO/OIDC, invitations UI, MFA, password reset by email. The `auth.Provider` interface is shaped so OIDC can be added later.

## Implementation details

### Backend (`internal/auth`)
- **Passwords:** argon2id (`m=64 MiB, t=3, p=2`, 16-byte salt, 32-byte key), encoded in PHC string format. A password must be ≥ 12 characters and is checked against a small embedded list of the most common passwords (top 10k).
- **Sign-up** `POST /api/v1/auth/signup {email, password, displayName}` runs one transaction that creates the user, a workspace named `"<displayName>'s workspace"`, and an owner membership, then creates a session. When sign-up is disabled (`OC_SIGNUP_ENABLED=false`), it returns 403 `SIGNUP_DISABLED`.
- **Sign-in** `POST /api/v1/auth/login {email, password}` compares in constant time and returns the same `INVALID_CREDENTIALS` error for an unknown email and a wrong password. To defeat user enumeration by timing, a dummy hash is verified when the user doesn't exist.
- **Sessions:**
  - The token is 32 random bytes (base64url) and goes in the cookie `oc_session` with `HttpOnly; Secure (config); SameSite=Lax; Path=/`.
  - The DB stores only the `sha256(token)`.
  - Idle TTL is 12 h (sliding: `last_seen_at` is updated at most once per minute) and absolute TTL is 7 days.
  - The session ID is rotated on login.
- **CSRF:** a per-session random `csrf_token` is returned in `GET /auth/me` and the login response body. The `CSRF` middleware requires `X-CSRF-Token` to match (constant-time) on POST, PUT, PATCH, and DELETE. This is combined with `SameSite=Lax` and an `Origin` check against `OC_PUBLIC_URL`.
- **Sign-out** `POST /auth/logout` deletes the session row and clears the cookie.
- **Me** `GET /auth/me` returns `{user, workspace, role, csrfToken}`.
- **Auth middleware** loads the session, then puts a `Principal{UserID, WorkspaceID, Role}` in the context. Endpoint groups declare the minimum role through `RequireRole(auth.Editor)`: viewers can read everything but cannot create, update, or delete, or start jobs.
- **Login rate limit:** a token bucket keyed by IP+email, 5 attempts per minute and 20 per hour, in memory with an LRU of 100k keys. For multi-node deployments it is backed by counter documents in the `rate_limits` collection with expiry (Couchbase atomic `Increment`), chosen by config. Exceeding it returns `429` with `Retry-After`.
- **Expired sessions** disappear on their own: each session document is written with a Couchbase **expiry** equal to the absolute TTL, so no cleanup job is needed.
- **Audit log:** `auth.login.success`, `auth.login.failure`, `auth.logout`, and `auth.signup`.

### Frontend (`src/features/auth`)
- The `/signin` and `/signup` pages are a centered card with the product logo, and forms use react-hook-form + zod. They have an inline password strength meter (zxcvbn-ts) and a show/hide toggle, and the submit button shows a loading state.
- A `useSession()` hook wraps `GET /auth/me` in TanStack Query with `staleTime` 5 min, and `RequireAuth` redirects to `/signin?next=<path>`.
- A global 401 handler in the API client clears the query cache and redirects to sign-in, preserving `next`.
- Role-aware UI: a `<Can role="editor">` wrapper hides or disables mutating actions for viewers, with a tooltip saying "Viewer role — read only".

## Acceptance criteria & unit tests

### AC1 — Sign-up creates user, workspace & session
**Given** a valid email and a password of ≥ 12 characters that isn't in the common list, **when** the user signs up, **then** a user, a workspace, and an owner membership are created atomically, a session cookie is set, and the user lands on the Dashboard. **Given** a duplicate email, **then** a 409 `EMAIL_TAKEN` is returned. **Given** a weak password, **then** a 422 is returned with a field error on `password`.

| Test | Location | Verifies |
|---|---|---|
| `TestSignup_AC1_CreatesUserWorkspaceMembership` | `internal/auth/service_test.go` | Fake repo asserts a single tx with 3 inserts |
| `TestSignup_AC1_DuplicateEmail` | same | |
| `TestValidatePassword_AC1_Rules` | `internal/auth/password_test.go` | Table: too short, common, OK |
| `SignUpPage.test.tsx` › `AC1: submits and redirects to dashboard` | `src/features/auth/SignUpPage.test.tsx` | MSW |
| `SignUpPage.test.tsx` › `AC1: shows server field errors` | same | |

### AC2 — Password hashing
**Given** a password, **then** the stored hash is argon2id in PHC format with the configured params, and verification succeeds for the right password and fails for a wrong one.

| Test | Location | Verifies |
|---|---|---|
| `TestHash_AC2_PHCFormatAndParams` | `internal/auth/password_test.go` | |
| `TestVerify_AC2_RightAndWrong` | same | |
| `TestVerify_AC2_ConstantTimeCompareUsed` | same | Uses `subtle.ConstantTimeCompare` (checked through an injected comparer spy) |

### AC3 — Sign-in with non-enumerable errors
**Given** an unknown email or a wrong password, **then** the response is the same 401 `INVALID_CREDENTIALS`, and the unknown-email path still performs one hash verification.

| Test | Location | Verifies |
|---|---|---|
| `TestLogin_AC3_UnknownEmailSameErrorAndHashes` | `internal/auth/service_test.go` | Hasher spy called once |
| `TestLogin_AC3_WrongPassword` | same | |
| `TestLogin_AC3_RotatesSession` | same | Previous session for that browser is deleted |

### AC4 — Session lifecycle
**Given** a valid session, requests succeed and `last_seen_at` is updated at most once per minute. **Given** idle > 12 h or age > 7 days, **then** 401 `SESSION_EXPIRED` is returned. **When** the user signs out, the session row is deleted and the cookie is cleared (`Max-Age=0`).

| Test | Location | Verifies |
|---|---|---|
| `TestSessionMiddleware_AC4_ValidSession` | `internal/auth/middleware_test.go` | Fake clock |
| `TestSessionMiddleware_AC4_IdleExpired` | same | |
| `TestSessionMiddleware_AC4_AbsoluteExpired` | same | |
| `TestSessionMiddleware_AC4_TouchThrottled` | same | Two requests within 60 s → one touch |
| `TestLogout_AC4_DeletesAndClearsCookie` | `internal/auth/handlers_test.go` | |
| `TestSessionStore_AC4_StoresOnlyHash` | `internal/auth/session_test.go` | |

### AC5 — CSRF protection
**Given** a mutating request without `X-CSRF-Token`, with a wrong token, or with a foreign `Origin`, **then** it returns 403 `CSRF_FAILED`. GET requests are unaffected.

| Test | Location | Verifies |
|---|---|---|
| `TestCSRF_AC5_MissingToken` | `internal/auth/csrf_test.go` | |
| `TestCSRF_AC5_WrongToken` | same | |
| `TestCSRF_AC5_ForeignOrigin` | same | |
| `TestCSRF_AC5_SafeMethodsPass` | same | |

### AC6 — Role enforcement
**Given** a viewer, **when** they call a mutating endpoint (e.g., `POST /connections`), **then** it returns 403 `FORBIDDEN_ROLE`. Read endpoints return 200. In the UI, mutating buttons are disabled with an explanatory tooltip.

| Test | Location | Verifies |
|---|---|---|
| `TestRequireRole_AC6_ViewerBlockedFromEditor` | `internal/auth/middleware_test.go` | |
| `TestRequireRole_AC6_OwnerAllowed` | same | |
| `Can.test.tsx` › `AC6: disables children with tooltip for viewer` | `src/features/auth/Can.test.tsx` | |

### AC7 — Login rate limiting
**Given** 5 failed logins for the same IP+email within 1 minute, **then** the 6th returns 429 with a `Retry-After` header, and a successful login after the window is allowed.

| Test | Location | Verifies |
|---|---|---|
| `TestLoginLimiter_AC7_BlocksSixth` | `internal/auth/ratelimit_test.go` | Fake clock |
| `TestLoginLimiter_AC7_WindowResets` | same | |
| `SignInPage.test.tsx` › `AC7: shows retry-after message on 429` | `src/features/auth/SignInPage.test.tsx` | |

### AC8 — Protected routes
**Given** an unauthenticated visit to `/projects/abc`, **then** the user is redirected to `/signin?next=%2Fprojects%2Fabc`, and after sign-in lands on `/projects/abc`. A 401 from any API call triggers the same redirect.

| Test | Location | Verifies |
|---|---|---|
| `RequireAuth.test.tsx` › `AC8: redirects with next param` | `src/features/auth/RequireAuth.test.tsx` | |
| `SignInPage.test.tsx` › `AC8: navigates to next after login` | `src/features/auth/SignInPage.test.tsx` | Rejects `next` values that are not same-origin paths (open redirect) |
| `client.test.ts` › `AC8: 401 triggers auth redirect` | `src/lib/api/client.test.ts` | |

## Implementation notes (as built)
- **Sessions** are Couchbase documents keyed by `sha256(token)` with a document expiry equal to the absolute TTL. The first session is written right after the sign-up transaction, because Couchbase transactions can't set expiry. `lastSeenAt` is updated with a sub-document mutation that preserves the expiry.
- **Login rate limiting** is in memory, per server node (5/minute, 20/hour per IP + email). The shared, Couchbase-backed limiter for multi-node deployments is left for OC-033.
- **Password strength meter** in the UI is a lightweight length and character-variety heuristic rather than zxcvbn-ts, avoiding a ~1 MB dictionary download. The server-side policy (≥ 12 characters, not in the 10k common list, not the email) is what's enforced.
- `problem.HasCode` was added because `problem.Problem` values can't be compared with `errors.Is`.
