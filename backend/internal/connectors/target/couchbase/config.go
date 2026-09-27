// Package couchbase is the Couchbase target connector (OC-012): self-managed clusters and Capella.
package couchbase

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"omnidatabus/internal/problem"
)

// Deployment kinds.
const (
	SelfManaged = "self-managed"
	Capella     = "capella"
)

// Config is the non-secret Couchbase connection configuration.
type Config struct {
	ConnectionString string `json:"connectionString" validate:"required,max=2048"`
	Username         string `json:"username" validate:"required,max=128"`
	Deployment       string `json:"deployment" validate:"oneof=self-managed capella"`
	TLSVerify        *bool  `json:"tlsVerify,omitempty"`
	KVTimeoutMs      int    `json:"kvTimeoutMs" validate:"min=100,max=60000"`
	ConnectTimeoutMs int    `json:"connectTimeoutMs" validate:"min=1000,max=120000"`
}

// VerifyTLS reports whether server certificates are verified (default true; always for Capella).
func (c Config) VerifyTLS() bool {
	return c.Deployment == Capella || c.TLSVerify == nil || *c.TLSVerify
}

// allowedParams are the only connection-string query parameters kept.
var allowedParams = map[string]bool{"network": true}

var validate = problem.NewValidator()

// ParseConfig decodes, defaults, validates and normalizes the connection string.
func ParseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fieldProblem("config", "is not valid Couchbase configuration: "+err.Error())
	}
	if c.Deployment == "" {
		c.Deployment = SelfManaged
	}
	if c.KVTimeoutMs == 0 {
		c.KVTimeoutMs = 2500
	}
	if c.ConnectTimeoutMs == 0 {
		c.ConnectTimeoutMs = 10000
	}
	c.ConnectionString = strings.TrimSpace(c.ConnectionString)
	c.Username = strings.TrimSpace(c.Username)
	if err := validate.Struct(c); err != nil {
		p := problem.From(err)
		for i := range p.FieldErrors {
			p.FieldErrors[i].Field = "config." + p.FieldErrors[i].Field
		}
		return Config{}, p
	}
	normalized, hosts, err := parseConnString(c.ConnectionString)
	if err != nil {
		return Config{}, err
	}
	if c.Deployment == Capella && !strings.HasPrefix(normalized, "couchbases://") {
		return Config{}, fieldProblem("config.connectionString", "must start with couchbases:// for Capella")
	}
	if len(hosts) == 0 {
		return Config{}, fieldProblem("config.connectionString", "must include at least one host")
	}
	c.ConnectionString = normalized
	return c, nil
}

// parseConnString validates "couchbase[s]://h1[:p],h2?network=external" and drops unknown params.
func parseConnString(s string) (string, []string, error) {
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok || (scheme != "couchbase" && scheme != "couchbases") {
		return "", nil, fieldProblem("config.connectionString", "must start with couchbase:// or couchbases://")
	}
	hostPart, query, _ := strings.Cut(rest, "?")
	hostPart = strings.TrimSuffix(hostPart, "/")
	var hosts []string
	for _, h := range strings.Split(hostPart, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, " /@") {
			return "", nil, fieldProblem("config.connectionString", "contains an invalid host: "+h)
		}
		hosts = append(hosts, h)
	}
	params, err := url.ParseQuery(query)
	if err != nil {
		return "", nil, fieldProblem("config.connectionString", "has invalid parameters")
	}
	kept := url.Values{}
	for k, v := range params {
		if allowedParams[k] {
			kept[k] = v
		}
	}
	out := scheme + "://" + strings.Join(hosts, ",")
	if len(kept) > 0 {
		out += "?" + kept.Encode()
	}
	return out, hosts, nil
}

// FirstHost is used for the default connection name.
func (c Config) FirstHost() string {
	_, rest, _ := strings.Cut(c.ConnectionString, "://")
	host, _, _ := strings.Cut(rest, "?")
	host, _, _ = strings.Cut(host, ",")
	return host
}

// JSON returns the normalized config.
func (c Config) JSON() json.RawMessage {
	b, _ := json.Marshal(c)
	return b
}

func fieldProblem(field, msg string) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "One or more fields are invalid.")
	p.FieldErrors = []problem.FieldError{{Field: field, Message: msg}}
	return p
}
