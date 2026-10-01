package directory

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"omnidatabus/internal/pathcodec"
)

var placeholder = regexp.MustCompile(`\{[^{}]+\}`)

// RenderKey builds a key from a key template and the values of its columns in template order
// ({order_id}::{line_no} with 1001, 3 → 1001::3). Without a template (or placeholders) the
// values are joined with "::" (the directory default for a composite key).
func RenderKey(template string, values []string) string {
	holes := placeholder.FindAllStringIndex(template, -1)
	if template == "" || len(holes) != len(values) || strings.Contains(template, "{$") {
		return strings.Join(values, pathcodec.Separator)
	}
	i := 0
	return placeholder.ReplaceAllStringFunc(template, func(string) string {
		v := values[i]
		i++
		return v
	})
}

// LookupFile finds a document under a directory target's folder without the server (odb doc
// get): the layout, compression and key template come from _odb/run.json when present, else
// from cfg. It returns the file's path and the document (decompressed).
func LookupFile(root, folder string, values []string, cfg Config) (string, []byte, error) {
	template := ""
	if info, err := ReadRunInfo(root); err == nil {
		if info.Layout != "" {
			cfg.Layout = info.Layout
		}
		if info.Compression != "" {
			cfg.Compression = info.Compression
		}
		template = info.Mappings[strings.Trim(folder, "/")].KeyTemplate
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	key := RenderKey(template, values)
	rel, _ := cfg.DocPath(folder, key)
	path := filepath.Join(root, filepath.FromSlash(rel))
	b, err := readDoc(path, cfg)
	return path, b, err
}
