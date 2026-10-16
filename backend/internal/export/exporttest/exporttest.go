// Package exporttest builds bundles for tests of the bundle CLI and the headless runner.
package exporttest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnidatabus/internal/export"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/store"
)

// OrdersSpec maps public.orders (id, customer_id, status) to {id, customerId, status}.
const OrdersSpec = `{"specVersion":2,"mode":"canvas","docSchema":{"root":{"id":"root","name":"","type":"object","children":[` +
	`{"id":"f_id","name":"id","type":"integer","required":true},{"id":"f_cust","name":"customerId","type":"integer"},` +
	`{"id":"f_status","name":"status","type":"string"}]},"source":"generated"},"key":{"template":"orders::{id}"},` +
	`"typeField":{"enabled":true,"name":"type","value":"order"},"nullHandling":"include","fieldMappings":[` +
	`{"id":"m1","targetFieldId":"f_id","sources":[{"column":"id"}],"transforms":[],"origin":"auto","state":"active"},` +
	`{"id":"m2","targetFieldId":"f_cust","sources":[{"column":"customer_id"}],"transforms":[],"origin":"auto","state":"active"},` +
	`{"id":"m3","targetFieldId":"f_status","sources":[{"column":"status"}],"transforms":[],"origin":"auto","state":"active"}],` +
	`"relations":[],"aggregates":[],"ignoredColumns":[]}`

// Options shape the bundle Bundle builds.
type Options struct {
	// Source is the PostgreSQL config (default: localhost:5432/shop as omni_ro).
	Source json.RawMessage
	// SourceSecrets are sealed with Passphrase, or plaintext when Passphrase is "".
	SourceSecrets export.Secrets
	Passphrase    string
	// ConfigMode leaves the source credentials to the run config (config.json, OC-155).
	ConfigMode bool
	// TargetPath makes a directory target (default /tmp/out).
	TargetPath string
	// Mappings replace the default orders mapping.
	Mappings []export.Mapping
	// Fingerprint is the expected source shape (default: none).
	Fingerprint export.Fingerprint
	Version     string
	Options     *jobs.Options
}

// Bundle builds and finalizes a bundle with a directory target.
func Bundle(t testing.TB, o Options) export.Bundle {
	t.Helper()
	if o.Source == nil {
		o.Source = json.RawMessage(`{"host":"localhost","port":5432,"database":"shop","username":"omni_ro","sslMode":"disable"}`)
	}
	if o.TargetPath == "" {
		o.TargetPath = "/tmp/out"
	}
	if o.Mappings == nil {
		o.Mappings = []export.Mapping{{
			ID: "map-orders", Name: "orders", Source: export.TableRef{Schema: "public", Table: "orders"},
			Location: store.Location{Kind: store.KindDirectory, Folder: "orders", CreateIfMissing: true},
			Spec:     json.RawMessage(OrdersSpec), PlanHash: "0000000000000000000000000000000000000000000000000000000000000000",
		}}
	}
	if o.Version == "" {
		o.Version = "2.0.0"
	}
	opts := jobs.DefaultOptions()
	if o.Options != nil {
		opts = *o.Options
	}
	target, _ := json.Marshal(map[string]any{"path": o.TargetPath, "layout": "flat", "compression": "none", "durability": "none", "fileMode": "0644", "manifest": true, "maxDocBytes": 20 << 20})
	b := export.Bundle{
		BundleVersion: export.BundleVersion, OdbVersion: export.RangeFor(o.Version), CreatedAt: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
		CreatedBy: "user@example.com", Project: export.ProjectRef{ID: "p1", Name: "Shop → docs"},
		Source:   export.Connection{Type: "postgres", Config: o.Source},
		Target:   export.Connection{Type: store.KindDirectory, Config: target},
		Mappings: o.Mappings, Relationships: []export.Relationship{}, JobOptions: export.OptionsFrom(opts),
		SourceFingerprint: o.Fingerprint,
	}
	if b.SourceFingerprint.SHA256 == "" {
		b.SourceFingerprint = export.Fingerprint{SHA256: "0000000000000000000000000000000000000000000000000000000000000000", Tables: map[string]export.TableFingerprint{}}
	}
	if o.ConfigMode {
		b.Source.Credentials = &export.Credentials{Mode: export.ModeConfig}
	} else if o.SourceSecrets != nil {
		if o.Passphrase == "" {
			b.Source.Credentials = &export.Credentials{Mode: export.ModePlaintext, Values: o.SourceSecrets}
		} else {
			aad, err := b.ContentHash()
			if err != nil {
				t.Fatal(err)
			}
			c, err := export.Seal(o.SourceSecrets, o.Passphrase, aad, export.KDF{Alg: "argon2id", T: 1, M: 1024, P: 1})
			if err != nil {
				t.Fatal(err)
			}
			b.Source.Credentials = c
		}
	}
	if err := b.Finalize(); err != nil {
		t.Fatal(err)
	}
	return b
}

// Write encodes b into dir/name and returns the path.
func Write(t testing.TB, dir, name string, b export.Bundle) string {
	t.Helper()
	raw, err := export.Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
