package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"omnidatabus/internal/export"
	"omnidatabus/internal/headless"
)

// In a container (the README's one-line docker run, OC-183), two mistakes are easy: a directory
// target outside the mounted folder, whose files vanish with the container, and a database on
// "localhost", which is the container itself. odb says so instead of failing obscurely.

// inContainer reports whether odb runs in a container: Docker's /.dockerenv or Podman's
// /run/.containerenv (a variable for tests).
var inContainer = func() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// mountInfo is /proc/self/mountinfo (a variable for tests).
var mountInfo = func() ([]byte, error) { return os.ReadFile("/proc/self/mountinfo") }

// mounted reports whether path is under a mount other than the container's root filesystem,
// i.e. a folder that docker run -v shares with the host. Unknown (no mountinfo): true.
func mounted(path string) bool {
	raw, err := mountInfo()
	if err != nil {
		return true
	}
	best := ""
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mp := strings.ReplaceAll(f[4], `\040`, " ")
		if (path == mp || strings.HasPrefix(path, strings.TrimSuffix(mp, "/")+"/")) && len(mp) > len(best) {
			best = mp
		}
	}
	return best != "" && best != "/"
}

// unmountedTarget explains a directory target that isn't on a mounted folder, in a container;
// "" otherwise.
func unmountedTarget(configPath, targetFlag string) string {
	if !inContainer() {
		return ""
	}
	p := targetFlag
	if p == "" && configPath != "" {
		p, _ = headless.DirectoryTargetPath(configPath)
	}
	if p == "" {
		return ""
	}
	p = absPath(p)
	if mounted(p) {
		return ""
	}
	return fmt.Sprintf("the directory target %s isn't visible outside this container, so its files would be lost: add -v %s:%s to docker run, or use a target.path inside the export folder.",
		p, filepath.ToSlash(p), filepath.ToSlash(p))
}

// localhostHint is advice when, in a container, config.json points a connection at localhost.
func localhostHint(rc *export.RunConfig) string {
	if rc == nil || !inContainer() {
		return ""
	}
	for _, c := range []*export.RunConnection{rc.Source, rc.Target} {
		if c == nil {
			continue
		}
		for _, k := range []string{"host", "connectionString"} {
			var v string
			if json.Unmarshal(c.Config[k], &v) == nil && (strings.Contains(v, "localhost") || strings.Contains(v, "127.0.0.1")) {
				return "odb runs in a container here, where localhost is the container itself: add --network host to docker run (Linux), " +
					"or use host.docker.internal instead of localhost in config.json (Docker Desktop)."
			}
		}
	}
	return ""
}
