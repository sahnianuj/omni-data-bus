package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/couchbase/gocb/v2"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// adminAPI is the slice of the SDK the keyspace browser uses (faked in tests).
type adminAPI interface {
	GetAllBuckets(ctx context.Context) (map[string]gocb.BucketSettings, error)
	CreateBucket(ctx context.Context, s gocb.CreateBucketSettings) error
	WaitBucket(ctx context.Context, bucket string, timeout time.Duration) error
	GetAllScopes(ctx context.Context, bucket string) ([]gocb.ScopeSpec, error)
	CreateScope(ctx context.Context, bucket, scope string) error
	CreateCollection(ctx context.Context, bucket, scope, collection string, maxExpiry time.Duration) error
	// ProbeCollection reads a document that never exists: ErrDocumentNotFound means the
	// collection is usable on the node that owns the key.
	ProbeCollection(ctx context.Context, bucket, scope, collection string) error
	QueryRows(ctx context.Context, statement string, params map[string]any, timeout time.Duration) ([]json.RawMessage, error)
	MyRoles(ctx context.Context) ([]Role, error)
	// Version is the server version ("7.6.2-…").
	Version(ctx context.Context) (string, error)
	// Exists reports whether a document exists.
	Exists(ctx context.Context, bucket, scope, collection, key string) (bool, error)
	// Get reads one document's raw JSON (gocb.ErrDocumentNotFound when absent).
	Get(ctx context.Context, bucket, scope, collection, key string) ([]byte, error)
	// SampleScan draws a random sample with a KV range scan (Couchbase Server 7.6+).
	SampleScan(ctx context.Context, bucket, scope, collection string, n int) ([]json.RawMessage, error)
}

// Timing for the readiness waits and best-effort lookups (OC-013, OC-014).
const (
	readyTimeout     = 30 * time.Second
	firstBackoff     = 100 * time.Millisecond
	maxBackoff       = 2 * time.Second
	openBucketWait   = 5 * time.Second
	countTimeout     = 3 * time.Second
	maxProbedBuckets = 50
	probeCollection  = "__oc_probe__"
)

type keyspaces struct {
	api        adminAPI
	deployment string
	// sleep waits between readiness polls; tests replace it to run instantly.
	sleep func(ctx context.Context, d time.Duration) error
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func exists(kind, name string) problem.Problem {
	return problem.New(http.StatusConflict, problem.CodeKeyspaceExists, strings.ToUpper(kind[:1])+kind[1:]+" already exists",
		"The "+kind+" "+name+" already exists; select it instead.")
}

func notFound(kind, name string) problem.Problem {
	return problem.New(http.StatusNotFound, problem.CodeNotFound, strings.ToUpper(kind[:1])+kind[1:]+" not found", "There is no "+kind+" "+name+".")
}

func denied(action string) error {
	return &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindPermission, Cause: errors.New("this credential can't " + action)}
}

// forbidden reports an HTTP 403 from the management API.
func forbidden(err error) bool {
	var he *gocb.HTTPError
	return errors.As(err, &he) && he.StatusCode == http.StatusForbidden
}

// mapErr turns SDK errors into problems the UI understands.
func mapErr(err error, kind, name string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gocb.ErrBucketExists), errors.Is(err, gocb.ErrScopeExists), errors.Is(err, gocb.ErrCollectionExists):
		return exists(kind, name)
	case errors.Is(err, gocb.ErrBucketNotFound):
		return notFound("bucket", name)
	case errors.Is(err, gocb.ErrScopeNotFound):
		return notFound("scope", name)
	case forbidden(err):
		return &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindPermission, Cause: err}
	case enterpriseOnly(err) && kind == "collection":
		// Community Edition (e.g. the all-in-one image's bundled node, OC-072) has no collection TTL.
		return problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Max expiry needs Enterprise Edition",
			"This cluster runs Couchbase Server Community Edition, where collections can't have a max expiry. Leave max expiry empty.")
	case errors.Is(err, gocb.ErrInvalidArgument):
		return problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", err.Error())
	}
	return classify(err)
}

// enterpriseOnly reports Couchbase's refusal of an Enterprise-only setting on Community Edition.
func enterpriseOnly(err error) bool {
	return strings.Contains(err.Error(), "Supported in enterprise edition only")
}

