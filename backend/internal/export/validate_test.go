package export

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// OC-171 AC6: a bundle exported by 2.5 (variants or not) needs odb 2.5 or later; a 2.0 odb
// refuses it before reading any data, and 2.0 bundles still run on 2.5.
func TestBundle_OC171_AC6_FeatureGate(t *testing.T) {
	err := CheckVersion(BundleVersion, RangeFor("2.5.0"), "2.0.3")
	var e *Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, CodeVersionUnsupported, e.Code)
	require.Contains(t, e.Message, "needs odb >=2.5.0 <3.0.0")
	require.NoError(t, CheckVersion(BundleVersion, RangeFor("2.0.0"), "2.5.0"))
}

// OC-182 AC2, OC-184 AC2: odb 2.5 runs a 2.0 export; an older odb is told to install a matching one.
func TestCheckVersion_OC184_AC2(t *testing.T) {
	require.NoError(t, CheckVersion(BundleVersion, RangeFor("2.0.0"), "2.5.0"), "a 2.0 export on odb 2.5")
	require.NoError(t, CheckVersion(BundleVersion, RangeFor("2.5.0"), "2.7.1"))
	err := CheckVersion(BundleVersion, RangeFor("2.5.0"), "2.0.1")
	require.ErrorContains(t, err, "This export needs odb >=2.5.0 <3.0.0; this is odb 2.0.1. Install a matching odb (README.md, Install odb).")
}
