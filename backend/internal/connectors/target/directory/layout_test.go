package directory

import (
	"crypto/sha1" //nolint:gosec // test
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath_AC1_Layouts(t *testing.T) {
	flat := Config{Layout: "flat", Compression: "none"}
	p, hashed := flat.DocPath("orders", "1001")
	require.Equal(t, "orders/1001.json", p)
	require.False(t, hashed)

	sum := sha1.Sum([]byte("1001")) //nolint:gosec // test
	x := hex.EncodeToString(sum[:])
	p, _ = Config{Layout: "sharded"}.DocPath("orders", "1001")
	require.Equal(t, "orders/"+x[:2]+"/"+x[2:4]+"/1001.json", p)

	p, _ = Config{Layout: "key-path"}.DocPath("orders", "2026::10::1001")
	require.Equal(t, "orders/2026/10/1001.json", p)
	p, _ = Config{Layout: "key-path"}.DocPath("/orders/", "a::::b")
	require.Equal(t, "orders/a/%00/b.json", p)

	p, _ = Config{Layout: "flat", Compression: "gzip"}.DocPath("orders", "1001")
	require.Equal(t, "orders/1001.json.gz", p)

	// Keys are encoded: no separator or unsafe character reaches the path.
	p, _ = flat.DocPath("orders", "../../etc/passwd")
	require.Equal(t, "orders/%2E.%2F..%2Fetc%2Fpasswd.json", p)
	_, hashed = flat.DocPath("orders", strings.Repeat("k", 400))
	require.True(t, hashed)
	_, hashed = Config{Layout: "key-path"}.DocPath("o", "a::"+strings.Repeat("k", 400))
	require.True(t, hashed)
}
