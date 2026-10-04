package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

// maxErrorsFileBytes rotates errors.jsonl (to errors.jsonl.1) when it grows past it.
const maxErrorsFileBytes = 100 << 20

// maxReportSamples is how many error records report.json keeps.
const maxReportSamples = 100

// stateStore is the pipeline's metadata store in headless mode: an in-memory store holding the
// bundle, whose job writes go to the state directory instead (architecture §3, "local ports").
type stateStore struct {
	*memstore.Store
	jobs *stateJobs
}

func (s *stateStore) Jobs() store.Jobs { return s.jobs }

// stateJobs intercepts the job writes a run makes: the checkpoint (CheckpointStore), the
// dead-lettered rows (ErrorSink), the progress document (ProgressSink), the timeline and
// the verification report.
type stateJobs struct {
	store.Jobs
	dir  StateDir
	sink *ProgressSink
	log  *slog.Logger

	mu       sync.Mutex
	state    State
	lastCP   []byte
	saveErr  error
	errs     *errorLog
	last     progress.Snapshot
	verified json.RawMessage
}

// Update saves the job and, when its checkpoint moved, checkpoint.json.
func (j *stateJobs) Update(ctx context.Context, job store.Job) (store.Job, error) {
	out, err := j.Jobs.Update(ctx, job)
	if err != nil || len(job.Checkpoint) == 0 {
		return out, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if bytes.Equal(job.Checkpoint, j.lastCP) {
		return out, nil
	}
	st := j.state
	st.Checkpoint, st.UpdatedAt = job.Checkpoint, time.Now().UTC()
	if serr := j.dir.Save(st); serr != nil {
		j.saveErr = serr
		return out, serr // the pipeline logs it and retries on the next persist
	}
	j.state, j.lastCP = st, append([]byte(nil), job.Checkpoint...)
	return out, nil
}

// AddErrors appends the rows to errors.jsonl (they aren't kept in memory).
func (j *stateJobs) AddErrors(_ context.Context, errs []store.JobError, _ time.Duration) error {
	return j.errs.append(errs)
}

// PutProgress keeps the document and turns it into stdout events.
func (j *stateJobs) PutProgress(ctx context.Context, jobID string, doc json.RawMessage) error {
	if err := j.Jobs.PutProgress(ctx, jobID, doc); err != nil {
		return err
	}
	s := progress.Parse(jobID, doc)
	j.mu.Lock()
	j.last = s
	j.mu.Unlock()
	if j.sink != nil {
		j.sink.Snapshot(s)
	}
	return nil
}

// PutReport keeps the verification results for report.json.
func (j *stateJobs) PutReport(ctx context.Context, jobID string, doc json.RawMessage) error {
	j.mu.Lock()
	j.verified = append(json.RawMessage(nil), doc...)
	j.mu.Unlock()
	return j.Jobs.PutReport(ctx, jobID, doc)
}

// AppendEvent logs the timeline; warnings also go to stdout.
func (j *stateJobs) AppendEvent(ctx context.Context, jobID string, e store.JobEvent) error {
	if e.Kind == "warning" && j.sink != nil {
		j.sink.Emit(Event{Event: EventWarning, Message: e.Message})
	}
	j.log.Info("job event", "kind", e.Kind, "message", e.Message)
	return j.Jobs.AppendEvent(ctx, jobID, e)
}

func (j *stateJobs) snapshot() (progress.Snapshot, json.RawMessage) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.last, j.verified
}

// errorLog is errors.jsonl: one OC-026 error record per line, append-only, rotated at 100 MB.
// It counts errors by stage and code and keeps the first records for report.json, including
// those of earlier attempts on the same state directory.
type errorLog struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	size    int64
	total   int64
	byCode  map[[2]string]int64
	samples []store.JobError
}

func openErrorLog(path string) (*errorLog, error) {
	l := &errorLog{path: path, byCode: map[[2]string]int64{}}
	if f, err := os.Open(path); err == nil { //nolint:gosec // G304: inside the state directory
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e store.JobError
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				l.count(e)
			}
		}
		_ = f.Close()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // G304: inside the state directory
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	l.f, l.size = f, st.Size()
	return l, nil
}

func (l *errorLog) count(e store.JobError) {
	l.total++
	l.byCode[[2]string{e.Stage, e.Code}]++
	if len(l.samples) < maxReportSamples {
		l.samples = append(l.samples, e)
	}
}

func (l *errorLog) append(errs []store.JobError) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var buf bytes.Buffer
	for _, e := range errs {
		raw, err := json.Marshal(e)
		if err != nil {
			continue
		}
		buf.Write(raw)
		buf.WriteByte('\n')
		l.count(e)
	}
	if l.size+int64(buf.Len()) > maxErrorsFileBytes && l.size > 0 {
		if err := l.rotate(); err != nil {
			return err
		}
	}
	n, err := l.f.Write(buf.Bytes())
	l.size += int64(n)
	if err != nil {
		return err
	}
	return l.f.Sync()
}

func (l *errorLog) rotate() error {
	if err := l.f.Close(); err != nil {
		return err
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	l.f, l.size = f, 0
	return nil
}

func (l *errorLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		_ = l.f.Close()
	}
}

// summary is report.json's errors section.
func (l *errorLog) summary() ReportErrors {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := ReportErrors{Total: l.total, ByCode: []ErrorCount{}, Samples: append([]store.JobError{}, l.samples...)}
	for k, n := range l.byCode {
		out.ByCode = append(out.ByCode, ErrorCount{Stage: k[0], Code: k[1], Count: n})
	}
	sort.Slice(out.ByCode, func(i, j int) bool {
		if out.ByCode[i].Count != out.ByCode[j].Count {
			return out.ByCode[i].Count > out.ByCode[j].Count
		}
		return out.ByCode[i].Code < out.ByCode[j].Code
	})
	return out
}
