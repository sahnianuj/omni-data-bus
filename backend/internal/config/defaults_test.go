package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type composeFile struct {
	Services map[string]struct {
		Image       string            `yaml:"image"`
		Ports       []string          `yaml:"ports"`
		Environment map[string]string `yaml:"environment"`
	} `yaml:"services"`
}

func loadDevCompose(t *testing.T) composeFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "compose", "dev.yml"))
	require.NoError(t, err)
	var c composeFile
	require.NoError(t, yaml.Unmarshal(raw, &c))
	return c
}

func TestConfig_AC1_DefaultsMatchDevCompose(t *testing.T) {
	c := loadDevCompose(t)

	cb, ok := c.Services["couchbase"]
	require.True(t, ok, "dev compose must define a couchbase service")
	require.Equal(t, "couchbase/server:enterprise-8.0.3", cb.Image)
	ports := strings.Join(cb.Ports, " ")
	require.Contains(t, ports, DevKVPort+":"+DevKVPort, "KV port must be published for the SDK")
	require.Contains(t, ports, "8091-8097:8091-8097", "management/query ports must be published")

	initSvc, ok := c.Services["couchbase-init"]
	require.True(t, ok)
	require.Equal(t, DefaultMetaBucket, initSvc.Environment["META_BUCKET"])
	require.Equal(t, DefaultMetaUsername, initSvc.Environment["CB_USERNAME"])
	require.Equal(t, DefaultMetaPassword, initSvc.Environment["CB_PASSWORD"])
	require.Equal(t, "couchbase://localhost", DefaultMetaConnStr)
}
