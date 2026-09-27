package observability

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func routed() http.Handler {
	r := chi.NewRouter()
	r.Use(HTTPMetrics)
	r.Get("/api/v1/projects/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/api/v1/missing/{id}", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	return r
}

func TestMetricsMiddleware_AC1_RoutePatternLabel(t *testing.T) {
	h := routed()
	ok := HTTPRequests.WithLabelValues("/api/v1/projects/{id}", "GET", "200")
	missing := HTTPRequests.WithLabelValues("/api/v1/missing/{id}", "GET", "404")
	before, beforeMissing := testutil.ToFloat64(ok), testutil.ToFloat64(missing)
	for _, id := range []string{"a", "b", "c"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+id, nil))
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/missing/x", nil))
	require.InDelta(t, 3, testutil.ToFloat64(ok)-before, 0, "one series for every project ID")
	require.InDelta(t, 1, testutil.ToFloat64(missing)-beforeMissing, 0)

	// No series is labelled with a raw path.
	n := testutil.CollectAndCount(HTTPRequests)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/projects/zzz", nil))
	require.Equal(t, n, testutil.CollectAndCount(HTTPRequests))
}

func TestMetricsMiddleware_AC1_Histogram(t *testing.T) {
	h := routed()
	count := func() uint64 {
		return histogramCount(t, HTTPDuration.WithLabelValues("/api/v1/projects/{id}", "GET").(prometheus.Metric))
	}
	before := count()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/projects/p1", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/projects/p2", nil))
	require.Equal(t, before+2, count())
}

func TestRoutePattern_UnmatchedIsBounded(t *testing.T) {
	require.Equal(t, "unmatched", RoutePattern(httptest.NewRequest(http.MethodGet, "/no/router/here", nil)))
}

// denied labels would give one series per row, table, or person.
var denied = map[string]bool{"key": true, "table": true, "user_id": true, "email": true}

var descLabels = regexp.MustCompile(`variableLabels: \{([^}]*)\}`)

func TestMetrics_AC3_NoHighCardinalityLabels(t *testing.T) {
	names := map[string]bool{}
	for _, c := range Collectors() {
		ch := make(chan *prometheus.Desc, 8)
		go func() { c.Describe(ch); close(ch) }()
		for d := range ch {
			m := descLabels.FindStringSubmatch(d.String())
			require.NotNil(t, m, d.String())
			for _, l := range regexp.MustCompile(`[a-z_]+`).FindAllString(m[1], -1) {
				require.False(t, denied[l], "%s uses label %q", d, l)
			}
			names[fqName(d)] = true
		}
	}
	require.GreaterOrEqual(t, len(names), 20, "every application metric is registered through the recorder")

	// And everything the default registry exposes (runtime, process, and any series so far).
	fams, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range fams {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				require.False(t, denied[l.GetName()], "%s uses label %q", f.GetName(), l.GetName())
			}
		}
	}
}

func histogramCount(t *testing.T, m prometheus.Metric) uint64 {
	t.Helper()
	var out dto.Metric
	require.NoError(t, m.Write(&out))
	return out.GetHistogram().GetSampleCount()
}

var descName = regexp.MustCompile(`fqName: "([^"]+)"`)

func fqName(d *prometheus.Desc) string {
	if m := descName.FindStringSubmatch(d.String()); m != nil {
		return m[1]
	}
	return ""
}