// sortNames orders case-insensitively with _default first.
func sortNames(names []string) {
	sort.Slice(names, func(i, j int) bool {
		a, b := names[i], names[j]
		if (a == "_default") != (b == "_default") {
			return a == "_default"
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
}

// canManageBucket reports whether roles allow creating scopes and collections in bucket.
func canManageBucket(roles []Role, bucket string) bool {
	for _, r := range roles {
		switch r.Role {
		case "admin", "cluster_admin":
			return true
		case "bucket_admin", "scope_admin":
			if r.Bucket == "*" || r.Bucket == bucket {
				return true
			}
		}
	}
	return false
}

func (k *keyspaces) ListBuckets(ctx context.Context) (target.BucketList, error) {
	roles, rolesErr := k.api.MyRoles(ctx)
	caps := CapabilitiesFromRoles(roles, k.deployment)
	if rolesErr != nil {
		caps.Reasons = append(caps.Reasons, "Couldn't read this credential's roles: create actions are disabled.")
	}
	out := target.BucketList{Buckets: []target.BucketInfo{}}
	out.Capabilities, _ = json.Marshal(caps)

	all, err := k.api.GetAllBuckets(ctx)
	if err == nil {
		for _, b := range all {
			out.Buckets = append(out.Buckets, target.BucketInfo{
				Name: b.Name, Type: bucketType(b.BucketType), RAMQuotaMB: b.RAMQuotaMB, NumReplicas: b.NumReplicas,
				StorageBackend: string(b.StorageBackend), CanManage: canManageBucket(roles, b.Name),
			})
		}
		sort.Slice(out.Buckets, func(i, j int) bool {
			return strings.ToLower(out.Buckets[i].Name) < strings.ToLower(out.Buckets[j].Name)
		})
		return out, nil
	}
	if ctx.Err() != nil {
		return out, classify(ctx.Err())
	}
	// No bucket-management access (Capella database credentials, scoped users): derive the
	// list from the roles, or from the query catalog, and keep the buckets that open.
	out.Partial = true
	names := caps.BucketsAllowed
	if caps.AllBuckets || len(names) == 0 {
		if rows, err := k.api.QueryRows(ctx, "SELECT RAW b.name FROM system:buckets AS b", nil, countTimeout); err == nil {
			names = names[:0:0]
			for _, r := range rows {
				var n string
				if json.Unmarshal(r, &n) == nil && n != "" {
					names = append(names, n)
				}
			}
		}
	}
	if len(names) > maxProbedBuckets {
		names = names[:maxProbedBuckets]
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if k.api.WaitBucket(ctx, n, openBucketWait) != nil {
				return
			}
			mu.Lock()
			out.Buckets = append(out.Buckets, target.BucketInfo{Name: n, CanManage: canManageBucket(roles, n)})
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out.Buckets, func(i, j int) bool {
		return strings.ToLower(out.Buckets[i].Name) < strings.ToLower(out.Buckets[j].Name)
	})
	return out, nil
}

func bucketType(t gocb.BucketType) string {
	if t == gocb.EphemeralBucketType {
		return "ephemeral"
	}
	return "couchbase"
}

// openBucket checks that a bucket exists and is reachable. The SDK retries unknown buckets
// until its timeout, so a bounded wait keeps a typo from hanging the request.
func (k *keyspaces) openBucket(ctx context.Context, bucket string) error {
	if !ValidBucketName(bucket) {
		return notFound("bucket", bucket)
	}
	if err := k.api.WaitBucket(ctx, bucket, openBucketWait); err != nil {
		if ctx.Err() != nil {
			return classify(ctx.Err())
		}
		return problem.New(http.StatusNotFound, problem.CodeNotFound, "Bucket not found",
			"There is no bucket "+bucket+", or this credential can't access it.")
	}
	return nil
}

func (k *keyspaces) ListScopes(ctx context.Context, bucket string) ([]target.ScopeInfo, error) {
	if err := k.openBucket(ctx, bucket); err != nil {
		return nil, err
	}
	specs, err := k.api.GetAllScopes(ctx, bucket)
	if err != nil {
		if !forbidden(err) {
			return nil, mapErr(err, "bucket", bucket)
		}
		// No collection-management access: read the collections from the query catalog.
		if specs, err = k.scopesFromCatalog(ctx, bucket); err != nil {
			return nil, &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindPermission, Cause: err}
		}
	}
	byName := map[string]target.ScopeInfo{}
	names := []string{}
	for _, s := range specs {
		if s.Name == "_system" {
			continue // system scope: never a migration target
		}
		si := target.ScopeInfo{Name: s.Name, Collections: []target.CollectionInfo{}}
		collNames := []string{}
		colls := map[string]target.CollectionInfo{}
		for _, c := range s.Collections {
			ci := target.CollectionInfo{Name: c.Name, MaxExpirySec: int64(c.MaxExpiry / time.Second)}
			if c.History != nil {
				h := c.History.Enabled
				ci.History = &h
			}
			colls[c.Name] = ci
			collNames = append(collNames, c.Name)
		}
		sortNames(collNames)
		for _, n := range collNames {
			si.Collections = append(si.Collections, colls[n])
		}
		byName[s.Name] = si
		names = append(names, s.Name)
	}
	sortNames(names)
	out := make([]target.ScopeInfo, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out, nil
}

func (k *keyspaces) scopesFromCatalog(ctx context.Context, bucket string) ([]gocb.ScopeSpec, error) {
	rows, err := k.api.QueryRows(ctx, "SELECT k.`scope` AS s, k.name AS c FROM system:keyspaces AS k WHERE k.`bucket` = $bucket",
		map[string]any{"bucket": bucket}, countTimeout)
	if err != nil {
		return nil, err
	}
	scopes := map[string]*gocb.ScopeSpec{}
	var order []string
	for _, r := range rows {
		var row struct{ S, C string }
		if json.Unmarshal(r, &row) != nil || row.S == "" {
			continue
		}
		sp, ok := scopes[row.S]
		if !ok {
			sp = &gocb.ScopeSpec{Name: row.S}
			scopes[row.S] = sp
			order = append(order, row.S)
		}
		sp.Collections = append(sp.Collections, gocb.CollectionSpec{Name: row.C, ScopeName: row.S})
	}
	out := make([]gocb.ScopeSpec, 0, len(order))
	for _, n := range order {
		out = append(out, *scopes[n])
	}
	return out, nil
}

func (k *keyspaces) Collection(ctx context.Context, bucket, scope, collection string) (target.CollectionInfo, error) {
	ks, err := Keyspace(bucket, scope, collection)
	if err != nil {
		return target.CollectionInfo{}, err
	}
	scopes, err := k.ListScopes(ctx, bucket)
	if err != nil {
		return target.CollectionInfo{}, err
	}
	for _, s := range scopes {
		if s.Name != scope {
			continue
		}
		for _, c := range s.Collections {
			if c.Name == collection {
				c.DocCountEstimate = k.count(ctx, ks)
				return c, nil
			}
		}
	}
	return target.CollectionInfo{}, notFound("collection", bucket+"."+scope+"."+collection)
}

// count is best effort: nil when Query is unavailable, slow (> 3 s) or not permitted.
func (k *keyspaces) count(ctx context.Context, keyspace string) *int64 {
	rows, err := k.api.QueryRows(ctx, "SELECT RAW COUNT(*) FROM "+keyspace, nil, countTimeout)
	if err != nil || len(rows) != 1 {
		return nil
	}
	var n int64
	if json.Unmarshal(rows[0], &n) != nil {
		return nil
	}
	return &n
}

func (k *keyspaces) CreateBucket(ctx context.Context, spec target.BucketSpec) error {
	if k.deployment == Capella {
		return problem.New(http.StatusUnprocessableEntity, problem.CodeCapellaBucketCreate, "Create buckets in Capella",
			"Capella buckets are created in the Capella UI (Cluster → Buckets → Create). Then pick the bucket here: https://docs.couchbase.com/cloud/clusters/data-service/manage-buckets.html")
	}
	if err := ValidateBucketSpec(spec); err != nil {
		return err
	}
	roles, err := k.api.MyRoles(ctx)
	if err != nil || !CapabilitiesFromRoles(roles, k.deployment).CanCreateBucket {
		return denied("create buckets")
	}
	settings := gocb.CreateBucketSettings{BucketSettings: gocb.BucketSettings{
		Name: spec.Name, RAMQuotaMB: spec.RAMQuotaMB, NumReplicas: spec.NumReplicas, FlushEnabled: spec.FlushEnabled,
		BucketType: gocb.CouchbaseBucketType,
	}}
	if spec.BucketType == "ephemeral" {
		settings.BucketType = gocb.EphemeralBucketType
	} else {
		settings.StorageBackend = gocb.StorageBackend(spec.StorageBackend)
	}
	if err := k.api.CreateBucket(ctx, settings); err != nil {
		return mapErr(err, "bucket", spec.Name)
	}
	if err := k.api.WaitBucket(ctx, spec.Name, readyTimeout); err != nil {
		return target.ErrPending
	}
	return nil
}

func (k *keyspaces) checkManage(ctx context.Context, bucket string) error {
	roles, err := k.api.MyRoles(ctx)
	if err != nil || !canManageBucket(roles, bucket) {
		return denied("create scopes or collections in bucket " + bucket)
	}
	return nil
}

func (k *keyspaces) CreateScope(ctx context.Context, bucket, scope string) error {
	if err := ValidateScopeName(scope); err != nil {
		return err
	}
	if err := k.checkManage(ctx, bucket); err != nil {
		return err
	}
	if err := k.openBucket(ctx, bucket); err != nil {
		return err
	}
	if err := k.api.CreateScope(ctx, bucket, scope); err != nil {
		return mapErr(err, "scope", scope)
	}
	return k.poll(ctx, func() bool { return k.inManifest(ctx, bucket, scope, "") })
}

func (k *keyspaces) CreateCollection(ctx context.Context, bucket, scope string, spec target.CollectionSpec) error {
	if err := ValidateCollectionSpec(spec); err != nil {
		return err
	}
	if !validExisting(scope) {
		return notFound("scope", scope)
	}
	if err := k.checkManage(ctx, bucket); err != nil {
		return err
	}
	if err := k.openBucket(ctx, bucket); err != nil {
		return err
	}
	if err := k.api.CreateCollection(ctx, bucket, scope, spec.Name, time.Duration(spec.MaxExpirySec)*time.Second); err != nil {
		return mapErr(err, "collection", spec.Name)
	}
	// Creation is eventually consistent across nodes: wait until the manifest lists it, then
	// until a read reaches it (DocumentNotFound rather than an unknown-collection error).
	return k.poll(ctx, func() bool {
		return k.inManifest(ctx, bucket, scope, spec.Name) &&
			errors.Is(k.api.ProbeCollection(ctx, bucket, scope, spec.Name), gocb.ErrDocumentNotFound)
	})
}

func (k *keyspaces) inManifest(ctx context.Context, bucket, scope, collection string) bool {
	specs, err := k.api.GetAllScopes(ctx, bucket)
	if err != nil {
		return false
	}
	for _, s := range specs {
		if s.Name != scope {
			continue
		}
		if collection == "" {
			return true
		}
		for _, c := range s.Collections {
			if c.Name == collection {
				return true
			}
		}
	}
	return false
}

// poll retries ready with backoff (100 ms × 1.5, at most 2 s apart) for up to 30 s of waiting.
func (k *keyspaces) poll(ctx context.Context, ready func() bool) error {
	wait, waited := firstBackoff, time.Duration(0)
	for {
		if ready() {
			return nil
		}
		if waited >= readyTimeout {
			return target.ErrPending
		}
		if err := k.sleep(ctx, wait); err != nil {
			return target.ErrPending
		}
		waited += wait
		wait = min(time.Duration(float64(wait)*1.5), maxBackoff)
	}
}

// CodeSamplingUnavailable is returned when neither a range scan nor Query can sample documents.
const CodeSamplingUnavailable = problem.CodeSamplingUnavailable

// SampleDocuments prefers a KV sampling range scan (server ≥ 7.6), falls back to
// SELECT RAW … LIMIT n through Query, and otherwise reports SAMPLING_UNAVAILABLE (OC-015 AC4).
func (k *keyspaces) SampleDocuments(ctx context.Context, bucket, scope, collection string, n int) ([]json.RawMessage, string, error) {
	ks, err := Keyspace(bucket, scope, collection)
	if err != nil {
		return nil, "", err
	}
	if n <= 0 || n > 1000 {
		n = 50
	}
	var reasons []string
	if v, err := k.api.Version(ctx); err == nil {
		if major, minor := parseVersion(v); major > 7 || (major == 7 && minor >= 6) {
			docs, err := k.api.SampleScan(ctx, bucket, scope, collection, n)
			if err == nil {
				return docs, "range-scan", nil
			}
			reasons = append(reasons, "range scan failed: "+err.Error())
		} else {
			reasons = append(reasons, "range scans need Couchbase Server 7.6+ (this is "+v+")")
		}
	}
	rows, err := k.api.QueryRows(ctx, "SELECT RAW c FROM "+ks+" AS c LIMIT "+strconv.Itoa(n), nil, 10*time.Second)
	if err == nil {
		return rows, "query", nil
	}
	reasons = append(reasons, "Query unavailable: "+err.Error())
	return nil, "", problem.New(http.StatusUnprocessableEntity, CodeSamplingUnavailable, "Can't sample documents",
		"Sampling needs a range scan (Couchbase Server 7.6+) or the Query service: "+strings.Join(reasons, "; "))
}

// maxExistsChecks bounds a collision check.
const maxExistsChecks = 50

// ExistingKeys checks up to 50 keys with KV Exists (OC-022 AC7).
func (k *keyspaces) ExistingKeys(ctx context.Context, bucket, scope, collection string, keys []string) (map[string]bool, error) {
	if _, err := Keyspace(bucket, scope, collection); err != nil {
		return nil, err
	}
	if len(keys) > maxExistsChecks {
		keys = keys[:maxExistsChecks]
	}
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		ok, err := k.api.Exists(ctx, bucket, scope, collection, key)
		if err != nil {
			return nil, mapErr(err, "collection", collection)
		}
		out[key] = ok
	}
	return out, nil
}
