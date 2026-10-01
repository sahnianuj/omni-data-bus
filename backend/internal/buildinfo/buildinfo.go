// Package buildinfo is the single source of the running binary's release and build metadata
// (OC-058): the HTTP API, the CLI, logs, metrics and exports all read it.
package buildinfo

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
)

// Set at build time with -ldflags "-X omnidatabus/internal/buildinfo.version=… ". A plain
// `go build` keeps the defaults.
var (
	version   = "dev"
	commit    = "none"
	buildTime = ""
	channel   = ""
)

// Channels.
const (
	ChannelRelease = "release"
	ChannelRC      = "rc"
	ChannelDev     = "dev"
)

// Info describes the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	Channel   string `json:"channel"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
	Dirty     bool   `json:"dirty"`
}

// Get returns the build metadata of this binary.
func Get() Info { return build(version, commit, buildTime, channel) }

func build(v, c, t, ch string) Info {
	v = Normalize(v)
	if ch == "" {
		ch = ChannelFor(v)
	}
	return Info{
		Version: v, Commit: c, BuildTime: t, Channel: ch,
		GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Dirty: strings.HasSuffix(v, "-dirty"),
	}
}

// Normalize drops the "v" of a tag ("v2.0.0" → "2.0.0"); "" becomes "dev".
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "dev"
	}
	if len(v) > 1 && v[0] == 'v' && v[1] >= '0' && v[1] <= '9' {
		return v[1:]
	}
	return v
}

var (
	releaseTag = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)
	rcTag      = regexp.MustCompile(`^v?\d+\.\d+\.\d+-(rc|beta|alpha)[.\d]*$`)
)

// ChannelFor derives the channel from a version: release for a tagged build (v2.0.0), rc for a
// pre-release tag (v2.0.0-rc.1), dev for anything else (untagged, dirty or "dev").
func ChannelFor(v string) string {
	switch {
	case releaseTag.MatchString(v):
		return ChannelRelease
	case rcTag.MatchString(v):
		return ChannelRC
	default:
		return ChannelDev
	}
}

// ShortCommit is the first 7 characters of the commit.
func (i Info) ShortCommit() string {
	if len(i.Commit) > 7 {
		return i.Commit[:7]
	}
	return i.Commit
}

// Text is the plain-text block printed by `odb --version` and copied by the footer's
// "Copy build info" (the web client adds the browser line).
func (i Info) Text() string {
	built := i.BuildTime
	if built == "" {
		built = "unknown"
	}
	name := "Omni-Data-Bus " + i.Version
	if i.Dirty {
		name += " (dirty)"
	}
	return fmt.Sprintf("%s (%s)\nCommit: %s  Built: %s  Go: %s  Platform: %s\n",
		name, i.Channel, i.ShortCommit(), built, i.GoVersion, i.Platform)
}

// Override replaces the build variables until the returned restore func is called. For tests
// of packages that print build info; production builds set them with -ldflags.
func Override(v, c, t, ch string) (restore func()) {
	ov, oc, ot, och := version, commit, buildTime, channel
	version, commit, buildTime, channel = v, c, t, ch
	return func() { version, commit, buildTime, channel = ov, oc, ot, och }
}
