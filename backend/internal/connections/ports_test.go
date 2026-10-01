package connections

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

// portTarget is a Couchbase-like kind implementing the OC-036 ports.
type portTarget struct {
	*fakeTarget
	gotConn target.Conn
	gotLoc  target.Location
	gotLocs []target.PreflightLocation
}

func (p *portTarget) ListLocations(_ context.Context, c target.Conn, parent target.Location) ([]target.LocationNode, error) {
	p.gotConn, p.gotLoc = c, parent
	return []target.LocationNode{{Name: "shop", Path: "shop"}}, nil
}

func (p *portTarget) StatLocation(_ context.Context, _ target.Conn, loc target.Location) (target.LocationNode, error) {
	p.gotLoc = loc
	if loc.Collection == "missing" {
		return target.LocationNode{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "")
	}
	n := int64(3)
	return target.LocationNode{Name: loc.Collection, DocCount: &n}, nil
}

func (p *portTarget) CreateLocation(_ context.Context, _ target.Conn, loc target.Location) error {
	p.gotLoc = loc
	return nil
}

func (p *portTarget) Preflight(_ context.Context, _ target.Conn, locs []target.PreflightLocation) []target.LocationPreflight {
	p.gotLocs = locs
	return make([]target.LocationPreflight, len(locs))
}

func portsFixture(t *testing.T) (*Service, *portTarget, *memstore.Store, string) {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	pt := &portTarget{fakeTarget: &fakeTarget{typ: store.KindCouchbase}}
	targets := target.NewRegistry()
	targets.Register(pt)
	targets.Register(&fakeTarget{typ: "plain"})
	svc := NewService(st, sealer(t), source.NewRegistry(), targets, nil, nil)
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	_, _, err = svc.Save(ctx, ws, "u1", p.ID, store.RoleTarget, SaveInput{Type: store.KindCouchbase, Config: json.RawMessage(`{"host":"cb"}`), Secrets: SecretsPatch{Password: ptr("pw")}})
	require.NoError(t, err)
	return svc, pt, st, p.ID
}

func TestPorts_OpenTargetAndLocations(t *testing.T) {
	svc, pt, _, pid := portsFixture(t)
	id := store.ConnectionID(pid, store.RoleTarget)

	nodes, tg, err := svc.ListLocations(ctx, ws, id, "shop/sales")
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, store.KindCouchbase, tg.Kind())
	require.Equal(t, target.Location{Kind: "couchbase", Bucket: "shop", Scope: "sales"}, pt.gotLoc)
	require.Equal(t, "pw", pt.gotConn.Creds.Password, "stored credentials are decrypted for the port")
	require.Equal(t, "1", pt.gotConn.Version)
	require.JSONEq(t, `{"host":"cb"}`, string(pt.gotConn.Config))

	n, err := svc.StatLocation(ctx, ws, id, store.Location{Bucket: "b", Scope: "s", Collection: "c"})
	require.NoError(t, err)
	require.EqualValues(t, 3, *n.DocCount)
	require.Equal(t, store.KindCouchbase, pt.gotLoc.Kind, "the kind comes from the stored connection")

	_, err = svc.CreateLocation(ctx, ws, id, store.Location{Bucket: "b", Scope: "s", Collection: "new"})
	require.NoError(t, err)
	require.Equal(t, "new", pt.gotLoc.Collection)

	res, err := svc.PreflightTarget(ctx, ws, id, []target.PreflightLocation{{MappingID: "m1"}, {MappingID: "m2"}})
	require.NoError(t, err)
	require.Len(t, res, 2)
	require.Len(t, pt.gotLocs, 2)

	caps, kind, err := svc.Capabilities(ctx, ws, id)
	require.NoError(t, err)
	require.Equal(t, store.KindCouchbase, kind)
	require.True(t, caps.RequiresCredentials)
	require.Len(t, svc.TargetKinds(), 2)
}

