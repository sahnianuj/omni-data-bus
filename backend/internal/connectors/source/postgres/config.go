// Package postgres is the PostgreSQL source connector (OC-009).
package postgres

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"omnidatabus/internal/problem"
)

// Config is the non-secret PostgreSQL connection configuration (stored as JSON).
type Config struct {
	Host              string            `json:"host" validate:"required,max=253"`
	Port              int               `json:"port" validate:"min=1,max=65535"`
	Database          string            `json:"database" validate:"required,max=63"`
	Username          string            `json:"username" validate:"required,max=63"`
	SSLMode           string            `json:"sslMode" validate:"oneof=disable prefer require verify-ca verify-full"`
	ApplicationName   string            `json:"applicationName" validate:"max=63"`
	ConnectTimeoutSec int               `json:"connectTimeoutSec" validate:"min=1,max=60"`
	Options           map[string]string `json:"options,omitempty"`
}

// Defaults.
const (
	DefaultPort    = 5432
	DefaultSSLMode = "require"
	DefaultAppName = "omni-data-bus"
	DefaultTimeout = 10
)

// allowedOptions are the only extra runtime parameters a user may set.
var allowedOptions = map[string]*regexp.Regexp{
	"search_path":       regexp.MustCompile(`^[A-Za-z0-9_$, "]{1,256}$`),
	"statement_timeout": regexp.MustCompile(`^[0-9]{1,9}(ms|s|min)?$`),
}

var hostPattern = regexp.MustCompile(`^(\[[0-9a-fA-F:.]+\]|[0-9a-fA-F:]+|[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?)$`)

var validate = problem.NewValidator()

// ParseConfig decodes, applies defaults and validates.
func ParseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fieldProblem("config", "is not valid PostgreSQL configuration: "+err.Error())
	}
	c.Host = strings.TrimSpace(c.Host)
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.SSLMode == "" {
		c.SSLMode = DefaultSSLMode
	}
	if c.ApplicationName == "" {
		c.ApplicationName = DefaultAppName
	}
	if c.ConnectTimeoutSec == 0 {
		c.ConnectTimeoutSec = DefaultTimeout
	}
	if err := validate.Struct(c); err != nil {
		return Config{}, prefixFields(err)
	}
	if !hostPattern.MatchString(c.Host) {
		return Config{}, fieldProblem("config.host", "must be a host name or IP address")
	}
	for k, v := range c.Options {
		re, ok := allowedOptions[k]
		if !ok {
			return Config{}, fieldProblem("config.options."+k, "is not an allowed option (allowed: search_path, statement_timeout)")
		}
		if !re.MatchString(v) {
			return Config{}, fieldProblem("config.options."+k, "has an invalid value")
		}
	}
	return c, nil
}

// JSON returns the normalized config for storage.
func (c Config) JSON() json.RawMessage {
	b, _ := json.Marshal(c)
	return b
}

func fieldProblem(field, msg string) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "One or more fields are invalid.")
	p.FieldErrors = []problem.FieldError{{Field: field, Message: msg}}
	return p
}

func prefixFields(err error) error {
	p := problem.From(err)
	for i := range p.FieldErrors {
		p.FieldErrors[i].Field = "config." + p.FieldErrors[i].Field
	}
	return p
}

// DefaultName is "<database>@<host>".
func (c Config) DefaultName() string { return fmt.Sprintf("%s@%s", c.Database, c.Host) }
