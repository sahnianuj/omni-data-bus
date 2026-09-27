package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnidatabus/bench/internal/api"
	"omnidatabus/bench/internal/dataset"
)

func TestGatesFor(t *testing.T) {
	o := parseFlags(nil)
	if g, err := gatesFor(o); g != nil || err != nil {
		t.Fatal("default: no gates")
	}
	o.gate = "integrity"
	g, _ := gatesFor(o)
	if g.MinThroughput != 0 || !g.RequireNoLoss || !g.RequireVerified {
		t.Fatalf("integrity = %+v", g)
	}
	o.gate, o.minDocsPerSec, o.maxRSSMB, o.maxLatency = "release", 1000, 256, time.Second
	g, _ = gatesFor(o)
	if g.MinThroughput != 1000 || g.MaxRSSBytes != 256<<20 || g.MaxLatencyP95 != time.Second || !g.RequireVerified {
		t.Fatalf("release = %+v", g)
	}
	o.gate = "strict"
	if _, err := gatesFor(o); err == nil {
		t.Fatal("unknown gate accepted")
	}
}

func TestVerification_WorstAcrossMappings(t *testing.T) {
	rep := func(results ...string) api.Report {
		var r api.Report
		for _, res := range results {
			m := struct {
				Table        string            `json:"table"`
				RowsRead     int64             `json:"rowsRead"`
				Written      int64             `json:"written"`
				Failed       int64             `json:"failed"`
				Verification *api.Verification `json:"verification"`
			}{}
			if res != "none" {
				m.Verification = &api.Verification{Result: res}
			}
			r.Mappings = append(r.Mappings, m)
		}
		return r
	}
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"passed", "passed"}, "passed"},
		{[]string{"passed", "passed_with_warnings"}, "passed_with_warnings"},
		{[]string{"passed", "none"}, ""},
		{[]string{"failed", "passed"}, "failed"},
	} {
		if got := verification(rep(c.in...)); got != c.want {
			t.Fatalf("%v → %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnvironmentNotes(t *testing.T) {
	o := parseFlags([]string{"--env", "worker=8 vCPU / 16 GB", "--env", "broken"})
	env := environment(o)
	if env["worker"] != "8 vCPU / 16 GB" || env["harness"] == "" || len(env) != 2 {
		t.Fatalf("env = %v", env)
	}
}

// fakePsql writes a script that logs its stdin and prints count.
func fakePsql(t *testing.T, count string) (command, logFile string) {
	dir := t.TempDir()
	logFile = filepath.Join(dir, "sql.log")
	script := filepath.Join(dir, "psql")
	body := "#!/bin/sh\ncat >> " + logFile + "\necho '" + count + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, logFile
}

func TestPrepare_GeneratesOnlyWhenNeeded(t *testing.T) {
	d, _ := dataset.Get("smoke", 100)
	ctx := context.Background()
	var log bytes.Buffer

	cmd, sqlLog := fakePsql(t, "100")
	if err := prepare(ctx, options{psql: cmd, generate: "auto"}, d, &log); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(sqlLog)
	if strings.Contains(string(b), "CREATE TABLE") || !strings.Contains(log.String(), "already has 100 rows") {
		t.Fatalf("auto with the right count must not regenerate: %s", b)
	}

	cmd, sqlLog = fakePsql(t, "42")
	if err := prepare(ctx, options{psql: cmd, generate: "auto"}, d, &log); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(sqlLog)
	if !strings.Contains(string(b), "CREATE TABLE bench.smoke") {
		t.Fatalf("wrong count must regenerate: %s", b)
	}

	if err := prepare(ctx, options{psql: cmd, generate: "never"}, d, &log); err != nil {
		t.Fatal(err)
	}
	if err := prepare(ctx, options{psql: cmd, generate: "sometimes"}, d, &log); err == nil {
		t.Fatal("bad --generate accepted")
	}
	if err := prepare(ctx, options{psql: "false", generate: "always"}, d, &log); err == nil {
		t.Fatal("a failing psql is an error")
	}
	if _, err := psql(ctx, " ", "SELECT 1"); err == nil {
		t.Fatal("empty command")
	}
}
