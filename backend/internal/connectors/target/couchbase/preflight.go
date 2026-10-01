package couchbase

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// Pre-flight check statuses and warning codes (the jobs checklist vocabulary, OC-023).
const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "fail"

	WarnCollectionHasDocs = "COLLECTION_NOT_EMPTY"
	WarnInsertExisting    = "INSERT_EXISTING_DOCS"
)

// Preflight implements target.Preflighter: each mapping's collection exists (or is created
// when the mapping allows it), and a warning when it already holds documents (OC-023 AC2,
// moved here from jobs in OC-036).
func (c *Connector) Preflight(ctx context.Context, conn target.Conn, locs []target.PreflightLocation) []target.LocationPreflight {
	ks, err := c.keyspacesFor(ctx, conn)
	if err != nil {
		out := make([]target.LocationPreflight, len(locs))
		for i, l := range locs {
			out[i] = target.LocationPreflight{Checks: []target.PreflightCheck{{
				ID: "keyspace", Label: "target " + l.Location.String(), Status: checkFail, Detail: errMessage(err), MappingID: l.MappingID,
			}}}
		}
		return out
	}
	return PreflightKeyspaces(ctx, ks, locs)
}

// PreflightKeyspaces runs the checks through a keyspace browser. Check IDs ("keyspace",
// "empty") and labels are relative: the caller prefixes them with the mapping's name.
func PreflightKeyspaces(ctx context.Context, ks target.Keyspaces, locs []target.PreflightLocation) []target.LocationPreflight {
	out := make([]target.LocationPreflight, len(locs))
	var wg sync.WaitGroup
	for i, l := range locs {
		wg.Go(func() { out[i] = preflightOne(ctx, ks, l) }) // counts go through Query: check in parallel
	}
	wg.Wait()
	return out
}

func preflightOne(ctx context.Context, ks target.Keyspaces, l target.PreflightLocation) target.LocationPreflight {
	loc := l.Location
	name := loc.String()
	c := target.PreflightCheck{ID: "keyspace", Label: "target " + name, Status: checkOK, MappingID: l.MappingID}
	info, err := ks.Collection(ctx, loc.Bucket, loc.Scope, loc.Collection)
	switch {
	case err == nil:
	case problem.HasCode(err, problem.CodeNotFound) && l.CreateIfMissing:
		if cerr := EnsureCollection(ctx, ks, loc); cerr != nil {
			c.Status, c.Detail = checkFail, "Couldn't create the collection: "+errMessage(cerr)
			c.Hint = "Create it in Couchbase, or give the target credential permission to manage scopes and collections."
			return target.LocationPreflight{Checks: []target.PreflightCheck{c}}
		}
		c.Detail = "Created " + name + "."
		return target.LocationPreflight{Checks: []target.PreflightCheck{c}, Empty: true}
	case problem.HasCode(err, problem.CodeNotFound):
		c.Status, c.Code = checkFail, target.CodeKeyspaceMissing
		c.Detail, c.Hint = "The collection doesn't exist.", "Create it, or turn on \"Create when migration starts\" in the Tables step."
		return target.LocationPreflight{Checks: []target.PreflightCheck{c}}
	default:
		c.Status, c.Detail = checkFail, errMessage(err)
		return target.LocationPreflight{Checks: []target.PreflightCheck{c}}
	}
	res := target.LocationPreflight{Checks: []target.PreflightCheck{c}, Empty: info.DocCountEstimate != nil && *info.DocCountEstimate == 0}
	if info.DocCountEstimate != nil && *info.DocCountEstimate > 0 {
		w := target.PreflightCheck{
			ID: "empty", Label: "target collection is empty", Status: checkWarn, Code: WarnCollectionHasDocs, MappingID: l.MappingID,
			Detail: fmt.Sprintf("%s already holds about %d documents.", name, *info.DocCountEstimate),
		}
		if l.WriteMode == "insert" {
			w.Code = WarnInsertExisting
			w.Detail += " Insert mode skips documents whose keys already exist."
		}
		res.Checks = append(res.Checks, w)
	}
	return res
}

func errMessage(err error) string {
	var pr problem.Problemer
	if errors.As(err, &pr) {
		p := pr.Problem()
		if p.Detail != "" {
			return p.Detail
		}
		return p.Title
	}
	return err.Error()
}
