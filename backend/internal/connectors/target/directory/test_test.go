package directory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
)

func steps(res connectors.TestResult) map[string]connectors.TestStep {
	out := map[string]connectors.TestStep{}
	for _, s := range res.Steps {
		out[s.ID] = s
	}
	return out
}

func roomy(string) (FSInfo, error) {
	return FSInfo{FreeBytes: 100 << 30, FreeInodes: 50_000_000, Type: "apfs"}, nil
}

func TestTest_AC2_Writable(t *testing.T) {
	root, c := rootFixture(t)
	c.statFS = roomy
	dir := filepath.Join(root, "shop")
	res := c.Test(context.Background(), cfgJSON(t, map[string]any{"path": dir}), connectors.Credentials{})
	require.True(t, res.OK, "%+v", res.Steps)
	s := steps(res)
	for _, id := range []string{"enabled", "path", "exists", "isdir", "probe", "space", "fstype", "case"} {
		require.Equal(t, connectors.StepOK, s[id].Status, id)
	}
	require.Contains(t, s["fstype"].Detail, "apfs")
	require.Contains(t, s["case"].Detail, "ase-")
	left, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, left, "no probe file remains")

	// createIfMissing creates a missing folder whose parent exists.
	res = c.Test(context.Background(), cfgJSON(t, map[string]any{"path": filepath.Join(root, "new"), "createIfMissing": true}), connectors.Credentials{})
	require.True(t, res.OK)
	require.DirExists(t, filepath.Join(root, "new"))
	res = c.Test(context.Background(), cfgJSON(t, map[string]any{"path": filepath.Join(root, "missing")}), connectors.Credentials{})
	require.Equal(t, CodeNotFound, steps(res)["exists"].Code)
}

func TestTest_AC2_ReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to read-only folders")
	}
	root, c := rootFixture(t)
	c.statFS = roomy
	ro := filepath.Join(root, "ro")
	require.NoError(t, os.Mkdir(ro, 0o500))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	res := c.Test(context.Background(), cfgJSON(t, map[string]any{"path": ro}), connectors.Credentials{})
	require.False(t, res.OK)
	require.Equal(t, CodeNotWritable, steps(res)["probe"].Code)
}

func TestTest_AC2_NotADirectory(t *testing.T) {
	root, c := rootFixture(t)
	file := filepath.Join(root, "x.json")
	require.NoError(t, os.WriteFile(file, []byte("{}"), 0o600))
	res := c.Test(context.Background(), cfgJSON(t, map[string]any{"path": file}), connectors.Credentials{})
	require.False(t, res.OK)
	require.Equal(t, CodeNotADir, steps(res)["isdir"].Code)

	res = c.Test(context.Background(), cfgJSON(t, map[string]any{"path": "/etc"}), connectors.Credentials{})
	require.Equal(t, CodeOutsideRoots, steps(res)["path"].Code)
}

func TestTest_AC2_LowSpaceWarns(t *testing.T) {
	root, c := rootFixture(t)
	dir := filepath.Join(root, "shop")
	c.statFS = func(string) (FSInfo, error) {
		return FSInfo{FreeBytes: 1 << 30, FreeInodes: 5_000_000, Type: "nfs"}, nil
	}
	res := c.Test(context.Background(), cfgJSON(t, map[string]any{"path": dir, "durability": "file+dir"}), connectors.Credentials{})
	require.True(t, res.OK, "warnings don't fail the test")
	s := steps(res)
	require.Equal(t, CodeLowSpace, s["space"].Code)
	require.Equal(t, connectors.StepWarn, s["space"].Status)
	require.Equal(t, CodeNetworkFS, s["fstype"].Code)

	c.statFS = func(string) (FSInfo, error) { return FSInfo{FreeBytes: 100 << 30, FreeInodes: 10}, nil }
	require.Equal(t, CodeLowSpace, steps(c.Test(context.Background(), cfgJSON(t, map[string]any{"path": dir}), connectors.Credentials{}))["space"].Code, "few inodes")

	c.statFS = func(string) (FSInfo, error) { return FSInfo{}, errors.New("no statfs") }
	s = steps(c.Test(context.Background(), cfgJSON(t, map[string]any{"path": dir}), connectors.Credentials{}))
	require.Equal(t, connectors.StepWarn, s["space"].Status)
	require.Equal(t, connectors.StepOK, s["fstype"].Status)

	// The abs statfs works on this machine.
	info, err := statFS(dir)
	require.NoError(t, err)
	require.NotZero(t, info.FreeBytes)
}
