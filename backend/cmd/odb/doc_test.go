package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
)

func TestDocPath(t *testing.T) {
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"doc", "path", "--folder", "orders", "1001"}, &out, &errOut))
	require.Equal(t, "orders/1001.json\n", out.String())
	out.Reset()
	require.Equal(t, 0, run([]string{"doc", "path", "--folder", "orders", "--layout", "key-path", "--gzip", "2026::10::1001"}, &out, &errOut))
	require.Equal(t, "orders/2026/10/1001.json.gz\n", out.String())
	out.Reset()
	require.Equal(t, 0, run([]string{"doc", "path", "--folder", "o", strings.Repeat("k", 300)}, &out, &errOut))
	require.Contains(t, errOut.String(), "hash")
	require.Equal(t, 2, run([]string{"doc", "path", "1001"}, &out, &errOut))
	require.Equal(t, 2, run([]string{"doc"}, &out, &errOut))
	require.Equal(t, 2, run([]string{"doc", "nope"}, &out, &errOut))
}

func TestDocGet_AC5_ByPrimaryKey(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "order_lines"), 0o750))
	c := directory.New([]string{root})
	cfg, _ := json.Marshal(map[string]any{"path": root, "layout": "key-path", "durability": "none"})
	w, err := c.OpenWriter(context.Background(), cfg, connectors.Credentials{}, target.WriteOptions{
		Location: target.Location{Kind: "directory", Folder: "order_lines"}, Mode: "upsert", MappingID: "m1", KeyTemplate: "{order_id}::{line_no}",
	})
	require.NoError(t, err)
	w.WriteBatch(context.Background(), []target.Doc{{Key: "1001::3", Value: []byte(`{"order":1001,"line":3}`)}})
	require.NoError(t, w.Close())

	var out, errOut bytes.Buffer
	// The composite key's values in template order; layout and template come from run.json.
	require.Equal(t, 0, run([]string{"doc", "get", "--root", root, "--folder", "order_lines", "1001", "3"}, &out, &errOut), errOut.String())
	require.JSONEq(t, `{"order":1001,"line":3}`, out.String())
	out.Reset()
	require.Equal(t, 0, run([]string{"doc", "get", "--root", root, "--folder", "order_lines", "1001::3"}, &out, &errOut), "or the whole key")
	require.Equal(t, 1, run([]string{"doc", "get", "--root", root, "--folder", "order_lines", "9999", "1"}, &out, &errOut))
	require.Contains(t, errOut.String(), "no document")
	require.Equal(t, 2, run([]string{"doc", "get", "--folder", "x", "1"}, &out, &errOut))
}

func TestRenderKey(t *testing.T) {
	require.Equal(t, "order::1001", directory.RenderKey("order::{order_id}", []string{"1001"}))
	require.Equal(t, "1001::3", directory.RenderKey("", []string{"1001", "3"}))
	require.Equal(t, "a::b", directory.RenderKey("{$uuid}", []string{"a", "b"}))
}
