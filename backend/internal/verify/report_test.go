package verify

import (
	"encoding/csv"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func sampleReport() Report {
	start := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	end := start.Add(19 * time.Second)
	res := MappingResult{
		MappingID: "m1", Counts: Reconcile(CountsInput{RowsRead: 2000, Written: 1998, Failed: 2, Distinct: 1998, Writes: 1998}),
		Sample:      &SampleResult{Checked: 100, Matched: 99, Mismatched: 1, MissingKeys: []string{}, Mismatches: []Mismatch{{Key: "customer::7", Expected: json.RawMessage(`{"total":1}`), Actual: json.RawMessage(`{"total":2}`), Diffs: []Diff{{Path: "$.total", Expected: 1, Actual: 2}}}}},
		AvgDocBytes: 612, ThroughputAvg: 1050.2, ThroughputPeak: 1800, DurationSec: 19,
	}
	res.Decide()
	return Report{
		GeneratedAt: end,
		Job: JobInfo{
			ID: "j1", Status: "completed_with_errors", ProjectID: "p1", ProjectName: "Shop <migration>", StartedBy: "ana@example.com",
			Source: Endpoint{Type: "postgres", Host: "db.internal:5432", Database: "shop"}, Target: Endpoint{Type: "couchbase", Host: "cb.example.com"},
			Keyspaces: []string{"shop.sales.customer"}, StartedAt: &start, FinishedAt: &end, DurationSec: 19, Options: json.RawMessage(`{"writeMode":"upsert"}`),
		},
		Mappings: []MappingReport{
			{MappingID: "m1", Table: "shop.customer", Target: "shop.sales.customer", KeyTemplate: "customer::{customer_id}", RowsRead: 2000, Written: 1998, Failed: 2, Verification: &res},
			{MappingID: "m2", Table: "shop.orders", Target: "shop.sales.orders", KeyTemplate: "order::{order_id}"},
		},
		Errors: ErrorSummary{Total: 2, ByCode: []ErrorCount{{Stage: "transform", Code: "PARSE_FAILED", Count: 2}}, ExportURL: "/api/v1/jobs/j1/errors/export?format=csv"},
		Specs:  map[string]json.RawMessage{"m1": json.RawMessage(`{"key":{"template":"customer::{customer_id}"}}`)},
	}
}

func TestReport_AC4_JSONSections(t *testing.T) {
	raw, err := json.Marshal(sampleReport())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	for _, k := range []string{"generatedAt", "job", "mappings", "errors", "specs"} {
		require.Contains(t, doc, k)
	}
	job := doc["job"].(map[string]any)
	for _, k := range []string{"projectName", "source", "target", "keyspaces", "startedBy", "startedAt", "finishedAt", "durationSeconds", "options"} {
		require.Contains(t, job, k)
	}
	m := doc["mappings"].([]any)[0].(map[string]any)
	for _, k := range []string{"rowsRead", "written", "skipped", "failed", "keyTemplate", "verification"} {
		require.Contains(t, m, k)
	}
	v := m["verification"].(map[string]any)
	for _, k := range []string{"result", "counts", "sample", "avgDocBytes", "throughputAvg", "throughputPeak"} {
		require.Contains(t, v, k)
	}
	require.Equal(t, PassedWithWarnings, v["result"])
	require.Equal(t, "db.internal:5432", job["source"].(map[string]any)["host"])
}

func TestReport_AC4_CSV(t *testing.T) {
	raw, err := sampleReport().CSV()
	require.NoError(t, err)
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3, "a header and one row per mapping")
	require.Equal(t, CSVHeader, rows[0])
	require.Equal(t, []string{"m1", "shop.customer", "shop.sales.customer", "customer::{customer_id}", "2000", "1998", "0", "2", "612", "1050.2", "1800.0", "passed_with_warnings", "100", "99", "1", "0", "true", ""}, rows[1])
	require.Equal(t, "skipped", rows[2][11], "not verified")
}

func TestReport_AC4_HTMLStandalone(t *testing.T) {
	raw, err := sampleReport().HTML()
	require.NoError(t, err)
	html := string(raw)
	require.True(t, strings.HasPrefix(html, "<!doctype html>"))
	require.Contains(t, html, "<style>", "inline CSS")
	require.Contains(t, html, "Save as PDF")
	require.Contains(t, html, "Shop &lt;migration&gt;", "escaped")
	require.Contains(t, html, "shop@db.internal:5432")
	require.Contains(t, html, `class="pill passed_with_warnings"`)
	require.Contains(t, html, "99/100 samples match")
	require.Contains(t, html, "$.total")
	require.Contains(t, html, "PARSE_FAILED")
	require.Regexp(t, `2,000`, html)
	// Standalone: no external resources.
	for _, attr := range regexp.MustCompile(`(?i)(src|href)\s*=\s*"([^"]*)"`).FindAllStringSubmatch(html, -1) {
		require.NotRegexp(t, `^(https?:)?//`, attr[2], "external %s", attr[0])
	}
	require.NotContains(t, html, "@import")
	require.NotContains(t, html, "url(")
}
