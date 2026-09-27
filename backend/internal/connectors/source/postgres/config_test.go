package postgres

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func cfgJSON(over map[string]any) json.RawMessage {
	base := map[string]any{"host": "db.example.com", "database": "shop", "username": "omni_ro"}
	for k, v := range over {
		if v == nil {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	b, _ := json.Marshal(base)
	return b
}

func TestPostgresConfig_AC1_Validation(t *testing.T) {
	cases := map[string]struct {
		over  map[string]any
		field string
	}{
		"port zero is defaulted not invalid": {map[string]any{"port": 0}, ""},
		"port too high":                      {map[string]any{"port": 70000}, "config.port"},
		"empty host":                         {map[string]any{"host": ""}, "config.host"},
		"bad host":                           {map[string]any{"host": "db example.com"}, "config.host"},
		"unknown sslMode":                    {map[string]any{"sslMode": "sometimes"}, "config.sslMode"},
		"missing database":                   {map[string]any{"database": nil}, "config.database"},
		"missing username":                   {map[string]any{"username": nil}, "config.username"},
		"bad option":                         {map[string]any{"options": map[string]string{"host": "evil"}}, "config.options.host"},
		"bad option value":                   {map[string]any{"options": map[string]string{"statement_timeout": "1; DROP"}}, "config.options.statement_timeout"},
		"ok ipv6":                            {map[string]any{"host": "::1"}, ""},
		"ok option":                          {map[string]any{"options": map[string]string{"search_path": "shop, public"}}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig(cfgJSON(c.over))
			if c.field == "" {
				require.NoError(t, err)
				return
			}
			p := problem.From(err)
			require.Equal(t, 422, p.Status)
			require.Equal(t, c.field, p.FieldErrors[0].Field)
		})
	}
	_, err := ParseConfig(json.RawMessage(`{"host":"h","database":"d","username":"u","dsn":"x"}`))
	require.Equal(t, 422, problem.From(err).Status, "unknown fields rejected")
}

func TestPostgresConfig_AC1_Defaults(t *testing.T) {
	c, err := ParseConfig(cfgJSON(nil))
	require.NoError(t, err)
	require.Equal(t, 5432, c.Port)
	require.Equal(t, "require", c.SSLMode)
	require.Equal(t, 10, c.ConnectTimeoutSec)
	require.Equal(t, "omni-data-bus", c.ApplicationName)
	require.Equal(t, "shop@db.example.com", c.DefaultName())
	var round Config
	require.NoError(t, json.Unmarshal(c.JSON(), &round))
	require.Equal(t, c, round)
}
