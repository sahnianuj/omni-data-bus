package docschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Import kinds (the infer endpoint's request).
const (
	KindJSONSample = "json-sample"
	KindJSONSchema = "json-schema"
)

// Import turns a customer's design into a document design (OC-059): sample documents (one
// object, an array or NDJSON) or a JSON Schema. Fields keep the input's order, scalar fields
// an example, and the first sample is kept as the reference document when it fits in
// MaxReferenceBytes. The result records the import (Imported) and carries constant hints.
func Import(kind, content, fileName string) (Schema, []string, error) {
	if kind == KindJSONSchema {
		s, warnings, err := FromJSONSchema([]byte(content))
		if err != nil {
			return Schema{}, nil, err
		}
		s.Imported = &Imported{At: time.Now().UTC(), FileName: fileName, Format: FormatJSONSchema, FieldCount: namedFields(&s), Fields: Contract(&s)}
		return s, warnings, nil
	}
	docs, err := ParseSamples([]byte(content))
	if err != nil {
		return Schema{}, nil, err
	}
	s, warnings, err := InferFromSamples(docs)
	if err != nil {
		return Schema{}, nil, err
	}
	format := FormatSample
	if t := bytes.TrimSpace([]byte(content)); t[0] != '[' && len(docs) > 1 {
		format = FormatNDJSON
	}
	s.Imported = &Imported{At: time.Now().UTC(), FileName: fileName, Format: format, Samples: len(docs), FieldCount: namedFields(&s), Fields: Contract(&s)}
	var ref bytes.Buffer
	switch err := json.Compact(&ref, docs[0]); {
	case err != nil:
	case ref.Len() > MaxReferenceBytes:
		warnings = append(warnings, fmt.Sprintf("The first sample is %d KB, over the %d KB kept as the reference document; only the field examples are kept.", ref.Len()>>10, MaxReferenceBytes>>10))
	default:
		s.Reference = json.RawMessage(ref.Bytes())
	}
	return s, warnings, nil
}

// ImportFiles imports a JSON Schema design split across files (OC-171): the first is the
// document, the others its referenced or item designs (FromJSONSchemaFiles).
func ImportFiles(files []File) (Schema, []string, error) {
	s, warnings, err := FromJSONSchemaFiles(files)
	if err != nil {
		return Schema{}, nil, err
	}
	s.Imported = &Imported{At: time.Now().UTC(), FileName: files[0].Name, Format: FormatJSONSchema, FieldCount: namedFields(&s), Fields: Contract(&s)}
	return s, warnings, nil
}

// namedFields counts the design's fields, not the item node of each array.
func namedFields(s *Schema) int {
	n := 0
	s.Walk(func(_ *DocField, path string, _ *DocField) {
		if !strings.HasSuffix(path, "[]") {
			n++
		}
	})
	return n
}
