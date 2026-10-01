// Package config loads and validates Omni-Data-Bus's runtime configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"

	"omnidatabus/internal/netguard"
)

// DevMasterKey is a fixed key used only when OC_DEV=true. Never use it outside local development.
const DevMasterKey = "ZGV2LW9ubHktbWFzdGVyLWtleS1kby1ub3QtdXNlISE=" // base64("dev-only-master-key-do-not-use!!")

// Config is the complete runtime configuration.
type Config struct {
	Dev       bool   `env:"OC_DEV"`
	Role      string `env:"OC_ROLE" envDefault:"all" validate:"oneof=all api worker"`
	HTTPAddr  string `env:"OC_HTTP_ADDR" envDefault:":8080"`
	PublicURL string `env:"OC_PUBLIC_URL" envDefault:"http://localhost:5173" validate:"url"`
	LogLevel  string `env:"OC_LOG_LEVEL" envDefault:"info" validate:"oneof=debug info warn error"`
	LogFormat string `env:"OC_LOG_FORMAT" envDefault:"json" validate:"oneof=json text"`

	// MasterKey (or MasterKeys + MasterKeyActive) is the key-encryption key for stored credentials (OC-006).
	MasterKey       string `env:"OC_MASTER_KEY"`
	MasterKeys      string `env:"OC_MASTER_KEYS"`
	MasterKeyActive string `env:"OC_MASTER_KEY_ACTIVE"`

	Meta    MetaConfig
	Session SessionConfig
	Worker  WorkerConfig
	Observe ObservabilityConfig
	Guard   SecurityConfig

	SignupEnabled bool `env:"OC_SIGNUP_ENABLED" envDefault:"true"`
	// ExposeBuildInfo shows the release and channel to anonymous callers of /api/v1/version
	// (the sign-in page footer, OC-058); signed-in users always see the full build.
	ExposeBuildInfo bool          `env:"OC_EXPOSE_BUILD_INFO" envDefault:"true"`
	ShutdownTimeout time.Duration `env:"OC_SHUTDOWN_TIMEOUT" envDefault:"30s"`
}

// MetaConfig points at the Couchbase bucket that stores Omni-Data-Bus's own data (OC-003).
type MetaConfig struct {
	ConnStr       string `env:"OC_META_CONNSTR"`
	Username      string `env:"OC_META_USERNAME"`
	Password      string `env:"OC_META_PASSWORD"`
	Bucket        string `env:"OC_META_BUCKET" envDefault:"omniconnect" validate:"required"`
	Scope         string `env:"OC_META_SCOPE" envDefault:"meta" validate:"required"`
	Capella       bool   `env:"OC_META_CAPELLA"`
	TLSSkipVerify bool   `env:"OC_META_TLS_SKIP_VERIFY"`
	AutoProvision bool   `env:"OC_META_AUTO_PROVISION" envDefault:"true"`
}

// WorkerConfig tunes migration workers (OC-023…OC-026).
type WorkerConfig struct {
	MaxJobs        int           `env:"OC_WORKER_MAX_JOBS" envDefault:"2" validate:"min=1,max=32"`
	MemoryBudgetMB int           `env:"OC_WORKER_MEMORY_BUDGET_MB" envDefault:"256" validate:"min=32"`
	ErrorsMaxRows  int64         `env:"OC_JOB_ERRORS_MAX_ROWS" envDefault:"100000" validate:"min=0"`
	ErrorRetention time.Duration `env:"OC_JOB_ERRORS_RETENTION" envDefault:"720h"`
	// MaxRelationDepth limits how deep mapping relations nest (OC-037; hard cap 6).
	MaxRelationDepth int `env:"OC_MAX_RELATION_DEPTH" envDefault:"4" validate:"min=1,max=6"`
	// RelationMemoryMB caps a batch's related rows in memory; bigger batches are split (OC-043).
	RelationMemoryMB int `env:"OC_RELATION_MEMORY_MB" envDefault:"256" validate:"min=1,max=65536"`
}

