package export

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

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

// Platforms odb is released for.
var Platforms = []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}

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
	// Self is the running binary's path (os.Executable by default).
	Self func() (string, error)
}

// Checksums reads SHA256SUMS: platform → sha256 of the tarball. Missing → none.
func (d Dist) Checksums() map[string]string {
	out := map[string]string{}
	if d.Dir == "" {
		return out
	}
	f, err := os.Open(filepath.Join(d.Dir, "SHA256SUMS"))
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		if !ok {
			continue
		}
		for _, p := range Platforms {
			if name == "odb_"+d.Version+"_"+p+".tar.gz" {
				out[p] = sum
			}
		}
	}
	return out
}

// Binary returns the odb binary for a platform: from the dist tarball, or the running binary
// when it is that platform.
func (d Dist) Binary(platform string) ([]byte, error) {
	var raw []byte
	if d.Dir != "" {
		if b, err := binaryFromTarball(filepath.Join(d.Dir, "odb_"+d.Version+"_"+platform+".tar.gz")); err == nil {
			raw = b
		}
	}
	if raw == nil && platform == runtime.GOOS+"_"+runtime.GOARCH {
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

// Available lists the platforms an embedded binary is available for.
func (d Dist) Available() []string {
	var out []string
	for _, p := range Platforms {
		if d.Dir != "" {
			if _, err := os.Stat(filepath.Join(d.Dir, "odb_"+d.Version+"_"+p+".tar.gz")); err == nil {
				out = append(out, p)
				continue
			}
		}
		if p == runtime.GOOS+"_"+runtime.GOARCH {
			out = append(out, p)
		}
	}
	return out
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
	// EmbedWhenNoDownload includes odb in every export when no platform can be downloaded (no
	// release checksums, e.g. a self-built server), so the script still runs as is.
	EmbedWhenNoDownload bool
}

// fallbackPlatform is the platform an export includes odb for when nothing can be downloaded:
// this server's own if it has that binary, else the first it has. Empty: none.
func (c ScriptConfig) fallbackPlatform(checksums map[string]string) string {
	if !c.EmbedWhenNoDownload {
		return ""
	}
	for _, sum := range checksums {
		if sum != "" {
			return ""
		}
	}
	avail := c.Dist.Available()
	for _, p := range avail {
		if p == runtime.GOOS+"_"+runtime.GOARCH {
			return p
		}
	}
	if len(avail) > 0 {
		return avail[0]
	}
	return ""
}

type scriptMapping struct{ Name, Source, Target string }

// scriptData is all migrate.sh holds: the odb release it runs (OC-159). Nothing about the
// export, so every export from one release has the same script.
type scriptData struct {
	Version, Image, DownloadURL, CosignIdentity string
	Checksums                                   map[string]string
}

// readmeData describes one export in its README.md.
type readmeData struct {
	Project, ExportedAt, ExportedBy, Version, BundleSHA256 string
	Mappings                                               []scriptMapping
	SourceSummary, TargetSummary, TargetKind, TargetPath   string
	TargetHasCredentials                                   bool
	Image, DownloadURL                                     string
	// OdbPlatform is the platform of the odb included beside the script ("" = none).
	OdbPlatform string
	// Folder is the zip's top-level directory: everything unzips (and runs) inside it.
	Folder string
}

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

// RenderScript builds the export (OC-160): one folder, named like the zip, with migrate.sh
// (0755), README.md, config.json (0600), mappings.json and, when include names a platform, odb
// (0755) for it. With no platform, odb is still included when nothing can be downloaded.
func RenderScript(b Bundle, mappingsJSON, runConfig []byte, include string, cfg ScriptConfig, version string) (Artifact, error) {
	if include == "" {
		include = cfg.fallbackPlatform(cfg.Dist.Checksums())
	}
	script, err := GenericScript(cfg, version)
	if err != nil {
		return Artifact{}, err
	}
	readme, err := RenderReadme(b, runConfig, include, cfg, version)
	if err != nil {
		return Artifact{}, err
	}
	folder := exportFolder(b)
	dir := folder + "/"
	files := []zipFile{{dir, os.ModeDir | 0o755, nil}, {dir + "migrate.sh", 0o755, script}, {dir + "README.md", 0o644, readme}}
	if runConfig != nil {
		files = append(files, zipFile{dir + RunConfigFile, 0o600, runConfig})
	}
	files = append(files, zipFile{dir + MappingsFile, 0o644, mappingsJSON})
	if include != "" {
		bin, err := cfg.Dist.Binary(include)
		if err != nil {
			return Artifact{}, err
		}
		files = append(files, zipFile{dir + "odb", 0o755, bin})
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

// GenericScript is migrate.sh for this odb release: the same for every export (OC-159).
func GenericScript(cfg ScriptConfig, version string) ([]byte, error) {
	d := scriptData{
		Version: version, Image: cfg.Image, DownloadURL: strings.TrimRight(cfg.DownloadURL, "/"), CosignIdentity: cfg.CosignIdentity,
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
		Project: b.Project.Name, ExportedAt: b.CreatedAt.UTC().Format(time.RFC3339), ExportedBy: b.CreatedBy, Version: version,
		BundleSHA256: b.SHA256(), SourceSummary: connSummary(src), TargetSummary: connSummary(tgt), TargetKind: b.Target.Type,
		TargetHasCredentials: b.Target.Credentials != nil, Image: cfg.Image, DownloadURL: strings.TrimRight(cfg.DownloadURL, "/"),
		OdbPlatform: include, Folder: exportFolder(b),
	}
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
