package export

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omnidatabus/internal/problem"
)

// Fetcher gets a machine's odb from the release the first time an export asks for it (OC-188):
// the archive is downloaded from BaseURL, checked against the SHA-256 the server image was built
// with (SumsDir/SHA256SUMS, from the same release), and cached in CacheDir. A server with a dist
// folder (OC_EXPORT_DIST_DIR) uses that first and never downloads what it has.
type Fetcher struct {
	BaseURL  string // where odb_<version>_<platform>.tar.gz (.zip) are, e.g. the GitHub release
	SumsDir  string // SHA256SUMS and SIGNED built into the image (/usr/local/share/odb)
	CacheDir string // verified downloads
	Client   *http.Client
	Timeout  time.Duration // one download (default 2 minutes)
	// RetryAfter is how long a failed download marks its machine unavailable (default 10 minutes).
	RetryAfter time.Duration
	Now        func() time.Time

	mu     sync.Mutex
	locks  map[string]*sync.Mutex // one download at a time per archive
	failed map[string]failure
}

type failure struct {
	at     time.Time
	reason string
}

// Fetch error codes (problem codes on the export API).
const (
	CodeDownloadInvalid = problem.CodeOdbDownloadInvalid // the download's SHA-256 isn't the release's
	CodeDownloadFailed  = problem.CodeOdbDownloadFailed  // no route, an HTTP error, a timeout
)

// FetchError is a failed download of a machine's odb.
type FetchError struct {
	Code, Platform, URL, Message string
}

func (e *FetchError) Error() string { return e.Message }

// Machine is one platform an export can add odb for, and whether this server can (OC-188).
type Machine struct {
	Platform string `json:"platform"`
	// State: "available" (in the dist folder, cached, or this server's own), "onDemand"
	// (downloaded and verified on first use) or "unavailable" (Reason says why).
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// Machine states.
const (
	MachineAvailable   = "available"
	MachineOnDemand    = "onDemand"
	MachineUnavailable = "unavailable"
)

func (f *Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Fetcher) retryAfter() time.Duration {
	if f.RetryAfter > 0 {
		return f.RetryAfter
	}
	return 10 * time.Minute
}

// enabled reports whether this fetcher can download at all.
func (f *Fetcher) enabled() bool { return f != nil && f.BaseURL != "" && f.CacheDir != "" }

// lastFailure is a recent failed download of archive, if any.
func (f *Fetcher) lastFailure(archive string) (failure, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.failed[archive]
	if ok && f.now().Sub(fl.at) >= f.retryAfter() {
		delete(f.failed, archive)
		return failure{}, false
	}
	return fl, ok
}

func (f *Fetcher) fail(archive, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed == nil {
		f.failed = map[string]failure{}
	}
	f.failed[archive] = failure{at: f.now(), reason: reason}
}

func (f *Fetcher) lock(archive string) *sync.Mutex {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locks == nil {
		f.locks = map[string]*sync.Mutex{}
	}
	if f.locks[archive] == nil {
		f.locks[archive] = &sync.Mutex{}
	}
	return f.locks[archive]
}

// cached is the verified archive in the cache, or "" (a stale or tampered file is removed).
func (f *Fetcher) cached(archive, want string) string {
	p := filepath.Join(f.CacheDir, archive)
	if got, err := fileSHA256(p); err == nil {
		if got == want {
			return p
		}
		_ = os.Remove(p)
	}
	return ""
}

// fetch downloads archive into the cache, verified against want, and returns its path.
func (f *Fetcher) fetch(ctx context.Context, platform, archive, want string) (string, error) {
	l := f.lock(archive)
	l.Lock()
	defer l.Unlock()
	if p := f.cached(archive, want); p != "" {
		return p, nil // another export fetched it meanwhile
	}
	url := strings.TrimRight(f.BaseURL, "/") + "/" + archive
	fail := func(code, msg string) (string, error) {
		f.fail(archive, msg)
		return "", &FetchError{Code: code, Platform: platform, URL: url, Message: msg}
	}
	if err := os.MkdirAll(f.CacheDir, 0o750); err != nil {
		return fail(CodeDownloadFailed, fmt.Sprintf("Can't cache odb for %s: %v", PlatformLabel(platform), err))
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fail(CodeDownloadFailed, fmt.Sprintf("Can't download odb for %s from %s: %v", PlatformLabel(platform), url, err))
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req) //nolint:gosec // G107: the configured release URL
	if err != nil {
		return fail(CodeDownloadFailed, fmt.Sprintf("Can't download odb for %s from %s: %v", PlatformLabel(platform), url, err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fail(CodeDownloadFailed, fmt.Sprintf("Can't download odb for %s: %s answered %s", PlatformLabel(platform), url, resp.Status))
	}
	tmp, err := os.CreateTemp(f.CacheDir, "."+archive+".*")
	if err != nil {
		return fail(CodeDownloadFailed, fmt.Sprintf("Can't cache odb for %s: %v", PlatformLabel(platform), err))
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 200<<20))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fail(CodeDownloadFailed, fmt.Sprintf("Can't download odb for %s from %s: %v", PlatformLabel(platform), url, err))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fail(CodeDownloadInvalid, fmt.Sprintf("The odb download for %s from %s doesn't match its checksum (got %s, expected %s): it was discarded.",
			PlatformLabel(platform), url, short(got), short(want)))
	}
	dst := filepath.Join(f.CacheDir, archive)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fail(CodeDownloadFailed, fmt.Sprintf("Can't cache odb for %s: %v", PlatformLabel(platform), err))
	}
	return dst, nil
}

