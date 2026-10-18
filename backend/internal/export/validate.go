package export

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// bundleSchemaJSON is api/bundle.schema.json (a test keeps the two identical).
//
//go:embed bundle.schema.json
var bundleSchemaJSON []byte

var bundleSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(bundleSchemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource("bundle.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("bundle.schema.json")
})

// SchemaErrors validates a bundle document against the JSON Schema; nil when it's valid.
func SchemaErrors(raw []byte) []string {
	sch, err := bundleSchema()
	if err != nil {
		return []string{"the bundle schema doesn't compile: " + err.Error()}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	if err := sch.Validate(inst); err != nil {
		var out []string
		for _, line := range strings.Split(err.Error(), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "jsonschema validation failed") {
				out = append(out, strings.TrimPrefix(line, "- "))
			}
		}
		if len(out) == 0 {
			out = []string{err.Error()}
		}
		return out
	}
	return nil
}

// Parse reads a bundle file and checks, in order: the version (so a newer format says so rather
// than failing its schema), the JSON Schema, and integrity. running is this binary's version.
func Parse(raw []byte, running string) (*Bundle, error) {
	var head struct {
		BundleVersion *int   `json:"bundleVersion"`
		OdbVersion    string `json:"odbVersion"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, &Error{Code: CodeInvalid, Message: "The file isn't a bundle: " + err.Error()}
	}
	if head.BundleVersion == nil {
		return nil, &Error{Code: CodeInvalid, Message: "The file isn't a bundle: bundleVersion is missing."}
	}
	if err := CheckVersion(*head.BundleVersion, head.OdbVersion, running); err != nil {
		return nil, err
	}
	if errs := SchemaErrors(raw); len(errs) > 0 {
		return nil, &Error{Code: CodeInvalid, Message: "The bundle doesn't match its schema.", Details: errs}
	}
	var b Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, &Error{Code: CodeInvalid, Message: err.Error()}
	}
	if err := b.Verify(); err != nil {
		return nil, err
	}
	return &b, nil
}

// CheckVersion is the version gate (OC-050 AC6): the bundle format must be one this binary
// reads, and the binary must be in the bundle's odbVersion range. A development build (version
// "dev" or a git description) runs any bundle of a known format.
func CheckVersion(bundleVersion int, odbRange, running string) error {
	if bundleVersion != BundleVersion {
		return &Error{Code: CodeVersionUnsupported, Message: fmt.Sprintf(
			"This bundle has format version %d; odb %s reads format version %d. Use an odb release that matches the tool it was exported from.",
			bundleVersion, running, BundleVersion)}
	}
	v, ok := parseSemver(running)
	if !ok || odbRange == "" || odbRange == "*" {
		return nil
	}
	if !inRange(v, odbRange) {
		return &Error{Code: CodeVersionUnsupported, Message: fmt.Sprintf(
			"This export needs odb %s; this is odb %s. Install a matching odb (README.md, Install odb).", odbRange, running)}
	}
	return nil
}

// RangeFor is the odbVersion range a bundle exported by version gets: this minor release up to
// the next major (">=2.0.0 <3.0.0"), or "*" for a development build.
func RangeFor(version string) string {
	v, ok := parseSemver(version)
	if !ok {
		return "*"
	}
	return fmt.Sprintf(">=%d.%d.0 <%d.0.0", v[0], v[1], v[0]+1)
}

var semverRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(?:rc|beta|alpha)[.\d]*)?$`)

// parseSemver accepts release and pre-release versions; anything else (dev, a git description,
// -dirty) is a development build.
func parseSemver(s string) ([3]int, bool) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func cmpSemver(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// inRange checks v against space-separated comparators (">=2.0.0 <3.0.0"); an unreadable
// comparator fails the check.
func inRange(v [3]int, r string) bool {
	for _, c := range strings.Fields(r) {
		op := strings.TrimRight(c, "0123456789.v")
		w, ok := parseSemver(c[len(op):])
		if !ok {
			return false
		}
		d := cmpSemver(v, w)
		var pass bool
		switch op {
		case ">=":
			pass = d >= 0
		case ">":
			pass = d > 0
		case "<=":
			pass = d <= 0
		case "<":
			pass = d < 0
		case "=", "":
			pass = d == 0
		}
		if !pass {
			return false
		}
	}
	return true
}
