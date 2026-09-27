package events

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

const ws store.WorkspaceID = "ws-1"

type sseFixture struct {
	st  *memstore.Store
	job store.Job
	srv *httptest.Server
}

func newSSE(t *testing.T, workspace store.WorkspaceID, heartbeat time.Duration) *sseFixture {
	t.Helper()
	st := memstore.New(nil)
	p, err := st.Projects().Create(context.Background(), ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	j, err := st.Jobs().Create(context.Background(), ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`)})
	require.NoError(t, err)
	j.Status = store.JobRunning
	j, err = st.Jobs().Update(context.Background(), j)
	require.NoError(t, err)
	require.NoError(t, st.Jobs().PutProgress(context.Background(), j.ID, json.RawMessage(`{"jobId":"`+j.ID+`","status":"running","seq":1,"processed":10}`)))
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: workspace, Role: store.RoleViewer})))
		})
	}
	h := httpapi.NewRouter(httpapi.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features:   []httpapi.Registrar{Handlers{Store: st, Hub: NewHub(st.Jobs(), 5*time.Millisecond), Heartbeat: heartbeat}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &sseFixture{st: st, job: j, srv: srv}
}

type sseMsg struct {
	id, event, data string
	comment         bool
}

// read parses SSE messages from the stream into ch until it closes.
func read(_ *testing.T, body io.Reader) <-chan sseMsg {
	ch := make(chan sseMsg, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var m sseMsg
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if m != (sseMsg{}) {
					ch <- m
				}
				m = sseMsg{}
			case strings.HasPrefix(line, ":"):
				m.comment = true
			case strings.HasPrefix(line, "id: "):
				m.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				m.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				m.data += strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return ch
}

func recv(t *testing.T, ch <-chan sseMsg) sseMsg {
	t.Helper()
	select {
	case m, ok := <-ch:
		require.True(t, ok, "stream closed")
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no message within 3 s")
		return sseMsg{}
	}
}

type opened struct {
	StatusCode int
	Header     http.Header
}

// open starts a stream; the body is closed when the test ends.
func (f *sseFixture) open(t *testing.T, path string) (opened, <-chan sseMsg) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.srv.URL+path, nil)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	return opened{res.StatusCode, res.Header}, read(t, res.Body)
}

func TestSSEHandler_AC6_InitialSnapshotThenStream(t *testing.T) {
	f := newSSE(t, ws, time.Hour)
	res, ch := f.open(t, "/api/v1/jobs/"+f.job.ID+"/events")
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	require.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
	require.Equal(t, "no", res.Header.Get("X-Accel-Buffering"))

	first := recv(t, ch)
	require.Equal(t, "progress", first.event)
	require.Equal(t, "1", first.id)
	require.Contains(t, first.data, `"processed":10`)

	last := 1
	for seq := 2; seq <= 4; seq++ {
		require.NoError(t, f.st.Jobs().PutProgress(context.Background(), f.job.ID,
			json.RawMessage(`{"jobId":"`+f.job.ID+`","status":"running","seq":`+itoa(int64(seq))+`}`)))
		m := recv(t, ch)
		require.Equal(t, "progress", m.event)
		require.Greater(t, atoiT(t, m.id), last, "ids increase")
		last = atoiT(t, m.id)
	}
}

func TestSSEHandler_AC6_HeartbeatAndClose(t *testing.T) {
	f := newSSE(t, ws, 20*time.Millisecond)
	_, ch := f.open(t, "/api/v1/jobs/"+f.job.ID+"/events")
	require.Equal(t, "progress", recv(t, ch).event)
	require.True(t, recv(t, ch).comment, ": ping")

	require.NoError(t, f.st.Jobs().PutProgress(context.Background(), f.job.ID,
		json.RawMessage(`{"jobId":"`+f.job.ID+`","status":"completed","seq":9,"percent":100}`)))
	var events []string
	for m := range ch { // the server closes the stream after "completed"
		if !m.comment {
			events = append(events, m.event)
		}
	}
	require.Equal(t, []string{"status", "progress", "completed"}, events)

	// A finished job gets its final snapshot and "completed" at once.
	j, _ := f.st.Jobs().Get(context.Background(), ws, f.job.ID)
	j.Status = store.JobCompleted
	_, err := f.st.Jobs().Update(context.Background(), j)
	require.NoError(t, err)
	_, ch = f.open(t, "/api/v1/jobs/"+f.job.ID+"/events")
	require.Equal(t, "progress", recv(t, ch).event)
	require.Equal(t, "completed", recv(t, ch).event)
}

func TestSSEHandler_AC6_TenantIsolation(t *testing.T) {
	f := newSSE(t, "ws-other", time.Hour)
	res, err := http.Get(f.srv.URL + "/api/v1/jobs/" + f.job.ID + "/events")
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestSSEHandler_MultiplexSkipsForeignJobs(t *testing.T) {
	f := newSSE(t, ws, time.Hour)
	_, ch := f.open(t, "/api/v1/events?jobs="+f.job.ID+",not-mine")
	m := recv(t, ch)
	require.Equal(t, "progress", m.event)
	require.Contains(t, m.data, f.job.ID)
	res, err := http.Get(f.srv.URL + "/api/v1/events?jobs=")
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func atoiT(t *testing.T, s string) int {
	var n int
	require.NoError(t, json.Unmarshal([]byte(s), &n))
	return n
}
