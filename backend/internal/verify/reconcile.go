package verify

import "fmt"

// CollisionTolerance is how far below the writes a distinct estimate may be before keys count as repeated.
const CollisionTolerance = 0.005

// CountsInput is what a mapping's run counted.
type CountsInput struct {
	RowsRead, Written, Skipped, Failed int64
	// Distinct is the HLL estimate of distinct keys among the writes (Writes of them);
	// Writes 0 means no estimate (e.g. a resumed run only saw part of the writes).
	Distinct uint64
	Writes   int64
	// TargetCount is the collection's document count, when it was empty at the start and the
	// count was available; nil otherwise.
	TargetCount *int64
	// Mode is the write mode: skipped rows exist in the target for insert and replace.
	Mode string
}

// Counts is the reconciliation of a mapping.
type Counts struct {
	SourceRows       int64    `json:"sourceRows"`
	Written          int64    `json:"written"`
	Skipped          int64    `json:"skipped"`
	Failed           int64    `json:"failed"`
	DistinctKeys     *uint64  `json:"distinctKeysEstimate,omitempty"`
	DuplicateKeys    int64    `json:"duplicateKeys"`
	ExpectedDocs     int64    `json:"expectedDocs"`
	TargetCount      *int64   `json:"targetCount,omitempty"`
	TargetCountMatch *bool    `json:"targetCountMatch,omitempty"`
	Reconciled       bool     `json:"reconciled"`
	Warnings         []string `json:"warnings"`
	Messages         []string `json:"messages"`
}

// Reconcile checks that every row read has an outcome, estimates duplicate keys and, when
// known, compares the collection's count (OC-030 AC1).
func Reconcile(in CountsInput) Counts {
	c := Counts{SourceRows: in.RowsRead, Written: in.Written, Skipped: in.Skipped, Failed: in.Failed, Warnings: []string{}, Messages: []string{}}
	c.Reconciled = in.RowsRead == in.Written+in.Skipped+in.Failed
	if !c.Reconciled {
		c.Messages = append(c.Messages, fmt.Sprintf("%d rows read, but %d written + %d skipped + %d failed", in.RowsRead, in.Written, in.Skipped, in.Failed))
	}
	if in.Writes > 0 {
		d := in.Distinct
		c.DistinctKeys = &d
		if float64(d) < float64(in.Writes)*(1-CollisionTolerance) {
			c.DuplicateKeys = in.Writes - int64(d) //nolint:gosec // d < Writes here, so it fits
			c.Warnings = append(c.Warnings, CodeKeyCollisions)
			c.Messages = append(c.Messages, fmt.Sprintf("about %d distinct keys for %d writes: the key template isn't unique, so documents overwrote each other", d, in.Writes))
		}
	} else {
		c.Warnings = append(c.Warnings, CodePartial)
	}
	c.ExpectedDocs = in.Written - c.DuplicateKeys
	if in.Mode == "insert" || in.Mode == "replace" {
		c.ExpectedDocs += in.Skipped // skipped rows' documents are there (insert) or were required (replace)
	}
	if in.TargetCount != nil {
		c.TargetCount = in.TargetCount
		ok := *in.TargetCount == c.ExpectedDocs || (c.DuplicateKeys > 0 && abs(*in.TargetCount-c.ExpectedDocs) <= int64(float64(in.Writes)*0.02))
		c.TargetCountMatch = &ok
		if !ok {
			c.Warnings = append(c.Warnings, CodeTargetMismatch)
			c.Messages = append(c.Messages, fmt.Sprintf("the collection has %d documents; %d were expected", *in.TargetCount, c.ExpectedDocs))
		}
	}
	return c
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
