package crypto

import (
	"context"
	"errors"
	"fmt"

	"omnidatabus/internal/store"
)

// RewrapStore is the part of store.Connections that key rotation needs.
type RewrapStore interface {
	ListForRewrap(ctx context.Context, keyID, afterID string, limit int) ([]store.Connection, error)
	ReplaceSecret(ctx context.Context, c store.Connection, sealed store.Sealed) error
}

// RotateFailure records a secret that could not be re-wrapped.
type RotateFailure struct {
	ConnectionID string
	Err          error
}

// RotateReport summarizes a rotation run.
type RotateReport struct {
	Rewrapped int
	Failed    []RotateFailure
}

// Rotate re-wraps every data key that isn't on the active master key. Ciphertexts are not
// touched. It is idempotent (a second run finds nothing) and never aborts on a single bad
// record: unknown key IDs and concurrent edits are reported and skipped.
func Rotate(ctx context.Context, repo RewrapStore, sealer *LocalSealer, batchSize int) (RotateReport, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	var report RotateReport
	after := ""
	for {
		batch, err := repo.ListForRewrap(ctx, sealer.kr.Active(), after, batchSize)
		if err != nil {
			return report, fmt.Errorf("list secrets: %w", err)
		}
		if len(batch) == 0 {
			return report, nil
		}
		for _, c := range batch {
			aad := ConnectionAAD(string(c.WorkspaceID), c.ProjectID, c.ID)
			sealed, changed, err := sealer.Rewrap(c.Secret, aad)
			if err != nil {
				report.Failed = append(report.Failed, RotateFailure{ConnectionID: c.ID, Err: err})
				continue
			}
			if !changed {
				continue
			}
			if err := repo.ReplaceSecret(ctx, c, sealed); err != nil {
				if errors.Is(err, store.ErrVersionConflict) {
					err = fmt.Errorf("changed while rotating; re-run to finish: %w", err)
				}
				report.Failed = append(report.Failed, RotateFailure{ConnectionID: c.ID, Err: err})
				continue
			}
			report.Rewrapped++
		}
		after = batch[len(batch)-1].ID
	}
}
