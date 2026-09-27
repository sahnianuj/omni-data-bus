package audit

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

// sources returns the non-test Go sources of every other internal package.
func sources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "audit" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = string(b)
		}
		return nil
	})
	require.NoError(t, err)
	return out
}

// recordedAction finds actions passed to the recording helpers: Action: "…", s.audit(…, "…"),
// s.save(…, "…") (mapping saves audit their action), h.record(r, "…"), h.control("…"), .Record(ctx, ws, user,
// "…", …) and action := "…".
var recordedAction = regexp.MustCompile(`(?:Action:\s*|\.audit\([^\n]*?|\.save\([^\n]*?|\.record\(r,\s*|\.control\(|\.Record\([^\n]*?|action\s*:?=\s*)"([a-z]+\.[a-z.]+)"`)

func TestAudit_AC7_AllActionsCovered(t *testing.T) {
	src := sources(t)
	recorded := map[string][]string{}
	for path, code := range src {
		for _, m := range recordedAction.FindAllStringSubmatch(code, -1) {
			recorded[m[1]] = append(recorded[m[1]], path)
		}
	}
	for _, a := range Actions {
		require.NotEmpty(t, recorded[a], "no code records %q", a)
	}
	for a, where := range recorded {
		require.True(t, slices.Contains(Actions, a), "%q is recorded in %v but isn't in audit.Actions", a, where)
	}
	for _, required := range []string{
		"project.create", "project.update", "project.delete", "connection.create", "connection.update",
		"connection.delete", "connection.test", "keyspace.create", "mapping.update", "job.create", "job.pause", "job.resume",
		"job.cancel", "job.retry", "report.download",
	} {
		require.Contains(t, Actions, required)
	}
	var auth int
	for _, a := range Actions {
		if strings.HasPrefix(a, "auth.") {
			auth++
		}
	}
	require.GreaterOrEqual(t, auth, 4, "sign-up, sign-in (both outcomes) and sign-out")
}

func TestRecorder_ThrottlesPerKey(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := &Recorder{Now: func() time.Time { return now }}
	require.False(t, r.Throttled("ws/m1", MappingUpdateEvery))
	require.True(t, r.Throttled("ws/m1", MappingUpdateEvery), "a second update within 10 minutes")
	require.False(t, r.Throttled("ws/m2", MappingUpdateEvery), "other mappings are separate")
	now = now.Add(MappingUpdateEvery)
	require.False(t, r.Throttled("ws/m1", MappingUpdateEvery))

	var nilRec *Recorder
	nilRec.Record(context.Background(), "ws", "u", "job.create", "job", "j1", nil) // no-op
	require.False(t, nilRec.Throttled("x", time.Minute))
}

func auditRouter(st store.Store, role store.MemberRole) http.Handler {
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "owner", WorkspaceID: "ws1", Role: role})))
		})
	}
	return httpapi.NewRouter(httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Features: []httpapi.Registrar{Handlers{Store: st.Audit()}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
}

func TestAuditHandler_AC7_OwnerOnly(t *testing.T) {
	clock := storetest.NewClock()
	st := memstore.New(clock)
	rec := &Recorder{Store: st.Audit()}
	ctx := context.Background()
	for i, a := range []string{"project.create", "job.create", "job.pause", "job.cancel"} {
		clock.Advance(time.Second)
		rec.Record(ctx, "ws1", []string{"owner", "ed"}[i%2], a, strings.Split(a, ".")[0], "e1", map[string]string{"n": a})
	}
	rec.Record(ctx, "ws2", "x", "job.create", "job", "j9", nil)

	get := func(role store.MemberRole, q string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		auditRouter(st, role).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/audit"+q, nil))
		return w
	}
	require.Equal(t, http.StatusForbidden, get(store.RoleEditor, "").Code, "editors can't read the audit log")
	require.Equal(t, http.StatusForbidden, get(store.RoleViewer, "").Code)

	w := get(store.RoleOwner, "?limit=3")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var page Page
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Items, 3)
	require.Equal(t, "job.cancel", page.Items[0].Action, "newest first")
	require.NotEmpty(t, page.NextCursor)
	for _, e := range page.Items {
		require.Equal(t, store.WorkspaceID("ws1"), e.WorkspaceID, "only the caller's workspace")
	}
	w = get(store.RoleOwner, "?limit=3&cursor="+page.NextCursor)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.Empty(t, page.NextCursor)

	w = get(store.RoleOwner, "?action=job.&userId=ed")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Items, 2, "job.* by ed")
	require.Equal(t, http.StatusBadRequest, get(store.RoleOwner, "?cursor=%25%25").Code)
}
