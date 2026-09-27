package cbstore

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

//go:embed queries/*.sqlpp
var queryFiles embed.FS

// Query is one named SQL++ statement from queries/*.sqlpp.
type Query struct {
	Name   string
	SQL    string // with #{collection} placeholders expanded
	Raw    string // as written
	Tenant bool   // annotated "-- tenant": must filter on workspaceId
}

var placeholder = regexp.MustCompile(`#\{([a-z_]+)\}`)

// loadQueries parses every embedded file. keyspace maps a collection name to its quoted keyspace.
func loadQueries(files fs.FS, keyspace func(string) string) (map[string]Query, error) {
	paths, err := fs.Glob(files, "queries/*.sqlpp")
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := map[string]Query{}
	for _, p := range paths {
		raw, err := fs.ReadFile(files, p)
		if err != nil {
			return nil, err
		}
		for _, block := range strings.Split(string(raw), "-- name: ")[1:] {
			name, body, _ := strings.Cut(block, "\n")
			name = strings.TrimSpace(name)
			if _, dup := out[name]; dup {
				return nil, fmt.Errorf("%s: duplicate query name %q", p, name)
			}
			q := Query{Name: name, Raw: strings.TrimSpace(body)}
			var sqlLines []string
			for _, line := range strings.Split(q.Raw, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "-- tenant" {
					q.Tenant = true
				}
				if strings.HasPrefix(trimmed, "--") {
					continue // comments are documentation only; never sent to the server
				}
				sqlLines = append(sqlLines, line)
			}
			q.SQL = placeholder.ReplaceAllStringFunc(strings.Join(sqlLines, "\n"), func(m string) string {
				return keyspace(placeholder.FindStringSubmatch(m)[1])
			})
			out[name] = q
		}
	}
	return out, nil
}
