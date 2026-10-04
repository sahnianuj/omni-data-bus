package cbstore

import (
	"context"
	"errors"
	"sort"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"

	"omnidatabus/internal/store"
)

// Export history (OC-055) is one document per export, keyed "<projectId>::<exportId>", plus an
// index document per project ("expidx::<projectId>") listing the IDs and bundle hashes, updated
// with CAS: listing and the report import's lookup are KV-only and see an export at once.

type exportDoc struct {
	Type           string            `json:"type"`
	ID             string            `json:"id"`
	WorkspaceID    string            `json:"workspaceId"`
	ProjectID      string            `json:"projectId"`
	CreatedBy      string            `json:"createdBy"`
	CreatedByEmail string            `json:"createdByEmail"`
	CreatedAt      string            `json:"createdAt"`
	MappingIDs     []string          `json:"mappingIds"`
	PlanHashes     map[string]string `json:"planHashes"`
	TargetType     string            `json:"targetType"`
	CredentialMode string            `json:"credentialMode"`
	Runtime        string            `json:"runtime"`
	OdbVersion     string            `json:"odbVersion"`
	BundleSHA256   string            `json:"bundleSha256"`
	PlanHash       string            `json:"planHash"`
}

func (d exportDoc) toRecord() store.ExportRecord {
	return store.ExportRecord{
		ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), ProjectID: d.ProjectID, CreatedBy: d.CreatedBy,
		CreatedByEmail: d.CreatedByEmail, CreatedAt: parseTS(d.CreatedAt), MappingIDs: d.MappingIDs, PlanHashes: d.PlanHashes,
		TargetType: d.TargetType, CredentialMode: d.CredentialMode, Runtime: d.Runtime,
		OdbVersion: d.OdbVersion, BundleSHA256: d.BundleSHA256, PlanHash: d.PlanHash,
	}
}

type exportIndex struct {
	Type        string            `json:"type"`
	WorkspaceID string            `json:"workspaceId"`
	ProjectID   string            `json:"projectId"`
	IDs         []string          `json:"ids"`     // oldest first (UUIDv7: time ordered)
	Bundles     map[string]string `json:"bundles"` // bundle sha256 → export ID
}

func exportIndexKey(projectID string) string { return "expidx::" + projectID }

type exports struct{ s *Store }

// Exports implements store.Store.
func (s *Store) Exports() store.Exports { return exports{s} }

func (r exports) coll() *gocb.Collection { return r.s.coll("exports") }

func (r exports) index(ctx context.Context, ws store.WorkspaceID, projectID string) (exportIndex, gocb.Cas, error) {
	idx := exportIndex{Type: "export_index", WorkspaceID: string(ws), ProjectID: projectID, IDs: []string{}, Bundles: map[string]string{}}
	res, err := r.coll().Get(exportIndexKey(projectID), &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return idx, 0, nil
	}
	if err != nil {
		return idx, 0, Translate(err, "")
	}
	if err := res.Content(&idx); err != nil {
		return idx, 0, err
	}
	if idx.Bundles == nil {
		idx.Bundles = map[string]string{}
	}
	return idx, res.Cas(), nil
}

