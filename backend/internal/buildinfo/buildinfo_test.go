package buildinfo

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildInfo_AC1_ChannelFromVersion(t *testing.T) {
	cases := map[string]string{
		"v2.0.0":            ChannelRelease,
		"2.0.0":             ChannelRelease,
		"v2.0.0-rc.1":       ChannelRC,
		"v2.1.0-beta.2":     ChannelRC,
		"v2.0.0-3-g3f2a9c1": ChannelDev,
		"v2.0.0-dirty":      ChannelDev,
		"3f2a9c1":           ChannelDev,
		"dev":               ChannelDev,
	}
	for v, want := range cases {
		assert.Equal(t, want, ChannelFor(v), v)
	}
	i := build("v2.0.0", "3f2a9c1e0b", "2026-11-02T14:05:11Z", "")
	assert.Equal(t, "2.0.0", i.Version)
	assert.Equal(t, ChannelRelease, i.Channel)
	assert.Equal(t, "3f2a9c1", i.ShortCommit())
	assert.False(t, i.Dirty)
	assert.Equal(t, ChannelRC, build("v2.0.0-rc.1", "x", "", "").Channel)
	// An explicit channel (the release workflow's CHANNEL) wins.
	assert.Equal(t, ChannelRC, build("v2.0.0", "x", "", ChannelRC).Channel)
	assert.True(t, build("v2.0.0-dirty", "x", "", "").Dirty)
}

func TestBuildInfo_AC1_Defaults(t *testing.T) {
	i := Get() // no -ldflags in `go test`
	assert.Equal(t, "dev", i.Version)
	assert.Equal(t, "none", i.Commit)
	assert.Equal(t, ChannelDev, i.Channel)
	assert.Equal(t, runtime.Version(), i.GoVersion)
	assert.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, i.Platform)
	assert.Equal(t, "dev", Normalize(""))
	text := i.Text()
	assert.True(t, strings.HasPrefix(text, "Omni-Data-Bus dev (dev)\n"), text)
	assert.Contains(t, text, "Built: unknown")
}

func TestBuildInfo_Text(t *testing.T) {
	i := Info{Version: "2.0.0", Commit: "3f2a9c1", BuildTime: "2026-11-02T14:05:11Z", Channel: "release", GoVersion: "go1.26.8", Platform: "linux/amd64"}
	assert.Equal(t, "Omni-Data-Bus 2.0.0 (release)\nCommit: 3f2a9c1  Built: 2026-11-02T14:05:11Z  Go: go1.26.8  Platform: linux/amd64\n", i.Text())
	i.Dirty = true
	assert.Contains(t, i.Text(), "2.0.0 (dirty) (release)")
}
