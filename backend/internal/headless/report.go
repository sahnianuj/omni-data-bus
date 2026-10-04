package headless

import (
	"encoding/json"

	"omnidatabus/internal/export"
	"omnidatabus/internal/store"
)

// The report model lives in export, which imports reports back into the tool (OC-055).
type (
	// Report is report.json.
	Report = export.Report
	// ReportBuild is the runner binary.
	ReportBuild = export.ReportBuild
	// ReportTotals are the run's counts.
	ReportTotals = export.ReportTotals
	// ReportMapping is one mapping's outcome.
	ReportMapping = export.ReportMapping
	// ReportErrors summarizes errors.jsonl.
	ReportErrors = export.ReportErrors
	// ErrorCount is one (stage, code) group.
	ErrorCount = export.ErrorCount
)

// ReportVersion is report.json's format.
const ReportVersion = export.ReportVersion

// Run statuses in report.json and the done event: the job statuses of the tool.
const (
	StatusCompleted           = store.JobCompleted
	StatusCompletedWithErrors = store.JobCompletedWithErrors
	StatusFailed              = store.JobFailed
	StatusCancelled           = store.JobCancelled
)

// Exit codes (architecture §6.4).
const (
	ExitOK         = 0
	ExitWithErrors = 2
	ExitFailed     = 3
	ExitPreflight  = 4
	ExitCancelled  = 5
)

// ReadReport reads a state directory's report.json.
func (s StateDir) ReadReport() (Report, error) {
	raw, err := readFile(s.file(ReportFile))
	if err != nil {
		return Report{}, err
	}
	var r Report
	return r, json.Unmarshal(raw, &r)
}

// WriteReport writes report.json atomically.
func (s StateDir) WriteReport(r Report) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.file(ReportFile), append(raw, '\n'))
}
