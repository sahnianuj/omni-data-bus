// Package api is the benchmark's client for the Omni-Data-Bus REST API: it signs in, sets up a
// project, starts a migration and follows it.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Client is a signed-in session (cookie + CSRF token).
type Client struct {
	base string
	http *http.Client
	csrf string
}

// New returns a client for base (e.g. http://localhost:8080) with its own cookie jar.
func New(base string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Jar: jar, Timeout: 60 * time.Second}}
}

// Error is a non-2xx response.
type Error struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

// Do sends a JSON request and decodes a JSON response into out (when non-nil). Mutating
// requests carry the session's CSRF token and the Origin the server checks.
func (c *Client) Do(ctx context.Context, method, path string, in, out any, header ...string) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", c.base)
		if c.csrf != "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return &Error{Method: method, Path: path, Status: res.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type session struct {
	CSRFToken string `json:"csrfToken"`
}

// SignIn logs in, or signs up when the account doesn't exist yet (signup must be enabled).
func (c *Client) SignIn(ctx context.Context, email, password string) error {
	var s session
	err := c.Do(ctx, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password}, &s)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
		err = c.Do(ctx, http.MethodPost, "/api/v1/auth/signup", map[string]string{"email": email, "password": password, "displayName": "Benchmark"}, &s)
	}
	if err != nil {
		return err
	}
	c.csrf = s.CSRFToken
	return nil
}

// Version is the server build.
type Version struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Version reads /api/v1/version.
func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	return v, c.Do(ctx, http.MethodGet, "/api/v1/version", nil, &v)
}

// Source is the PostgreSQL repository, as the app (not the harness) reaches it.
type Source struct {
	Host, Database, Username, Password string
	Port                               int
}

// Target is the Couchbase repository, as the app reaches it.
type Target struct {
	ConnectionString, Username, Password, Bucket string
	Capella                                      bool
}

// Project creates a project with its repositories saved and tested, and the source discovered.
func (c *Client) Project(ctx context.Context, name string, src Source, tgt Target) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := c.Do(ctx, http.MethodPost, "/api/v1/projects", map[string]string{"name": name}, &p); err != nil {
		return "", err
	}
	if err := c.Do(ctx, http.MethodPut, "/api/v1/projects/"+p.ID+"/connections/source", map[string]any{
		"type": "postgres",
		"config": map[string]any{"host": src.Host, "port": src.Port, "database": src.Database, "username": src.Username,
			"sslMode": "disable", "connectTimeoutSec": 10},
		"secrets": map[string]string{"password": src.Password},
	}, nil); err != nil {
		return "", err
	}
	deployment := "self-managed"
	if tgt.Capella {
		deployment = "capella"
	}
	if err := c.Do(ctx, http.MethodPut, "/api/v1/projects/"+p.ID+"/connections/target", map[string]any{
		"type":    "couchbase",
		"config":  map[string]any{"connectionString": tgt.ConnectionString, "username": tgt.Username, "deployment": deployment, "kvTimeoutMs": 10000, "connectTimeoutMs": 30000},
		"secrets": map[string]string{"password": tgt.Password},
	}, nil); err != nil {
		return "", err
	}
	for _, side := range []string{"source", "target"} {
		if err := c.Do(ctx, http.MethodPost, "/api/v1/connections/"+url.PathEscape(p.ID+"::"+side)+"/test", nil, nil); err != nil {
			return "", err
		}
	}
	return p.ID, c.discover(ctx, p.ID+"::source")
}

func (c *Client) discover(ctx context.Context, connID string) error {
	path := "/api/v1/connections/" + url.PathEscape(connID)
	var d struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := c.Do(ctx, http.MethodPost, path+"/discover", map[string]any{}, &d); err != nil {
		return err
	}
	for d.Status == "running" || d.Status == "queued" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		if err := c.Do(ctx, http.MethodGet, path+"/discoveries/"+d.ID, nil, &d); err != nil {
			return err
		}
	}
	if d.Status != "completed" {
		return fmt.Errorf("schema discovery %s: %s", d.Status, d.Error)
	}
	return nil
}

