package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
)

// CodeVersionUnsupported is reported for servers without collections (< 7.0).
const CodeVersionUnsupported = "TARGET_VERSION_UNSUPPORTED"

// capellaIPHint is appended to timeouts, the usual Capella cause.
const capellaIPHint = "For Capella, add this server's public IP under Settings → Networking → Allowed IP addresses."

// Connector implements target.Connector for Couchbase.
type Connector struct {
	connect func(connStr string, opts gocb.ClusterOptions) (cluster, error)
	now     func() time.Time
	pool    *Pool
	probe   authProber
}

// New returns the Couchbase connector.
func New() *Connector {
	return &Connector{connect: connectGocb, now: time.Now, pool: NewPool(10*time.Minute, time.Now), probe: newAuthProber()}
}

// Descriptor implements target.Connector.
func (*Connector) Descriptor() connectors.Descriptor {
	return connectors.Descriptor{
		Type: "couchbase", Role: connectors.RoleTarget, DisplayName: "Couchbase", Icon: "couchbase",
		Capabilities: connectors.Capabilities{CreateKeyspaces: true},
	}
}

// Validate implements target.Connector.
func (*Connector) Validate(cfg json.RawMessage) error {
	_, err := ParseConfig(cfg)
	return err
}

// Normalize returns the validated config and a default name (the first host).
func (*Connector) Normalize(cfg json.RawMessage) (json.RawMessage, string, error) {
	c, err := ParseConfig(cfg)
	if err != nil {
		return nil, "", err
	}
	return c.JSON(), c.FirstHost(), nil
}

// Keyspaces implements target.KeyspaceBrowser on a pooled cluster connection: bootstrapping the
// SDK takes seconds, so browsing reuses one connection per repository (and version).
func (c *Connector) Keyspaces(ctx context.Context, id, version string, raw json.RawMessage, creds connectors.Credentials) (target.Keyspaces, error) {
	cfg, err := ParseConfig(raw)
	if err != nil {
		return nil, err
	}
	cl, err := c.pool.Get(id, version, func() (Closer, error) {
		opts, _, err := buildClusterOptions(cfg, creds)
		if err != nil {
			return nil, err
		}
		cl, err := c.open(ctx, cfg, opts, time.Duration(cfg.ConnectTimeoutMs)*time.Millisecond)
		if err != nil {
			return nil, classify(err)
		}
		return cl, nil
	})
	if err != nil {
		return nil, err
	}
	api, ok := cl.(adminAPI)
	if !ok {
		return nil, errors.New("couchbase: connection doesn't support keyspace administration")
	}
	return &keyspaces{api: api, deployment: cfg.Deployment, sleep: sleepCtx}, nil
}

// Close releases pooled cluster connections.
func (c *Connector) Close(context.Context) error {
	c.pool.CloseAll()
	return nil
}

