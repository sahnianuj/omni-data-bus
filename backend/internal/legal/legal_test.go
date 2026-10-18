package legal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// apacheSHA256 is the apache.org LICENSE-2.0.txt, as in capellactl (CAP-044 AC1).
const apacheSHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

// OC-189 AC1: LICENSE is the unmodified Apache License 2.0, in the repository and embedded.
func TestLicense_OC189_AC1(t *testing.T) {
	root, err := os.ReadFile(filepath.Join("..", "..", "..", "LICENSE"))
	require.NoError(t, err)
	for name, b := range map[string][]byte{"LICENSE": root, "embedded": License} {
		sum := sha256.Sum256(b)
		require.Equal(t, apacheSHA256, hex.EncodeToString(sum[:]), name)
	}
	for _, f := range []string{"NOTICE", "THIRD_PARTY_NOTICES.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", f))
		require.NoError(t, err)
		embedded := map[string][]byte{"NOTICE": Notice, "THIRD_PARTY_NOTICES.md": ThirdParty}[f]
		require.Equal(t, string(raw), string(embedded), "%s: run make notices", f)
	}
}

// OC-189 AC2: NOTICE names the product, the copyright holder and the author.
func TestLicense_OC189_AC2_NoticeAndVersion(t *testing.T) {
	n := string(Notice)
	require.True(t, strings.HasPrefix(n, "Omni-Data-Bus\n"))
	require.Contains(t, n, Copyright)
	require.Contains(t, n, "Author: "+Author)
	require.Contains(t, n, "Apache License, Version 2.0")
	require.Contains(t, n, "Couchbase is a trademark of Couchbase, Inc.")
	require.Equal(t, "Licensed under Apache-2.0 · Copyright 2026 Couchbase, Inc.", Line())
	require.Contains(t, string(ThirdParty), "| github.com/couchbase/gocb/v2 |")
}

// Files lists the three files in reading order, with the embedded contents.
func TestFiles_OC189(t *testing.T) {
	fs := Files()
	require.Len(t, fs, 3)
	require.Equal(t, []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md"}, []string{fs[0].Name, fs[1].Name, fs[2].Name})
	require.Equal(t, License, fs[0].Data)
	require.Equal(t, Notice, fs[1].Data)
	require.Equal(t, ThirdParty, fs[2].Data)
}
