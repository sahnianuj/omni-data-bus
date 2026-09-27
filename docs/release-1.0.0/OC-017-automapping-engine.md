# OC-017 — Auto-mapping engine

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 8 | OC-015 | Implemented |

## User story
As a **data engineer**, I want the application to automatically match source columns to target document fields with an explainable confidence score, so that most of the mapping is done for me and I only handle the leftovers.

## Design goals
- **Deterministic.** The same inputs always produce the same output. There is no ML model in 1.0, so results are explainable and testable.
- **Explainable.** Every proposed mapping carries its score, the signals that fired, and a reason code.
- **Non-destructive.** Re-running never overwrites manual mappings or brings back pairs the user rejected.
- **Fast.** < 50 ms for 500 × 500 (NFR).

## Implementation details (`internal/automap`)

### Inputs & outputs
```go
type SourceCol struct { Name string; JSONType FieldType; Ordinal int; IsPK bool; Table string; EmbedPath string }
type TargetLeaf struct { FieldID, Path, Name string; Type FieldType; ParentNames []string; Required bool }

type Options struct {
    AutoThreshold      float64 // default 0.85
    SuggestThreshold   float64 // default 0.60
    Dictionaries       Dicts   // embedded defaults + future workspace extensions
    Existing           []mapping.FieldMapping // manual/accepted/rejected are respected
}

type Result struct {
    Mappings []mapping.FieldMapping // origin=auto (state active) | suggested (state pending)
    Stats    Stats
    Explain  map[string]Explanation // by mapping id
}
type Explanation struct { Score, NameScore, TypeScore float64; Signals []Signal; Reason string }
```
**Target leaves** are fields of scalar type, `any`, and `array` of scalar. Objects are containers only. Arrays of objects are handled by embeds (OC-021), except that a source `jsonb`/`json` column may map to an `object` or `array` field. For that case, object and array fields with no children are also leaves when the candidate source is `any`.

### Step 1 — Normalization (precomputed once per name)
`tokens(name)`:
1. Split on `_`, `-`, space, and `.`, on camelCase and PascalCase boundaries (`customerID` → customer, id), and on letter/digit boundaries (`addr2` → addr, 2).
2. Lowercase.
3. Expand abbreviations from `dict/abbreviations.yaml`:
   `addr→address, amt→amount, qty→quantity, desc→description, dt→date, ts→timestamp, num|no|nbr→number, cust→customer, acct→account, tel|ph→phone, pwd→password, cnt→count, fname→first name, lname→last name, dob→birth date, zip|postcode→postal code, …` (~150 entries)
4. Singularize with rule-based English (`categories→category`, `addresses→address`, `statuses→status`).
5. Drop the noise tokens `tbl` and `col`.

**Source variants:**
- the raw name;
- the tokens;
- the tokens with the table name prefix stripped (`cust_name` on `customer` → `name`; `customer_email` → `email`);
- for the PK column, the alias tokens `[id]`.

**Target variants:**
- the leaf name tokens;
- the full-path tokens (`billing.address.city` → billing, address, city);
- the path tokens without the root-level type prefix.

### Step 2 — Name score (maximum over variant pairs)
| Signal | Condition | Score |
|---|---|---|
| `EXACT` | Raw names equal | 1.00 |
| `NORMALIZED_EQUAL` | Token sequences equal (e.g., `first_name` ↔ `firstName`; `billing_city` ↔ `billing.city`) | 0.97 |
| `EXPANDED_EQUAL` | Equal after abbreviation expansion and singularization (`cust_addr` ↔ `customerAddress`) | 0.93 |
| `SYNONYM` | Token sequences in the same synonym group (`dict/synonyms.yaml`: `{email, email address, mail}`, `{phone, telephone, mobile, cell}`, `{created at, created, creation date, created on, create time}`, `{updated at, modified, last modified, updated on}`, `{zip, postal code}`, `{surname, last name, family name}`, `{given name, first name, forename}`, `{is active, active, enabled}`, …) | 0.88 |
| `TOKEN_OVERLAP` | Jaccard(token sets) J ≥ 0.5 | 0.55 + 0.35·J |
| `FUZZY` | Jaro-Winkler(joined tokens) JW ≥ 0.90 (handles typos such as `adress`) | 0.85·JW |
| `PREFIX_STRIPPED` | Any of the above matched via the table-prefix-stripped variant | −0.02 penalty |

