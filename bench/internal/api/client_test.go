package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer records requests and answers from a route table.
type fakeServer struct {
	mu     sync.Mutex
	seen   []*http.Request
	bodies []string
	routes map[string]func(w http.ResponseWriter, r *http.Request)
}

func newFake(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*fakeServer, *Client) {
	f := &fakeServer{routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.seen = append(f.seen, r)
		f.bodies = append(f.bodies, string(b))
		f.mu.Unlock()
		h, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.Error(w, `{"title":"not found"}`, http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return f, New(srv.URL + "/")
}

func js(v any) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

func TestSignIn_FallsBackToSignupAndSendsCSRF(t *testing.T) {
	f, c := newFake(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /api/v1/auth/login":  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "{}", http.StatusUnauthorized) },
		"POST /api/v1/auth/signup": js(map[string]string{"csrfToken": "tok"}),
		"POST /api/v1/projects":    js(map[string]string{"id": "p1"}),
	})
	ctx := context.Background()
	if err := c.SignIn(ctx, "b@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	var p struct{ ID string }
	if err := c.Do(ctx, http.MethodPost, "/api/v1/projects", map[string]string{"name": "x"}, &p); err != nil || p.ID != "p1" {
		t.Fatalf("%v %v", p, err)
	}
	last := f.seen[len(f.seen)-1]
	if last.Header.Get("X-CSRF-Token") != "tok" || last.Header.Get("Origin") == "" || last.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", last.Header)
	}
	if !strings.Contains(f.bodies[1], `"displayName":"Benchmark"`) {
		t.Fatalf("signup body = %s", f.bodies[1])
	}
}

func TestDo_ErrorsCarryStatusAndBody(t *testing.T) {
	_, c := newFake(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /api/v1/auth/login": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"title":"locked"}`, http.StatusForbidden)
		},
	})
	err := c.SignIn(context.Background(), "b@example.com", "pw")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || !strings.Contains(err.Error(), `POST /api/v1/auth/login: 403 {"title":"locked"}`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatal("404 is an error")
	}
}

func TestProject_SetsUpReposTestsAndDiscovers(t *testing.T) {
	polls := 0
	f, c := newFake(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /api/v1/projects":                        js(map[string]string{"id": "p1"}),
		"PUT /api/v1/projects/p1/connections/source":   js(map[string]string{}),
		"PUT /api/v1/projects/p1/connections/target":   js(map[string]string{}),
		"POST /api/v1/connections/p1::source/test":     js(map[string]string{}),
		"POST /api/v1/connections/p1::target/test":     js(map[string]string{}),
		"POST /api/v1/connections/p1::source/discover": js(map[string]string{"id": "d1", "status": "running"}),
		"GET /api/v1/connections/p1::source/discoveries/d1": func(w http.ResponseWriter, r *http.Request) {
			polls++
			js(map[string]string{"id": "d1", "status": "completed"})(w, r)
		},
	})
	id, err := c.Project(context.Background(), "bench", Source{Host: "pg", Port: 5432, Database: "shop", Username: "u", Password: "s"},
		Target{ConnectionString: "couchbases://cb.example.com", Username: "a", Password: "t", Capella: true})
	if err != nil || id != "p1" || polls != 1 {
		t.Fatalf("id=%s err=%v polls=%d", id, err, polls)
	}
	if !strings.Contains(f.bodies[2], `"deployment":"capella"`) || !strings.Contains(f.bodies[1], `"password":"s"`) {
		t.Fatalf("bodies = %v", f.bodies[1:3])
	}
}

func TestTarget_OC056_Directory(t *testing.T) {
	tgt := Target{Directory: "/data/odb-out/bench_1"}
	conn := tgt.connection()
	cfg := conn["config"].(map[string]any)
	if conn["type"] != "directory" || cfg["durability"] != "file" || cfg["path"] != "/data/odb-out/bench_1" || conn["secrets"] != nil {
		t.Fatalf("%v", conn)
	}
	if loc := tgt.Location("ignored", "files"); loc["kind"] != "directory" || loc["folder"] != "files" {
		t.Fatalf("%v", loc)
	}
}

