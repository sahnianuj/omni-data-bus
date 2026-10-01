package pathcodec

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

type fixture struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Hashed bool   `json:"hashed"`
}

// fixtureKeys are the cases shared with the web client (web/src/lib/pathcodec).
var fixtureKeys = []string{
	"1001", "order::1001", "a/b\\c", "C:\\x", "50%", "tilde~key", "*?\"<>|", ".hidden", "..", "-flag",
	"trailing.", "trailing ", "CON", "con.json", "Com1", "LPT9.txt", "CONSOLE", "nul", "tab\there", "bell\x07\x7f",
	"café", "cafe\u0301", "日本語::注文", "🎉 party", "spaces in key", "UPPER lower",
	strings.Repeat("x", 247), strings.Repeat("x", 248), strings.Repeat("é", 150), strings.Repeat("/", 100),
}

// validName checks a name against the Linux, macOS and Windows rules.
func validName(t *testing.T, name string) {
	t.Helper()
	require.NotEmpty(t, name)
	require.LessOrEqual(t, len(name), MaxName)
	require.True(t, utf8.ValidString(name), name)
	for i := 0; i < len(name); i++ {
		b := name[i]
		require.False(t, strings.ContainsRune(`/\:*?"<>|`, rune(b)) || b < 0x20 || b == 0x7F, "byte %q in %q", b, name)
	}
	require.False(t, strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "), name)
	base, _, _ := strings.Cut(name, ".")
	require.False(t, reserved[strings.ToUpper(base)], name)
}

func TestEncode_AC2_Fixtures(t *testing.T) {
	var want []fixture
	if os.Getenv("ODB_UPDATE_GOLDEN") != "" {
		for _, k := range fixtureKeys {
			n, h := Encode(k)
			want = append(want, fixture{Key: k, Name: n, Hashed: h})
		}
		b, _ := json.MarshalIndent(map[string]any{"version": Version, "cases": want}, "", "  ")
		require.NoError(t, os.WriteFile("testdata/pathcodec.json", append(b, '\n'), 0o644))
	}
	raw, err := os.ReadFile("testdata/pathcodec.json")
	require.NoError(t, err)
	var f struct {
		Cases []fixture `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Len(t, f.Cases, len(fixtureKeys))
	for _, c := range f.Cases {
		name, hashed := Encode(c.Key)
		require.Equal(t, c.Name, name, "%q", c.Key)
		require.Equal(t, c.Hashed, hashed, "%q", c.Key)
		validName(t, name)
		back, ok := Decode(name)
		require.Equal(t, !hashed, ok, "%q", c.Key)
		if ok {
			require.Equal(t, NFC(c.Key), back, "%q", c.Key)
		}
	}
	// Spot checks of the rules.
	for key, name := range map[string]string{
		"1001": "1001", "a/b": "a%2Fb", "50%": "50%25", ".x": "%2Ex", "CON": "%43ON", "x.": "x%2E", "é": "é", "a~b": "a%7Eb",
	} {
		got, _ := Encode(key)
		require.Equal(t, name, got, key)
	}
	nfc, _ := Encode("cafe\u0301")
	nfc2, _ := Encode("café")
	require.Equal(t, nfc, nfc2, "NFD and NFC keys get the same name")
	long, hashed := Encode(strings.Repeat("x", 300))
	require.True(t, hashed)
	require.Len(t, long, hashedHead+1+hashLen)
}

func TestDecode_Invalid(t *testing.T) {
	for _, bad := range []string{"%", "%4", "%zz", "%4g", "a~0123456789abcdef"} {
		_, ok := Decode(bad)
		require.False(t, ok, bad)
	}
	seg, _ := SegmentName("")
	require.Equal(t, "%00", seg)
	seg, _ = SegmentName("2026")
	require.Equal(t, "2026", seg)
}

func FuzzEncodeDecode(f *testing.F) {
	for _, k := range fixtureKeys {
		f.Add(k)
	}
	f.Fuzz(func(t *testing.T, key string) {
		if key == "" || !utf8.ValidString(key) {
			return
		}
		name, hashed := Encode(key)
		validName(t, name)
		require.NotContains(t, name, "/")
		if !hashed {
			back, ok := Decode(name)
			require.True(t, ok)
			require.Equal(t, NFC(key), back)
		}
	})
}
