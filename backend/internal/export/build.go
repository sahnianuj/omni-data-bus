package export

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"omnidatabus/internal/connections"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// Request is how the builder packages a project: what is exported is always the project as
// configured (its source, target, saved credentials and every ready mapping). The API always
// exports config-mode credentials (Input, OC-155); the builder's other modes serve tests and
// tooling.
type Request struct {
	// JobOptions override the defaults; omitted fields keep them.
	JobOptions json.RawMessage `json:"jobOptions,omitempty"`
	// CredentialMode is sealed (default), plaintext, external or config.
	CredentialMode string `json:"credentialMode"`
	// Passphrase seals the credentials (sealed mode). Never stored or logged.
	Passphrase string `json:"passphrase,omitempty"` //nolint:gosec // G117: request field, never persisted
	// Refs name where the runner finds each secret (external mode), per role:
	// {"source": {"password": "env:PG_PASSWORD"}}.
	Refs map[string]map[string]string `json:"refs,omitempty"`
	// Runtime is "bundle" or "script": both give the export folder now (OC-160, OC-182).
	Runtime string `json:"runtime"`
	Script  Script `json:"script"`
}

// Builder makes bundles from a project's stored data.
type Builder struct {
	Store    store.Store
	Sealer   crypto.Sealer
	Validate func(ctx context.Context, ws store.WorkspaceID, projectID, mappingID string) (mappings.ValidationResult, error)
	Latest   func(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, *schema.Snapshot, error)
	// IsBundled reports the all-in-one image's bundled Couchbase target (OC-072).
	IsBundled func(store.Connection) bool
	Version   string
	Now       func() time.Time
}

// Built is a bundle and the secrets it carries (for the leak checks), before encoding.
type Built struct {
	Bundle  Bundle
	Project store.Project
	// Secrets are every credential value placed in the bundle or its run config, so callers
	// can prove none leaks elsewhere. Never logged.
	Secrets []string
	// RunConfig is config.json, with the connections and their secrets (config mode only).
	RunConfig []byte
}

func unprocessable(code, title, detail string) problem.Problem {
	return problem.New(http.StatusUnprocessableEntity, code, title, detail)
}