// Mapping creates the table's mapping into bucket.scope.<table> (collection created on start).
func (c *Client) Mapping(ctx context.Context, projectID, schema, table, bucket, scope string) (string, error) {
	var res struct {
		Results []struct {
			Mapping *struct {
				ID string `json:"id"`
			} `json:"mapping"`
			Error json.RawMessage `json:"error"`
		} `json:"results"`
	}
	err := c.Do(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/mappings:bulk", map[string]any{"items": []map[string]any{{
		"sourceSchema": schema, "sourceTable": table,
		"target": map[string]any{"bucket": bucket, "scope": scope, "collection": table, "createIfMissing": true},
	}}}, &res)
	if err != nil {
		return "", err
	}
	if len(res.Results) != 1 || res.Results[0].Mapping == nil {
		detail := "no result"
		if len(res.Results) == 1 {
			detail = string(res.Results[0].Error)
		}
		return "", fmt.Errorf("create mapping for %s.%s: %s", schema, table, detail)
	}
	return res.Results[0].Mapping.ID, nil
}

// EmbedArray embeds child (joined on child.fk = parent.id) into the mapping as an array field
// with generated, auto-mapped fields.
func (c *Client) EmbedArray(ctx context.Context, projectID, mappingID, schema, child, fk string, limit int) error {
	path := "/api/v1/projects/" + projectID + "/mappings/" + mappingID
	var m struct {
		Version int64 `json:"version"`
	}
	if err := c.Do(ctx, http.MethodGet, path, nil, &m); err != nil {
		return err
	}
	return c.Do(ctx, http.MethodPost, path+"/embeds", map[string]any{
		"kind": "one-to-many", "table": map[string]string{"schema": schema, "name": child},
		"join": []map[string]string{{"parent": "id", "child": fk}}, "targetName": child,
		"orderBy": []map[string]any{{"column": "id"}}, "limit": limit, "generate": true,
	}, nil, "If-Match", fmt.Sprintf(`"%d"`, m.Version))
}

// Progress is the part of a job's progress snapshot the benchmark reads.
type Progress struct {
	Status     string    `json:"status"`
	TS         time.Time `json:"ts"`
	Total      int64     `json:"total"`
	Percent    float64   `json:"percent"`
	Written    int64     `json:"written"`
	Failed     int64     `json:"failed"`
	RowsRead   int64     `json:"rowsRead"`
	Throughput float64   `json:"throughput"`
}

// Job is a migration job.
type Job struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Reason   string   `json:"reason"`
	Progress Progress `json:"progress"`
}

// Terminal reports whether the job has finished.
func (j Job) Terminal() bool {
	switch j.Status {
	case "completed", "completed_with_errors", "failed", "cancelled":
		return true
	}
	return false
}

// StartJob runs the pre-flight and queues a migration of the project's mappings.
func (c *Client) StartJob(ctx context.Context, projectID string, options map[string]any) (Job, error) {
	var res struct {
		Job Job `json:"job"`
	}
	return res.Job, c.Do(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/jobs", options, &res)
}

// Job reads a job.
func (c *Client) Job(ctx context.Context, id string) (Job, error) {
	var j Job
	return j, c.Do(ctx, http.MethodGet, "/api/v1/jobs/"+id, nil, &j)
}

// Verification is one mapping's post-migration check.
type Verification struct {
	Result string `json:"result"`
	Error  string `json:"error"`
	Counts struct {
		SourceRows       int64 `json:"sourceRows"`
		Written          int64 `json:"written"`
		TargetCount      int64 `json:"targetCount"`
		TargetCountMatch *bool `json:"targetCountMatch"`
	} `json:"counts"`
}

// Report is the part of the job report the benchmark reads.
type Report struct {
	Mappings []struct {
		Table        string        `json:"table"`
		RowsRead     int64         `json:"rowsRead"`
		Written      int64         `json:"written"`
		Failed       int64         `json:"failed"`
		Verification *Verification `json:"verification"`
	} `json:"mappings"`
}

// Report reads the job report (with verification, OC-030).
func (c *Client) Report(ctx context.Context, jobID string) (Report, error) {
	var r Report
	return r, c.Do(ctx, http.MethodGet, "/api/v1/jobs/"+jobID+"/report", nil, &r)
}