### Step 3 — Type compatibility (multiplier)
| Source → Target | typeScore | Class |
|---|---|---|
| Same type; integer → number; any → any | 1.0 | exact |
| integer, number, boolean, datetime → string; datetime → datetime(format) | 0.85 | lossless |
| number → integer (truncation); string → number, integer, boolean, datetime (parse may fail); datetime → integer (epoch) | 0.5 | lossy |
| scalar ↔ object / array (except json/jsonb any → object/array) | 0.0 | incompatible (pair excluded) |

`score = nameScore × (0.8 + 0.2 × typeScore)`, or 0 when typeScore is 0.
**Structural bonus:** +0.03 (capped at 1.0) when the target's parent object name tokens are a prefix of the source tokens. For example, target `address.city` gets the bonus for source `address_city` over `billing_city`.

### Step 4 — Assignment
1. Remove targets already mapped by `manual`/`active` mappings and sources already used by them. Keep rejected pairs as a blocklist.
2. Build all candidate pairs with score ≥ SuggestThreshold that are not blocklisted.
3. Stable sort by (score desc, target required desc, source ordinal asc, target path asc).
4. Greedy one-to-one assignment: take a pair if neither side is taken. (The optimal Hungarian algorithm is not needed. The greedy approach is deterministic and easier to explain, and the test fixture set checks it matches Hungarian on ≥ 95% of cases.)
5. Pairs with score ≥ AutoThreshold get `origin=auto, state=active`. Pairs in [Suggest, Auto) get `origin=suggested, state=pending`.
6. Transforms: when the type class is `lossless` or `lossy`, attach the default conversion step from OC-020 (e.g., `toString`, `parseNumber`), and set `needsReview=true` for lossy ones.

### Step 5 — Stats (the score shown to the customer)
```go
type Stats struct {
    TotalSourceColumns      int     // excluding ignoredColumns
    AutoMapped              int     // sources with origin=auto active
    Suggested               int     // pending suggestions
    ManuallyMapped          int     // origin=manual or accepted suggestion
    UnmappedSource          int
    TotalTargetLeaves       int
    MappedTargetLeaves      int
    RequiredTargetUnmapped  int
    AutoMapRatio            float64 // AutoMapped / TotalSourceColumns  → "10 of 23 fields automatically mapped"
    CoverageRatio           float64 // (Auto+Manual) / TotalSourceColumns
    AverageConfidence       float64 // over auto mappings
}
```
- `automap_stats` holds the **snapshot taken at the time of the last automap run** ("initial score"). Live stats are recomputed on every save (`mapping.ComputeStats`). The UI shows both: the headline auto score and the live coverage.

### API
- `POST /projects/{id}/mappings/{mid}/automap {mode: "fill" | "reset"}`:
  - `fill` (the default) respects existing mappings.
  - `reset` clears auto and suggested mappings (never manual ones unless `includeManual:true`) and re-runs.
  - Returns `{spec, stats, explain}` and saves with a version bump.
- It also runs automatically on mapping creation (OC-007) and after a doc design replace or merge (OC-015).

### Dictionaries
`dict/*.yaml` are embedded with `go:embed` and loaded once. A unit test validates that no token appears in two synonym groups.

## Acceptance criteria & unit tests

### AC1 — Name normalization
**Given** the names `customerID`, `Customer_Id`, `CUSTOMER-ID`, `cust_id`, and `customerIds`, **then** all normalize to the token sequence `[customer, id]` (the last via singularization, `cust_id` via expansion).

| Test | Location | Verifies |
|---|---|---|
| `TestTokens_AC1_CaseAndSeparators` | `internal/automap/normalize_test.go` | |
| `TestTokens_AC1_AbbreviationExpansion` | `internal/automap/normalize_test.go` | |
| `TestTokens_AC1_Singularize` | `internal/automap/normalize_test.go` | Irregulars: `people→person`, `statuses→status`, `data` unchanged |
| `TestTokens_AC1_TablePrefixStripped` | `internal/automap/normalize_test.go` | |

### AC2 — Signal scoring
**Given** these pairs, **then** the scores and signals are as listed:
`first_name ↔ firstName` = 0.97 NORMALIZED_EQUAL; `cust_addr ↔ customerAddress` = 0.93 EXPANDED_EQUAL; `email_address ↔ email` = 0.88 SYNONYM; `adress_line ↔ addressLine` FUZZY ≥ 0.80; `created_on ↔ createdAt` SYNONYM; `billing_city ↔ billing.city` NORMALIZED_EQUAL via path.

