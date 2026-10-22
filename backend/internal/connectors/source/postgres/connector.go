package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
)

// session is the subset of a live connection the connector uses (faked in tests).
type session interface {
	source.Session
}

// Connector implements source.Connector for PostgreSQL.
type Connector struct {
	lookupHost func(ctx context.Context, host string) ([]string, error)
	dialTCP    func(ctx context.Context, addr string) (net.Conn, error)
	connect    func(ctx context.Context, cc *pgx.ConnConfig) (session, error)
	now        func() time.Time
}

// New returns the PostgreSQL connector.
func New() *Connector {
	d := &net.Dialer{}
	return &Connector{
		lookupHost: net.DefaultResolver.LookupHost,
		dialTCP:    func(ctx context.Context, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) },
		connect:    connectPgx,
		now:        time.Now,
	}
}

// Descriptor implements source.Connector.
func (*Connector) Descriptor() connectors.Descriptor {
	return connectors.Descriptor{
		Type: "postgres", Role: connectors.RoleSource, DisplayName: "PostgreSQL", Icon: "postgres",
		Capabilities: connectors.Capabilities{SnapshotExport: true, KeysetPagination: true, RangePartitioning: true, RowEstimate: true},
	}
}

// Validate implements source.Connector.
func (*Connector) Validate(cfg json.RawMessage) error {
	_, err := ParseConfig(cfg)
	return err
}

// Normalize returns the validated config with defaults applied, and its default display name.
func (*Connector) Normalize(cfg json.RawMessage) (json.RawMessage, string, error) {
	c, err := ParseConfig(cfg)
	if err != nil {
		return nil, "", err
	}
	return c.JSON(), c.DefaultName(), nil
}

// Open implements source.Connector.
func (p *Connector) Open(ctx context.Context, cfg json.RawMessage, creds connectors.Credentials) (source.Session, error) {
	c, err := ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	return p.open(ctx, c, creds)
}

func (p *Connector) open(ctx context.Context, c Config, creds connectors.Credentials) (source.Session, error) {
	cc, err := buildConnConfig(c, creds)
	if err != nil {
		return nil, err
	}
	s, err := p.connect(ctx, cc)
	if err != nil {
		return nil, connectors.Classify(connectors.RoleSource, err)
	}
	return s, nil
}

// Test implements connectors.Tester: resolve → connect → authenticate → version → privileges.
func (p *Connector) Test(ctx context.Context, cfg json.RawMessage, creds connectors.Credentials) connectors.TestResult {
	r := connectors.NewStepRunner(p.now)
	c, err := ParseConfig(cfg)
	if err != nil {
		r.Run("config", "Check configuration", func() connectors.TestStep { return failStep(err) })
		return r.Result()
	}
	// The test decides within ReachTimeout (OC-190); a run keeps the configured connect timeout.
	c.ConnectTimeoutSec = min(c.ConnectTimeoutSec, int(connectors.ReachTimeout/time.Second))
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.ConnectTimeoutSec+5)*time.Second)
	defer cancel()

	r.Run("resolve", "Resolve host", func() connectors.TestStep {
		if ip := net.ParseIP(strings.Trim(c.Host, "[]")); ip != nil {
			return connectors.TestStep{Status: connectors.StepOK, Detail: ip.String()}
		}
		rctx, cancel := context.WithTimeout(ctx, connectors.ReachTimeout)
		defer cancel()
		addrs, err := p.lookupHost(rctx, c.Host)
		if err != nil {
			return failStep(connectors.Classify(connectors.RoleSource, connectors.NewReachError(address(c), err)))
		}
		return connectors.TestStep{Status: connectors.StepOK, Detail: strings.Join(addrs, ", ")}
	})
	r.Run("connect", openLabel(c), func() connectors.TestStep {
		dial := func(ctx context.Context, _, addr string) (net.Conn, error) { return p.dialTCP(ctx, addr) }
		if err := connectors.Reach(ctx, dial, []string{address(c)}); err != nil {
			return failStep(connectors.Classify(connectors.RoleSource, err))
		}
		return connectors.TestStep{Status: connectors.StepOK}
	})

	var sess source.Session
	r.Run("auth", "Authenticate as "+c.Username, func() connectors.TestStep {
		var err error
		sess, err = p.open(ctx, c, creds)
		if err != nil {
			return failStep(err)
		}
		return connectors.TestStep{Status: connectors.StepOK}
	})
	if sess != nil {
		defer func() { _ = sess.Close() }()
	}
	r.Run("version", "Server version", func() connectors.TestStep {
		info, err := sess.Ping(ctx)
		if err != nil {
			return failStep(connectors.Classify(connectors.RoleSource, err))
		}
		return connectors.TestStep{Status: connectors.StepOK, Detail: strings.TrimSpace(info.Product + " " + info.Version)}
	})
	r.Run("privileges", "Read privileges", func() connectors.TestStep {
		rep, err := sess.CheckPrivileges(ctx)
		if err != nil {
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "Couldn't check privileges: " + err.Error()}
		}
		return privilegeStep(rep, c.Username)
	})
	return r.Result()
}

