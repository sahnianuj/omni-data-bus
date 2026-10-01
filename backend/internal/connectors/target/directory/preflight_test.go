package directory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
)

func checkByID(res []target.LocationPreflight, id string) (target.PreflightCheck, bool) {
	for _, r := range res {
		for _, c := range r.Checks {
			if c.ID == id {
				return c, true
			}
		}
	}
	return target.PreflightCheck{}, false
}

func TestPreflight_AC5_SpaceWarningsAndErrors(t *testing.T) {
	root, c := rootFixture(t)
	// 10 GB estimated: 8.33M rows × 1 KiB × 1.2 ≈ 10 GB.
	locs := []target.PreflightLocation{{MappingID: "m1", Location: target.Location{Kind: "directory", Folder: "shop"}, EstimatedDocs: 8_333_333, AvgDocBytes: 1000}}
	c.statFS = func(string) (FSInfo, error) { return FSInfo{FreeBytes: 4 << 30, FreeInodes: 100_000_000}, nil }
	chk, ok := checkByID(c.Preflight(context.Background(), conn(t, root), locs), "space")
	require.True(t, ok)
	require.Equal(t, "warn", chk.Status)
	require.Contains(t, chk.Detail, "4.0 GiB free")
	require.Contains(t, chk.Detail, "9.3 GiB needed")

	c.statFS = func(string) (FSInfo, error) { return FSInfo{FreeBytes: 512 << 20, FreeInodes: 100_000_000}, nil }
	chk, _ = checkByID(c.Preflight(context.Background(), conn(t, root), locs), "space")
	require.Equal(t, "fail", chk.Status, "below 10% of the estimate blocks")
	require.Equal(t, target.CodeDiskFull, chk.Code)

	c.statFS = func(string) (FSInfo, error) { return FSInfo{FreeBytes: 1 << 40, FreeInodes: 1_000}, nil }
	chk, _ = checkByID(c.Preflight(context.Background(), conn(t, root), locs), "space")
	require.Equal(t, "fail", chk.Status, "too few inodes for one file per document")
	require.Contains(t, chk.Detail, "inodes")

	c.statFS = func(string) (FSInfo, error) { return FSInfo{FreeBytes: 1 << 40, FreeInodes: 1 << 30}, nil }
	chk, _ = checkByID(c.Preflight(context.Background(), conn(t, root), locs), "space")
	require.Equal(t, "ok", chk.Status)

	// No estimate: no space check.
	_, ok = checkByID(c.Preflight(context.Background(), conn(t, root), []target.PreflightLocation{{Location: target.Location{Folder: "shop"}}}), "space")
	require.False(t, ok)
}

func TestPreflight_AC5_OrphanCleanup(t *testing.T) {
	root, c := rootFixture(t)
	tmp := filepath.Join(root, "shop", tmpDirName)
	require.NoError(t, os.MkdirAll(tmp, 0o750))
	old := time.Now().Add(-time.Hour)
	for _, n := range []string{"w1-1.tmp", "w1-2.tmp"} {
		p := filepath.Join(tmp, n)
		require.NoError(t, os.WriteFile(p, []byte("{partial"), 0o600))
		require.NoError(t, os.Chtimes(p, old, old))
	}
	res := c.Preflight(context.Background(), conn(t, root), []target.PreflightLocation{{MappingID: "m1", Location: target.Location{Kind: "directory", Folder: "shop"}}})
	chk, ok := checkByID(res, "orphans")
	require.True(t, ok)
	require.Contains(t, chk.Detail, "Removed 2")
	left, _ := os.ReadDir(tmp)
	require.Empty(t, left)
}
