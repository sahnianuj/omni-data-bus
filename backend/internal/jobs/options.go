package jobs

import (
	"encoding/json"

	"omnidatabus/internal/jobs/jobspec"
)

// The job options live in jobspec, which the pipeline imports without this package (OC-051).

// Write modes.
const (
	WriteUpsert  = jobspec.WriteUpsert
	WriteInsert  = jobspec.WriteInsert
	WriteReplace = jobspec.WriteReplace
)

// Durability levels.
const (
	DurabilityNone                       = jobspec.DurabilityNone
	DurabilityMajority                   = jobspec.DurabilityMajority
	DurabilityMajorityAndPersistOnMaster = jobspec.DurabilityMajorityAndPersistOnMaster
	DurabilityPersistToMajority          = jobspec.DurabilityPersistToMajority
)

type (
	// ErrorPolicy aborts a job that fails too much (OC-026).
	ErrorPolicy = jobspec.ErrorPolicy
	// Options are a job's settings (jobs.options).
	Options = jobspec.Options
)

// DefaultOptions returns the defaults shown in the Review step's advanced options.
func DefaultOptions() Options { return jobspec.DefaultOptions() }

// ParseOptions decodes options over the defaults.
func ParseOptions(raw json.RawMessage) (Options, error) { return jobspec.ParseOptions(raw) }