func TestPorts_Errors(t *testing.T) {
	svc, _, st, pid := portsFixture(t)
	_, err := svc.OpenTarget(ctx, ws, "nope")
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = st.Connections().Upsert(ctx, ws, pid, store.RoleSource, store.ConnectionInput{Type: "postgres", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, _, err = svc.ListLocations(ctx, ws, store.ConnectionID(pid, store.RoleSource), "")
	require.True(t, problem.HasCode(err, problem.CodeValidation), "a source has no locations")

	// A kind without the ports says so.
	_, err = st.Connections().Upsert(ctx, ws, pid, store.RoleTarget, store.ConnectionInput{Type: "plain", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	id := store.ConnectionID(pid, store.RoleTarget)
	_, _, err = svc.ListLocations(ctx, ws, id, "")
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))
	_, err = svc.StatLocation(ctx, ws, id, store.Location{})
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))
	_, err = svc.CreateLocation(ctx, ws, id, store.Location{})
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))
	_, err = svc.PreflightTarget(ctx, ws, id, nil)
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))
	_, _, err = svc.Capabilities(ctx, ws, "nope")
	require.ErrorIs(t, err, store.ErrNotFound)

	// An unknown stored kind (e.g. removed from this build).
	_, err = st.Connections().Upsert(ctx, ws, pid, store.RoleTarget, store.ConnectionInput{Type: "gone", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, err = svc.OpenTarget(ctx, ws, id)
	require.True(t, problem.HasCode(err, problem.CodeTargetKindUnknown))
	_, _, err = svc.Capabilities(ctx, ws, id)
	require.True(t, problem.HasCode(err, problem.CodeTargetKindUnknown))
}

func TestPorts_CreateLocationNeedsTheCapability(t *testing.T) {
	st := memstore.New(storetest.NewClock())
	noCreate := &noCreateTarget{portTarget: &portTarget{fakeTarget: &fakeTarget{typ: "ro"}}}
	targets := target.NewRegistry()
	targets.Register(noCreate)
	targets.Register(directory.New([]string{}))
	svc := NewService(st, sealer(t), source.NewRegistry(), targets, nil, nil)
	p, _ := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	_, err := st.Connections().Upsert(ctx, ws, p.ID, store.RoleTarget, store.ConnectionInput{Type: "ro", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, err = svc.CreateLocation(ctx, ws, store.ConnectionID(p.ID, store.RoleTarget), store.Location{})
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))

	// Testing unsaved values of a disabled kind is refused before anything runs.
	_, err = svc.TestUnsaved(ctx, store.RoleTarget, store.KindDirectory, json.RawMessage(`{"path":"/data"}`), SecretsPatch{})
	require.True(t, problem.HasCode(err, problem.CodeTargetKindDisabled))
}

type noCreateTarget struct{ *portTarget }

func (n *noCreateTarget) Capabilities() target.Capabilities { return target.Capabilities{} }

// dirService is a project whose target is a real directory kind over a temp folder.
func dirService(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	st := memstore.New(storetest.NewClock())
	targets := target.NewRegistry()
	targets.Register(directory.New([]string{root}))
	svc := NewService(st, sealer(t), source.NewRegistry(), targets, nil, nil)
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	_, _, err = svc.Save(ctx, ws, "u1", p.ID, store.RoleTarget, SaveInput{Type: store.KindDirectory, Config: json.RawMessage(`{"path":"` + root + `","durability":"none"}`)})
	require.NoError(t, err)
	return svc, store.ConnectionID(p.ID, store.RoleTarget), root
}

func TestPorts_DirectoryDocumentsCountAndCase(t *testing.T) {
	svc, id, root := dirService(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "orders", "1001.json"), []byte(`{"id":1001}`), 0o600))
	loc := store.Location{Folder: "orders"}

	d, err := svc.FindDocument(ctx, ws, id, loc, "1001")
	require.NoError(t, err)
	require.Equal(t, "orders/1001.json", d.Path)
	items, _, err := svc.ListOutput(ctx, ws, id, loc, "m1", "", 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	n, err := svc.CountDocuments(ctx, ws, id, loc)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, err = svc.CaseSensitive(ctx, ws, id)
	require.NoError(t, err)

	_, err = svc.FindDocument(ctx, ws, "nope", loc, "1")
	require.ErrorIs(t, err, store.ErrNotFound)
	_, _, err = svc.ListOutput(ctx, ws, "nope", loc, "m1", "", 1)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.CountDocuments(ctx, ws, "nope", loc)
	require.ErrorIs(t, err, store.ErrNotFound)
	sensitive, err := svc.CaseSensitive(ctx, ws, "nope")
	require.ErrorIs(t, err, store.ErrNotFound)
	require.True(t, sensitive)
}

func TestPorts_KindsWithoutTheNewPorts(t *testing.T) {
	svc, _, _, pid := portsFixture(t) // a Couchbase-like kind with a browser only
	id := store.ConnectionID(pid, store.RoleTarget)
	loc := store.Location{Bucket: "b", Scope: "s", Collection: "c"}
	_, err := svc.FindDocument(ctx, ws, id, loc, "k")
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))
	_, _, err = svc.ListOutput(ctx, ws, id, loc, "m1", "", 1)
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))
	sensitive, err := svc.CaseSensitive(ctx, ws, id)
	require.NoError(t, err)
	require.True(t, sensitive, "kinds that don't probe case tell keys apart")
	// Counting falls back to the browser's count, and says so when there is none.
	n, err := svc.CountDocuments(ctx, ws, id, loc)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	_, err = svc.CountDocuments(ctx, ws, id, store.Location{Collection: "missing"})
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
}

func TestFolderPicker_ServiceErrors(t *testing.T) {
	svc, _, root := dirService(t)
	l, err := svc.DirectoryFolders("")
	require.NoError(t, err)
	require.NotEmpty(t, l.Folders)
	_, err = svc.MakeDirectoryFolder(ctx, ws, "u1", root, ".hidden")
	require.True(t, problem.HasCode(err, problem.CodeValidation))
	path, err := svc.MakeDirectoryFolder(ctx, ws, "u1", root, "exports")
	require.NoError(t, err)
	require.DirExists(t, path)

	// A registry without the directory kind.
	none := NewService(memstore.New(nil), sealer(t), source.NewRegistry(), target.NewRegistry(), nil, nil)
	_, err = none.DirectoryFolders("")
	require.True(t, problem.HasCode(err, problem.CodeTargetKindUnknown))
	_, err = none.MakeDirectoryFolder(ctx, ws, "u1", "/x", "y")
	require.True(t, problem.HasCode(err, problem.CodeTargetKindUnknown))
}
