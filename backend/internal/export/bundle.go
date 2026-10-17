package export

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/store"
)

// BundleVersion is the bundle format this code writes; runners refuse other major versions.
const BundleVersion = 1

// Credential modes (architecture D4, OC-052).
const (
	ModeSealed    = "sealed"
	ModePlaintext = "plaintext"
	ModeExternal  = "external"
	// ModeConfig: the secrets are in the run's config file, config.json (OC-155).
	ModeConfig = "config"
)

// Bundle is a portable migration (architecture §6.3, schema api/bundle.schema.json): everything
// `odb run` needs to migrate without the tool. It never holds a secret in clear text unless its
// credential mode is plaintext; in config mode the secrets are in config.json beside it.
type Bundle struct {
	BundleVersion int        `json:"bundleVersion"`
	OdbVersion    string     `json:"odbVersion"` // the runner versions that may run it, e.g. ">=2.0.0 <3.0.0"
	CreatedAt     time.Time  `json:"createdAt"`
	CreatedBy     string     `json:"createdBy"`
	Project       ProjectRef `json:"project"`
	Source        Connection `json:"source"`
	Target        Connection `json:"target"`
	Mappings      []Mapping  `json:"mappings"`
	// Relationships are the project's virtual relationships that the mappings use (OC-042).
	Relationships     []Relationship `json:"relationships"`
	JobOptions        JobOptions     `json:"jobOptions"`
	SourceFingerprint Fingerprint    `json:"sourceFingerprint"`
	Integrity         Integrity      `json:"integrity"`
}

// ProjectRef names the exported project (informational).
type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Connection is the source or target: its kind and non-secret config, plus its credentials.
// A directory target has no credentials.
type Connection struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Config      json.RawMessage `json:"config"`
	Credentials *Credentials    `json:"credentials,omitempty"`
}