| Test | Location | Verifies |
|---|---|---|
| `TestNameScore_AC2_SignalTable` | `internal/automap/score_test.go` | Table-driven with an epsilon |
| `TestNameScore_AC2_MaxOverVariants` | `internal/automap/score_test.go` | |
| `TestDicts_AC2_NoTokenInTwoSynonymGroups` | `internal/automap/normalize_test.go` | |

### AC3 — Type compatibility
**Given** `int4 → string`, **then** typeScore 0.85, a `toString` transform is attached, and needsReview is false. **Given** `text → integer`, **then** 0.5, a `parseInteger` transform, and needsReview true. **Given** `text → object`, **then** excluded. **Given** `jsonb → object`, **then** 1.0.

| Test | Location | Verifies |
|---|---|---|
| `TestTypeCompat_AC3_Matrix` | `internal/mapping/compat_test.go` | Full matrix of 8×8 |
| `TestAutomap_AC3_AttachesDefaultTransform` | `internal/automap/engine_test.go` | |

### AC4 — Deterministic one-to-one assignment with thresholds
**Given** a fixture where two sources compete for one target, **then** the higher score wins, the loser can take its next best target, results are identical across 100 runs with shuffled input order, scores ≥ 0.85 are `auto`, scores 0.60–0.85 are `suggested/pending`, and anything lower is unmapped.

| Test | Location | Verifies |
|---|---|---|
| `TestAutomap_AC4_OneToOneGreedy` | `internal/automap/engine_test.go` | |
| `TestAutomap_AC4_DeterministicUnderShuffle` | `internal/automap/engine_test.go` | 100 shuffles |
| `TestAutomap_AC4_Thresholds` | `internal/automap/engine_test.go` | |
| `TestAutomap_AC4_GreedyMatchesHungarianOnCorpus` | `internal/automap/hungarian_test.go` | ≥ 95% agreement over `testdata/corpus/*.json` |

### AC5 — Respect existing decisions
**Given** a manual mapping `phone → contact.mobile` and a rejected suggestion `notes → description`, **when** automap runs in `fill` mode, **then** the manual mapping is unchanged, neither `phone` nor `contact.mobile` is reassigned, and `notes → description` is not proposed again. **Given** `reset` mode, **then** auto and suggested mappings are rebuilt and the manual ones are kept.

| Test | Location | Verifies |
|---|---|---|
| `TestAutomap_AC5_FillRespectsManual` | `internal/automap/engine_test.go` | |
| `TestAutomap_AC5_RejectedNotResuggested` | `internal/automap/engine_test.go` | |
| `TestAutomap_AC5_ResetKeepsManual` | `internal/automap/engine_test.go` | |

### AC6 — Score statistics
**Given** a source table with 23 columns (1 ignored → 22 counted) and a target design where 10 columns match with ≥ 0.85, 4 with 0.6–0.85, and 8 not at all, **then** `Stats` = {TotalSourceColumns:22, AutoMapped:10, Suggested:4, UnmappedSource:8, AutoMapRatio≈0.4545}, and `RequiredTargetUnmapped` counts the required target leaves with no active mapping.

| Test | Location | Verifies |
|---|---|---|
| `TestComputeStats_AC6_Counts` | `internal/mapping/stats_test.go` | |
| `TestComputeStats_AC6_AcceptedSuggestionCountsAsManual` | `internal/mapping/stats_test.go` | |
| `TestComputeStats_AC6_IgnoredExcludedFromDenominator` | `internal/mapping/stats_test.go` | |

### AC7 — Explainability
**Given** any proposed mapping, **then** `explain[id]` contains the score, nameScore, typeScore, the ordered signals that fired (with the variant used), and a human reason string such as *"Names match after expanding 'cust' → 'customer' (93%); integer → string is lossless"*.

| Test | Location | Verifies |
|---|---|---|
| `TestExplain_AC7_ContainsSignalsAndReason` | `internal/automap/explain_test.go` | |

### AC8 — Performance
**Given** 500 source columns × 500 target leaves, **then** automap completes in < 50 ms and allocates < 20 MB.

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkAutomap_AC8_500x500` | `internal/automap/engine_bench_test.go` | Threshold helper fails the benchmark when exceeded |

### AC9 — API
**Given** `POST …/automap {mode:"fill"}`, **then** the response contains the updated spec, stats, and explain, and the mapping version is incremented. With a stale `If-Match`, it returns 412.

| Test | Location | Verifies |
|---|---|---|
| `TestAutomapHandler_AC9_ReturnsSpecStatsExplain` | `internal/mappings/service_test.go` | |
| `TestAutomapHandler_AC9_VersionConflict` | `internal/mappings/service_test.go` | |
