package export

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// RunConfigFile is the name of the run's config file next to migrate.sh (OC-155).
const RunConfigFile = "config.json"

// SecretPrefix marks a base64-encoded secret in the run config (OC-156). A value without it is
// plain text, encoded on the next run: plain base64 can't be told from a password ("abcd1234").
const SecretPrefix = "base64:"

// secretKeys are the connection fields that hold secrets (connections.Secrets).
var secretKeys = []string{"password", "caCertPem", "clientCertPem", "clientKeyPem"}

// CodeRunConfigInvalid is a run config the runner can't use (exit code 4).
const CodeRunConfigInvalid = "RUN_CONFIG_INVALID"

// RunConnection is one section of the run config: a connection's kind, settings and secrets
// (in clear text in memory, base64-encoded in the file).
type RunConnection struct {
	Type    string
	Config  map[string]json.RawMessage
	Secrets Secrets
}

// RunConfig is config.json: the source and target connection details a run uses instead of
// the bundle's, so one export runs against any environment (OC-155). A nil section keeps the
// bundle's connection.
type RunConfig struct {
	Source *RunConnection
	Target *RunConnection
}

// NewRunConnection makes a section from a bundle connection's config and its secrets.
func NewRunConnection(c Connection, s Secrets) (*RunConnection, error) {
	cfg := map[string]json.RawMessage{}
	if len(c.Config) > 0 {
		if err := json.Unmarshal(c.Config, &cfg); err != nil {
			return nil, err
		}
	}
	return &RunConnection{Type: c.Type, Config: cfg, Secrets: s}, nil
}

// connection is the section as a bundle connection (settings only, no secrets), for display;
// its type defaults to def's.
func (rc *RunConnection) connection(def Connection) Connection {
	raw, _ := json.Marshal(rc.Config)
	typ := rc.Type
	if typ == "" {
		typ = def.Type
	}
	return Connection{Type: typ, Name: def.Name, Config: raw}
}

// EncodeRunConfig writes config.json: per section, its type, settings (sorted) and secrets, each
// secret as "base64:…".
func EncodeRunConfig(rc RunConfig) ([]byte, error) {
	var sections []string
	for _, s := range []struct {
		name string
		conn *RunConnection
	}{{"source", rc.Source}, {"target", rc.Target}} {
		if s.conn == nil {
			continue
		}
		var fields []string
		field := func(k string, v []byte) {
			name, _ := json.Marshal(k)
			fields = append(fields, string(name)+":"+string(v))
		}
		if s.conn.Type != "" {
			typ, _ := json.Marshal(s.conn.Type)
			field("type", typ)
		}
		keys := make([]string, 0, len(s.conn.Config))
		for k := range s.conn.Config {
			if k != "type" && !isSecretKey(k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			field(k, s.conn.Config[k])
		}
		for _, k := range secretKeys {
			if v, ok := s.conn.Secrets[k]; ok {
				enc, _ := json.Marshal(EncodeSecret(v))
				field(k, enc)
			}
		}
		sections = append(sections, `"`+s.name+`":{`+strings.Join(fields, ",")+"}")
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte("{"+strings.Join(sections, ",")+"}"), "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

// EncodeSecret is how a secret is kept in the run config; "" stays empty (no secret).
func EncodeSecret(v string) string {
	if v == "" {
		return ""
	}
	return SecretPrefix + base64.StdEncoding.EncodeToString([]byte(v))
}

// ParseRunConfig reads config.json. Plain lists the secrets that were in plain text
// ("source.password"), which the caller writes back encoded (EncodeRunConfig).
func ParseRunConfig(raw []byte) (rc RunConfig, plain []string, err error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return RunConfig{}, nil, runConfigErr("not valid JSON: " + err.Error())
	}
	for name := range top {
		if name != "source" && name != "target" {
			return RunConfig{}, nil, runConfigErr(fmt.Sprintf("unknown section %q (use source and target)", name))
		}
	}
	for _, s := range []struct {
		name string
		dst  **RunConnection
	}{{"source", &rc.Source}, {"target", &rc.Target}} {
		sec, ok := top[s.name]
		if !ok || string(sec) == "null" {
			continue
		}
		conn, p, err := parseRunConnection(s.name, sec)
		if err != nil {
			return RunConfig{}, nil, err
		}
		*s.dst = conn
		plain = append(plain, p...)
	}
	return rc, plain, nil
}

func parseRunConnection(section string, raw json.RawMessage) (*RunConnection, []string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, nil, runConfigErr(section + " must be an object of connection settings")
	}
	conn := &RunConnection{Config: map[string]json.RawMessage{}, Secrets: Secrets{}}
	var plain []string
	for k, v := range fields {
		switch {
		case k == "type":
			if err := json.Unmarshal(v, &conn.Type); err != nil {
				return nil, nil, runConfigErr(section + ".type must be a string")
			}
		case isSecretKey(k):
			var s *string
			if err := json.Unmarshal(v, &s); err != nil {
				return nil, nil, runConfigErr(section + "." + k + " must be a string")
			}
			if s == nil || *s == "" {
				continue
			}
			enc, ok := strings.CutPrefix(*s, SecretPrefix)
			if !ok {
				conn.Secrets[k] = *s
				plain = append(plain, section+"."+k)
				continue
			}
			dec, err := base64.StdEncoding.DecodeString(enc)
			if err != nil {
				return nil, nil, runConfigErr(section + "." + k + ` starts with "base64:" but the rest isn't valid base64 (type the value in plain text to have it encoded)`)
			}
			conn.Secrets[k] = string(dec)
		default:
			conn.Config[k] = v
		}
	}
	sort.Strings(plain)
	return conn, plain, nil
}

func isSecretKey(k string) bool { return slices.Contains(secretKeys, k) }

// runConfigErr is a problem with the run config; the caller names the file.
func runConfigErr(msg string) *Error { return &Error{Code: CodeRunConfigInvalid, Message: msg} }