// Credentials is a connection's secrets in one of the modes (OC-052, OC-155). A config-mode
// block holds nothing: the run's config file has the secrets.
type Credentials struct {
	Mode string `json:"mode"`
	// Sealed: AES-256-GCM over the secrets JSON, key from KDF, AAD = the bundle's content hash.
	KDF        *KDF   `json:"kdf,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
	// Plaintext: the secrets themselves.
	Values map[string]string `json:"values,omitempty"`
	// External: where the runner reads each secret, "env:NAME" or "file:/path".
	Refs map[string]string `json:"refs,omitempty"`
}

// KDF is the sealed mode's key derivation.
type KDF struct {
	Alg  string `json:"alg"` // argon2id
	Salt string `json:"salt"`
	T    uint32 `json:"t"`
	M    uint32 `json:"m"` // KiB
	P    uint8  `json:"p"`
}

// TableRef names a source table.
type TableRef struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
}

// String is "schema.table".
func (t TableRef) String() string { return t.Schema + "." + t.Table }

// Mapping is one exported mapping.
type Mapping struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Source   TableRef       `json:"source"`
	Location store.Location `json:"location"`
	// Outline says what each document field is filled from, for people reading the file
	// (OC-158). Generated from Spec; the runner never reads it.
	Outline []string        `json:"outline,omitempty"`
	Spec    json.RawMessage `json:"spec"`
	// PlanHash is the execution plan's hash when exported (OC-037), to compare runs.
	PlanHash string `json:"planHash"`
}

// Relationship is a virtual relationship without its bookkeeping.
type Relationship struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	From        store.RelationshipEnd `json:"from"`
	To          store.RelationshipEnd `json:"to"`
	Cardinality string                `json:"cardinality"`
}

// JobOptions are the run's settings: the jobs.Options a user can set, without the fields the
// job service fills in when a job is created.
type JobOptions struct {
	WriteMode        string           `json:"writeMode"`
	BatchSize        int              `json:"batchSize"`
	ReadParallelism  string           `json:"readParallelism"`
	WriteConcurrency int              `json:"writeConcurrency"`
	TableConcurrency int              `json:"tableConcurrency"`
	MaxDocsPerSecond *int             `json:"maxDocsPerSecond,omitempty"`
	Durability       string           `json:"durability"`
	ErrorPolicy      jobs.ErrorPolicy `json:"errorPolicy"`
	ExactCount       string           `json:"exactCount"`
	VerifySampleSize int              `json:"verifySampleSize"`
}

// OptionsFrom keeps the exportable part of job options.
func OptionsFrom(o jobs.Options) JobOptions {
	return JobOptions{
		WriteMode: o.WriteMode, BatchSize: o.BatchSize, ReadParallelism: o.ReadParallelism, WriteConcurrency: o.WriteConcurrency,
		TableConcurrency: o.TableConcurrency, MaxDocsPerSecond: o.MaxDocsPerSecond, Durability: o.Durability,
		ErrorPolicy: o.ErrorPolicy, ExactCount: o.ExactCount, VerifySampleSize: o.VerifySampleSize,
	}
}

// Jobs converts back to job options.
func (o JobOptions) Jobs() jobs.Options {
	j := jobs.DefaultOptions()
	j.WriteMode, j.BatchSize, j.ReadParallelism, j.WriteConcurrency = o.WriteMode, o.BatchSize, o.ReadParallelism, o.WriteConcurrency
	j.TableConcurrency, j.MaxDocsPerSecond, j.Durability, j.ErrorPolicy = o.TableConcurrency, o.MaxDocsPerSecond, o.Durability, o.ErrorPolicy
	j.ExactCount, j.VerifySampleSize = o.ExactCount, o.VerifySampleSize
	return j
}

// Integrity makes a bundle tamper-evident. SHA256 covers the canonical JSON of the whole bundle
// without the integrity member; ContentSHA256 covers it without the credentials too, and is the
// sealed credentials' AAD, so a sealed block only opens in the bundle it was made for.
type Integrity struct {
	SHA256        string `json:"sha256"`
	ContentSHA256 string `json:"contentSha256"`
}

// StripDesignTime removes what only the design tools use from a spec: the imported reference
// sample, constant hints and field examples (OC-059). Documents don't depend on them.
func StripDesignTime(raw json.RawMessage) (json.RawMessage, error) {
	var spec mapping.Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	spec.DocSchema.Reference = nil
	spec.DocSchema.ConstantHints = nil
	var strip func(f *docschema.DocField)
	strip = func(f *docschema.DocField) {
		f.Example = nil
		for i := range f.Children {
			strip(&f.Children[i])
		}
		if f.Items != nil {
			strip(f.Items)
		}
	}
	strip(&spec.DocSchema.Root)
	return json.Marshal(spec)
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// hashes returns the integrity hashes of b as it is (its Integrity is ignored).
func (b Bundle) hashes() (Integrity, error) {
	c := b
	c.Integrity = Integrity{}
	full, err := canonicalWithout(c, "integrity")
	if err != nil {
		return Integrity{}, err
	}
	c.Source.Credentials, c.Target.Credentials = nil, nil
	content, err := canonicalWithout(c, "integrity")
	if err != nil {
		return Integrity{}, err
	}
	return Integrity{SHA256: sum(full), ContentSHA256: sum(content)}, nil
}

// ContentHash is the hash sealed credentials are bound to (their AAD).
func (b Bundle) ContentHash() (string, error) {
	h, err := b.hashes()
	return h.ContentSHA256, err
}

// Finalize computes the integrity hashes; call it last, after sealing.
func (b *Bundle) Finalize() error {
	h, err := b.hashes()
	if err != nil {
		return err
	}
	b.Integrity = h
	return nil
}

// Verify checks both hashes against the bundle's content.
func (b Bundle) Verify() error {
	h, err := b.hashes()
	if err != nil {
		return err
	}
	if h != b.Integrity {
		return &Error{Code: CodeIntegrity, Message: "The bundle was changed after it was exported (integrity hash mismatch). Export it again from Omni-Data-Bus."}
	}
	return nil
}

// PlanHash identifies what a run does: the mappings, relationships, source config, target and
// options, but not credentials or when it was exported. A state directory belongs to one plan.
func (b Bundle) PlanHash() string {
	c, _ := Canonical(struct {
		Source        json.RawMessage `json:"source"`
		Target        Connection      `json:"target"`
		Mappings      []Mapping       `json:"mappings"`
		Relationships []Relationship  `json:"relationships"`
		JobOptions    JobOptions      `json:"jobOptions"`
	}{b.Source.Config, Connection{Type: b.Target.Type, Config: b.Target.Config}, b.Mappings, b.Relationships, b.JobOptions})
	return sum(c)
}

// SHA256 is the bundle's identity in reports and export history (its integrity hash).
func (b Bundle) SHA256() string { return b.Integrity.SHA256 }

func canonicalWithout(v any, key string) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	delete(m, key)
	raw, err = json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(raw)
}

// Encode writes the bundle file: canonical JSON, indented, so the same bundle produces the same
// bytes and a git diff shows only real changes. The integrity hash covers the canonical form,
// so re-indenting the file doesn't break it.
func Encode(b Bundle) ([]byte, error) {
	c, err := Canonical(b)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, c, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// Error is a bundle problem the runner reports with exit code 4.
type Error struct {
	Code    string
	Message string
	Details []string
}

func (e *Error) Error() string {
	if len(e.Details) == 0 {
		return e.Code + ": " + e.Message
	}
	return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Details)
}

// Bundle error codes (exit code 4).
const (
	CodeInvalid            = "BUNDLE_INVALID"
	CodeIntegrity          = "BUNDLE_INTEGRITY"
	CodeVersionUnsupported = "BUNDLE_VERSION_UNSUPPORTED"
	CodePassphraseInvalid  = "BUNDLE_PASSPHRASE_INVALID" //nolint:gosec // G101: an error code, not a credential
	CodeRefUnresolved      = "CREDENTIAL_REF_UNRESOLVED"
	CodeSchemaDrift        = "SCHEMA_DRIFT"
)
