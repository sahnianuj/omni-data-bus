package app

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type spec struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Enum []string `yaml:"enum"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadSpec(t *testing.T) spec {
	t.Helper()
	raw, err := os.ReadFile("../../../api/openapi.yaml")
	require.NoError(t, err)
	var s spec
	require.NoError(t, yaml.Unmarshal(raw, &s))
	return s
}

// TestContract_SpecRoutesAreRegistered keeps api/openapi.yaml and the router in sync.
func TestContract_SpecRoutesAreRegistered(t *testing.T) {
	s := loadSpec(t)
	var want []string
	for path, ops := range s.Paths {
		for method := range ops {
			switch method {
			case "get", "put", "post", "patch", "delete", "head", "options":
				want = append(want, strings.ToUpper(method)+" "+path)
			}
		}
	}
	router := httpapi.NewRouter(httpapi.Options{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features: Features(newTestDeps(t)),
	})
	var got []string
	require.NoError(t, chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "" || route == "/*" { // the SPA
			return nil
		}
		// A trailing wildcard takes the rest of the path as one parameter (a document key).
		route = strings.TrimSuffix(route, "/*") + map[bool]string{true: "/{key}"}[strings.HasSuffix(route, "/*")]
		got = append(got, method+" "+route)
		return nil
	}))
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got, "api/openapi.yaml and the router must declare the same routes")
}

func TestContract_ErrorCodesMatchSpec(t *testing.T) {
	s := loadSpec(t)
	require.ElementsMatch(t, problem.All, s.Components.Schemas["ErrorCode"].Enum)
}

// newTestDeps builds in-memory dependencies for router-level tests.
func newTestDeps(t testing.TB) Deps {
	st := memstore.New(nil)
	svc, err := auth.NewService(st, auth.Argon2Hasher{Params: auth.Argon2Params{MemoryKiB: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}},
		store.SystemClock{}, auth.Settings{IdleTTL: time.Hour, AbsoluteTTL: 24 * time.Hour, SignupEnabled: true})
	require.NoError(t, err)
	sources, targets := Registries(nil)
	return Deps{Store: st, Auth: svc, PublicURL: "http://localhost:5173", Sources: sources, Targets: targets}
}
