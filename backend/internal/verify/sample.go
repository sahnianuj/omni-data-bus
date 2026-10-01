package verify

import (
	"context"
	"encoding/json"
	"fmt"
)

// MaxMismatchesKept bounds the mismatches kept in the report (the drawer shows them).
const MaxMismatchesKept = 20

// Mismatch is a sampled document that differs from its source row.
type Mismatch struct {
	Key       string          `json:"key"`
	SourceKey json.RawMessage `json:"sourceKey,omitempty"`
	Expected  json.RawMessage `json:"expected"`
	Actual    json.RawMessage `json:"actual"`
	Diffs     []Diff          `json:"diffs"`
}

// SampleResult is the outcome of re-reading and comparing sampled rows.
type SampleResult struct {
	Checked       int        `json:"checked"`
	Matched       int        `json:"matched"`
	Mismatched    int        `json:"mismatched"`
	Missing       int        `json:"missing"`
	SourceChanged int        `json:"sourceChanged"`
	MissingKeys   []string   `json:"missingKeys"`
	Mismatches    []Mismatch `json:"mismatches"`
}

// Sources re-reads source rows by key, in a new transaction.
type Sources func(ctx context.Context, keys [][]any) ([][]any, error)

// Built is one row's document (or why it couldn't be built).
type Built struct {
	Key string
	Doc []byte
	Err error
}

// Checker compares sampled rows (OC-030 AC3). KeyOf returns a row's key (matching
// Sample.SourceKey); Transform builds the rows' documents with the job's frozen program (in
// one go, so embedded rows are looked up once); Target fetches documents (absent keys are
// missing from the map).
type Checker struct {
	Read      Sources
	KeyOf     func(row []any) []any
	Transform func(ctx context.Context, rows [][]any) ([]Built, error)
	Target    func(ctx context.Context, keys []string) (map[string][]byte, error)
}

func keyString(k []any) string {
	b, _ := json.Marshal(jsonable(k))
	return string(b)
}

// jsonable makes driver values comparable after a JSON round trip (ints as float-free text).
func jsonable(k []any) []any {
	out := make([]any, len(k))
	for i, v := range k {
		switch x := v.(type) {
		case int16:
			out[i] = int64(x)
		case int32:
			out[i] = int64(x)
		case [16]byte:
			out[i] = fmt.Sprintf("%x", x)
		default:
			out[i] = v
		}
	}
	return out
}

// Check re-reads the samples' rows, transforms them and compares each with its document.
// A row that disappeared, or now makes a different key, counts as source-changed, not a mismatch.
func (c Checker) Check(ctx context.Context, samples []Sample) (SampleResult, error) {
	res := SampleResult{MissingKeys: []string{}, Mismatches: []Mismatch{}}
	if len(samples) == 0 {
		return res, nil
	}
	keys := make([][]any, len(samples))
	for i, s := range samples {
		keys[i] = s.SourceKey
	}
	rows, err := c.Read(ctx, keys)
	if err != nil {
		return res, fmt.Errorf("re-read sampled rows: %w", err)
	}
	built, err := c.Transform(ctx, rows)
	if err != nil {
		return res, fmt.Errorf("transform sampled rows: %w", err)
	}
	byKey := make(map[string]Built, len(rows))
	for i, r := range rows {
		byKey[keyString(c.KeyOf(r))] = built[i]
	}
	docKeys := make([]string, len(samples))
	for i, s := range samples {
		docKeys[i] = s.DocKey
	}
	docs, err := c.Target(ctx, docKeys)
	if err != nil {
		return res, fmt.Errorf("read sampled documents: %w", err)
	}
	for _, s := range samples {
		res.Checked++
		b, ok := byKey[keyString(s.SourceKey)]
		if !ok {
			res.SourceChanged++ // deleted since the migration
			continue
		}
		want := b.Doc
		if b.Err != nil || b.Key != s.DocKey {
			res.SourceChanged++ // changed so that it no longer transforms to the same document
			continue
		}
		got, ok := docs[s.DocKey]
		if !ok {
			res.Missing++
			res.MissingKeys = append(res.MissingKeys, s.DocKey)
			continue
		}
		diffs, err := Compare(want, got)
		if err != nil {
			return res, err
		}
		if len(diffs) == 0 {
			res.Matched++
			continue
		}
		res.Mismatched++
		if len(res.Mismatches) < MaxMismatchesKept {
			sk, _ := json.Marshal(jsonable(s.SourceKey))
			res.Mismatches = append(res.Mismatches, Mismatch{Key: s.DocKey, SourceKey: sk, Expected: want, Actual: got, Diffs: diffs})
		}
	}
	return res, nil
}

// MappingResult is one mapping's verification.
type MappingResult struct {
	MappingID string        `json:"mappingId"`
	Result    string        `json:"result"`
	Counts    Counts        `json:"counts"`
	Sample    *SampleResult `json:"sample,omitempty"`
	Error     string        `json:"error,omitempty"`
	// TargetKind names what was counted (couchbase: documents in a collection, directory:
	// files in a folder), and Integrity is a directory's manifest check (OC-041).
	TargetKind string     `json:"targetKind,omitempty"`
	Integrity  *Integrity `json:"integrity,omitempty"`
	// Stats of the run, for the report.
	AvgDocBytes    int64   `json:"avgDocBytes"`
	ThroughputAvg  float64 `json:"throughputAvg"`
	ThroughputPeak float64 `json:"throughputPeak"`
	DurationSec    float64 `json:"durationSeconds"`
}

// Decide sets Result: failed on any missing document or more than 1% mismatched samples;
// passed with warnings on other mismatches, count warnings or changed sources.
func (r *MappingResult) Decide() {
	switch {
	case r.Error != "":
		r.Result = PassedWithWarnings
	case r.Sample != nil && (r.Sample.Missing > 0 || float64(r.Sample.Mismatched) > 0.01*float64(max(r.Sample.Checked, 1))):
		r.Result = Failed
	case !r.Counts.Reconciled, r.Integrity != nil && r.Integrity.Missing > 0:
		r.Result = Failed
	case (r.Sample != nil && (r.Sample.Mismatched > 0 || r.Sample.SourceChanged > 0)) || hasRealWarning(r.Counts.Warnings),
		r.Integrity != nil && r.Integrity.Mismatched > 0:
		r.Result = PassedWithWarnings
	default:
		r.Result = Passed
	}
}

func hasRealWarning(ws []string) bool {
	for _, w := range ws {
		if w != CodePartial {
			return true
		}
	}
	return false
}

// Integrity is a manifest check: listed documents sampled against the stored files (OC-041).
type Integrity struct {
	Listed     int              `json:"listed"`
	Checked    int              `json:"checked"`
	Missing    int              `json:"missing"`
	Mismatched int              `json:"mismatched"`
	Issues     []IntegrityIssue `json:"issues"`
}

// IntegrityIssue is one document that doesn't check out: missing, or hash_mismatch.
type IntegrityIssue struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	Problem string `json:"problem"`
}