// ObservabilityConfig places /metrics and turns on tracing (OC-031).
type ObservabilityConfig struct {
	// MetricsAddr serves /metrics on its own listener (e.g. ":9090") instead of the public port.
	MetricsAddr string `env:"OC_METRICS_ADDR"`
	// PprofAddr serves net/http/pprof on its own listener (e.g. "127.0.0.1:6060") for benchmark
	// runs (OC-034); empty turns profiling off. Never expose it publicly.
	PprofAddr string `env:"OC_PPROF_ADDR"`
	// OTLPEndpoint is the OTLP/HTTP collector URL; empty turns tracing off.
	OTLPEndpoint string  `env:"OC_OTEL_EXPORTER_OTLP_ENDPOINT" validate:"omitempty,url"`
	SampleRatio  float64 `env:"OC_OTEL_SAMPLE_RATIO" envDefault:"0.1" validate:"min=0,max=1"`
}

// SecurityConfig holds the egress policy and audit retention (OC-033).
type SecurityConfig struct {
	// AllowPrivateNetworks permits connections to loopback, RFC 1918, CGNAT and ULA addresses:
	// true for self-hosted installs, false for a multi-tenant service.
	AllowPrivateNetworks bool   `env:"OC_ALLOW_PRIVATE_NETWORKS" envDefault:"true"`
	EgressAllowCIDRs     string `env:"OC_EGRESS_ALLOW_CIDRS"`
	EgressDenyCIDRs      string `env:"OC_EGRESS_DENY_CIDRS"`
	// EgressPorts replaces the default port list, e.g. "5432-5439,6432,11207,18091-18097,443".
	EgressPorts        string `env:"OC_EGRESS_PORTS"`
	AuditRetentionDays int    `env:"OC_AUDIT_RETENTION_DAYS" envDefault:"365" validate:"min=1,max=3650"`
}

// EgressPolicy builds the policy for connections to users' databases.
func (s SecurityConfig) EgressPolicy() (netguard.Policy, error) {
	allow, err := netguard.ParseCIDRs(s.EgressAllowCIDRs)
	if err != nil {
		return netguard.Policy{}, fmt.Errorf("OC_EGRESS_ALLOW_CIDRS: %w", err)
	}
	deny, err := netguard.ParseCIDRs(s.EgressDenyCIDRs)
	if err != nil {
		return netguard.Policy{}, fmt.Errorf("OC_EGRESS_DENY_CIDRS: %w", err)
	}
	ports, err := netguard.ParsePorts(s.EgressPorts)
	if err != nil {
		return netguard.Policy{}, fmt.Errorf("OC_EGRESS_PORTS: %w", err)
	}
	return netguard.Policy{AllowPrivateNetworks: s.AllowPrivateNetworks, Allow: allow, Deny: deny, Ports: ports}, nil
}

// SessionConfig controls login sessions (OC-005).
type SessionConfig struct {
	IdleTTL      time.Duration `env:"OC_SESSION_IDLE_TTL" envDefault:"12h"`
	AbsoluteTTL  time.Duration `env:"OC_SESSION_ABSOLUTE_TTL" envDefault:"168h"`
	CookieSecure bool          `env:"OC_SESSION_COOKIE_SECURE" envDefault:"true"`
}

// Load reads configuration from the given environment (nil means the process environment).
func Load(environ map[string]string) (Config, error) {
	if environ == nil {
		environ = env.ToMap(os.Environ())
	}
	environ, err := readSecretFiles(environ)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: environ}); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if cfg.Dev {
		applyDevDefaults(&cfg)
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// SecretFileVars can be given as <NAME>_FILE: a path whose content is the value (Docker and
// Kubernetes secrets mounted as files, OC-032).
var SecretFileVars = []string{"OC_MASTER_KEY", "OC_MASTER_KEYS", "OC_META_PASSWORD"}

func readSecretFiles(environ map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(environ))
	maps.Copy(out, environ)
	for _, name := range SecretFileVars {
		path := environ[name+"_FILE"]
		if path == "" {
			continue
		}
		if environ[name] != "" {
			return nil, fmt.Errorf("config: set %s or %s_FILE, not both", name, name)
		}
		b, err := os.ReadFile(path) //nolint:gosec // an operator-chosen secret file
		if err != nil {
			return nil, fmt.Errorf("config: %s_FILE: %w", name, err)
		}
		out[name] = strings.TrimSpace(string(b))
	}
	return out, nil
}

