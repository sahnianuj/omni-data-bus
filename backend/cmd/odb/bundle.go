package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/export"
	"omnidatabus/internal/mapping"
)

const bundleUsage = `usage: odb bundle <command>

commands:
  validate FILE     check the schema, integrity, version and each mapping's plan (exit 0 or 4)
  inspect FILE      what the bundle migrates: source, target, mappings, credential mode (no secrets)
  diff A B          what changed between two bundles: mappings, relationships, target, options`

// Exit codes of the bundle and run commands (architecture §6.4). A usage error is a
// configuration error (4): 2 means "completed with errors" to scripts.
const (
	exitOK        = 0
	exitPreflight = 4
	exitUsage     = exitPreflight
)

// runBundle implements `odb bundle` (OC-050).
func runBundle(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, bundleUsage)
		return exitUsage
	}
	switch args[0] {
	case "validate":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: odb bundle validate FILE")
			return exitUsage
		}
		b, err := loadBundle(args[1])
		if err != nil {
			return reportBundleError(stderr, err)
		}
		if errs := checkPlans(b); len(errs) > 0 {
			return reportBundleError(stderr, &export.Error{Code: export.CodeInvalid, Message: "Some mappings can't run.", Details: errs})
		}
		fmt.Fprintf(stdout, "OK: %s — %d mapping(s), bundle sha256 %s\n", args[1], len(b.Mappings), b.SHA256())
		return exitOK
	case "inspect":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: odb bundle inspect FILE")
			return exitUsage
		}
		b, err := loadBundle(args[1])
		if err != nil {
			return reportBundleError(stderr, err)
		}
		inspect(stdout, b)
		return exitOK
	case "diff":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "usage: odb bundle diff A B")
			return exitUsage
		}
		a, err := loadBundle(args[1])
		if err != nil {
			return reportBundleError(stderr, err)
		}
		b, err := loadBundle(args[2])
		if err != nil {
			return reportBundleError(stderr, err)
		}
		changes := diffBundles(a, b)
		if len(changes) == 0 {
			fmt.Fprintln(stdout, "No differences in what the bundles migrate.")
			return exitOK
		}
		for _, c := range changes {
			fmt.Fprintln(stdout, c)
		}
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown bundle command %q\n\n%s\n", args[0], bundleUsage)
		return exitUsage
	}
}

func loadBundle(path string) (*export.Bundle, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the operator names the bundle file
	if err != nil {
		return nil, &export.Error{Code: export.CodeInvalid, Message: err.Error()}
	}
	return export.Parse(raw, buildinfo.Get().Version)
}

// reportBundleError prints a bundle problem and returns exit code 4.
func reportBundleError(stderr io.Writer, err error) int {
	var be *export.Error
	if errors.As(err, &be) {
		fmt.Fprintf(stderr, "%s: %s\n", be.Code, be.Message)
		for _, d := range be.Details {
			fmt.Fprintf(stderr, "  - %s\n", d)
		}
		return exitPreflight
	}
	fmt.Fprintln(stderr, err)
	return exitPreflight
}

// checkPlans checks what can be checked without the source: each spec decodes, its relation
// tree is valid, its key template parses and its location fits the target kind. The rest of the
// plan compiles against the live schema at preflight.
func checkPlans(b *export.Bundle) []string {
	var errs []string
	for _, m := range b.Mappings {
		var spec mapping.Spec
		if err := json.Unmarshal(m.Spec, &spec); err != nil {
			errs = append(errs, m.Name+": spec: "+err.Error())
			continue
		}
		spec.Normalize()
		if _, _, is := mapping.Depths(&spec); is != nil {
			errs = append(errs, m.Name+": "+is.Message)
		}
		if _, err := mapping.ParseKeyTemplate(spec.Key.Template); err != nil {
			errs = append(errs, m.Name+": key template: "+err.Error())
		}
		if err := mapping.ValidateLocation(m.Location, b.Target.Type); err != nil {
			errs = append(errs, m.Name+": location: "+err.Error())
		}
	}
	return errs
}

