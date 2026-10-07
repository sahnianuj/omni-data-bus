package export

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"
	"time"

	"omnidatabus/internal/store"
)

// Script settings for runtime "script" (OC-053).
type Script struct {
	// Runtime is how migrate.sh runs odb: auto (default), binary, docker or embedded.
	Runtime string `json:"runtime"`
	// OS and Arch choose the embedded binary (runtime embedded only).
	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
	// IncludeBundle adds bundle.odb.json next to migrate.sh in the zip.
	IncludeBundle bool `json:"includeBundle"`
}

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

// Binary returns the gzip'd odb binary for a platform: from the dist tarball, or the running
// binary when it is that platform.
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
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	// EmbedWhenNoDownload makes an "auto" script carry odb when no platform can be downloaded
	// (no release checksums, e.g. a self-built server), so the script still runs as is.
	EmbedWhenNoDownload bool
}

// fallbackPlatform is the platform an "auto" script embeds when nothing can be downloaded:
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

type scriptData struct {
	Project, ExportedAt, ExportedBy, Version, BundleSHA256 string
	MappingList, CredentialSummary                         string
	Plaintext, Sealed, TargetHasCredentials                bool
	Image, DownloadURL, CosignIdentity                     string
	Checksums                                              map[string]string
	EmbeddedPlatform                                       string
	TargetKind, TargetPath, Runtime                        string
	EnvRefs                                                []string
	Mappings                                               []scriptMapping
	SourceSummary, TargetSummary                           string
	Refs, EnvExample                                       []string
	BundleBase64, BinaryBase64                             string
	// Folder is the zip's top-level directory: everything unzips (and runs) inside it.
	Folder string
}

// Artifact is a generated download.
type Artifact struct {
	FileName    string
	ContentType string
	Body        []byte
}

// MaxArtifactBytes caps a generated download (the embedded binary is the largest part).
const MaxArtifactBytes = 50 << 20

// RenderScript builds the export zip: migrate.sh (0755), README.md and optionally the bundle,
// all inside one folder named like the zip, so unzipping never scatters files.
func RenderScript(b Bundle, bundleJSON []byte, s Script, cfg ScriptConfig, version string) (Artifact, error) {
	d, err := scriptDataFor(b, bundleJSON, s, cfg, version)
	if err != nil {
		return Artifact{}, err
	}
	var script, readme bytes.Buffer
	if err := templates.ExecuteTemplate(&script, "migrate.sh.tmpl", d); err != nil {
		return Artifact{}, err
	}
	if err := templates.ExecuteTemplate(&readme, "README.md.tmpl", d); err != nil {
		return Artifact{}, err
	}
	dir := d.Folder + "/"
	files := []zipFile{{dir, os.ModeDir | 0o755, nil}, {dir + "migrate.sh", 0o755, script.Bytes()}, {dir + "README.md", 0o644, readme.Bytes()}}
	if s.IncludeBundle {
		files = append(files, zipFile{dir + "bundle.odb.json", 0o600, bundleJSON})
	}
	body, err := zipFiles(files, b.CreatedAt)
	if err != nil {
		return Artifact{}, err
	}
	if len(body) > MaxArtifactBytes {
		return Artifact{}, errors.New("the export is larger than 50 MB")
	}
	return Artifact{FileName: d.Folder + ".zip", ContentType: "application/zip", Body: body}, nil
}

// exportFolder names the zip and its top-level folder: odb-export-<project>-<yyyymmdd>.
func exportFolder(b Bundle) string {
	return "odb-export-" + slug(b.Project.Name) + "-" + b.CreatedAt.Format("20060102")
}

// RenderScriptFiles returns migrate.sh and README.md alone (tests, previews).
func RenderScriptFiles(b Bundle, bundleJSON []byte, s Script, cfg ScriptConfig, version string) (script, readme []byte, err error) {
	d, err := scriptDataFor(b, bundleJSON, s, cfg, version)
	if err != nil {
		return nil, nil, err
	}
	var sb, rb bytes.Buffer
	if err := templates.ExecuteTemplate(&sb, "migrate.sh.tmpl", d); err != nil {
		return nil, nil, err
	}
	if err := templates.ExecuteTemplate(&rb, "README.md.tmpl", d); err != nil {
		return nil, nil, err
	}
	return sb.Bytes(), rb.Bytes(), nil
}

