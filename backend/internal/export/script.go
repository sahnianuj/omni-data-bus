package export

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/template"
	"time"

	"omnidatabus/internal/legal"
	"omnidatabus/internal/store"
)

// Script is the older export request's script settings (OC-053), still accepted: runtime
// "embedded" with an OS and architecture means "include odb" (OC-160). The other runtimes and
// IncludeBundle no longer change the download: the script finds odb itself, and mappings.json
// is always in it.
type Script struct {
	Runtime       string `json:"runtime"`
	OS            string `json:"os,omitempty"`
	Arch          string `json:"arch,omitempty"`
	IncludeBundle bool   `json:"includeBundle"`
}

// Platform is an operating system and architecture odb is released for.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// String is "os_arch", as in Platforms.
func (p Platform) String() string { return p.OS + "_" + p.Arch }

// Platforms odb is released for. darwin_universal is one signed, notarized binary for Intel and
// Apple silicon Macs (OC-185); the per-architecture darwin builds stay for older requests and
// servers whose dist has no universal binary. Windows gets odb.exe in a .zip (OC-186).
var Platforms = []string{"linux_amd64", "linux_arm64", "darwin_universal", "darwin_amd64", "darwin_arm64", "windows_amd64", "windows_arm64"}

// SignedFile lists, one archive name per line, the release archives whose odb is signed for its
// OS (Apple notarization, OC-185; Authenticode, OC-186). The release workflow writes it next to
// SHA256SUMS; a dist without it (make dist, a self-built server) is unsigned.
const SignedFile = "SIGNED"

// platformLabels name each platform for people: in the README and the export dialog.
var platformLabels = map[string]string{
	"linux_amd64": "Linux x86-64", "linux_arm64": "Linux ARM64",
	"darwin_amd64": "macOS (Intel)", "darwin_arm64": "macOS (Apple silicon)",
	"darwin_universal": "macOS (Intel or Apple silicon)", "windows_amd64": "Windows x86-64", "windows_arm64": "Windows ARM64",
}

// PlatformLabel is a platform's name for people ("Linux x86-64"); the platform itself if unknown.
func PlatformLabel(p string) string {
	if l, ok := platformLabels[p]; ok {
		return l
	}
	return p
}

// isWindows reports whether a platform is Windows (odb.exe, a .zip download).
func isWindows(p string) bool { return strings.HasPrefix(p, "windows_") }

// OdbPath is where an export holds the included odb for a platform (OC-182): bin/odb, or
// bin/odb.exe for Windows.
func OdbPath(p string) string {
	if isWindows(p) {
		return "bin/odb.exe"
	}
	return "bin/odb"
}

// Archive is the release download of odb for a platform: a .tar.gz, or a .zip for Windows.
func Archive(version, p string) string {
	if isWindows(p) {
		return "odb_" + version + "_" + p + ".zip"
	}
	return "odb_" + version + "_" + p + ".tar.gz"
}

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"sh":      shellQuote,
	"dq":      doubleQuoteSafe,
	"comment": func(s string) string { return strings.Join(strings.Fields(s), " ") },
}).ParseFS(templateFS, "templates/*.tmpl"))