// Build assembles and finalizes a bundle (OC-050, OC-052).
func (b *Builder) Build(ctx context.Context, ws store.WorkspaceID, userEmail, projectID string, req Request) (Built, error) {
	proj, err := b.Store.Projects().Get(ctx, ws, projectID)
	if err != nil {
		return Built{}, err
	}
	srcConn, err := b.Store.Connections().Get(ctx, ws, store.ConnectionID(projectID, store.RoleSource))
	if err != nil {
		return Built{}, mappings.ErrRepositoriesIncomplete
	}
	tgtConn, err := b.Store.Connections().Get(ctx, ws, store.ConnectionID(projectID, store.RoleTarget))
	if err != nil {
		return Built{}, mappings.ErrRepositoriesIncomplete
	}
	opts, err := jobs.ParseOptions(req.JobOptions)
	if err != nil {
		return Built{}, err
	}
	if err := opts.Validate(); err != nil {
		return Built{}, err
	}
	maps, err := b.readyMappings(ctx, ws, projectID)
	if err != nil {
		return Built{}, err
	}
	_, snap, err := b.Latest(ctx, ws, srcConn.ID)
	if err != nil || snap == nil {
		return Built{}, unprocessable(problem.CodeValidation, "No schema snapshot", "Discover the source schema before exporting.")
	}

	out := Built{Project: proj}
	bundle := Bundle{
		BundleVersion: BundleVersion, OdbVersion: RangeFor(b.Version), CreatedAt: b.now().Truncate(time.Second), CreatedBy: userEmail,
		Project: ProjectRef{ID: proj.ID, Name: proj.Name}, Relationships: []Relationship{}, JobOptions: OptionsFrom(opts),
		Source: Connection{Type: srcConn.Type, Name: srcConn.Name, Config: srcConn.Config},
	}

	// The project's target, with its saved credentials.
	if b.IsBundled != nil && b.IsBundled(tgtConn) {
		return Built{}, unprocessable(problem.CodeBundledTarget, "Bundled target can't be exported",
			"This project writes to the Couchbase node inside the all-in-one image, which a migration running elsewhere can't reach. Change the project's target in the Target step (a folder or another Couchbase cluster), then export.")
	}
	bundle.Target = Connection{Type: tgtConn.Type, Name: tgtConn.Name, Config: tgtConn.Config}
	tgtSecrets, err := connections.Open(b.Sealer, tgtConn)
	if err != nil {
		return Built{}, err
	}
	srcSecrets, err := connections.Open(b.Sealer, srcConn)
	if err != nil {
		return Built{}, err
	}

	// Mappings: valid, design-time data stripped.
	refs := References{}
	virtual, err := b.Store.Relationships().ListForProject(ctx, ws, projectID)
	if err != nil {
		return Built{}, err
	}
	used := map[string]bool{}
	var invalid []string
	for _, m := range maps {
		res, err := b.Validate(ctx, ws, projectID, m.ID)
		if err != nil {
			return Built{}, err
		}
		if !res.Ready {
			invalid = append(invalid, fmt.Sprintf("%s.%s: %s", m.SourceSchema, m.SourceTable, firstError(res.Issues)))
			continue
		}
		raw, err := StripDesignTime(m.Spec)
		if err != nil {
			return Built{}, err
		}
		var spec mapping.Spec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return Built{}, err
		}
		spec.Normalize()
		schemaName, table := m.Driving()
		root := snap.Table(schemaName, table)
		if root == nil {
			invalid = append(invalid, schemaName+"."+table+": the table isn't in the latest schema snapshot")
			continue
		}
		pl, _, err := plan.Compile(plan.Input{
			Spec: &spec, Root: mapping.TableRef{Schema: schemaName, Name: table}, Table: root,
			Related: func(s, t string) *schema.Table { return snap.Table(s, t) },
		})
		if err != nil || pl.Program == nil {
			invalid = append(invalid, schemaName+"."+table+": the mapping doesn't compile")
			continue
		}
		Referenced(refs, pl, root, &spec)
		for _, r := range virtual {
			if usesRelationship(&spec, mapping.TableRef{Schema: schemaName, Name: table}, r) {
				used[r.ID] = true
			}
		}
		loc := m.Where()
		bundle.Mappings = append(bundle.Mappings, Mapping{
			ID: m.ID, Name: mappingName(m), Source: TableRef{Schema: m.SourceSchema, Table: m.SourceTable},
			Location: loc, Outline: mapping.Outline(&spec, mapping.TableRef{Schema: schemaName, Name: table}), Spec: raw, PlanHash: pl.Hash,
		})
	}
	if len(invalid) > 0 {
		p := unprocessable(problem.CodeMappingInvalid, "Mappings need fixing",
			"Fix these mappings before exporting: "+strings.Join(invalid, "; "))
		return Built{}, p
	}
	for _, r := range virtual {
		if used[r.ID] {
			bundle.Relationships = append(bundle.Relationships, Relationship{ID: r.ID, Name: r.Name, From: r.From, To: r.To, Cardinality: r.Cardinality})
		}
	}
	sort.Slice(bundle.Relationships, func(i, j int) bool { return bundle.Relationships[i].ID < bundle.Relationships[j].ID })
	bundle.SourceFingerprint = ComputeFingerprint(snap, refs)

	// Credentials last: sealed blocks are bound to the content hash.
	mode := req.CredentialMode
	if mode == "" {
		mode = ModeSealed
	}
	src, tgt := secretsMap(srcSecrets), Secrets(nil)
	if bundle.Target.Type != store.KindDirectory {
		tgt = secretsMap(tgtSecrets)
	}
	// Config mode: every connection setting goes to config.json, and the bundle (mappings.json)
	// keeps only what the migration does (OC-158). A directory target's folder is there too.
	if mode == ModeConfig {
		rc := RunConfig{}
		if rc.Source, err = NewRunConnection(bundle.Source, src); err != nil {
			return Built{}, err
		}
		if rc.Target, err = NewRunConnection(bundle.Target, tgt); err != nil {
			return Built{}, err
		}
		if out.RunConfig, err = EncodeRunConfig(rc); err != nil {
			return Built{}, err
		}
		bundle.Source.Config, bundle.Target.Config = json.RawMessage(`{}`), json.RawMessage(`{}`)
	}
	aad, err := bundle.ContentHash()
	if err != nil {
		return Built{}, err
	}
	for _, c := range []struct {
		role    string
		secrets Secrets
		conn    *Connection
	}{{"source", src, &bundle.Source}, {"target", tgt, &bundle.Target}} {
		if c.secrets == nil {
			continue
		}
		for _, v := range c.secrets {
			out.Secrets = append(out.Secrets, v)
		}
		creds, err := credentialsFor(mode, c.role, c.secrets, req, aad)
		if err != nil {
			return Built{}, err
		}
		c.conn.Credentials = creds
	}
	if err := bundle.Finalize(); err != nil {
		return Built{}, err
	}
	out.Bundle = bundle
	return out, nil
}

