package couchbase

import (
	"net/http"
	"regexp"
	"strings"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// Couchbase naming rules (OC-014). web/src/features/targets/names.ts mirrors them, and both
// test suites run testdata/keyspace-names.json so the two can't drift.
var (
	bucketNameRE = regexp.MustCompile(`^[A-Za-z0-9._%-]{1,100}$`)
	scopeNameRE  = regexp.MustCompile(`^[A-Za-z0-9_%-]{1,251}$`)
)

// Limits for new buckets and collections.
const (
	MinRAMQuotaMB      = 100
	MinMagmaRAMQuotaMB = 1024
	MaxReplicas        = 3
	MaxExpirySec       = 2147483647
)

// ValidBucketName reports a valid bucket name: 1–100 of [A-Za-z0-9._%-], not starting with a dot.
func ValidBucketName(name string) bool {
	return bucketNameRE.MatchString(name) && !strings.HasPrefix(name, ".")
}

// ValidScopeName applies to new scopes and collections: 1–251 of [A-Za-z0-9_%-], not starting
// with "_" or "%" (names like _default are reserved for the system).
func ValidScopeName(name string) bool {
	return scopeNameRE.MatchString(name) && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "%")
}

// validExisting accepts any existing scope or collection name, including _default.
func validExisting(name string) bool { return scopeNameRE.MatchString(name) }

func invalid(field, msg string) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "One or more fields are invalid.")
	p.FieldErrors = []problem.FieldError{{Field: field, Message: msg}}
	return p
}

// ValidateBucketSpec checks a new bucket's name and settings.
func ValidateBucketSpec(s target.BucketSpec) error {
	switch {
	case !ValidBucketName(s.Name):
		return invalid("name", "use 1–100 letters, digits, '.', '_', '%' or '-', not starting with '.'")
	case s.BucketType != "couchbase" && s.BucketType != "ephemeral":
		return invalid("bucketType", "must be couchbase or ephemeral")
	case s.BucketType == "couchbase" && s.StorageBackend != "couchstore" && s.StorageBackend != "magma":
		return invalid("storageBackend", "must be couchstore or magma")
	case s.RAMQuotaMB < MinRAMQuotaMB:
		return invalid("ramQuotaMB", "must be at least 100 MB")
	case s.StorageBackend == "magma" && s.RAMQuotaMB < MinMagmaRAMQuotaMB:
		return invalid("ramQuotaMB", "Magma needs at least 1024 MB")
	case s.NumReplicas > MaxReplicas:
		return invalid("numReplicas", "must be between 0 and 3")
	}
	return nil
}

// ValidateScopeName checks a new scope name.
func ValidateScopeName(name string) error {
	if !ValidScopeName(name) {
		return invalid("name", "use 1–251 letters, digits, '_', '%' or '-', not starting with '_' or '%'")
	}
	return nil
}

// ValidateCollectionSpec checks a new collection's name and max TTL.
func ValidateCollectionSpec(s target.CollectionSpec) error {
	if err := ValidateScopeName(s.Name); err != nil {
		return err
	}
	if s.MaxExpirySec < 0 || s.MaxExpirySec > MaxExpirySec {
		return invalid("maxExpirySec", "must be 0 (bucket default) or 1–2147483647 seconds")
	}
	return nil
}

// Keyspace quotes bucket, scope and collection for SQL++ (`b`.`s`.`c`). Backticks can't
// appear in valid names, so names containing one are rejected rather than escaped.
func Keyspace(bucket, scope, collection string) (string, error) {
	if strings.Contains(bucket, "`") || !bucketNameRE.MatchString(bucket) {
		return "", invalid("bucket", "invalid bucket name")
	}
	if !validExisting(scope) {
		return "", invalid("scope", "invalid scope name")
	}
	if !validExisting(collection) {
		return "", invalid("collection", "invalid collection name")
	}
	return "`" + bucket + "`.`" + scope + "`.`" + collection + "`", nil
}
