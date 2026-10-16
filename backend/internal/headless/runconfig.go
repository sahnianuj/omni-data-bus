package headless

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"omnidatabus/internal/export"
	"omnidatabus/internal/store"
)

// LoadRunConfig reads the run config (odb run --config, OC-155). Secrets typed in plain text are
// encoded and the file written back in place, mode 0600 (OC-156); note names them, never their
// values.
func LoadRunConfig(path string, note func(string)) (*export.RunConfig, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the operator names the config file
	if err != nil {
		return nil, &ConfigError{Code: export.CodeRunConfigInvalid, Message: "Can't read the run config: " + err.Error()}
	}
	rc, plain, err := export.ParseRunConfig(raw)
	var be *export.Error
	if errors.As(err, &be) {
		return nil, &ConfigError{Code: be.Code, Message: path + ": " + be.Message + "."}
	}
	if err != nil {
		return nil, err
	}
	if len(plain) > 0 {
		enc, err := export.EncodeRunConfig(rc)
		if err != nil {
			return nil, err
		}
		if err := replaceFile(path, enc, 0o600); err != nil {
			return nil, &ConfigError{Code: export.CodeRunConfigInvalid, Message: "Can't save the run config with the encoded secrets: " + err.Error()}
		}
		note(fmt.Sprintf("%s: encoded %s as base64 and saved the file", path, strings.Join(plain, ", ")))
	}
	return &rc, nil
}

// replaceFile writes data to a temporary file beside path, then renames it over path, so a
// crash never leaves a half-written config.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // a no-op once renamed
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// connection lays the run config's section for role over the bundle's connection (OC-155): the
// settings it lists replace the bundle's, and its secrets replace the bundle's.
func (c *Config) connection(role string, conn export.Connection, secrets export.Secrets) (json.RawMessage, export.Secrets, error) {
	var sec *export.RunConnection
	if c.RunConfig != nil {
		sec = c.RunConfig.Source
		if role == "target" {
			sec = c.RunConfig.Target
		}
	}
	inConfig := conn.Credentials != nil && conn.Credentials.Mode == export.ModeConfig
	name := c.RunConfigPath
	if name == "" {
		name = export.RunConfigFile
	}
	if sec == nil {
		switch {
		case inConfig && c.RunConfig == nil:
			return nil, nil, &ConfigError{Code: export.CodeRunConfigInvalid, Message: "This bundle's " + role + " connection details are in its " +
				export.RunConfigFile + ": pass it with --config (migrate.sh does)."}
		case inConfig:
			return nil, nil, &ConfigError{Code: export.CodeRunConfigInvalid, Message: name + ": the " + role + " section is missing."}
		}
		return conn.Config, secrets, nil
	}
	if conn.Type == store.KindDirectory {
		return nil, nil, &ConfigError{Code: export.CodeRunConfigInvalid, Message: name + ": a directory target has no " + role +
			" section; set its folder with --target-path (ODB_TARGET_PATH for migrate.sh)."}
	}
	if sec.Type != "" && sec.Type != conn.Type {
		return nil, nil, &ConfigError{Code: export.CodeRunConfigInvalid, Message: fmt.Sprintf(
			"%s: %s.type is %q, but this migration's %s is %s. The config file changes a connection's details, not its kind.", name, role, sec.Type, role, conn.Type)}
	}
	merged := map[string]json.RawMessage{}
	if len(conn.Config) > 0 {
		if err := json.Unmarshal(conn.Config, &merged); err != nil {
			return nil, nil, err
		}
	}
	for k, v := range sec.Config {
		merged[k] = v
	}
	cfg, err := json.Marshal(merged)
	if err != nil {
		return nil, nil, err
	}
	if len(sec.Secrets) > 0 || inConfig {
		secrets = sec.Secrets
	}
	return cfg, secrets, nil
}
