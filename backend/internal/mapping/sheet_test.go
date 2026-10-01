package mapping

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMappingSheet_AC6_Golden(t *testing.T) {
	s, ctx := referenceSpec(t)
	// A constant and a transform, beside the fixture's mapped, bound and unmapped fields.
	for i := range s.DocSchema.Root.Children {
		if s.DocSchema.Root.Children[i].Name == "internal" {
			s.FieldMappings[3] = FieldMapping{ID: "m4", TargetFieldID: s.DocSchema.Root.Children[i].ID, Transforms: []Step{{Op: "constant", Args: map[string]any{"value": "v2"}}}, Origin: OriginManual, State: StateActive}
		}
	}
	s.FieldMappings[1].Transforms = []Step{{Op: "trim"}, {Op: "lower"}}
	rows := Sheet(s, TableRef{Schema: "shop", Name: "orders"}, Validate(s, ctx))

	var csvOut bytes.Buffer
	require.NoError(t, WriteSheetCSV(&csvOut, rows))
	golden := filepath.Join("testdata", "golden", "mapping_sheet_order.csv")
	if os.Getenv("ODB_UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
		require.NoError(t, os.WriteFile(golden, csvOut.Bytes(), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, string(want), csvOut.String())

	// Markdown: the same rows as a table.
	var md bytes.Buffer
	require.NoError(t, WriteSheetMarkdown(&md, "shop.orders", rows))
	lines := strings.Split(strings.TrimSpace(md.String()), "\n")
	require.Equal(t, "# shop.orders", lines[0])
	require.Len(t, lines, 4+len(rows)) // title, blank, header, separator, rows
	require.Equal(t, "| path | type | required | example | source | transform | constant | status | issues |", lines[2])
}
