package checkgen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestCheckGen_AC4_DetectsDiff(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	gen := filepath.Join(dir, "schema.d.ts")
	require.NoError(t, os.WriteFile(gen, []byte("v1"), 0o600))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")

	stale, err := Stale(context.Background(), dir, ".")
	require.NoError(t, err)
	require.Empty(t, stale)

	require.NoError(t, os.WriteFile(gen, []byte("v2"), 0o600))
	stale, err = Stale(context.Background(), dir, ".")
	require.NoError(t, err)
	require.Equal(t, []string{"schema.d.ts"}, stale)
}
