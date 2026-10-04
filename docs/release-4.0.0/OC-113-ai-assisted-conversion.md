# OC-113 — AI-assisted conversion for functions the rules can't finish

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P1 | 5 | OC-107, OC-111 | Not started |

## User story
As a **developer**, I want, when my organization allows it, to ask an AI model to propose a conversion for an Assisted or Manual unit, checked automatically against the unit's test cases, so that the long tail of PL/SQL takes hours instead of days.

## Scope
**In:**
- Opt-in workspace setting ([architecture D8](00-architecture.md#d8--ai-assistance-is-optional-and-off-by-default)), off by default, owner-only: provider, model, API key (sealed secret), and a data notice the owner must accept ("PL/SQL source, signatures and the mapping excerpt for referenced tables are sent to the provider; no row data"). Server config `OC_AI_ENABLED=false` disables it for the whole installation.
- **Propose with AI** on a unit: the prompt contains the PL/SQL, the rule-based conversion with its TODOs, the `odb_plsql` API reference, the resolution-index excerpt (OC-106) and the target UDF kind. The answer is parsed into a new draft version (`origin: ai`) with the model named.
- Optional **verify loop**: deploy the draft to a sandbox scope (`odb_sandbox_<user>`), run the unit's test cases (OC-111), and on mismatches send the diffs back for up to 3 attempts. The result is a draft that is never auto-accepted or deployed to the real scope.
- Cost and usage shown per request; a per-workspace monthly token cap.
- Audit events for every AI request (unit, model, tokens; not the content).

**Out:** sending row data to the provider; AI for mappings.

## Acceptance criteria & unit tests

### AC1 — Off by default
**Given** a fresh installation, **then** the Propose with AI button is not shown, and no outbound call to any AI provider is possible (egress test).

### AC2 — Draft only
**Given** AI enabled and a fake provider returning a valid conversion, **then** a draft `origin: ai` version is created, labeled with the model, and the deployed version is unchanged.

### AC3 — Verify loop
**Given** a fake provider that returns a wrong conversion and then a correct one, **then** the loop runs the test cases twice, stops at the passing version, and records both attempts.

| Test | Location | Verifies |
|---|---|---|
| `TestAI_AC1_DisabledByDefault` | `internal/plsql/assist/assist_test.go` | AC1 |
| `TestAI_AC2_DraftOnly` | same | AC2 |
| `TestAI_AC3_VerifyLoop` | `internal/plsql/assist/assist_integration_test.go` | AC3 |
