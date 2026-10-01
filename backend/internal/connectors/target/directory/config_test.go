package directory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

func cfgJSON(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// rootFixture is an allowed root with a sub-folder, a symlink out of it (to /etc) and one
// inside it.
func rootFixture(t *testing.T) (root string, c *Connector) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "odb-out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "shop"), 0o750))
	require.NoError(t, os.Symlink("/etc", filepath.Join(root, "escape")))
	require.NoError(t, os.Symlink(filepath.Join(root, "shop"), filepath.Join(root, "alias")))
	return root, New([]string{root})
}

func TestValidate_AC1_AllowedRoots(t *testing.T) {
	root, c := rootFixture(t)
	cases := []struct {
		path string
		ok   bool
	}{
		{root, true},
		{filepath.Join(root, "shop"), true},
		{filepath.Join(root, "not", "yet", "created"), true},
		{filepath.Join(root, "alias"), true}, // a symlink that stays inside
		{"/etc", false},
		{root + "/../etc", false},
		{root + "-sibling", false}, // a prefix of the name isn't a parent
		{filepath.Join(root, "escape"), false},
		{filepath.Join(root, "escape", "ssh"), false},
	}
	for _, tc := range cases {
		err := c.Validate(cfgJSON(t, map[string]any{"path": tc.path}))
		if tc.ok {
			require.NoError(t, err, tc.path)
			continue
		}
		require.Equal(t, CodeOutsideRoots, problem.From(err).Code, tc.path)
		require.Equal(t, "config.path", problem.From(err).FieldErrors[0].Field)
	}
	norm, name, err := c.Normalize(cfgJSON(t, map[string]any{"path": filepath.Join(root, "shop") + "/"}))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "shop"), name)
	var got Config
	require.NoError(t, json.Unmarshal(norm, &got))
	require.Equal(t, Config{Path: filepath.Join(root, "shop"), Layout: "flat", Compression: "none", FileMode: "0640", Durability: "file", Manifest: true, MaxDocBytes: DefaultMaxDocBytes}, got)
}

func TestRegistry_AC1_DisabledWithoutRoots(t *testing.T) {
	ok, why := New([]string{}).Available()
	require.False(t, ok)
	require.Equal(t, DisabledReason, why)
	ok, _ = New([]string{" ", ""}).Available()
	require.False(t, ok, "blank roots don't count")
	c := New([]string{"/data/odb-out"})
	ok, why = c.Available()
	require.True(t, ok)
	require.Empty(t, why)
	require.Equal(t, []string{"/data/odb-out"}, c.Roots())
	require.Equal(t, target.KindDirectory, c.Descriptor().Type)
	caps := c.Capabilities()
	require.False(t, caps.SupportsDurability || caps.SupportsExpiry || caps.RequiresCredentials)
	require.NoError(t, c.Close(context.Background()))
	res := New([]string{}).Test(context.Background(), json.RawMessage(`{"path":"/data"}`), connectors.Credentials{})
	require.Equal(t, problem.CodeTargetKindDisabled, res.Steps[0].Code)
}

func TestParseConfig_Fields(t *testing.T) {
	for raw, field := range map[string]string{
		`[1]`:                                    "config",
		`{}`:                                     "config.path",
		`{"path":"rel"}`:                         "config.path",
		`{"path":"/d","layout":"tree"}`:          "config.layout",
		`{"path":"/d","compression":"zstd"}`:     "config.compression",
		`{"path":"/d","durability":"always"}`:    "config.durability",
		`{"path":"/d","fileMode":"0777"}`:        "config.fileMode",
		`{"path":"/d","fileMode":"640"}`:         "config.fileMode",
		`{"path":"/d","fileMode":"rw"}`:          "config.fileMode",
		`{"path":"/d","maxDocBytes":10}`:         "config.maxDocBytes",
		`{"path":"/d","maxDocBytes":2147483648}`: "config.maxDocBytes",
	} {
		_, err := ParseConfig(json.RawMessage(raw))
		require.Equal(t, field, problem.From(err).FieldErrors[0].Field, raw)
	}
	c, err := ParseConfig(json.RawMessage(`{"path":"/d","manifest":false,"fileMode":"0600","layout":"sharded","pretty":true}`))
	require.NoError(t, err)
	require.False(t, c.Manifest)
	require.True(t, c.Pretty)
	require.Equal(t, os.FileMode(0o700), c.DirMode(), "x for every r")
	c.FileMode = "0644"
	require.Equal(t, os.FileMode(0o755), c.DirMode())
	c.FileMode = "bad"
	require.Equal(t, os.FileMode(0o750), c.DirMode(), "falls back to 0640's folders")
}
