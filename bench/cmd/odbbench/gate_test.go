package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnidatabus/bench/internal/gate"
)

func TestColdStartAndGate_OC056(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "migrate.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = preflight ] || exit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(dir, "results")
	var out, errb bytes.Buffer
	if code := coldStartCmd([]string{"--script", script, "--runs", "3", "--out", results}, &out, &errb); code != 0 {
		t.Fatalf("coldstart = %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "over 3 runs") {
		t.Fatal(out.String())
	}
	latest, _ := gate.Latest(results)
	if latest["coldstart"].ColdStartSeconds <= 0 {
		t.Fatalf("%+v", latest)
	}

	gates := filepath.Join(dir, "gates.json")
	_ = os.WriteFile(gates, []byte(`{"release":"t","gates":[
	  {"id":"G5","scenario":"coldstart","metric":"coldStartSeconds","max":5},
	  {"id":"G1","scenario":"directory","metric":"throughput","min":15000}]}`), 0o644)
	summary := filepath.Join(dir, "summary.md")
	out.Reset()
	if code := gateCmd([]string{"--gates", gates, "--results", results}, &out, &errb); code != 1 {
		t.Fatalf("a missing scenario fails: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := gateCmd([]string{"--gates", gates, "--results", results, "--allow-missing", "--summary", summary}, &out, &errb); code != 0 {
		t.Fatalf("allowed: %d\n%s", code, out.String())
	}
	if md, _ := os.ReadFile(summary); !strings.Contains(string(md), "⏳ pending") {
		t.Fatal(string(md))
	}
	if code := gateCmd([]string{"--gates", filepath.Join(dir, "none.json")}, &out, &errb); code != 2 {
		t.Fatal("unreadable gate file is a usage error")
	}
	if code := coldStartCmd([]string{"--script", filepath.Join(dir, "missing.sh")}, &out, &errb); code != 2 {
		t.Fatal("a failing warm-up is a harness error")
	}
}

func TestRun_OC056_RejectsMismatchedFlags(t *testing.T) {
	ctx := context.Background()
	for name, o := range map[string]options{
		"release gate on 2.0": {datasetName: "nested", gate: "release"},
		"directory missing":   {datasetName: "directory", gate: "integrity"},
		"directory elsewhere": {datasetName: "smoke", gate: "integrity"},
		"strategy no embed":   {datasetName: "smoke", strategy: "batched"},
	} {
		if name == "directory elsewhere" {
			o.tgt.Directory = "/data/x"
		}
		if _, err := run(ctx, o, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
