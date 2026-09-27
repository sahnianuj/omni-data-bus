package config

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseDotEnv parses KEY=VALUE lines. Values are literal: no variable expansion or escapes,
// so passwords containing $ ` " # survive intact. Blank lines and lines starting with # are
// skipped, an optional leading "export " is accepted, and one pair of matching surrounding
// quotes is removed.
func ParseDotEnv(content string) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envKey.MatchString(key) {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out, sc.Err()
}

// LoadDotEnv reads the first existing file among paths and sets every variable that isn't
// already in the process environment. It returns the path it loaded ("" if none existed).
func LoadDotEnv(paths ...string) (string, error) {
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // operator-chosen config file path
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		vars, err := ParseDotEnv(string(raw))
		if err != nil {
			return "", fmt.Errorf("%s: %w", p, err)
		}
		for k, v := range vars {
			if _, set := os.LookupEnv(k); !set {
				if err := os.Setenv(k, v); err != nil {
					return "", err
				}
			}
		}
		return p, nil
	}
	return "", nil
}

// DotEnvCandidates are the files checked by default: OC_ENV_FILE if set, otherwise .env in the
// working directory and its parent (so both the repo root and backend/ work).
func DotEnvCandidates() []string {
	if p := os.Getenv("OC_ENV_FILE"); p != "" {
		return []string{p}
	}
	return []string{".env", "../.env"}
}
