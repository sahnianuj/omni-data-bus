// Package connectors holds what source and target connectors share: descriptors, credentials,
// step-by-step connection test results and driver-agnostic error classification.
package connectors

import (
	"context"
	"encoding/json"
	"time"
)

// Role says whether a connector reads (source) or writes (target).
type Role string

// Connector roles.
const (
	RoleSource Role = "source"
	RoleTarget Role = "target"
)

// Capabilities describe optional connector features.
type Capabilities struct {
	SnapshotExport    bool `json:"snapshotExport,omitempty"`
	KeysetPagination  bool `json:"keysetPagination,omitempty"`
	RangePartitioning bool `json:"rangePartitioning,omitempty"`
	RowEstimate       bool `json:"rowEstimate,omitempty"`
	CreateKeyspaces   bool `json:"createKeyspaces,omitempty"`
}

// Descriptor is shown in the UI's database picker.
type Descriptor struct {
	Type         string       `json:"type"`
	Role         Role         `json:"role"`
	DisplayName  string       `json:"displayName"`
	Icon         string       `json:"icon"`
	Capabilities Capabilities `json:"capabilities"`
}

// Credentials are the decrypted secrets a connector needs to open a session.
type Credentials struct {
	Password      string
	CACertPEM     string
	ClientCertPEM string
	ClientKeyPEM  string
}

// StepStatus is the outcome of one connection-test step.
type StepStatus string

// Step outcomes.
const (
	StepOK      StepStatus = "ok"
	StepWarn    StepStatus = "warn"
	StepFail    StepStatus = "fail"
	StepSkipped StepStatus = "skipped"
)

// TestStep is one line of the "Test connection" checklist.
type TestStep struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Status StepStatus `json:"status"`
	Ms     int64      `json:"ms,omitempty"`
	Detail string     `json:"detail,omitempty"`
	Hint   string     `json:"hint,omitempty"`
	Code   string     `json:"code,omitempty"`
}

// TestResult is the full "Test connection" response.
type TestResult struct {
	OK        bool       `json:"ok"`
	LatencyMs int64      `json:"latencyMs"`
	Steps     []TestStep `json:"steps"`
	// Capabilities are target-specific (what the credential may do), see OC-012.
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
}

// Tester runs a connection test from unsaved or stored configuration.
type Tester interface {
	Test(ctx context.Context, cfg json.RawMessage, creds Credentials) TestResult
}

// StepRunner accumulates checklist steps; after the first failure the rest are skipped.
type StepRunner struct {
	Steps  []TestStep
	failed bool
	start  time.Time
	now    func() time.Time
}

// NewStepRunner starts a checklist (now may be nil for time.Now).
func NewStepRunner(now func() time.Time) *StepRunner {
	if now == nil {
		now = time.Now
	}
	return &StepRunner{now: now, start: now()}
}

// Run executes fn as a step unless an earlier step failed. fn returns the step to record
// (ID/Label are filled in) and whether the checklist may continue.
func (r *StepRunner) Run(id, label string, fn func() TestStep) bool {
	if r.failed {
		r.Steps = append(r.Steps, TestStep{ID: id, Label: label, Status: StepSkipped})
		return false
	}
	t0 := r.now()
	s := fn()
	s.ID, s.Label = id, label
	if s.Ms == 0 {
		s.Ms = r.now().Sub(t0).Milliseconds()
	}
	r.Steps = append(r.Steps, s)
	if s.Status == StepFail {
		r.failed = true
		return false
	}
	return true
}

// Result finalizes the checklist.
func (r *StepRunner) Result() TestResult {
	return TestResult{OK: !r.failed, LatencyMs: r.now().Sub(r.start).Milliseconds(), Steps: r.Steps}
}
