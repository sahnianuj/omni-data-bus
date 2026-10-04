package export

import (
	"encoding/json"
	"time"

	"omnidatabus/internal/store"
)

// ReportVersion is report.json's format (api/report.schema.json).
const ReportVersion = 1

// Report is report.json, written by `odb run` (OC-051): what a run did, for people and for importing into the tool (OC-055).
type Report struct {
	ReportVersion int             `json:"reportVersion"`
	RunID         string          `json:"runId"`
	BundleSHA256  string          `json:"bundleSha256"`
	PlanHash      string          `json:"planHash"`
	Project       ProjectRef      `json:"project"`
	Odb           ReportBuild     `json:"odb"`
	Status        string          `json:"status"`
	ExitCode      int             `json:"exitCode"`
	Reason        string          `json:"reason,omitempty"`
	ReasonDetail  string          `json:"reasonDetail,omitempty"`
	StartedAt     time.Time       `json:"startedAt"`
	FinishedAt    time.Time       `json:"finishedAt"`
	DurationSec   float64         `json:"durationSec"`
	Totals        ReportTotals    `json:"totals"`
	DocsPerSec    float64         `json:"docsPerSec"`
	Mappings      []ReportMapping `json:"mappings"`
	Errors        ReportErrors    `json:"errors"`
	// Verification is the pipeline's verification results by mapping (OC-030), as the tool stores them.
	Verification json.RawMessage `json:"verification,omitempty"`
}

// ReportBuild is the runner binary (OC-058 AC7).
type ReportBuild struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// ReportTotals are the run's counts.
type ReportTotals struct {
	Read    int64 `json:"read"`
	Written int64 `json:"written"`
	Skipped int64 `json:"skipped"`
	Failed  int64 `json:"failed"`
}

// ReportMapping is one mapping's outcome.
type ReportMapping struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Table   string `json:"table"`
	Target  string `json:"target"`
	Phase   string `json:"phase"`
	Read    int64  `json:"read"`
	Written int64  `json:"written"`
	Skipped int64  `json:"skipped"`
	Failed  int64  `json:"failed"`
}

// ReportErrors summarizes errors.jsonl.
type ReportErrors struct {
	Total   int64            `json:"total"`
	ByCode  []ErrorCount     `json:"byCode"`
	Samples []store.JobError `json:"samples"`
}

// ErrorCount is one (stage, code) group.
type ErrorCount struct {
	Stage string `json:"stage"`
	Code  string `json:"code"`
	Count int64  `json:"count"`
}