// sumsDir is the folder whose SHA256SUMS and SIGNED describe this release's archives: the dist
// folder when it has them, else the ones built into the image.
func (d Dist) sumsDir() string {
	if d.Dir != "" {
		if _, err := os.Stat(filepath.Join(d.Dir, "SHA256SUMS")); err == nil {
			return d.Dir
		}
	}
	if d.Fetch != nil && d.Fetch.SumsDir != "" {
		return d.Fetch.SumsDir
	}
	return d.Dir
}

// readSums reads a SHA256SUMS file: archive name → sha256.
func readSums(dir string) map[string]string {
	out := map[string]string{}
	if dir == "" {
		return out
	}
	f, err := os.Open(filepath.Join(dir, "SHA256SUMS")) //nolint:gosec // G304: the configured dist or image folder
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  "); ok {
			out[name] = sum
		}
	}
	return out
}

// local reports whether the dist folder holds platform's archive.
func (d Dist) local(p string) bool {
	if d.Dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(d.Dir, Archive(d.Version, p)))
	return err == nil
}

// Machines is every platform with whether this server can add its odb, and why not (OC-188).
func (d Dist) Machines() []Machine {
	sums := d.Checksums()
	self := selfPlatform()
	out := make([]Machine, 0, len(Platforms))
	for _, p := range Platforms {
		m := Machine{Platform: p, State: MachineAvailable}
		archive := Archive(d.Version, p)
		switch {
		case d.local(p):
		case d.Fetch != nil && d.Fetch.CacheDir != "" && sums[p] != "" && d.Fetch.cached(archive, sums[p]) != "":
		case p == self:
		case !d.Fetch.enabled():
			m.State, m.Reason = MachineUnavailable, "This server doesn't download odb (no download URL) and its dist folder has no odb for this machine. An admin can set OC_EXPORT_DIST_DIR to the release's odb files."
		case sums[p] == "":
			m.State, m.Reason = MachineUnavailable, "This build doesn't know the release's checksum for this machine (a development build), so it can't download it safely. An admin can set OC_EXPORT_DIST_DIR to the release's odb files (make dist)."
		default:
			if fl, failed := d.Fetch.lastFailure(archive); failed {
				m.State, m.Reason = MachineUnavailable, fl.reason+" Trying again after "+fl.at.Add(d.Fetch.retryAfter()).Format("15:04")+"."
			} else {
				m.State = MachineOnDemand
			}
		}
		out = append(out, m)
	}
	return out
}

// fromRelease returns platform's archive: the verified cache, else a verified download. ok is
// false when this server can't download it (no fetcher, no URL or no checksum).
func (d Dist) fromRelease(ctx context.Context, p string) (path string, ok bool, err error) {
	if !d.Fetch.enabled() {
		return "", false, nil
	}
	want := d.Checksums()[p]
	if want == "" {
		return "", false, nil
	}
	archive := Archive(d.Version, p)
	if c := d.Fetch.cached(archive, want); c != "" {
		return c, true, nil
	}
	path, err = d.Fetch.fetch(ctx, p, archive, want)
	return path, true, err
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p) //nolint:gosec // G304: the cache folder
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// availableOrOnDemand lists the platforms an export can add odb for now or on first use.
func availableOrOnDemand(ms []Machine) []string {
	var out []string
	for _, m := range ms {
		if m.State != MachineUnavailable {
			out = append(out, m.Platform)
		}
	}
	return out
}

// isFetchError reports a FetchError in err's chain.
func isFetchError(err error) (*FetchError, bool) {
	var fe *FetchError
	ok := errors.As(err, &fe)
	return fe, ok
}
