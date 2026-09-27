package verify

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"time"
)

// ReportDoc is the stored verification document (job_reports): each mapping's result.
type ReportDoc struct {
	Mappings map[string]MappingResult `json:"mappings"`
}

// ParseReport decodes a stored report document (empty = none yet).
func ParseReport(raw json.RawMessage) ReportDoc {
	var r ReportDoc
	_ = json.Unmarshal(raw, &r)
	if r.Mappings == nil {
		r.Mappings = map[string]MappingResult{}
	}
	return r
}

// Endpoint names a repository without credentials: db@host for a source, the cluster host for
// a target.
type Endpoint struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Database string `json:"database,omitempty"`
}

// ErrorCount is one error code's count.
type ErrorCount struct {
	Stage string `json:"stage"`
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// MappingReport is one table's section.
type MappingReport struct {
	MappingID    string         `json:"mappingId"`
	Table        string         `json:"table"`
	Target       string         `json:"target"`
	KeyTemplate  string         `json:"keyTemplate"`
	RowsRead     int64          `json:"rowsRead"`
	Written      int64          `json:"written"`
	Skipped      int64          `json:"skipped"`
	Failed       int64          `json:"failed"`
	Verification *MappingResult `json:"verification,omitempty"`
}

// Report is the job report (OC-030): what ran, with which settings, what it produced and how
// it verified. It never contains credentials.
type Report struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	Job         JobInfo         `json:"job"`
	Mappings    []MappingReport `json:"mappings"`
	Errors      ErrorSummary    `json:"errors"`
	// Specs are the frozen mapping specs the job ran, by mapping id (for auditability).
	Specs map[string]json.RawMessage `json:"specs"`
}

