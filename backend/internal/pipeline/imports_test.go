package pipeline

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestImports_AC6_PipelineIsPortable (OC-051): the pipeline runs headless, so it must not
// depend on the HTTP API, the Couchbase metadata store or the Couchbase SDK except through the
// target port (connectors/target/couchbase). It uses internal/store's interfaces and domain
// types only, which `odb run` implements in memory with the state directory as its ports.
func TestImports_AC6_PipelineIsPortable(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-test=false", "omnidatabus/internal/pipeline").CombinedOutput()
	require.NoError(t, err, string(out))
	deps := strings.Fields(string(out))
	for _, forbidden := range []string{
		"omnidatabus/internal/httpapi",
		"omnidatabus/internal/store/cbstore",
		"omnidatabus/internal/connectors/target/couchbase",
		"github.com/couchbase/gocb/v2",
	} {
		require.NotContains(t, deps, forbidden)
	}
}
