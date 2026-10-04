package problem

// Stable error codes. Keep in sync with components.schemas.ErrorCode in api/openapi.yaml.
const (
	CodeInternal          = "INTERNAL"
	CodeValidation        = "VALIDATION_FAILED"
	CodeNotFound          = "NOT_FOUND"
	CodeBadRequest        = "BAD_REQUEST"
	CodeConflict          = "CONFLICT"
	CodeVersionConflict   = "VERSION_CONFLICT"
	CodeUnavailable       = "UNAVAILABLE"
	CodeUnauthenticated   = "UNAUTHENTICATED"
	CodeSessionExpired    = "SESSION_EXPIRED"
	CodeInvalidCreds      = "INVALID_CREDENTIALS" //nolint:gosec // an error code, not a credential
	CodeEmailTaken        = "EMAIL_TAKEN"
	CodeSignupDisabled    = "SIGNUP_DISABLED"
	CodeCSRFFailed        = "CSRF_FAILED"
	CodeForbiddenRole     = "FORBIDDEN_ROLE"
	CodeRateLimited       = "RATE_LIMITED"
	CodeInvalidCursor     = "INVALID_CURSOR"
	CodeProjectNameTaken  = "PROJECT_NAME_TAKEN"
	CodeProjectJobRunning = "PROJECT_HAS_RUNNING_JOB"
	CodeUnsupportedSource = "SOURCE_TYPE_UNSUPPORTED"
	CodeUnsupportedTarget = "TARGET_TYPE_UNSUPPORTED"
	// Target kinds (OC-036): an unknown kind, a kind switched off on this server, a kind change
	// while mappings have locations, and a malformed mapping location.
	CodeTargetKindUnknown  = "TARGET_KIND_UNKNOWN"
	CodeTargetKindDisabled = "TARGET_KIND_DISABLED"
	CodeTargetKindInUse    = "TARGET_KIND_IN_USE"
	CodeLocationInvalid    = "LOCATION_INVALID"
	// CodeDirectoryOutsideRoots: a directory target's path is outside the folders the server may
	// write to (home folder, /data), or in a hidden folder (OC-038).
	CodeDirectoryOutsideRoots = "DIRECTORY_OUTSIDE_ALLOWED_ROOTS"
	// CodeDocumentNotFound: no migrated document with that key (OC-039 lookup).
	CodeDocumentNotFound = "DOCUMENT_NOT_FOUND"
	// CodeSchemaNotDiscovered: the source repository has no discovery snapshot yet (OC-010).
	CodeSchemaNotDiscovered = "SCHEMA_NOT_DISCOVERED"
	// CodeKeyspaceExists: the bucket, scope or collection to create already exists (OC-014).
	CodeKeyspaceExists = "KEYSPACE_EXISTS"
	// CodeCapellaBucketCreate: Capella buckets are created in the Capella UI (OC-014).
	CodeCapellaBucketCreate = "CAPELLA_BUCKET_CREATE_UNSUPPORTED"
	// Mappings (OC-035, OC-022).
	CodeRepositoriesIncomplete = "PROJECT_REPOSITORIES_INCOMPLETE"
	CodeMappingExists          = "MAPPING_EXISTS"
	CodeMappingVersionConflict = "MAPPING_VERSION_CONFLICT"
	// CodeMappingNewerVersion: a mapping saved by a newer release (OC-057).
	CodeMappingNewerVersion = "MAPPING_NEWER_VERSION"
	CodePreviewTimeout      = "PREVIEW_TIMEOUT"
	CodeMappingInvalid      = "MAPPING_INVALID"
	CodeInvalidSample       = "INVALID_SAMPLE"
	CodeSchemaRefCycle      = "SCHEMA_REF_CYCLE"
	CodeSamplingUnavailable = "SAMPLING_UNAVAILABLE"
	// Jobs (OC-023, OC-026).
	CodeJobInvalidTransition = "JOB_INVALID_TRANSITION"
	CodePreflightFailed      = "PREFLIGHT_FAILED"
	CodeNoReadyMappings      = "NO_READY_MAPPINGS"
	CodeRetryNotPossible     = "RETRY_NOT_POSSIBLE"
	// OC-033 hardening.
	CodePayloadTooLarge      = "PAYLOAD_TOO_LARGE"
	CodeTooManyConcurrentOps = "TOO_MANY_CONCURRENT_OPERATIONS"
	// Exports (OC-050, OC-052, OC-055).
	CodeBundledTarget       = "BUNDLED_TARGET_NOT_EXPORTABLE"
	CodePassphraseWeak      = "PASSPHRASE_WEAK"
	CodeStepUpRequired      = "STEP_UP_REQUIRED"
	CodeReportUnknownBundle = "REPORT_UNKNOWN_BUNDLE"
	CodeExportTooLarge      = "EXPORT_TOO_LARGE"
)

// ConnectorCodes are produced by connectors.ConnError (role × failure kind).
var ConnectorCodes = []string{
	"SOURCE_UNREACHABLE",
	"SOURCE_DNS",
	"SOURCE_TIMEOUT",
	"SOURCE_TLS",
	"SOURCE_AUTH_FAILED",
	"SOURCE_DB_NOT_FOUND",
	"SOURCE_PERMISSION_DENIED",
	"SOURCE_UNKNOWN",
	"TARGET_UNREACHABLE",
	"TARGET_DNS",
	"TARGET_TIMEOUT",
	"TARGET_TLS",
	"TARGET_AUTH_FAILED",
	"TARGET_DB_NOT_FOUND",
	"TARGET_PERMISSION_DENIED",
	"TARGET_UNKNOWN",
	"EGRESS_DENIED", // either role: the server's egress policy refused the destination (OC-033)
}

// All lists every code, for the OpenAPI enum check.
var All = []string{
	CodeInternal, CodeValidation, CodeNotFound, CodeBadRequest, CodeConflict, CodeVersionConflict,
	CodeUnavailable, CodeUnauthenticated, CodeSessionExpired, CodeInvalidCreds, CodeEmailTaken,
	CodeSignupDisabled, CodeCSRFFailed, CodeForbiddenRole, CodeRateLimited, CodeInvalidCursor,
	CodeProjectNameTaken, CodeProjectJobRunning, CodeUnsupportedSource, CodeUnsupportedTarget,
	CodeTargetKindUnknown, CodeTargetKindDisabled, CodeTargetKindInUse, CodeLocationInvalid, CodeDirectoryOutsideRoots, CodeDocumentNotFound,
	CodeSchemaNotDiscovered, CodeKeyspaceExists, CodeCapellaBucketCreate,
	CodeRepositoriesIncomplete, CodeMappingExists, CodeMappingVersionConflict, CodeMappingNewerVersion, CodePreviewTimeout, CodeMappingInvalid,
	CodeInvalidSample, CodeSchemaRefCycle, CodeSamplingUnavailable,
	CodeJobInvalidTransition, CodePreflightFailed, CodeNoReadyMappings, CodeRetryNotPossible,
	CodePayloadTooLarge, CodeTooManyConcurrentOps,
	CodeBundledTarget, CodePassphraseWeak, CodeStepUpRequired, CodeReportUnknownBundle, CodeExportTooLarge,
}

func init() { All = append(All, ConnectorCodes...) }