// describeConn summarizes a connection's config without secrets.
func describeConn(c export.Connection) string {
	var cfg map[string]any
	_ = json.Unmarshal(c.Config, &cfg)
	str := func(k string) string {
		if v, ok := cfg[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	switch c.Type {
	case "postgres":
		return fmt.Sprintf("PostgreSQL %s@%s:%s/%s", str("username"), str("host"), str("port"), str("database"))
	case "couchbase":
		return fmt.Sprintf("Couchbase %s as %s", str("connectionString"), str("username"))
	case "directory":
		return fmt.Sprintf("Directory %s (layout %s)", str("path"), str("layout"))
	}
	return c.Type
}

func credentialMode(c *export.Credentials) string {
	switch {
	case c == nil:
		return "none"
	case c.Mode == export.ModeExternal:
		var refs []string
		for k, v := range c.Refs {
			refs = append(refs, k+" from "+v)
		}
		sort.Strings(refs)
		return "external (" + strings.Join(refs, ", ") + ")"
	case c.Mode == export.ModeSealed:
		return "sealed (passphrase required)"
	}
	return c.Mode + " (WARNING: readable by anyone with the file)"
}

// inspect prints what the bundle will do, with no secret.
func inspect(w io.Writer, b *export.Bundle) {
	fmt.Fprintf(w, "Bundle      %s (format %d, odb %s)\n", b.SHA256(), b.BundleVersion, b.OdbVersion)
	fmt.Fprintf(w, "Project     %s — exported %s by %s\n", b.Project.Name, b.CreatedAt.Format("2006-01-02 15:04 MST"), b.CreatedBy)
	fmt.Fprintf(w, "Source      %s\n", describeConn(b.Source))
	fmt.Fprintf(w, "            credentials: %s\n", credentialMode(b.Source.Credentials))
	fmt.Fprintf(w, "Target      %s\n", describeConn(b.Target))
	if b.Target.Credentials != nil {
		fmt.Fprintf(w, "            credentials: %s\n", credentialMode(b.Target.Credentials))
	}
	fmt.Fprintf(w, "Mappings    %d (row counts are estimated at preflight)\n", len(b.Mappings))
	for _, m := range b.Mappings {
		fmt.Fprintf(w, "  %-20s %s → %s\n", m.Name, m.Source, m.Location.String())
	}
	if len(b.Relationships) > 0 {
		fmt.Fprintf(w, "Virtual relationships  %d\n", len(b.Relationships))
	}
	o := b.JobOptions
	fmt.Fprintf(w, "Options     write %s, batch %d, readers %s, writers %d, errors ≤ %d\n",
		o.WriteMode, o.BatchSize, o.ReadParallelism, o.WriteConcurrency, o.ErrorPolicy.MaxErrors)
}

// diffBundles lists semantic differences: what a and b migrate, not when or by whom.
func diffBundles(a, b *export.Bundle) []string {
	var out []string
	if describeConn(a.Source) != describeConn(b.Source) || !jsonEqual(a.Source.Config, b.Source.Config) {
		out = append(out, "~ source: "+describeConn(a.Source)+" → "+describeConn(b.Source))
	}
	if a.Target.Type != b.Target.Type || !jsonEqual(a.Target.Config, b.Target.Config) {
		out = append(out, "~ target: "+describeConn(a.Target)+" → "+describeConn(b.Target))
	}
	for _, role := range []struct {
		name string
		x, y *export.Credentials
	}{{"source", a.Source.Credentials, b.Source.Credentials}, {"target", a.Target.Credentials, b.Target.Credentials}} {
		if mx, my := modeOf(role.x), modeOf(role.y); mx != my {
			out = append(out, fmt.Sprintf("~ %s credentials: %s → %s", role.name, mx, my))
		}
	}
	am, bm := map[string]export.Mapping{}, map[string]export.Mapping{}
	for _, m := range a.Mappings {
		am[m.ID] = m
	}
	for _, m := range b.Mappings {
		bm[m.ID] = m
	}
	for _, m := range a.Mappings {
		n, ok := bm[m.ID]
		switch {
		case !ok:
			out = append(out, "- mapping "+m.Name+" ("+m.Source.String()+")")
		default:
			out = append(out, diffMapping(m, n)...)
		}
	}
	for _, m := range b.Mappings {
		if _, ok := am[m.ID]; !ok {
			out = append(out, "+ mapping "+m.Name+" ("+m.Source.String()+" → "+m.Location.String()+")")
		}
	}
	ar, br := map[string]export.Relationship{}, map[string]export.Relationship{}
	for _, r := range a.Relationships {
		ar[r.ID] = r
	}
	for _, r := range b.Relationships {
		br[r.ID] = r
	}
	for id, r := range ar {
		if n, ok := br[id]; !ok {
			out = append(out, "- relationship "+r.Name)
		} else if !reflect.DeepEqual(r, n) {
			out = append(out, "~ relationship "+r.Name)
		}
	}
	for id, r := range br {
		if _, ok := ar[id]; !ok {
			out = append(out, "+ relationship "+r.Name)
		}
	}
	out = append(out, diffFields("option", a.JobOptions, b.JobOptions)...)
	if a.SourceFingerprint.SHA256 != b.SourceFingerprint.SHA256 {
		out = append(out, "~ source schema fingerprint changed (the tables or columns read differ)")
	}
	return out
}

func modeOf(c *export.Credentials) string {
	if c == nil {
		return "none"
	}
	return c.Mode
}

func diffMapping(a, b export.Mapping) []string {
	var out []string
	if a.Location != b.Location {
		out = append(out, fmt.Sprintf("~ mapping %s: location %s → %s", a.Name, a.Location.String(), b.Location.String()))
	}
	if jsonEqual(a.Spec, b.Spec) {
		return out
	}
	var x, y mapping.Spec
	_ = json.Unmarshal(a.Spec, &x)
	_ = json.Unmarshal(b.Spec, &y)
	var what []string
	if x.Key != y.Key {
		what = append(what, fmt.Sprintf("key %q → %q", x.Key.Template, y.Key.Template))
	}
	if x.TypeField != y.TypeField {
		what = append(what, "type field")
	}
	if len(x.FieldMappings) != len(y.FieldMappings) || !reflect.DeepEqual(x.FieldMappings, y.FieldMappings) {
		what = append(what, fmt.Sprintf("field mappings (%d → %d)", len(x.FieldMappings), len(y.FieldMappings)))
	}
	if !reflect.DeepEqual(x.Relations, y.Relations) {
		what = append(what, fmt.Sprintf("relations (%d → %d)", len(x.Relations), len(y.Relations)))
	}
	if !reflect.DeepEqual(x.DocSchema.Root, y.DocSchema.Root) {
		what = append(what, "document design")
	}
	if !reflect.DeepEqual(x.Aggregates, y.Aggregates) {
		what = append(what, "aggregates")
	}
	if len(what) == 0 {
		what = append(what, "settings")
	}
	return append(out, fmt.Sprintf("~ mapping %s: %s", a.Name, strings.Join(what, ", ")))
}

// diffFields compares two structs field by field through their JSON form.
func diffFields(prefix string, a, b any) []string {
	var x, y map[string]any
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	_ = json.Unmarshal(ra, &x)
	_ = json.Unmarshal(rb, &y)
	keys := map[string]bool{}
	for k := range x {
		keys[k] = true
	}
	for k := range y {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, k := range names {
		if !reflect.DeepEqual(x[k], y[k]) {
			out = append(out, fmt.Sprintf("~ %s %s: %v → %v", prefix, k, show(x[k]), show(y[k])))
		}
	}
	return out
}

func show(v any) string {
	if v == nil {
		return "(unset)"
	}
	if m, ok := v.(map[string]any); ok {
		raw, _ := json.Marshal(m)
		return string(raw)
	}
	return fmt.Sprint(v)
}

func jsonEqual(a, b json.RawMessage) bool {
	ca, err1 := export.CanonicalJSON(a)
	cb, err2 := export.CanonicalJSON(b)
	return err1 == nil && err2 == nil && string(ca) == string(cb)
}
