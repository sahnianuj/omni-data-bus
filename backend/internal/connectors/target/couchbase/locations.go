package couchbase

import (
	"context"
	"errors"
	"net/http"

	"github.com/couchbase/gocb/v2"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// maxDocBytes is Couchbase's document size limit.
const maxDocBytes = 20 << 20

// Capabilities implements target.Connector (OC-036).
func (*Connector) Capabilities() target.Capabilities {
	return target.Capabilities{
		SupportsDurability: true, SupportsExpiry: true, SupportsCreateLocation: true, RequiresCredentials: true,
		MaxDocBytes: maxDocBytes, LocationLevels: []string{"bucket", "scope", "collection"},
	}
}

func (c *Connector) keyspacesFor(ctx context.Context, conn target.Conn) (target.Keyspaces, error) {
	return c.Keyspaces(ctx, conn.ID, conn.Version, conn.Config, conn.Creds)
}

// ListLocations implements target.LocationBrowser: buckets, then a bucket's scopes, then a
// scope's collections (the 1.0 keyspace browser, OC-013).
func (c *Connector) ListLocations(ctx context.Context, conn target.Conn, parent target.Location) ([]target.LocationNode, error) {
	ks, err := c.keyspacesFor(ctx, conn)
	if err != nil {
		return nil, err
	}
	return ListKeyspaceLocations(ctx, ks, parent)
}

// StatLocation implements target.LocationBrowser for a collection.
func (c *Connector) StatLocation(ctx context.Context, conn target.Conn, loc target.Location) (target.LocationNode, error) {
	ks, err := c.keyspacesFor(ctx, conn)
	if err != nil {
		return target.LocationNode{}, err
	}
	return StatKeyspaceLocation(ctx, ks, loc)
}

// CreateLocation implements target.LocationBrowser: the scope (when missing) and the
// collection. Buckets need sizing, so they're created through the keyspace endpoints.
func (c *Connector) CreateLocation(ctx context.Context, conn target.Conn, loc target.Location) error {
	if loc.Bucket == "" || loc.Scope == "" || loc.Collection == "" {
		return invalidLocation("A Couchbase location needs a bucket, a scope and a collection.")
	}
	ks, err := c.keyspacesFor(ctx, conn)
	if err != nil {
		return err
	}
	return EnsureCollection(ctx, ks, loc)
}

func invalidLocation(detail string) problem.Problem {
	return problem.New(http.StatusUnprocessableEntity, problem.CodeLocationInvalid, "Invalid location", detail)
}

func node(level, name string, loc target.Location) target.LocationNode {
	loc.Kind = target.KindCouchbase
	return target.LocationNode{Name: name, Level: level, Path: loc.Path(), Location: loc, Leaf: level == "collection"}
}

// ListKeyspaceLocations lists the children of parent through a keyspace browser.
func ListKeyspaceLocations(ctx context.Context, ks target.Keyspaces, parent target.Location) ([]target.LocationNode, error) {
	switch {
	case parent.Bucket == "":
		list, err := ks.ListBuckets(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]target.LocationNode, 0, len(list.Buckets))
		for _, b := range list.Buckets {
			n := node("bucket", b.Name, target.Location{Bucket: b.Name})
			n.CanCreate = b.CanManage
			out = append(out, n)
		}
		return out, nil
	case parent.Collection != "":
		return []target.LocationNode{}, nil
	}
	scopes, err := ks.ListScopes(ctx, parent.Bucket)
	if err != nil {
		return nil, err
	}
	out := []target.LocationNode{}
	for _, s := range scopes {
		if parent.Scope == "" {
			n := node("scope", s.Name, target.Location{Bucket: parent.Bucket, Scope: s.Name})
			n.CanCreate = true
			out = append(out, n)
			continue
		}
		if s.Name != parent.Scope {
			continue
		}
		for _, col := range s.Collections {
			out = append(out, node("collection", col.Name, target.Location{Bucket: parent.Bucket, Scope: s.Name, Collection: col.Name}))
		}
		return out, nil
	}
	if parent.Scope != "" {
		return nil, notFound("scope", parent.Bucket+"."+parent.Scope)
	}
	return out, nil
}

// StatKeyspaceLocation describes one collection with its best-effort document count.
func StatKeyspaceLocation(ctx context.Context, ks target.Keyspaces, loc target.Location) (target.LocationNode, error) {
	if loc.Bucket == "" || loc.Scope == "" || loc.Collection == "" {
		return target.LocationNode{}, invalidLocation("A Couchbase location needs a bucket, a scope and a collection.")
	}
	info, err := ks.Collection(ctx, loc.Bucket, loc.Scope, loc.Collection)
	if err != nil {
		return target.LocationNode{}, err
	}
	n := node("collection", loc.Collection, target.Location{Bucket: loc.Bucket, Scope: loc.Scope, Collection: loc.Collection})
	n.DocCount = info.DocCountEstimate
	return n, nil
}

// EnsureCollection creates the scope (if missing) and the collection; one that already exists
// or is still being made available counts as done.
func EnsureCollection(ctx context.Context, ks target.Keyspaces, loc target.Location) error {
	scopes, err := ks.ListScopes(ctx, loc.Bucket)
	if err != nil {
		return err
	}
	found := false
	for _, s := range scopes {
		if s.Name == loc.Scope {
			found = true
		}
	}
	if !found {
		if err := ks.CreateScope(ctx, loc.Bucket, loc.Scope); err != nil && !errors.Is(err, target.ErrPending) && !problem.HasCode(err, problem.CodeKeyspaceExists) {
			return err
		}
	}
	err = ks.CreateCollection(ctx, loc.Bucket, loc.Scope, target.CollectionSpec{Name: loc.Collection})
	if err != nil && (errors.Is(err, target.ErrPending) || problem.HasCode(err, problem.CodeKeyspaceExists)) {
		return nil
	}
	return err
}

// FindDocument implements target.DocumentFinder with a KV get.
func (c *Connector) FindDocument(ctx context.Context, conn target.Conn, loc target.Location, key string) (target.Document, error) {
	ks, err := c.keyspacesFor(ctx, conn)
	if err != nil {
		return target.Document{}, err
	}
	k, ok := ks.(*keyspaces)
	if !ok {
		return target.Document{}, errors.New("couchbase: this connection can't read documents")
	}
	b, err := k.api.Get(ctx, loc.Bucket, loc.Scope, loc.Collection, key)
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return target.Document{}, problem.New(http.StatusNotFound, problem.CodeDocumentNotFound, "Document not found", "No document with key "+key+".")
	}
	if err != nil {
		return target.Document{}, mapErr(err, "collection", loc.Collection)
	}
	return target.Document{Key: key, Bytes: int64(len(b)), Body: b}, nil
}
