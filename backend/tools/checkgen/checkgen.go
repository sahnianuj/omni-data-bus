// Package checkgen reports generated files that differ from what is committed,
// so CI can fail when someone forgets to run `make gen`.
package checkgen

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Stale returns the paths under the given pathspecs that differ from HEAD in repoDir.
func Stale(ctx context.Context, repoDir string, pathspecs ...string) ([]string, error) {
	args := append([]string{"-C", repoDir, "status", "--porcelain", "--"}, pathspecs...)
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed binary; args are repo paths from the Makefile
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git status: %w: %s", err, errOut.String())
	}
	var stale []string
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if len(line) > 3 {
			stale = append(stale, strings.TrimSpace(line[3:]))
		}
	}
	return stale, nil
}
