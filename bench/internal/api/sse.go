package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Latency measures progress latency the way the UI sees it: the time from a progress event's
// timestamp (set by the worker) to its arrival over the job's SSE stream. It returns when the
// stream ends (the job completed) or ctx is done.
func (c *Client) Latency(ctx context.Context, jobID string, now func() time.Time) ([]time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/jobs/"+jobID+"/events", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	// No client timeout: the stream lasts as long as the job.
	res, err := (&http.Client{Jar: c.http.Jar}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		return nil, &Error{Method: http.MethodGet, Path: req.URL.Path, Status: res.StatusCode, Body: string(b)}
	}
	var out []time.Duration
	err = ReadEvents(res.Body, func(typ string, data []byte) bool {
		if typ == "progress" {
			var p Progress
			if json.Unmarshal(data, &p) == nil && !p.TS.IsZero() {
				out = append(out, now().Sub(p.TS))
			}
		}
		return typ != "completed"
	})
	if ctx.Err() != nil {
		return out, nil
	}
	return out, err
}

// ReadEvents parses a Server-Sent Events stream, calling fn with each event's type (default
// "message") and data (multi-line data joined with \n) until fn returns false or r ends.
func ReadEvents(r io.Reader, fn func(typ string, data []byte) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	typ, data := "", []string{}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				t := typ
				if t == "" {
					t = "message"
				}
				if !fn(t, []byte(strings.Join(data, "\n"))) {
					return nil
				}
			}
			typ, data = "", data[:0]
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			typ = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[len("data:"):], " "))
		}
	}
	return sc.Err()
}
