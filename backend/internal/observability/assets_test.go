package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var metricRef = regexp.MustCompile(`\boc_[a-z_]+\b`)

// registeredNames are the application's metric names, with the series a histogram exposes.
func registeredNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, c := range Collectors() {
		ch := make(chan *prometheus.Desc, 8)
		go func() { c.Describe(ch); close(ch) }()
		for d := range ch {
			n := fqName(d)
			out[n] = true
			for _, s := range []string{"_bucket", "_sum", "_count"} {
				out[n+s] = true
			}
		}
	}
	return out
}

func asset(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "observability", name))
	require.NoError(t, err)
	return b
}

func TestDashboards_AC6_MetricNamesExist(t *testing.T) {
	names := registeredNames(t)

	var dash struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	raw := asset(t, "dashboard.json")
	require.NoError(t, json.Unmarshal(raw, &dash))
	exprs := 0
	for _, p := range dash.Panels {
		for _, tg := range p.Targets {
			exprs++
			refs := metricRef.FindAllString(tg.Expr, -1)
			require.NotEmpty(t, refs, "panel %q queries no oc_ metric", p.Title)
			for _, m := range refs {
				require.True(t, names[m], "panel %q uses unknown metric %s", p.Title, m)
			}
		}
	}
	require.Greater(t, exprs, 15)
	for _, row := range []string{"API health", "Jobs", "Workers", "Dependencies"} {
		require.Contains(t, string(raw), `"title": "`+row+`"`)
	}

	var rules struct {
		Groups []struct {
			Rules []struct {
				Alert string `yaml:"alert"`
				Expr  string `yaml:"expr"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	require.NoError(t, yaml.Unmarshal(asset(t, "alerts.yml"), &rules))
	var alerts []string
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			alerts = append(alerts, r.Alert)
			for _, m := range metricRef.FindAllString(r.Expr, -1) {
				require.True(t, names[m], "alert %s uses unknown metric %s", r.Alert, m)
			}
		}
	}
	require.Equal(t, "OmniDataBusHighErrorRate,OmniDataBusJobStalled,OmniDataBusWriterThrottled,OmniDataBusProgressPollFailing,OmniDataBusMixedVersions", strings.Join(alerts, ","))
}