func scriptDataFor(b Bundle, bundleJSON []byte, s Script, cfg ScriptConfig, version string) (scriptData, error) {
	runtimeName := s.Runtime
	if runtimeName == "" {
		runtimeName = "auto"
	}
	d := scriptData{
		Project: b.Project.Name, ExportedAt: b.CreatedAt.UTC().Format(time.RFC3339), ExportedBy: b.CreatedBy, Version: version,
		BundleSHA256: b.SHA256(), Image: cfg.Image, DownloadURL: strings.TrimRight(cfg.DownloadURL, "/"), CosignIdentity: cfg.CosignIdentity,
		Checksums: cfg.Dist.Checksums(), TargetKind: b.Target.Type, Runtime: runtimeName,
		SourceSummary: connSummary(b.Source), TargetSummary: connSummary(b.Target),
		BundleBase64: wrap(base64.StdEncoding.EncodeToString(bundleJSON), 76),
		Folder:       exportFolder(b),
	}
	for _, p := range Platforms {
		if _, ok := d.Checksums[p]; !ok {
			d.Checksums[p] = ""
		}
	}
	var names []string
	for _, m := range b.Mappings {
		names = append(names, m.Name+" (→ "+m.Location.String()+")")
		d.Mappings = append(d.Mappings, scriptMapping{Name: m.Name, Source: m.Source.String(), Target: m.Location.String()})
	}
	d.MappingList = strings.Join(names, ", ")
	if b.Target.Type == store.KindDirectory {
		var cfgDir struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(b.Target.Config, &cfgDir)
		d.TargetPath = cfgDir.Path
	}
	mode := ModeSealed
	for _, c := range []*Credentials{b.Source.Credentials, b.Target.Credentials} {
		if c != nil {
			mode = c.Mode
		}
	}
	d.TargetHasCredentials = b.Target.Credentials != nil
	switch mode {
	case ModeSealed:
		d.CredentialSummary = "sealed (passphrase required)"
		d.Sealed = true
	case ModePlaintext:
		d.CredentialSummary = "plaintext (no passphrase)"
		d.Plaintext = true
	case ModeExternal:
		d.CredentialSummary = "external (read from the environment or files at run time)"
		envs := map[string]bool{}
		for role, c := range map[string]*Credentials{"source": b.Source.Credentials, "target": b.Target.Credentials} {
			if c == nil {
				continue
			}
			for k, ref := range c.Refs {
				d.Refs = append(d.Refs, fmt.Sprintf("%s %s from `%s`", role, k, ref))
				if name, ok := strings.CutPrefix(ref, "env:"); ok {
					envs[name] = true
				}
			}
		}
		sort.Strings(d.Refs)
		for name := range envs {
			d.EnvRefs = append(d.EnvRefs, name)
		}
		sort.Strings(d.EnvRefs)
		for _, name := range d.EnvRefs {
			d.EnvExample = append(d.EnvExample, name+"='…'")
		}
	}
	// An OS and architecture embed odb (air-gapped sites); "auto" then tries it first, and
	// falls back on another machine.
	if runtimeName == "embedded" && (s.OS == "" || s.Arch == "") {
		return d, errors.New("choose the operating system and architecture of the machine that runs the script")
	}
	if runtimeName == "auto" && (s.OS == "" || s.Arch == "") {
		if p := cfg.fallbackPlatform(d.Checksums); p != "" {
			s.OS, s.Arch, _ = strings.Cut(p, "_")
		}
	}
	if s.OS != "" && s.Arch != "" {
		p := s.OS + "_" + s.Arch
		bin, err := cfg.Dist.Binary(p)
		if err != nil {
			return d, err
		}
		d.EmbeddedPlatform = p
		d.BinaryBase64 = wrap(base64.StdEncoding.EncodeToString(bin), 76)
		if runtimeName == "embedded" {
			d.Runtime = "auto"
		}
	}
	return d, nil
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

func wrap(s string, n int) string {
	var b strings.Builder
	for len(s) > n {
		b.WriteString(s[:n])
		b.WriteByte('\n')
		s = s[n:]
	}
	b.WriteString(s)
	return b.String()
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