func TestDiscover_Failure(t *testing.T) {
	_, c := newFake(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /api/v1/connections/x/discover": js(map[string]string{"id": "d1", "status": "failed", "error": "permission denied"}),
	})
	if err := c.discover(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestMappingEmbedJobAndReport(t *testing.T) {
	f, c := newFake(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /api/v1/projects/p1/mappings:bulk":      js(map[string]any{"results": []any{map[string]any{"mapping": map[string]string{"id": "m1"}}}}),
		"GET /api/v1/projects/p1/mappings/m1":         js(map[string]any{"version": 4}),
		"POST /api/v1/projects/p1/mappings/m1/embeds": js(map[string]any{}),
		"POST /api/v1/projects/p1/jobs":               js(map[string]any{"job": map[string]any{"id": "j1", "status": "queued"}, "preflight": map[string]any{"ok": true}}),
		"GET /api/v1/jobs/j1":                         js(map[string]any{"id": "j1", "status": "completed", "progress": map[string]any{"written": 10, "percent": 100}}),
		"GET /api/v1/jobs/j1/report": js(map[string]any{"mappings": []any{map[string]any{"table": "bench.x", "written": 10,
			"verification": map[string]any{"result": "passed", "counts": map[string]any{"sourceRows": 10, "targetCountMatch": true}}}}}),
		"GET /api/v1/version": js(map[string]string{"version": "1.0.0", "commit": "abc123"}),
	})
	ctx := context.Background()
	mid, err := c.Mapping(ctx, "p1", "bench", "x", Target{Bucket: "sandbox"}.Location("bench_1", "x"))
	if err != nil || mid != "m1" {
		t.Fatalf("%s %v", mid, err)
	}
	if !strings.Contains(f.bodies[0], `"location":{"bucket":"sandbox","collection":"x","createIfMissing":true,"kind":"couchbase","scope":"bench_1"}`) {
		t.Fatalf("mapping = %s", f.bodies[0])
	}
	if err := c.AddEmbed(ctx, "p1", "m1", map[string]any{"join": []map[string]string{{"parent": "id", "child": "customer_id"}}}); err != nil {
		t.Fatal(err)
	}
	embed := f.seen[len(f.seen)-1]
	if embed.Header.Get("If-Match") != `"4"` || !strings.Contains(f.bodies[len(f.bodies)-1], `{"child":"customer_id","parent":"id"}`) {
		t.Fatalf("embed = %v %s", embed.Header, f.bodies[len(f.bodies)-1])
	}
	j, err := c.StartJob(ctx, "p1", map[string]any{"batchSize": 1000})
	if err != nil || j.ID != "j1" || j.Terminal() {
		t.Fatalf("%+v %v", j, err)
	}
	j, _ = c.Job(ctx, "j1")
	if !j.Terminal() || j.Progress.Written != 10 {
		t.Fatalf("%+v", j)
	}
	r, err := c.Report(ctx, "j1")
	if err != nil || r.Mappings[0].Verification.Result != "passed" || !*r.Mappings[0].Verification.Counts.TargetCountMatch {
		t.Fatalf("%+v %v", r, err)
	}
	v, _ := c.Version(ctx)
	if v.Commit != "abc123" {
		t.Fatalf("%+v", v)
	}
}

func TestMapping_ReportsPerTableErrors(t *testing.T) {
	_, c := newFake(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /api/v1/projects/p1/mappings:bulk": js(map[string]any{"results": []any{map[string]any{"error": map[string]string{"title": "no such table"}}}}),
	})
	if _, err := c.Mapping(context.Background(), "p1", "bench", "x", Target{Directory: "/data/odb-out/b"}.Location("s", "x")); err == nil {
		t.Fatal("a failed item is an error")
	}
}

func TestReadEvents(t *testing.T) {
	stream := ": keep-alive\n\nid: 1\nevent: progress\ndata: {\"a\":\ndata: 1}\n\ndata: plain\n\nevent: completed\ndata: {}\n\nevent: progress\ndata: {}\n\n"
	var got []string
	if err := ReadEvents(strings.NewReader(stream), func(typ string, data []byte) bool {
		got = append(got, typ+"="+string(data))
		return typ != "completed"
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"progress={\"a\":\n1}", "message=plain", "completed={}"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q", got)
	}
}

func TestLatency_FromEventTimestampToArrival(t *testing.T) {
	ts := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	_, c := newFake(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /api/v1/jobs/j1/events": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for i, ms := range []int{0, 1000} {
				p, _ := json.Marshal(map[string]any{"ts": ts.Add(time.Duration(ms) * time.Millisecond), "percent": 50 * (i + 1)})
				_, _ = fmt.Fprintf(w, "event: progress\ndata: %s\n\n", p)
			}
			_, _ = fmt.Fprint(w, "event: completed\ndata: {}\n\n")
		},
		"GET /api/v1/jobs/nope/events": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "gone", http.StatusNotFound) },
	})
	arrival := ts.Add(1500 * time.Millisecond)
	got, err := c.Latency(context.Background(), "j1", func() time.Time { return arrival })
	if err != nil || len(got) != 2 || got[0] != 1500*time.Millisecond || got[1] != 500*time.Millisecond {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := c.Latency(context.Background(), "nope", time.Now); err == nil {
		t.Fatal("404 stream is an error")
	}
}
