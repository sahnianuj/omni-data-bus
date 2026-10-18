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
	assert.Equal(t, "Omni-Data-Bus 2.0.0 (release)\nCommit: 3f2a9c1  Built: 2026-11-02T14:05:11Z  Go: go1.26.8  Platform: linux/amd64\n"+
		"Licensed under Apache-2.0 · Copyright 2026 Couchbase, Inc.\n", i.Text()) // OC-189 AC2
	i.Dirty = true
	assert.Contains(t, i.Text(), "2.0.0 (dirty) (release)")
}

// OC-172 AC1: an untagged build of VERSION 2.5.0 says the release and the build, never a bare
// commit; a CI run number and a dirty tree are part of the build.
func TestBuildInfo_OC172_AC1_DevBuild(t *testing.T) {
	i := buildWith("2.5.0-dev", "7f6cea2e0b11", "2026-10-17T08:00:00Z", "", "", "")
	assert.Equal(t, "2.5.0-dev", i.Version)
	assert.Equal(t, ChannelDev, i.Channel)
	assert.Equal(t, "7f6cea2", i.Build, "the short commit by default")
	assert.Equal(t, "2.5.0-dev · build 7f6cea2", i.Summary())
	assert.False(t, i.Dirty)

	ci := buildWith("2.5.0", "7f6cea2e0b11", "", "", "7f6cea2 #418", "true")
	assert.Equal(t, ChannelRelease, ci.Channel)
	assert.Equal(t, "2.5.0 · build 7f6cea2 #418", ci.Summary())
	assert.True(t, ci.Dirty, "dirty from the build, not the version suffix")
	assert.Contains(t, ci.Text(), "Build: 7f6cea2 #418  Commit: 7f6cea2")

	assert.Equal(t, "dev", Get().Summary(), "plain go build: no commit, no build")
	defer Override("2.5.0", "abcdef0123", "", "")()
	assert.Equal(t, "2.5.0 · build abcdef0", Get().Summary())
}
