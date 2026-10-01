package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/pathcodec"
	"omnidatabus/internal/store"
	"omnidatabus/internal/verify"
)

// fileTamperWriter runs fn once the given number of documents has been written (after the last
// batch, before verification), like a user editing the output folder.
type fileTamperWriter struct {
	target.DocWriter
	n     *atomic.Int64
	after int64
	fn    func()
}

func (w fileTamperWriter) WriteBatch(ctx context.Context, docs []target.Doc) []target.WriteResult {
	res := w.DocWriter.WriteBatch(ctx, docs)
	if w.n.Add(int64(len(docs))) >= w.after && w.fn != nil {
		w.fn()
	}
	return res
}

// GetDocs and CheckIntegrity forward to the directory writer (the verifier looks for them).
func (w fileTamperWriter) GetDocs(ctx context.Context, keys []string) (map[string][]byte, error) {
	return w.DocWriter.(target.DocReader).GetDocs(ctx, keys)
}

func (w fileTamperWriter) CheckIntegrity(ctx context.Context, sample int) (target.IntegrityReport, error) {
	return w.DocWriter.(target.IntegrityChecker).CheckIntegrity(ctx, sample)
}

// dirFixture runs the fake people table into a directory target (folder "people"); rows is
// the source, total how many documents the run writes, tamper what happens to the folder then.
func dirFixture(t *testing.T, rows [][]any, total int64, sample int, tamper func(root string)) (*runFixture, string) {
	t.Helper()
	o := opts(500)
	o.VerifySampleSize = sample
	f := newRunFixture(t, rows, int64(len(rows)), o)
	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "people"), 0o750))
	m, err := f.st.Mappings().Get(context.Background(), ws, f.job.MappingIDs[0])
	require.NoError(t, err)
	m.SetLocation(store.Location{Kind: store.KindDirectory, Folder: "people"})
	_, err = f.st.Mappings().Update(context.Background(), ws, m)
	require.NoError(t, err)
	f.job.Options = mustJSON(func() jobs.Options {
		x := o
		x.MappingIDs = f.job.MappingIDs
		x.TargetEmpty = map[string]bool{m.ID: true}
		return x
	}())
	conn := directory.New([]string{root})
	cfg := []byte(`{"path":"` + root + `","durability":"none"}`)
	var n atomic.Int64
	var once atomic.Bool
	f.runner.d.OpenWriter = func(ctx context.Context, _ store.WorkspaceID, _ string, o target.WriteOptions) (target.DocWriter, error) {
		w, err := conn.OpenWriter(ctx, cfg, connectors.Credentials{}, o)
		if err != nil {
			return nil, err
		}
		return fileTamperWriter{DocWriter: w, n: &n, after: total, fn: func() {
			if tamper != nil && once.CompareAndSwap(false, true) {
				tamper(root)
			}
		}}, nil
	}
	f.runner.d.CountTarget = func(ctx context.Context, _ store.WorkspaceID, _ string, loc store.Location) (int64, error) {
		return conn.CountDocuments(ctx, target.Conn{Config: cfg}, loc)
	}
	return f, root
}

// file is where the fake table's person <id> lands (flat layout).
func personFile(root string, id int) string {
	name, _ := pathcodec.Encode("person::" + itoa(id))
	return filepath.Join(root, "people", name+".json")
}

func itoa(i int) string { return fmtInt(int64(i)) }

func fmtInt(i int64) string {
	b := []byte{}
	for {
		b = append([]byte{byte('0' + i%10)}, b...)
		if i /= 10; i == 0 {
			return string(b)
		}
	}
}

func TestVerifyDirectory_AC1_Counts(t *testing.T) {
	f, _ := dirFixture(t, peopleRows(1, 10_000, 11, 22, 33), 9_997, 100, nil)
	require.True(t, f.run(jobs.Control{}).Finished)
	r := storedReport(t, f)
	require.Equal(t, verify.Passed, r.Result, "%+v", r)
	require.Equal(t, store.KindDirectory, r.TargetKind)
	require.EqualValues(t, 9_997, r.Counts.Written)
	require.EqualValues(t, 3, r.Counts.Failed, "the dead-lettered rows are explained")
	require.EqualValues(t, 9_997, *r.Counts.TargetCount, "files counted in the folder")
	require.True(t, *r.Counts.TargetCountMatch)
	require.Equal(t, 100, r.Sample.Matched, "spot checks read the files at their computed paths")
	require.NotNil(t, r.Integrity)
	require.Equal(t, 9_997, r.Integrity.Listed)
	require.Equal(t, 1000, r.Integrity.Checked)
	require.Zero(t, r.Integrity.Missing+r.Integrity.Mismatched)
}

func TestVerifyDirectory_AC1_MissingFile(t *testing.T) {
	// 900 rows: under the 1,000-line integrity sample, so every manifest line is checked.
	f, _ := dirFixture(t, peopleRows(1, 900), 900, 900, func(root string) {
		_ = os.Remove(personFile(root, 42))
	})
	require.True(t, f.run(jobs.Control{}).Finished)
	r := storedReport(t, f)
	require.Equal(t, verify.Failed, r.Result)
	require.EqualValues(t, 899, *r.Counts.TargetCount)
	require.False(t, *r.Counts.TargetCountMatch)
	require.Equal(t, 1, r.Integrity.Missing)
	require.Equal(t, "person::42", r.Integrity.Issues[0].Key, "the missing file is named by its key")
	require.Equal(t, 1, r.Sample.Missing)
}

func TestVerifyDirectory_AC2_SpotCheckMismatch(t *testing.T) {
	f, _ := dirFixture(t, peopleRows(1, 300), 300, 300, func(root string) {
		_ = os.WriteFile(personFile(root, 7), []byte(`{"id":7,"name":"edited","age":27}`), 0o600)
	})
	require.True(t, f.run(jobs.Control{}).Finished)
	r := storedReport(t, f)
	require.Equal(t, 1, r.Sample.Mismatched)
	require.Equal(t, "person::7", r.Sample.Mismatches[0].Key)
	require.Equal(t, "$.name", r.Sample.Mismatches[0].Diffs[0].Path, "with a JSON diff")
}

func TestVerifyDirectory_AC3_ManifestHash(t *testing.T) {
	f, _ := dirFixture(t, peopleRows(1, 300), 300, 0, func(root string) {
		// Same JSON meaning, different bytes: only the manifest hash notices.
		b, _ := os.ReadFile(personFile(root, 9)) //nolint:gosec // test
		_ = os.WriteFile(personFile(root, 9), append(b, ' '), 0o600)
	})
	f.job.Options = mustJSON(func() jobs.Options {
		o, _ := jobs.ParseOptions(f.job.Options)
		o.VerifySampleSize = 1 // verification on; the spot check may or may not pick person 9
		return o
	}())
	require.True(t, f.run(jobs.Control{}).Finished)
	r := storedReport(t, f)
	require.Equal(t, 1, r.Integrity.Mismatched)
	require.Equal(t, "hash_mismatch", r.Integrity.Issues[0].Problem)
	require.Equal(t, "person::9", r.Integrity.Issues[0].Key)
	require.NotEqual(t, verify.Passed, r.Result)
}
