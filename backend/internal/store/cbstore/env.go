package cbstore

import (
	"context"
	"errors"
	"time"

	"omnidatabus/internal/store/provision"

	"github.com/couchbase/gocb/v2"
)

const schemaDocID = "schema"

type schemaDoc struct {
	Version   int    `json:"version"`
	AppliedAt string `json:"appliedAt"`
}

// env implements provision.Env on Couchbase.
type env struct{ s *Store }

func (e env) Scope() string               { return e.s.cfg.Scope }
func (e env) Keyspace(coll string) string { return e.s.keyspace(coll) }

// collectionExists checks the bucket manifest. (A KV read from a missing collection doesn't
// fail fast: the SDK retries it until timeout, expecting the manifest to catch up.)
func (e env) collectionExists(ctx context.Context, scope, coll string) (bool, error) {
	scopes, err := e.s.bucket.CollectionsV2().GetAllScopes(&gocb.GetAllScopesOptions{Context: ctx})
	if err != nil {
		return false, provisionErr(err)
	}
	for _, s := range scopes {
		if s.Name != scope {
			continue
		}
		for _, c := range s.Collections {
			if c.Name == coll {
				return true, nil
			}
		}
	}
	return false, nil
}

func (e env) CurrentVersion(ctx context.Context) (int, error) {
	exists, err := e.collectionExists(ctx, e.s.cfg.Scope, "system")
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	res, err := e.s.coll("system").Get(schemaDocID, &gocb.GetOptions{Context: ctx})
	switch {
	case errors.Is(err, gocb.ErrDocumentNotFound), errors.Is(err, gocb.ErrCollectionNotFound), errors.Is(err, gocb.ErrScopeNotFound):
		return 0, nil
	case err != nil:
		return 0, err
	}
	var d schemaDoc
	if err := res.Content(&d); err != nil {
		return 0, err
	}
	return d.Version, nil
}

func (e env) SetVersion(ctx context.Context, from, to int) error {
	c := e.s.coll("system")
	doc := schemaDoc{Version: to, AppliedAt: formatTS(e.s.clock.Now())}
	res, err := c.Get(schemaDocID, &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		if from != 0 {
			return provision.ErrConcurrent
		}
		_, err = c.Insert(schemaDocID, doc, &gocb.InsertOptions{Context: ctx})
		if errors.Is(err, gocb.ErrDocumentExists) {
			return provision.ErrConcurrent
		}
		return err
	}
	if err != nil {
		return err
	}
	var cur schemaDoc
	if err := res.Content(&cur); err != nil {
		return err
	}
	if cur.Version != from {
		return provision.ErrConcurrent
	}
	_, err = c.Replace(schemaDocID, doc, &gocb.ReplaceOptions{Cas: res.Cas(), Context: ctx})
	if errors.Is(err, gocb.ErrCasMismatch) {
		return provision.ErrConcurrent
	}
	return err
}

func (e env) CreateScope(ctx context.Context, scope string) error {
	return provisionErr(e.s.bucket.CollectionsV2().CreateScope(scope, &gocb.CreateScopeOptions{Context: ctx}))
}

func (e env) CreateCollection(ctx context.Context, scope, coll string) error {
	return provisionErr(e.s.bucket.CollectionsV2().CreateCollection(scope, coll, nil, &gocb.CreateCollectionOptions{Context: ctx}))
}

// WaitCollection polls until KV accepts operations on the new collection.
func (e env) WaitCollection(ctx context.Context, scope, coll string) error {
	c := e.s.bucket.Scope(scope).Collection(coll)
	delay := 100 * time.Millisecond
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, err := c.Get("__oc_probe__", &gocb.GetOptions{Context: ctx, Timeout: 2 * time.Second})
		if errors.Is(err, gocb.ErrDocumentNotFound) {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay = delay * 3 / 2
		}
	}
}

func (e env) Exec(ctx context.Context, statement string) error {
	res, err := e.s.cluster.Query(statement, &gocb.QueryOptions{Context: ctx, Timeout: 5 * time.Minute})
	if err != nil {
		return provisionErr(err)
	}
	return provisionErr(res.Close())
}
