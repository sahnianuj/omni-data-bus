package cbstore

import (
	"errors"
	"strings"

	"omnidatabus/internal/store"
	"omnidatabus/internal/store/provision"

	"github.com/couchbase/gocb/v2"
)

// Translate maps gocb errors (possibly wrapped by a transaction) to store domain errors.
// uniqueField names the field a document-exists conflict refers to.
func Translate(err error, uniqueField string) error {
	if err == nil {
		return nil
	}
	var conflict *store.ConflictError
	switch {
	// Domain errors returned from inside a transaction callback pass through unchanged.
	case errors.As(err, &conflict):
		return conflict
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrVersionConflict), errors.Is(err, store.ErrInvalidCursor):
		for _, domain := range []error{store.ErrNotFound, store.ErrVersionConflict, store.ErrInvalidCursor} {
			if errors.Is(err, domain) {
				return domain
			}
		}
	case errors.Is(err, gocb.ErrDocumentNotFound):
		return store.ErrNotFound
	case errors.Is(err, gocb.ErrDocumentExists):
		if uniqueField == "" {
			uniqueField = "id"
		}
		return &store.ConflictError{Field: uniqueField}
	case errors.Is(err, gocb.ErrCasMismatch):
		return store.ErrVersionConflict
	case errors.Is(err, gocb.ErrTimeout), errors.Is(err, gocb.ErrUnambiguousTimeout), errors.Is(err, gocb.ErrAmbiguousTimeout),
		errors.Is(err, gocb.ErrServiceNotAvailable), errors.Is(err, gocb.ErrTemporaryFailure), errors.Is(err, gocb.ErrOverload):
		return store.Unavailable(err)
	}
	return err
}

// provisionErr maps management/query errors to provisioning sentinels.
func provisionErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gocb.ErrScopeExists) || errors.Is(err, gocb.ErrCollectionExists) || errors.Is(err, gocb.ErrIndexExists) {
		return provision.ErrExists
	}
	msg := strings.ToLower(err.Error())
	// Another node is creating the same index right now (api and worker pods starting together):
	// the server keeps building it in the background, so for us it exists.
	if strings.Contains(msg, "build already in progress") || strings.Contains(msg, "will retry building in the background") {
		return provision.ErrExists
	}
	if errors.Is(err, gocb.ErrAuthenticationFailure) ||
		strings.Contains(msg, "forbidden") || strings.Contains(msg, "insufficient permission") ||
		strings.Contains(msg, "not authorized") || strings.Contains(msg, "does not have credentials") ||
		strings.Contains(msg, "status code: 403") {
		return provision.ErrForbidden
	}
	return err
}
