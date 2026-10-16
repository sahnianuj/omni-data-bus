# OC-148 — A wrong password when confirming an export doesn't sign you out

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 1 | OC-052, OC-053 | Implemented |

## User story
As a **data engineer** exporting a migration script, I want a mistyped password at the "Confirm your password" step to show an error, so that I can retype it instead of being signed out and losing the export dialog.

## Background (2026-10-15)
Exporting sealed credentials asks for the password again (step-up). A wrong password returns 401 `INVALID_CREDENTIALS`, and the web client signed the user out on every 401. After the final step the user landed on the sign-in page instead of getting the download. The message also said "The email or password is incorrect", though no email is asked for.

## Scope
**In:**
- The web client signs out only for a lost session (`UNAUTHENTICATED`, `SESSION_EXPIRED`), not for `INVALID_CREDENTIALS`. Signing in with a wrong password is unchanged (the sign-in page already handles it).
- Step-up failure says "Wrong password — The password is incorrect." The export dialog keeps the user's choices and lets them retry.
- Failed step-ups stay rate-limited and audited (`auth.stepup.failure`).

**Out:** changing the step-up window or the rate limit.

## Acceptance criteria & unit tests

### AC1 — No sign-out on a wrong password
**Given** a signed-in user, **when** a request returns 401 `INVALID_CREDENTIALS`, **then** the global sign-out handler isn't called. **When** it returns `UNAUTHENTICATED`, **then** it is.

### AC2 — Retry in place
**Given** the export dialog asks for the password, **when** I type a wrong one, **then** "The password is incorrect." shows in the dialog; **when** I type the right one, **then** the export downloads.

| Test | Location | Verifies |
|---|---|---|
| `client.test.ts` › `a wrong password while signed in doesn't sign out` | `web/src/lib/api/client.test.ts` | AC1 |
| `ExportDialog.test.tsx` › `a wrong password shows an error and keeps the dialog open, without signing out` | `web/src/features/export/ExportDialog.test.tsx` | AC2 |