func openLabel(c Config) string {
	if c.SSLMode == "disable" {
		return "Open TCP connection"
	}
	return "Open TLS connection"
}

// failStep is a failed step in plain words: what happened in Detail, what to do in Hint, and
// the driver's own message in Cause (OC-190).
func failStep(err error) connectors.TestStep {
	s := connectors.TestStep{Status: connectors.StepFail, Detail: err.Error()}
	ce, ok := err.(*connectors.ConnError)
	if !ok {
		return s
	}
	s.Code, s.Hint, s.Cause = ce.Code(), ce.Hint(), ce.Cause.Error()
	var re *connectors.ReachError
	switch {
	case errors.As(ce.Cause, &re):
		s.Detail, s.Cause = re.Message(), re.Cause.Error()
	case ce.Kind == connectors.KindAuth:
		s.Detail = "PostgreSQL rejected the username or password."
	case ce.Kind == connectors.KindDatabaseNotFound:
		s.Detail = "The database doesn't exist on this server."
	case ce.Kind == connectors.KindTimeout:
		s.Detail = "PostgreSQL didn't finish connecting within " + connectors.ReachTimeout.String() + "."
	default:
		s.Detail = ce.Title() + "."
	}
	return s
}

// privilegeStep turns a report into a warning with a ready-to-run GRANT hint, or OK.
func privilegeStep(rep source.PrivilegeReport, user string) connectors.TestStep {
	switch {
	case len(rep.UnreadableTables) > 0:
		bySchema := map[string]int{}
		for _, t := range rep.UnreadableTables {
			schema, _, _ := strings.Cut(t, ".")
			bySchema[schema]++
		}
		schemas := make([]string, 0, len(bySchema))
		for s := range bySchema {
			schemas = append(schemas, s)
		}
		sort.Slice(schemas, func(i, j int) bool {
			if bySchema[schemas[i]] != bySchema[schemas[j]] {
				return bySchema[schemas[i]] > bySchema[schemas[j]]
			}
			return schemas[i] < schemas[j]
		})
		top := schemas[0]
		detail := fmt.Sprintf("No SELECT on %d table(s)", len(rep.UnreadableTables))
		if len(schemas) == 1 {
			detail += fmt.Sprintf(" in schema '%s'", top)
		} else {
			detail += fmt.Sprintf(" across %d schemas (most in '%s')", len(schemas), top)
		}
		detail += ". These tables won't be available to migrate."
		q := quoteIdent
		hint := fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s; GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s;", q(top), q(user), q(top), q(user))
		return connectors.TestStep{Status: connectors.StepWarn, Detail: detail, Hint: hint}
	case rep.Superuser:
		return connectors.TestStep{
			Status: connectors.StepWarn, Detail: "Connected as a superuser. A read-only role is safer for migrations.",
			Hint: "CREATE ROLE omni_ro LOGIN PASSWORD '…'; GRANT pg_read_all_data TO omni_ro;",
		}
	case rep.CanWrite:
		return connectors.TestStep{Status: connectors.StepWarn, Detail: "This user can modify data. A read-only role is safer for migrations."}
	}
	return connectors.TestStep{Status: connectors.StepOK, Detail: "Can read every table"}
}

// quoteIdent quotes an identifier only when needed, so hints stay readable.
func quoteIdent(s string) string {
	simple := s != ""
	for i, r := range s {
		if r != '_' && (r < 'a' || r > 'z') && (i == 0 || r < '0' || r > '9') {
			simple = false
			break
		}
	}
	if simple {
		return s
	}
	return pgx.Identifier{s}.Sanitize()
}