func (r exports) Create(ctx context.Context, ws store.WorkspaceID, e store.ExportRecord) (store.ExportRecord, error) {
	if _, err := projects(r).Get(ctx, ws, e.ProjectID); err != nil {
		return store.ExportRecord{}, err
	}
	if e.ID == "" {
		e.ID = uuid.Must(uuid.NewV7()).String()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = r.s.clock.Now()
	}
	d := exportDoc{
		Type: "export", ID: e.ID, WorkspaceID: string(ws), ProjectID: e.ProjectID, CreatedBy: e.CreatedBy, CreatedByEmail: e.CreatedByEmail,
		CreatedAt: formatTS(e.CreatedAt), MappingIDs: e.MappingIDs, PlanHashes: e.PlanHashes, TargetType: e.TargetType,
		CredentialMode: e.CredentialMode, Runtime: e.Runtime, OdbVersion: e.OdbVersion,
		BundleSHA256: e.BundleSHA256, PlanHash: e.PlanHash,
	}
	key := e.ProjectID + "::" + e.ID
	if _, err := r.coll().Insert(key, d, &gocb.InsertOptions{Context: ctx}); err != nil {
		return store.ExportRecord{}, Translate(err, "id")
	}
	for range 10 {
		idx, cas, err := r.index(ctx, ws, e.ProjectID)
		if err != nil {
			return store.ExportRecord{}, err
		}
		idx.IDs = append(idx.IDs, e.ID)
		idx.Bundles[e.BundleSHA256] = e.ID
		if cas == 0 {
			_, err = r.coll().Insert(exportIndexKey(e.ProjectID), idx, &gocb.InsertOptions{Context: ctx})
		} else {
			_, err = r.coll().Replace(exportIndexKey(e.ProjectID), idx, &gocb.ReplaceOptions{Cas: cas, Context: ctx})
		}
		if errors.Is(err, gocb.ErrCasMismatch) || errors.Is(err, gocb.ErrDocumentExists) {
			continue
		}
		if err != nil {
			_, _ = r.coll().Remove(key, &gocb.RemoveOptions{Context: ctx})
			return store.ExportRecord{}, Translate(err, "")
		}
		return d.toRecord(), nil
	}
	_, _ = r.coll().Remove(key, &gocb.RemoveOptions{Context: ctx})
	return store.ExportRecord{}, store.ErrVersionConflict
}

func (r exports) get(ctx context.Context, ws store.WorkspaceID, projectID, id string) (store.ExportRecord, error) {
	res, err := r.coll().Get(projectID+"::"+id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.ExportRecord{}, Translate(err, "")
	}
	var d exportDoc
	if err := res.Content(&d); err != nil {
		return store.ExportRecord{}, err
	}
	if d.WorkspaceID != string(ws) || d.Type != "export" {
		return store.ExportRecord{}, store.ErrNotFound
	}
	return d.toRecord(), nil
}

func (r exports) List(ctx context.Context, ws store.WorkspaceID, projectID string, page store.Page) ([]store.ExportRecord, string, error) {
	page = page.Normalize()
	idx, _, err := r.index(ctx, ws, projectID)
	if err != nil || idx.WorkspaceID != string(ws) {
		return []store.ExportRecord{}, "", err
	}
	ids := append([]string(nil), idx.IDs...)
	sort.Sort(sort.Reverse(sort.StringSlice(ids))) // newest first
	out := []store.ExportRecord{}
	next := ""
	for _, id := range ids {
		if page.Cursor != "" && id >= page.Cursor {
			continue
		}
		if len(out) == page.Limit {
			next = out[len(out)-1].ID
			break
		}
		e, err := r.get(ctx, ws, projectID, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	return out, next, nil
}

func (r exports) FindByBundle(ctx context.Context, ws store.WorkspaceID, projectID, sha string) (store.ExportRecord, error) {
	idx, _, err := r.index(ctx, ws, projectID)
	if err != nil {
		return store.ExportRecord{}, err
	}
	id, ok := idx.Bundles[sha]
	if !ok || idx.WorkspaceID != string(ws) {
		return store.ExportRecord{}, store.ErrNotFound
	}
	return r.get(ctx, ws, projectID, id)
}

func (r exports) deleteForProject(ctx context.Context, projectID string) error {
	res, err := r.coll().Get(exportIndexKey(projectID), &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	if err != nil {
		return Translate(err, "")
	}
	var idx exportIndex
	if err := res.Content(&idx); err != nil {
		return err
	}
	for _, id := range idx.IDs {
		if _, err := r.coll().Remove(projectID+"::"+id, &gocb.RemoveOptions{Context: ctx}); err != nil && !errors.Is(err, gocb.ErrDocumentNotFound) {
			return Translate(err, "")
		}
	}
	_, err = r.coll().Remove(exportIndexKey(projectID), &gocb.RemoveOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	return Translate(err, "")
}
