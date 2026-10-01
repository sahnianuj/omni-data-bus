package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
	"omnidatabus/internal/verify"
)

// Report assembles the job report (OC-030): metadata, per-table counts and verification, the
// error summary and the frozen specs. It never includes credentials: repositories are named
// by type, host and database only.
func (s *Service) Report(ctx context.Context, ws store.WorkspaceID, id string) (verify.Report, error) {
	j, err := s.store.Jobs().Get(ctx, ws, id)
	if err != nil {
		return verify.Report{}, err
	}
	v := s.view(ctx, j)
	opts, _ := ParseOptions(j.Options)
	r := verify.Report{GeneratedAt: s.now(), Specs: map[string]json.RawMessage{}, Mappings: []verify.MappingReport{}}
	r.Job = verify.JobInfo{ID: j.ID, Status: j.Status, ProjectID: j.ProjectID, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, Keyspaces: []string{}}
	if p, err := s.store.Projects().Get(ctx, ws, j.ProjectID); err == nil {
		r.Job.ProjectName = p.Name
	}
	if u, err := s.store.Accounts().User(ctx, j.CreatedBy); err == nil {
		r.Job.StartedBy = u.Email
	}
	if j.StartedAt != nil && j.FinishedAt != nil {
		r.Job.DurationSec = j.FinishedAt.Sub(*j.StartedAt).Seconds()
	}
	if c, err := s.store.Connections().Get(ctx, ws, store.ConnectionID(j.ProjectID, store.RoleSource)); err == nil {
		r.Job.Source = sourceEndpoint(c)
	}
	if c, err := s.store.Connections().Get(ctx, ws, store.ConnectionID(j.ProjectID, store.RoleTarget)); err == nil {
		r.Job.Target = targetEndpoint(c)
	}
	shown := opts
	shown.Specs, shown.TargetEmpty = nil, nil
	r.Job.Options, _ = json.Marshal(shown)

	stored, err := s.store.Jobs().GetReport(ctx, j.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return verify.Report{}, err
	}
	results := verify.ParseReport(stored)
	progressOf := map[string]progress.MappingProgress{}
	for _, mp := range v.Progress.Mappings {
		progressOf[mp.MappingID] = mp
	}
	for _, mid := range j.MappingIDs {
		mr := verify.MappingReport{MappingID: mid}
		spec := opts.Specs[mid]
		if m, err := s.store.Mappings().Get(ctx, ws, mid); err == nil {
			srcSchema, srcTable := m.Driving()
			mr.Table = srcSchema + "." + srcTable
			mr.Target = m.Where().String()
			if len(spec) == 0 {
				spec = m.Spec
			}
		}
		if mr.Target != "" {
			r.Job.Keyspaces = append(r.Job.Keyspaces, mr.Target)
		}
		var sp mapping.Spec
		if json.Unmarshal(spec, &sp) == nil {
			mr.KeyTemplate = sp.Key.Template
		}
		if len(spec) > 0 {
			r.Specs[mid] = spec
		}
		if mp, ok := progressOf[mid]; ok {
			mr.Written, mr.Skipped, mr.Failed = mp.Written, mp.Skipped, mp.Failed
			mr.RowsRead = mp.Processed
		}
		if res, ok := results.Mappings[mid]; ok {
			res := res
			mr.Verification = &res
			mr.RowsRead = res.Counts.SourceRows
		}
		r.Mappings = append(r.Mappings, mr)
	}
	sum, err := s.store.Jobs().SummarizeErrors(ctx, ws, j.ID)
	if err != nil {
		return verify.Report{}, err
	}
	r.Errors = verify.ErrorSummary{ByCode: []verify.ErrorCount{}, ExportURL: "/api/v1/jobs/" + url.PathEscape(j.ID) + "/errors/export?format=csv"}
	for _, e := range sum {
		r.Errors.ByCode = append(r.Errors.ByCode, verify.ErrorCount{Stage: e.Stage, Code: e.Code, Count: e.Count})
		r.Errors.Total += e.Count
	}
	return r, nil
}

// sourceEndpoint is db@host:port, from the non-secret config fields only.
func sourceEndpoint(c store.Connection) verify.Endpoint {
	var cfg struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Database string `json:"database"`
	}
	_ = json.Unmarshal(c.Config, &cfg)
	host := cfg.Host
	if cfg.Port != 0 {
		host = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	}
	return verify.Endpoint{Type: c.Type, Host: host, Database: cfg.Database}
}

// targetEndpoint is the cluster's hosts, without any user info in the connection string.
func targetEndpoint(c store.Connection) verify.Endpoint {
	var cfg struct {
		ConnectionString string `json:"connectionString"`
	}
	_ = json.Unmarshal(c.Config, &cfg)
	host := cfg.ConnectionString
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		host = u.Host
	} else if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if i := strings.IndexAny(host, "/?"); i >= 0 {
		host = host[:i]
	}
	return verify.Endpoint{Type: c.Type, Host: host}
}