// shellQuote single-quotes s for bash.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// doubleQuoteSafe escapes s for use inside double quotes.
func doubleQuoteSafe(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`", "\n", " ")
	return r.Replace(s)
}

// Dist is where the server finds released odb binaries and their checksums (OC_EXPORT_DIST_DIR):
// odb_<version>_<os>_<arch>.tar.gz files and SHA256SUMS, as `make dist` and the release build
// them. Without it, scripts can still embed the server's own binary for its own platform.
type Dist struct {
	Dir     string
	Version string
	// Fetch downloads, verifies and caches what Dir doesn't have (OC-188); nil = never download.
	Fetch *Fetcher
	// Self is the running binary's path (os.Executable by default).
	Self func() (string, error)
}

// Checksums reads SHA256SUMS: platform → sha256 of its release archive. It comes from the dist
// folder, else from the one built into the image (OC-188). Missing → none.
func (d Dist) Checksums() map[string]string {
	out := map[string]string{}
	byName := readSums(d.sumsDir())
	for _, p := range Platforms {
		if sum := byName[Archive(d.Version, p)]; sum != "" {
			out[p] = sum
		}
	}
	return out
}

// selfPlatform is the platform this server runs on.
func selfPlatform() string { return runtime.GOOS + "_" + runtime.GOARCH }

// Signed lists the platforms whose release archive is signed for its OS (SignedFile).
func (d Dist) Signed() []string {
	var out []string
	if d.Dir == "" {
		return out
	}
	raw, err := os.ReadFile(filepath.Join(d.sumsDir(), SignedFile)) //nolint:gosec // G304: the configured dist or image folder
	if err != nil {
		return out
	}
	names := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		names[strings.TrimSpace(l)] = true
	}
	for _, p := range Platforms {
		if names[Archive(d.Version, p)] {
			out = append(out, p)
		}
	}
	return out
}

// Preferred is the platform to include for a request: an older request for one Mac architecture
// gets the universal binary when the dist has it (OC-185).
func (d Dist) Preferred(p string) string {
	if strings.HasPrefix(p, "darwin_") && p != "darwin_universal" && slices.Contains(d.Available(), "darwin_universal") {
		return "darwin_universal"
	}
	return p
}

// Binary returns the odb binary for a platform: from the dist folder, else from the release
// (cached, or downloaded and verified on first use, OC-188), else the running binary when it is
// that platform.
func (d Dist) Binary(ctx context.Context, platform string) ([]byte, error) {
	var raw []byte
	if d.Dir != "" {
		if b, err := binaryFromArchive(filepath.Join(d.Dir, Archive(d.Version, platform))); err == nil {
			raw = b
		}
	}
	if raw == nil && platform != selfPlatform() {
		p, ok, err := d.fromRelease(ctx, platform)
		if err != nil {
			return nil, err
		}
		if ok {
			if raw, err = binaryFromArchive(p); err != nil {
				return nil, err
			}
		}
	}
	if raw == nil && platform == selfPlatform() {
		self := d.Self
		if self == nil {
			self = os.Executable
		}
		p, err := self()
		if err != nil {
			return nil, err
		}
		if raw, err = os.ReadFile(p); err != nil { //nolint:gosec // G304: the server's own executable
			return nil, err
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("no odb binary for %s on this server (set OC_EXPORT_DIST_DIR to the release binaries)", platform)
	}
	return raw, nil
}

// Available lists the platforms an export can add odb for: now, or downloaded on first use.
func (d Dist) Available() []string { return availableOrOnDemand(d.Machines()) }

// binaryFromArchive reads odb from a release .tar.gz, or odb.exe from a Windows .zip.
func binaryFromArchive(path string) ([]byte, error) {
	if strings.HasSuffix(path, ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		for _, f := range zr.File {
			if f.Name == "odb.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer func() { _ = rc.Close() }()
				return io.ReadAll(io.LimitReader(rc, 200<<20))
			}
		}
		return nil, fmt.Errorf("%s has no odb.exe", path)
	}
	return binaryFromTarball(path)
}

func binaryFromTarball(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the configured dist folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, err
		}
		if h.Name == "odb" {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// ScriptConfig is the server's script settings (OC_EXPORT_*).
type ScriptConfig struct {
	Image          string // the image the docker runtime runs, ideally pinned by digest
	DownloadURL    string // where odb_<ver>_<os>_<arch>.tar.gz are downloaded from
	CosignIdentity string // the release workflow identity cosign verifies (regexp)
	Dist           Dist
	// Build is the build of this odb release ("7f6cea2+418"), named in the script and README
	// headers so an export says exactly what produced it (OC-172); "" = the release only.
	Build string
}

type scriptMapping struct{ Name, Source, Target string }

// scriptData is all migrate.sh holds: the odb release it runs (OC-159). Nothing about the
// export, so every export from one release has the same script.
type scriptData struct {
	Version, Build, Image, DownloadURL, CosignIdentity string
	Checksums                                          map[string]string
}

// readmeData describes one export in its README.md.
type readmeData struct {
	Project, ExportedAt, ExportedBy, Version, Build, BundleSHA256 string
	Mappings                                                      []scriptMapping
	SourceSummary, TargetSummary, TargetKind, TargetPath          string
	TargetHasCredentials                                          bool
	Image, DownloadURL                                            string
	// OdbPlatform is the platform of the included odb ("" = none, OC-182); OdbBin is its path
	// in the folder and OdbPlatformLabel its name for people.
	OdbPlatform, OdbBin, OdbBinWindows, OdbPlatformLabel string
	OdbIsWindows                                         bool
	// OdbUnsigned: the included odb isn't signed for its OS (a development build), so macOS
	// Gatekeeper or Windows SmartScreen will stop it the first time (OC-185, OC-186).
	OdbUnsignedMac, OdbUnsignedWindows bool
	// Needs is the odb releases that run this export, in words.
	Needs string
	// Downloads are the release archives the README links to, one per platform with a checksum.
	Downloads  []download
	HasWindows bool
	// Folder is the zip's top-level directory: everything unzips (and runs) inside it.
	Folder string
}

// download is one row of the README's Install odb table.
type download struct{ Label, File, URL, SHA256 string }

// Artifact is a generated download.
type Artifact struct {
	FileName    string
	ContentType string
	Body        []byte
	// IncludedOdb is the platform of the odb in the export ("" = none).
	IncludedOdb string
}

// MaxArtifactBytes caps a generated download (the included odb is the largest part).
const MaxArtifactBytes = 80 << 20

// MappingsFile is the export's mappings: the bundle, without connection details (OC-158).
const MappingsFile = "mappings.json"

// RenderScript builds the export (OC-160, OC-182): one folder, named like the zip, with
// README.md, config.json (0600) and mappings.json, run by the odb installed on any OS. When
// include names a platform, odb for it is added as bin/odb (bin/odb.exe), 0755, for a machine
// without internet access. There is no script: odb finds the files itself (OC-180).
func RenderScript(ctx context.Context, b Bundle, mappingsJSON, runConfig []byte, include string, cfg ScriptConfig, version string) (Artifact, error) {
	readme, err := RenderReadme(b, runConfig, include, cfg, version)
	if err != nil {
		return Artifact{}, err
	}
	folder := exportFolder(b)
	dir := folder + "/"
	files := []zipFile{{dir, os.ModeDir | 0o755, nil}, {dir + "README.md", 0o644, readme}}
	if runConfig != nil {
		files = append(files, zipFile{dir + RunConfigFile, 0o600, runConfig})
	}
	files = append(files, zipFile{dir + MappingsFile, 0o644, mappingsJSON})
	if include != "" {
		bin, err := cfg.Dist.Binary(ctx, include)
		if err != nil {
			return Artifact{}, err
		}
		files = append(files, zipFile{dir + "bin/", os.ModeDir | 0o755, nil}, zipFile{dir + OdbPath(include), 0o755, bin})
		// A binary handed on carries its licence and its dependencies' notices (OC-189).
		files = append(files, zipFile{dir + "bin/LICENSES/", os.ModeDir | 0o755, nil})
		for _, f := range legal.Files() {
			files = append(files, zipFile{dir + "bin/LICENSES/" + f.Name, 0o644, f.Data})
		}
	}
	art, err := zipArtifact(folder, files, b.CreatedAt)
	art.IncludedOdb = include
	return art, err
}

func zipArtifact(folder string, files []zipFile, at time.Time) (Artifact, error) {
	body, err := zipFiles(files, at)
	if err != nil {
		return Artifact{}, err
	}
	if len(body) > MaxArtifactBytes {
		return Artifact{}, fmt.Errorf("the export is larger than %d MB", MaxArtifactBytes>>20)
	}
	return Artifact{FileName: folder + ".zip", ContentType: "application/zip", Body: body}, nil
}

// exportFolder names the zip and its top-level folder: odb-export-<project>-<yyyymmdd>.
func exportFolder(b Bundle) string {
	return "odb-export-" + slug(b.Project.Name) + "-" + b.CreatedAt.Format("20060102")
}

// GenericScript is migrate.sh for this odb release (OC-159). Exports no longer include it
// (OC-182); it stays until 3.0 for the test that runs a 2.0 script against this odb.
func GenericScript(cfg ScriptConfig, version string) ([]byte, error) {
	d := scriptData{
		Version: version, Build: cfg.Build, Image: cfg.Image, DownloadURL: strings.TrimRight(cfg.DownloadURL, "/"), CosignIdentity: cfg.CosignIdentity,
		Checksums: cfg.Dist.Checksums(),
	}
	for _, p := range Platforms {
		if _, ok := d.Checksums[p]; !ok {
			d.Checksums[p] = ""
		}
	}
	var out bytes.Buffer
	if err := templates.ExecuteTemplate(&out, "migrate.sh.tmpl", d); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// RenderReadme is the export's README.md: what this export migrates, from where to where, and
// how to run it. Connection details come from config.json when the bundle has none (OC-158).
func RenderReadme(b Bundle, runConfig []byte, include string, cfg ScriptConfig, version string) ([]byte, error) {
	src, tgt := b.Source, b.Target
	if runConfig != nil {
		rc, _, err := ParseRunConfig(runConfig)
		if err != nil {
			return nil, err
		}
		if rc.Source != nil {
			src = rc.Source.connection(src)
		}
		if rc.Target != nil {
			tgt = rc.Target.connection(tgt)
		}
	}
	d := readmeData{
		Project: b.Project.Name, ExportedAt: b.CreatedAt.UTC().Format(time.RFC3339), ExportedBy: b.CreatedBy, Version: version, Build: cfg.Build,
		BundleSHA256: b.SHA256(), SourceSummary: connSummary(src), TargetSummary: connSummary(tgt), TargetKind: b.Target.Type,
		TargetHasCredentials: b.Target.Credentials != nil, Image: cfg.Image, DownloadURL: strings.TrimRight(cfg.DownloadURL, "/"),
		OdbPlatform: include, Folder: exportFolder(b), Needs: needs(version),
	}
	if include != "" {
		d.OdbBin, d.OdbPlatformLabel, d.OdbIsWindows = OdbPath(include), PlatformLabel(include), isWindows(include)
		d.OdbBinWindows = strings.ReplaceAll(d.OdbBin, "/", `\`)
		unsigned := !slices.Contains(cfg.Dist.Signed(), include)
		d.OdbUnsignedMac = unsigned && strings.HasPrefix(include, "darwin_")
		d.OdbUnsignedWindows = unsigned && d.OdbIsWindows
	}
	d.Downloads, d.HasWindows = downloads(cfg, version)
	for _, m := range b.Mappings {
		d.Mappings = append(d.Mappings, scriptMapping{Name: m.Name, Source: m.Source.String(), Target: m.Location.String()})
	}
	if b.Target.Type == store.KindDirectory {
		var cfgDir struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(tgt.Config, &cfgDir)
		d.TargetPath = cfgDir.Path
	}
	var out bytes.Buffer
	if err := templates.ExecuteTemplate(&out, "README.md.tmpl", d); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// needs says which odb releases run an export of version, as its odbVersion range does.
func needs(version string) string {
	v, ok := parseSemver(version)
	if !ok {
		return "odb " + version
	}
	return fmt.Sprintf("odb %d.%d.0 or a later %d.x release", v[0], v[1], v[0])
}

// downloads are the release archives with a known checksum, in Platforms order. Without a
// download URL there are none: the README then says to ask for odb or include it.
func downloads(cfg ScriptConfig, version string) ([]download, bool) {
	base := strings.TrimRight(cfg.DownloadURL, "/")
	if base == "" {
		return nil, false
	}
	sums := cfg.Dist.Checksums()
	var out []download
	win := false
	universal := sums["darwin_universal"] != ""
	for _, p := range Platforms {
		sum, ok := sums[p]
		if !ok || sum == "" || universal && strings.HasPrefix(p, "darwin_") && p != "darwin_universal" {
			continue // one download for every Mac when there's a universal one (OC-185)
		}
		f := Archive(version, p)
		out = append(out, download{Label: PlatformLabel(p), File: f, URL: base + "/" + f, SHA256: sum})
		win = win || isWindows(p)
	}
	return out, win
}

func connSummary(c Connection) string {
	var cfg map[string]any
	_ = json.Unmarshal(c.Config, &cfg)
	get := func(k string) string {
		if v, ok := cfg[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	switch c.Type {
	case "postgres":
		return fmt.Sprintf("PostgreSQL `%s@%s:%s/%s`", get("username"), get("host"), get("port"), get("database"))
	case store.KindCouchbase:
		return fmt.Sprintf("Couchbase `%s` as `%s`", get("connectionString"), get("username"))
	case store.KindDirectory:
		return fmt.Sprintf("JSON files in `%s` (layout %s)", get("path"), get("layout"))
	}
	return c.Type
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "project"
	}
	return out
}

type zipFile struct {
	name string
	mode os.FileMode
	data []byte
}

func zipFiles(files []zipFile, at time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: at}
		if f.mode.IsDir() {
			h.Method = zip.Store
		}
		h.SetMode(f.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
