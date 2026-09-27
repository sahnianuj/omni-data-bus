package covergate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoverageGate_AC5_FailsBelowThreshold(t *testing.T) {
	profile := `mode: atomic
omnidatabus/internal/automap/engine.go:10.2,12.3 72 1
omnidatabus/internal/automap/engine.go:14.2,16.3 28 0
omnidatabus/internal/config/config.go:1.1,2.2 10 1
`
	_, err := Evaluate(strings.NewReader(profile), DefaultThresholds())
	require.Error(t, err)
	require.Contains(t, err.Error(), "internal/automap 72% < 90%")
	require.NotContains(t, err.Error(), "internal/config")
}

func TestCoverageGate_AC5_PassesAtThreshold(t *testing.T) {
	profile := `mode: set
omnidatabus/internal/config/config.go:1.1,2.2 80 1
omnidatabus/internal/config/config.go:3.1,4.2 20 0
omnidatabus/internal/automap/engine.go:1.1,2.2 90 1
omnidatabus/internal/automap/engine.go:3.1,4.2 10 0
omnidatabus/cmd/omni-data-bus/main.go:1.1,2.2 10 0
`
	res, err := Evaluate(strings.NewReader(profile), DefaultThresholds())
	require.NoError(t, err)
	require.Len(t, res, 2, "cmd/ is excluded")
}

func TestCoverageGate_AC5_RejectsMalformedProfile(t *testing.T) {
	_, err := Evaluate(strings.NewReader("mode: set\ngarbage\n"), DefaultThresholds())
	require.Error(t, err)
}

func TestCoverageGate_AC5_IntegrationGatedPackages(t *testing.T) {
	profile := `mode: set
omnidatabus/internal/store/cbstore/projects.go:1.1,2.2 10 1
omnidatabus/internal/store/cbstore/projects.go:3.1,4.2 90 0
omnidatabus/internal/config/config.go:1.1,2.2 10 1
`
	res, err := Evaluate(strings.NewReader(profile), DefaultThresholds())
	require.NoError(t, err, "unit gate skips integration-covered packages")
	require.Len(t, res, 1)

	_, err = Evaluate(strings.NewReader(profile), IntegrationThresholds())
	require.ErrorContains(t, err, "internal/store/cbstore 10% < 80%")
}