// JobInfo is the report's header.
type JobInfo struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	ProjectID   string          `json:"projectId"`
	ProjectName string          `json:"projectName"`
	Source      Endpoint        `json:"source"`
	Target      Endpoint        `json:"target"`
	Keyspaces   []string        `json:"keyspaces"`
	StartedBy   string          `json:"startedBy"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	FinishedAt  *time.Time      `json:"finishedAt,omitempty"`
	DurationSec float64         `json:"durationSeconds"`
	Options     json.RawMessage `json:"options"`
}

// ErrorSummary counts errors by code, with where to download them all.
type ErrorSummary struct {
	Total     int64        `json:"total"`
	ByCode    []ErrorCount `json:"byCode"`
	ExportURL string       `json:"exportUrl"`
}

// CSVHeader is the report CSV's columns (one row per mapping).
var CSVHeader = []string{
	"mapping_id", "table", "target", "key_template", "rows_read", "written", "skipped", "failed", "avg_doc_bytes",
	"throughput_avg", "throughput_peak", "result", "samples_checked", "samples_matched", "samples_mismatched", "samples_missing",
	"counts_reconciled", "warnings",
}

// CSV renders one row per mapping.
func (r Report) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write(CSVHeader)
	for _, m := range r.Mappings {
		v := m.Verification
		if v == nil {
			v = &MappingResult{Result: Skipped}
		}
		s := v.Sample
		if s == nil {
			s = &SampleResult{}
		}
		warnings := ""
		for i, x := range v.Counts.Warnings {
			if i > 0 {
				warnings += ";"
			}
			warnings += x
		}
		_ = w.Write([]string{
			m.MappingID, m.Table, m.Target, m.KeyTemplate, i64(m.RowsRead), i64(m.Written), i64(m.Skipped), i64(m.Failed), i64(v.AvgDocBytes),
			strconv.FormatFloat(v.ThroughputAvg, 'f', 1, 64), strconv.FormatFloat(v.ThroughputPeak, 'f', 1, 64), v.Result,
			strconv.Itoa(s.Checked), strconv.Itoa(s.Matched), strconv.Itoa(s.Mismatched), strconv.Itoa(s.Missing),
			strconv.FormatBool(v.Counts.Reconciled), warnings,
		})
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }

// HTML renders a standalone, print-friendly page (inline CSS, no external resources).
func (r Report) HTML() ([]byte, error) {
	var b bytes.Buffer
	err := reportTemplate.Execute(&b, r)
	return b.Bytes(), err
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"n": func(v any) string {
		switch x := v.(type) {
		case int64:
			return group(x)
		case int:
			return group(int64(x))
		case float64:
			return group(int64(x + 0.5))
		}
		return fmt.Sprint(v)
	},
	"ts": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04:05 UTC")
	},
	"tsv": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") },
	"dur": func(s float64) string { return (time.Duration(s) * time.Second).String() },
	"json": func(v any) string {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	},
	"raw": func(v json.RawMessage) string {
		var out bytes.Buffer
		if json.Indent(&out, v, "", "  ") != nil {
			return string(v)
		}
		return out.String()
	},
}).Parse(reportHTML))

func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

const reportHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Migration report · {{.Job.ProjectName}}</title>
<style>
:root { --fg:#16181d; --muted:#5b6170; --border:#e3e5ea; --soft:#f6f7f9; --ok:#157347; --warn:#b45309; --bad:#b42318; --brand:#4f46e5; }
* { box-sizing: border-box; }
body { margin: 0; font: 14px/1.5 -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; color: var(--fg); background: #fff; }
main { max-width: 960px; margin: 0 auto; padding: 32px 24px 64px; }
header { display: flex; align-items: center; gap: 12px; border-bottom: 3px solid var(--brand); padding-bottom: 12px; margin-bottom: 24px; }
header .mark { width: 28px; height: 28px; border-radius: 7px; background: var(--brand); }
h1 { font-size: 22px; margin: 0; } h2 { font-size: 16px; margin: 28px 0 8px; }
.hint { color: var(--muted); font-size: 12px; }
dl { display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; margin: 0; }
dt { color: var(--muted); } dd { margin: 0; }
table { width: 100%; border-collapse: collapse; margin-top: 8px; font-size: 13px; }
th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid var(--border); vertical-align: top; }
th { background: var(--soft); font-weight: 600; }
td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
.pill { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: 12px; font-weight: 600; }
.passed { background: #e7f5ee; color: var(--ok); } .passed_with_warnings { background: #fff6e5; color: var(--warn); }
.failed { background: #fdecea; color: var(--bad); } .skipped { background: var(--soft); color: var(--muted); }
code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
pre { background: var(--soft); border: 1px solid var(--border); border-radius: 6px; padding: 12px; overflow-x: auto; white-space: pre-wrap; }
details { margin: 8px 0; }
@media print { .hint.print-hide { display: none; } main { padding: 0; } pre { white-space: pre-wrap; } }
</style>
</head>
<body>
<main>
<header><div class="mark" aria-hidden="true"></div><div><h1>Migration report</h1><div class="hint">Omni-Data-Bus · generated {{tsv .GeneratedAt}}</div></div></header>
<p class="hint print-hide">To keep a PDF, use your browser's Print → Save as PDF.</p>

<h2>Job</h2>
<dl>
<dt>Project</dt><dd>{{.Job.ProjectName}}</dd>
<dt>Job</dt><dd><code>{{.Job.ID}}</code> · {{.Job.Status}}</dd>
<dt>Source</dt><dd>{{.Job.Source.Type}} · {{if .Job.Source.Database}}{{.Job.Source.Database}}@{{end}}{{.Job.Source.Host}}</dd>
<dt>Target</dt><dd>{{.Job.Target.Type}} · {{.Job.Target.Host}}</dd>
<dt>Keyspaces</dt><dd>{{range $i, $k := .Job.Keyspaces}}{{if $i}}, {{end}}<code>{{$k}}</code>{{end}}</dd>
<dt>Started by</dt><dd>{{.Job.StartedBy}}</dd>
<dt>Started</dt><dd>{{ts .Job.StartedAt}}</dd>
<dt>Finished</dt><dd>{{ts .Job.FinishedAt}}</dd>
<dt>Duration</dt><dd>{{dur .Job.DurationSec}}</dd>
</dl>

<h2>Tables</h2>
<table>
<thead><tr><th>Table</th><th>Target</th><th class="num">Read</th><th class="num">Written</th><th class="num">Skipped</th><th class="num">Failed</th><th class="num">Avg doc</th><th class="num">Docs/s (avg · peak)</th><th>Verification</th></tr></thead>
<tbody>
{{range .Mappings}}<tr>
<td><code>{{.Table}}</code><div class="hint">key <code>{{.KeyTemplate}}</code></div></td>
<td><code>{{.Target}}</code></td>
<td class="num">{{n .RowsRead}}</td><td class="num">{{n .Written}}</td><td class="num">{{n .Skipped}}</td><td class="num">{{n .Failed}}</td>
{{with .Verification}}<td class="num">{{n .AvgDocBytes}} B</td><td class="num">{{n .ThroughputAvg}} · {{n .ThroughputPeak}}</td>
<td><span class="pill {{.Result}}">{{.Result}}</span>
{{with .Sample}}<div class="hint">{{.Matched}}/{{.Checked}} samples match{{if .Mismatched}} · {{.Mismatched}} mismatched{{end}}{{if .Missing}} · {{.Missing}} missing{{end}}{{if .SourceChanged}} · {{.SourceChanged}} changed at the source{{end}}</div>{{end}}
<div class="hint">{{if .Counts.Reconciled}}counts reconcile{{else}}counts don't reconcile{{end}}{{range .Counts.Messages}} · {{.}}{{end}}</div></td>
{{else}}<td class="num">—</td><td class="num">—</td><td><span class="pill skipped">not verified</span></td>{{end}}
</tr>{{end}}
</tbody>
</table>

<h2>Errors</h2>
{{if .Errors.ByCode}}<table><thead><tr><th>Stage</th><th>Code</th><th class="num">Rows</th></tr></thead><tbody>
{{range .Errors.ByCode}}<tr><td>{{.Stage}}</td><td><code>{{.Code}}</code></td><td class="num">{{n .Count}}</td></tr>{{end}}
</tbody></table>
<p class="hint">The full list: <code>{{.Errors.ExportURL}}</code> (CSV).</p>{{else}}<p>No rows failed.</p>{{end}}

{{range .Mappings}}{{with .Verification}}{{with .Sample}}{{if .Mismatches}}
<h2>Mismatches · {{$.Job.ID}}</h2>
{{range .Mismatches}}<details><summary><code>{{.Key}}</code> — {{len .Diffs}} differing path(s)</summary>
<table><thead><tr><th>Path</th><th>Expected</th><th>Actual</th></tr></thead><tbody>{{range .Diffs}}<tr><td><code>{{.Path}}</code></td><td><code>{{json .Expected}}</code></td><td><code>{{json .Actual}}</code></td></tr>{{end}}</tbody></table>
</details>{{end}}{{end}}{{end}}{{end}}{{end}}

<h2>Settings</h2>
<pre>{{raw .Job.Options}}</pre>

<h2>Mapping specifications</h2>
{{range $id, $spec := .Specs}}<details><summary><code>{{$id}}</code></summary><pre>{{raw $spec}}</pre></details>{{end}}
</main>
</body>
</html>
`
