package promtext

import (
	"strings"
	"testing"
)

const exposition = `# HELP process_resident_memory_bytes Resident memory size in bytes.
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes_extra 1
process_resident_memory_bytes 2.87309824e+08
oc_job_docs_total{outcome="written"} 1500
oc_job_docs_total{outcome="failed"} 3
`

func TestValue(t *testing.T) {
	v, ok, err := Value(strings.NewReader(exposition), "process_resident_memory_bytes")
	if err != nil || !ok || v != 287309824 {
		t.Fatalf("rss = %v %v %v", v, ok, err)
	}
	v, ok, _ = Value(strings.NewReader(exposition), "oc_job_docs_total")
	if !ok || v != 1500 {
		t.Fatalf("labelled = %v %v", v, ok)
	}
	if _, ok, _ := Value(strings.NewReader(exposition), "go_goroutines"); ok {
		t.Fatal("missing metric reported as present")
	}
	if _, _, err := Value(strings.NewReader("x NaNope\n"), "x"); err == nil {
		t.Fatal("bad number accepted")
	}
	if _, ok, _ := Value(strings.NewReader("x{broken 1\nx\n"), "x"); ok {
		t.Fatal("malformed lines are skipped")
	}
}