func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

// readyMappings are the project's mappings that are ready to migrate, as on the Review step.
func (b *Builder) readyMappings(ctx context.Context, ws store.WorkspaceID, projectID string) ([]store.Mapping, error) {
	all, err := b.Store.Mappings().ListForProject(ctx, ws, projectID)
	if err != nil {
		return nil, err
	}
	var out []store.Mapping
	for _, m := range all {
		if m.Status == mapping.StatusReady {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, unprocessable(problem.CodeNoReadyMappings, "Nothing to export", "Make at least one mapping ready in the Design & map step.")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func credentialsFor(mode, role string, s Secrets, req Request, aad string) (*Credentials, error) {
	switch mode {
	case ModeSealed:
		return Seal(s, req.Passphrase, aad, DefaultKDF)
	case ModePlaintext:
		return &Credentials{Mode: ModePlaintext, Values: s}, nil
	case ModeExternal:
		refs := map[string]string{}
		for k := range s {
			ref := req.Refs[role][k]
			if ref == "" {
				ref = "env:" + DefaultRefName(role, k)
			}
			if !refPattern.MatchString(ref) {
				return nil, unprocessable(problem.CodeValidation, "Invalid secret reference",
					fmt.Sprintf("%s %s: use env:NAME or file:/absolute/path.", role, k))
			}
			refs[k] = ref
		}
		return &Credentials{Mode: ModeExternal, Refs: refs}, nil
	case ModeConfig:
		return &Credentials{Mode: ModeConfig}, nil
	}
	return nil, unprocessable(problem.CodeValidation, "Invalid credential mode", "Use sealed, plaintext, external or config.")
}

// DefaultRefName is the env var an external export reads by default: ODB_SOURCE_PASSWORD…
func DefaultRefName(role, key string) string {
	var b strings.Builder
	for _, r := range key {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper("ODB_" + role + "_" + b.String())
}

func secretsMap(s connections.Secrets) Secrets {
	out := Secrets{}
	for k, v := range map[string]string{"password": s.Password, "caCertPem": s.CACertPEM, "clientCertPem": s.ClientCertPEM, "clientKeyPem": s.ClientKeyPEM} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// ToConnectionSecrets converts bundle secrets for a connector.
func ToConnectionSecrets(s Secrets) connections.Secrets {
	return connections.Secrets{Password: s["password"], CACertPEM: s["caCertPem"], ClientCertPEM: s["clientCertPem"], ClientKeyPEM: s["clientKeyPem"]}
}

func firstError(issues []mapping.Issue) string {
	for _, is := range issues {
		if is.Severity == mapping.SeverityError {
			return is.Message
		}
	}
	return "it isn't ready"
}

// mappingName is how a mapping is named in bundles and commands: its location's leaf.
func mappingName(m store.Mapping) string {
	l := m.Where()
	if l.Kind == store.KindDirectory {
		segs := strings.Split(strings.Trim(l.Folder, "/"), "/")
		if n := segs[len(segs)-1]; n != "" {
			return n
		}
	} else if l.Collection != "" {
		return l.Collection
	}
	return m.SourceTable
}

// usesRelationship reports whether a spec relation joins the tables and columns of r.
func usesRelationship(spec *mapping.Spec, root mapping.TableRef, r store.Relationship) bool {
	tableOf := func(id string) mapping.TableRef {
		if id == "" {
			return root
		}
		if p := spec.RelationByID(id); p != nil {
			return p.Table
		}
		return mapping.TableRef{}
	}
	match := func(t mapping.TableRef, cols []string, e store.RelationshipEnd) bool {
		return t.Schema == e.Schema && t.Name == e.Table && slices.Equal(cols, e.Columns)
	}
	for _, rel := range spec.Relations {
		parent := tableOf(rel.Parent())
		var pc, cc []string
		for _, j := range rel.Join {
			pc, cc = append(pc, j.Parent), append(cc, j.Child)
		}
		if (match(parent, pc, r.From) && match(rel.Table, cc, r.To)) || (match(parent, pc, r.To) && match(rel.Table, cc, r.From)) {
			return true
		}
	}
	return false
}

var refPattern = regexp.MustCompile(`^(env:[A-Za-z_][A-Za-z0-9_]*|file:/.+)$`)
