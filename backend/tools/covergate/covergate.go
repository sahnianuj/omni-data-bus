// Package covergate enforces per-package statement coverage thresholds from a Go cover profile.
package covergate

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Thresholds maps a package-path suffix (e.g. "internal/automap") to a minimum percentage.
// Packages without an explicit entry use Default.
type Thresholds struct {
	Default  float64
	Packages map[string]float64
	// Exclude lists package-path substrings that are not gated (generated code, main packages).
	Exclude []string
	// Only, when set, restricts gating to packages containing one of these substrings.
	Only []string
}

// IntegrationGated are packages whose coverage comes from integration tests
// (real Couchbase / PostgreSQL); the unit gate skips them and the integration gate checks them.
var IntegrationGated = []string{"internal/store/cbstore"}

// DefaultThresholds are the gates from the architecture testing strategy.
func DefaultThresholds() Thresholds {
	return Thresholds{
		Default: 80,
		Packages: map[string]float64{
			"internal/automap":   90,
			"internal/transform": 90,
			"internal/pipeline":  90,
		},
		Exclude: append([]string{"/cmd/", "/tools/", "/gen", "storetest", "/devseed"}, IntegrationGated...),
	}
}

// IntegrationThresholds gate only the integration-covered packages.
func IntegrationThresholds() Thresholds {
	return Thresholds{Default: 80, Only: IntegrationGated}
}

// Result is the coverage of one package.
type Result struct {
	Package   string
	Percent   float64
	Threshold float64
}

// Evaluate parses a cover profile and returns per-package results plus an error listing
// every package under its threshold.
func Evaluate(profile io.Reader, th Thresholds) ([]Result, error) {
	type agg struct{ total, covered int }
	pkgs := map[string]*agg{}
	sc := bufio.NewScanner(profile)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || strings.TrimSpace(line) == "" {
			continue
		}
		// file.go:12.2,14.3 3 1
		colon := strings.LastIndex(line, ":")
		fields := strings.Fields(line[colon+1:])
		if colon < 0 || len(fields) != 3 {
			return nil, fmt.Errorf("malformed cover line %q", line)
		}
		stmts, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("malformed cover line %q", line)
		}
		pkg := path.Dir(line[:colon])
		a := pkgs[pkg]
		if a == nil {
			a = &agg{}
			pkgs[pkg] = a
		}
		a.total += stmts
		if count > 0 {
			a.covered += stmts
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var results []Result
	var failures []string
	for pkg, a := range pkgs {
		if excluded(pkg, th.Exclude) || a.total == 0 || (len(th.Only) > 0 && !excluded(pkg, th.Only)) {
			continue
		}
		pct := 100 * float64(a.covered) / float64(a.total)
		want := threshold(pkg, th)
		results = append(results, Result{Package: pkg, Percent: pct, Threshold: want})
		if pct+1e-9 < want {
			failures = append(failures, fmt.Sprintf("%s %.0f%% < %.0f%%", pkg, pct, want))
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Package < results[j].Package })
	sort.Strings(failures)
	if len(failures) > 0 {
		return results, fmt.Errorf("coverage below threshold:\n  %s", strings.Join(failures, "\n  "))
	}
	return results, nil
}

func excluded(pkg string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(pkg+"/", p) {
			return true
		}
	}
	return false
}

func threshold(pkg string, th Thresholds) float64 {
	best, bestLen := th.Default, 0
	for suffix, v := range th.Packages {
		if strings.HasSuffix(pkg, suffix) && len(suffix) > bestLen {
			best, bestLen = v, len(suffix)
		}
	}
	return best
}
