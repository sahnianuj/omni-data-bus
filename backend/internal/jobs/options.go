package jobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"omnidatabus/internal/problem"
)

// Write modes.
const (
	WriteUpsert  = "upsert"
	WriteInsert  = "insert"  // skip documents that already exist
	WriteReplace = "replace" // only overwrite documents that exist
)

// Durability levels.
const (
	DurabilityNone                       = "none"
	DurabilityMajority                   = "majority"
	DurabilityMajorityAndPersistOnMaster = "majorityAndPersistActive"
	DurabilityPersistToMajority          = "persistToMajority"
)

// ErrorPolicy aborts a job that fails too much (OC-026).
type ErrorPolicy struct {
	// MaxErrors aborts when failed > MaxErrors; -1 disables the absolute limit.
	MaxErrors int64 `json:"maxErrors"`
	// MaxErrorRatio aborts when failed/processed exceeds it, once MinRowsForRatio are processed.
	MaxErrorRatio   float64 `json:"maxErrorRatio"`
	MinRowsForRatio int64   `json:"minRowsForRatio"`
}

// Options are a job's settings (jobs.options). Zero values are replaced by DefaultOptions.
type Options struct {
	MappingIDs []string `json:"mappingIds,omitempty"`
	WriteMode  string   `json:"writeMode"`
	BatchSize  int      `json:"batchSize"`
	// ReadParallelism is "auto" or "1".."16" partitions per table (OC-024).
	ReadParallelism  string      `json:"readParallelism"`
	WriteConcurrency int         `json:"writeConcurrency"`
	TableConcurrency int         `json:"tableConcurrency"`
	MaxDocsPerSecond *int        `json:"maxDocsPerSecond"`
	Durability       string      `json:"durability"`
	ErrorPolicy      ErrorPolicy `json:"errorPolicy"`
	// ExactCount is auto | always | never (OC-027).
	ExactCount       string `json:"exactCount"`
	VerifySampleSize int    `json:"verifySampleSize"`

	// Set by the service when the job is created (OC-029): each mapping's spec, frozen so a
	// resumed run is consistent, and the mapping version it was taken from.
	Specs           map[string]json.RawMessage `json:"specs,omitempty"`
	MappingVersions map[string]int64           `json:"mappingVersions,omitempty"`
	// TargetEmpty lists mappings whose collection was empty at the start (OC-030).
	TargetEmpty map[string]bool `json:"targetEmpty,omitempty"`

	// Set by the service for "retry failed rows" jobs (OC-026).
	RetryOf   string   `json:"retryOf,omitempty"`
	KeyFilter string   `json:"keyFilter,omitempty"` // "failed"
	OnlyCodes []string `json:"onlyCodes,omitempty"`
}

// DefaultOptions returns the defaults shown in the Review step's advanced options.
func DefaultOptions() Options {
	return Options{
		WriteMode: WriteUpsert, BatchSize: 1000, ReadParallelism: "auto", WriteConcurrency: 64, TableConcurrency: 1,
		Durability:  DurabilityNone,
		ErrorPolicy: ErrorPolicy{MaxErrors: 1000, MaxErrorRatio: 0.01, MinRowsForRatio: 10000},
		ExactCount:  "auto", VerifySampleSize: 100,
	}
}

// Parallelism returns the fixed partition count, or 0 for "auto".
func (o Options) Parallelism() int {
	n, err := strconv.Atoi(o.ReadParallelism)
	if err != nil {
		return 0
	}
	return n
}

// Validate returns a 422 problem listing every invalid field.
func (o Options) Validate() error {
	var fe []problem.FieldError
	bad := func(field, msg string) { fe = append(fe, problem.FieldError{Field: field, Message: msg}) }
	switch o.WriteMode {
	case WriteUpsert, WriteInsert, WriteReplace:
	default:
		bad("writeMode", "must be upsert, insert or replace")
	}
	if o.BatchSize < 100 || o.BatchSize > 10000 {
		bad("batchSize", "must be between 100 and 10000")
	}
	if o.ReadParallelism != "auto" {
		if n := o.Parallelism(); n < 1 || n > 16 {
			bad("readParallelism", `must be "auto" or 1 to 16`)
		}
	}
	if o.WriteConcurrency < 1 || o.WriteConcurrency > 512 {
		bad("writeConcurrency", "must be between 1 and 512")
	}
	if o.TableConcurrency < 1 || o.TableConcurrency > 4 {
		bad("tableConcurrency", "must be between 1 and 4")
	}
	if o.MaxDocsPerSecond != nil && *o.MaxDocsPerSecond < 1 {
		bad("maxDocsPerSecond", "must be at least 1, or empty for no limit")
	}
	switch o.Durability {
	case DurabilityNone, DurabilityMajority, DurabilityMajorityAndPersistOnMaster, DurabilityPersistToMajority:
	default:
		bad("durability", "must be none, majority, majorityAndPersistActive or persistToMajority")
	}
	if o.ErrorPolicy.MaxErrors < -1 {
		bad("errorPolicy.maxErrors", "must be -1 (no limit) or more")
	}
	if o.ErrorPolicy.MaxErrorRatio < 0 || o.ErrorPolicy.MaxErrorRatio > 1 {
		bad("errorPolicy.maxErrorRatio", "must be between 0 and 1")
	}
	if o.ErrorPolicy.MinRowsForRatio < 0 {
		bad("errorPolicy.minRowsForRatio", "must be 0 or more")
	}
	switch o.ExactCount {
	case "auto", "always", "never":
	default:
		bad("exactCount", "must be auto, always or never")
	}
	if o.VerifySampleSize < 0 || o.VerifySampleSize > 10000 {
		bad("verifySampleSize", "must be between 0 and 10000")
	}
	if len(fe) == 0 {
		return nil
	}
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid job options",
		fmt.Sprintf("%d option(s) need attention.", len(fe)))
	p.FieldErrors = fe
	return p
}

// ParseOptions decodes options over the defaults, so an omitted field keeps its default and
// an explicit zero (e.g. maxErrors: 0, "stop at the first error") is kept.
func ParseOptions(raw json.RawMessage) (Options, error) {
	o := DefaultOptions()
	if len(raw) == 0 || string(raw) == "null" {
		return o, nil
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return Options{}, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid job options", err.Error())
	}
	return o, nil
}
