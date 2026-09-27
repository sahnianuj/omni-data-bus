// Package tools holds repository quality-gate helpers and their tests.
package tools

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLintConfig_AC3_RequiredLintersEnabled(t *testing.T) {
	raw, err := os.ReadFile("../.golangci.yml")
	require.NoError(t, err)
	var cfg struct {
		Linters struct {
			Enable []string `yaml:"enable"`
		} `yaml:"linters"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	for _, l := range []string{"errcheck", "gosec", "staticcheck", "govet"} {
		require.Contains(t, cfg.Linters.Enable, l)
	}
}