// Test implements connectors.Tester: parse → bootstrap → services → version → permissions.
func (c *Connector) Test(ctx context.Context, raw json.RawMessage, creds connectors.Credentials) connectors.TestResult {
	r := connectors.NewStepRunner(c.now)
	var cfg Config
	var opts gocb.ClusterOptions
	var warnings []string
	r.Run("parse", "Connection string", func() connectors.TestStep {
		var err error
		if cfg, err = ParseConfig(raw); err != nil {
			return connectors.TestStep{Status: connectors.StepFail, Detail: err.Error()}
		}
		if opts, warnings, err = buildClusterOptions(cfg, creds); err != nil {
			return connectors.TestStep{Status: connectors.StepFail, Detail: err.Error()}
		}
		for _, w := range warnings {
			if w == WarnTLSVerifyDisabled {
				return connectors.TestStep{
					Status: connectors.StepWarn, Code: w,
					Detail: "TLS certificate verification is disabled. Only use this for local development.",
				}
			}
		}
		return connectors.TestStep{Status: connectors.StepOK, Detail: cfg.ConnectionString}
	})
	if cfg.ConnectionString == "" {
		return r.Result()
	}
	timeout := time.Duration(cfg.ConnectTimeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()

	var cl cluster
	r.Run("bootstrap", "Connect and authenticate", func() connectors.TestStep {
		var err error
		if cl, err = c.open(ctx, cfg, opts, timeout); err != nil {
			ce := classify(err)
			if ce.Kind == connectors.KindUnreachable && c.probe.lookupSRV != nil && c.probe.credentialsRejected(ctx, cfg, creds) {
				ce = &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindAuth, Cause: err}
			}
			return failStep(ce, cfg)
		}
		return connectors.TestStep{Status: connectors.StepOK}
	})
	if cl != nil {
		defer func() { _ = cl.Close() }()
	}

	caps := Capabilities{Deployment: cfg.Deployment}
	r.Run("services", "Services", func() connectors.TestStep {
		svc, err := cl.Services(ctx)
		if err != nil {
			return failStep(classify(err), cfg)
		}
		kv, query := svc["kv"], svc["query"]
		caps.QueryService = query.OK
		switch {
		case !kv.OK:
			return connectors.TestStep{Status: connectors.StepFail, Code: "TARGET_UNREACHABLE", Detail: "The Key-Value (data) service isn't reachable.", Hint: capellaIPHint}
		case !query.OK:
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "Key-Value OK, but the Query service isn't reachable: document sampling and verification queries won't be available."}
		}
		return connectors.TestStep{Status: connectors.StepOK, Detail: fmt.Sprintf("Key-Value OK · Query %d ms", query.Latency)}
	})
	r.Run("version", "Server version", func() connectors.TestStep {
		v, err := cl.Version(ctx)
		if err != nil {
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "Couldn't read the server version: " + err.Error()}
		}
		major, minor := parseVersion(v)
		caps.RangeScan = major > 7 || (major == 7 && minor >= 6)
		switch {
		case major < 7:
			return connectors.TestStep{
				Status: connectors.StepFail, Code: CodeVersionUnsupported, Detail: "Couchbase Server " + v,
				Hint: "Omni-Data-Bus needs Couchbase Server 7.0 or later (scopes and collections).",
			}
		case !caps.RangeScan:
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "Couchbase Server " + v + ": 7.6+ adds range-scan sampling of existing documents."}
		}
		return connectors.TestStep{Status: connectors.StepOK, Detail: "Couchbase Server " + v}
	})
	r.Run("permissions", "Permissions", func() connectors.TestStep {
		roles, err := cl.MyRoles(ctx)
		if err != nil {
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "Couldn't read this credential's roles: " + err.Error()}
		}
		probe := CapabilitiesFromRoles(roles, cfg.Deployment)
		probe.QueryService, probe.RangeScan = caps.QueryService, caps.RangeScan
		caps = probe
		if !caps.CanWriteDocs {
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "This credential can't write documents. Grant Read/Write (data_writer) access to the target bucket."}
		}
		return connectors.TestStep{Status: connectors.StepOK, Detail: roleSummary(roles)}
	})
	res := r.Result()
	res.Capabilities, _ = json.Marshal(caps)
	return res
}

func roleSummary(roles []Role) string {
	names := make([]string, 0, len(roles))
	seen := map[string]bool{}
	for _, r := range roles {
		label := r.Role
		if r.Bucket != "" {
			label += "[" + r.Bucket + "]"
		}
		if !seen[label] {
			seen[label] = true
			names = append(names, label)
		}
	}
	if len(names) > 6 {
		names = append(names[:6], fmt.Sprintf("+%d more", len(names)-6))
	}
	return strings.Join(names, ", ")
}

// classify maps SDK errors to connector error kinds.
func classify(err error) *connectors.ConnError {
	switch {
	case errors.Is(err, gocb.ErrAuthenticationFailure):
		return &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindAuth, Cause: err}
	case errors.Is(err, gocb.ErrUnambiguousTimeout), errors.Is(err, gocb.ErrTimeout), errors.Is(err, gocb.ErrAmbiguousTimeout),
		errors.Is(err, context.DeadlineExceeded):
		return &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindUnreachable, Cause: err}
	case errors.Is(err, gocb.ErrInvalidArgument):
		return &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindUnknown, Cause: err}
	}
	return connectors.Classify(connectors.RoleTarget, err)
}

func failStep(ce *connectors.ConnError, cfg Config) connectors.TestStep {
	hint := ce.Hint()
	if cfg.Deployment == Capella && (ce.Kind == connectors.KindUnreachable || ce.Kind == connectors.KindTimeout) {
		hint += " " + capellaIPHint
	}
	return connectors.TestStep{Status: connectors.StepFail, Code: ce.Code(), Detail: ce.Cause.Error(), Hint: hint}
}
