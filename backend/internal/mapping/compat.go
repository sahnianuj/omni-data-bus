package mapping

import (
	_ "embed"
	"encoding/json"

	"omnidatabus/internal/docschema"
)

// Compatibility classes (OC-017 step 3).
const (
	ClassExact        = "exact"
	ClassLossless     = "lossless"
	ClassLossy        = "lossy"
	ClassIncompatible = "incompatible"
)

// compat.json is the single source of the source → target type matrix. The web client's
// canConnect runs its own table against the same file (canConnect.test.ts).
//
//go:embed compat.json
var compatJSON []byte

type compatEntry struct {
	Class     string `json:"class"`
	Transform *Step  `json:"transform"`
}

var compatMatrix = func() map[string]map[string]compatEntry {
	var f struct {
		Matrix map[string]map[string]compatEntry `json:"matrix"`
	}
	if err := json.Unmarshal(compatJSON, &f); err != nil {
		panic("mapping: invalid compat.json: " + err.Error())
	}
	return f.Matrix
}()

// Compat classifies mapping a source jsonType into a target field type, and gives the default
// conversion step for lossless and lossy pairs.
func Compat(source string, target docschema.FieldType) (class string, transform *Step) {
	e, ok := compatMatrix[source][string(target)]
	if !ok {
		return ClassIncompatible, nil
	}
	if e.Transform != nil {
		t := Step{Op: e.Transform.Op}
		if len(e.Transform.Args) > 0 {
			t.Args = map[string]any{}
			for k, v := range e.Transform.Args {
				t.Args[k] = v
			}
		}
		return e.Class, &t
	}
	return e.Class, nil
}

// TypeScore is the automap multiplier for a class: 1.0 exact, 0.85 lossless, 0.5 lossy, 0.
func TypeScore(class string) float64 {
	switch class {
	case ClassExact:
		return 1
	case ClassLossless:
		return 0.85
	case ClassLossy:
		return 0.5
	}
	return 0
}