func applyDevDefaults(cfg *Config) {
	// Dev fallbacks only apply to the local dev stack. For a remote cluster (e.g. Capella)
	// credentials and the master key must be real, so a blank value is reported as missing
	// instead of silently becoming Administrator/password or the published dev key.
	local := cfg.Meta.ConnStr == "" || IsLocalConnStr(cfg.Meta.ConnStr)
	setDefault(&cfg.Meta.ConnStr, DefaultMetaConnStr)
	if local {
		setDefault(&cfg.Meta.Username, DefaultMetaUsername)
		setDefault(&cfg.Meta.Password, DefaultMetaPassword)
		if cfg.MasterKey == "" && cfg.MasterKeys == "" {
			cfg.MasterKey = DevMasterKey
		}
	}
	// Local dev runs over plain http, so the Secure cookie flag would break sign-in.
	cfg.Session.CookieSecure = false
}

func setDefault(field *string, value string) {
	if *field == "" {
		*field = value
	}
}

func validate(cfg Config) error {
	var missing []string
	required := map[string]string{
		"OC_META_CONNSTR":  cfg.Meta.ConnStr,
		"OC_META_USERNAME": cfg.Meta.Username,
		"OC_META_PASSWORD": cfg.Meta.Password,
	}
	for _, name := range []string{"OC_META_CONNSTR", "OC_META_USERNAME", "OC_META_PASSWORD"} {
		if required[name] == "" {
			missing = append(missing, name)
		}
	}
	if cfg.MasterKey == "" && cfg.MasterKeys == "" {
		missing = append(missing, "OC_MASTER_KEY")
	}
	if len(missing) > 0 {
		hint := "set OC_DEV=true for local development defaults"
		if cfg.Dev {
			hint = "development defaults only apply to a local cluster; set them in .env for " + cfg.Meta.ConnStr
		}
		return fmt.Errorf("config: missing required environment variables: %s (%s)", strings.Join(missing, ", "), hint)
	}
	if _, err := cfg.Guard.EgressPolicy(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.MasterKey == DevMasterKey && !IsLocalConnStr(cfg.Meta.ConnStr) {
		return errors.New("config: OC_MASTER_KEY is the published development key; generate a real one for a remote metadata store (openssl rand -base64 32)")
	}
	if err := validator.New().Struct(cfg); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			var parts []string
			for _, fe := range verrs {
				parts = append(parts, fmt.Sprintf("%s (%s)", fe.Namespace(), fe.Tag()))
			}
			return fmt.Errorf("config: invalid values: %s", strings.Join(parts, ", "))
		}
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// IsLocalConnStr reports whether every host in a Couchbase connection string is this machine
// or the dev compose service.
func IsLocalConnStr(connStr string) bool {
	_, rest, ok := strings.Cut(connStr, "://")
	if !ok {
		rest = connStr
	}
	hosts, _, _ := strings.Cut(rest, "?")
	hosts, _, _ = strings.Cut(hosts, "/")
	if hosts == "" {
		return false
	}
	for _, h := range strings.Split(hosts, ",") {
		h = strings.TrimSpace(h)
		if strings.HasPrefix(h, "[") { // [::1]:11210
			h = strings.TrimPrefix(strings.SplitN(h, "]", 2)[0], "[")
		} else if host, _, found := strings.Cut(h, ":"); found {
			h = host
		}
		switch strings.ToLower(h) {
		case "localhost", "127.0.0.1", "::1", "couchbase":
		default:
			return false
		}
	}
	return true
}

const redacted = "***"

// Redacted returns a copy that is safe to log.
func (c Config) Redacted() Config {
	r := c
	if r.MasterKey != "" {
		r.MasterKey = redacted
	}
	if r.MasterKeys != "" {
		r.MasterKeys = redacted
	}
	if r.Meta.Password != "" {
		r.Meta.Password = redacted
	}
	return r
}
