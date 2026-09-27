package sourcetest

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

func TestFakeReader_AC4_Batches(t *testing.T) {
	r := (&Fake{Rows: 2500, BatchSize: 1000}).NewReader()
	var sizes []int
	var seqs []uint64
	var last []int64
	for {
		b, err := r.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		sizes = append(sizes, len(b.Rows))
		seqs = append(seqs, b.Seq)
		last = append(last, b.LastKey[0].(int64))
	}
	require.Equal(t, []int{1000, 1000, 500}, sizes)
	require.Equal(t, []uint64{1, 2, 3}, seqs)
	require.Equal(t, []int64{1000, 2000, 2500}, last)
	require.NoError(t, r.Close())
}

func TestFakeReader_AC4_InjectedError(t *testing.T) {
	r := (&Fake{Rows: 5000, BatchSize: 1000, FailAtBatch: 2}).NewReader()
	_, err := r.Next(context.Background())
	require.NoError(t, err)
	_, err = r.Next(context.Background())
	require.ErrorIs(t, err, ErrInjected)
}

func TestFake_ImplementsConnector(t *testing.T) {
	f := &Fake{Type: "pg"}
	require.Equal(t, "pg", f.Descriptor().Type)
	require.NoError(t, f.Validate([]byte(`{}`)))
	require.Error(t, f.Validate([]byte(`{`)))
	s, err := f.Open(context.Background(), nil, struct {
		Password, CACertPEM, ClientCertPEM, ClientKeyPEM string
	}{})
	require.NoError(t, err)
	info, err := s.Ping(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Fake", info.Product)
	_, err = s.CheckPrivileges(context.Background())
	require.NoError(t, err)
	require.NoError(t, s.Close())
	f.OpenErr = errors.New("down")
	_, err = f.Open(context.Background(), nil, struct {
		Password, CACertPEM, ClientCertPEM, ClientKeyPEM string
	}{})
	require.Error(t, err)
}

func TestFakeDiscovery(t *testing.T) {
	ctx := context.Background()
	f := &Fake{Relations: 3}
	sess, err := f.Open(ctx, nil, connectors.Credentials{})
	require.NoError(t, err)
	n, err := sess.EstimateRelations(ctx, source.DiscoverOptions{})
	require.NoError(t, err)
	require.Equal(t, 3, n)
	snap, err := sess.Discover(ctx, source.DiscoverOptions{})
	require.NoError(t, err)
	require.Equal(t, "fake", snap.Database, "a default empty snapshot")

	f.Snapshot = &schema.Snapshot{Database: "shop"}
	snap, err = sess.Discover(ctx, source.DiscoverOptions{})
	require.NoError(t, err)
	require.Equal(t, "shop", snap.Database)
	require.NotSame(t, f.Snapshot, snap, "callers get a copy")

	f.DiscoverErr = errors.New("nope")
	_, err = sess.Discover(ctx, source.DiscoverOptions{})
	require.ErrorIs(t, err, f.DiscoverErr)
	require.Equal(t, int32(3), f.Discovers.Load())

	f.DiscoverGate = make(chan struct{})
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = sess.Discover(cctx, source.DiscoverOptions{})
	require.ErrorIs(t, err, context.Canceled, "a gated discovery honours cancellation")

	info, err := sess.Ping(ctx)
	require.NoError(t, err)
	require.Equal(t, "Fake", info.Product)
	_, err = sess.CheckPrivileges(ctx)
	require.NoError(t, err)
	require.NoError(t, sess.Close())
	require.NoError(t, f.Validate([]byte(`{}`)))
	require.Error(t, f.Validate([]byte(`{`)))
	require.Equal(t, "fake", f.Descriptor().Type)
	require.Equal(t, f.Result, f.Test(ctx, nil, connectors.Credentials{}))
	f.OpenErr = errors.New("down")
	_, err = f.Open(ctx, nil, connectors.Credentials{})
	require.Error(t, err)
}

func TestFakePreview(t *testing.T) {
	ctx := context.Background()
	f := &Fake{PreviewRows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}}
	sess, err := f.Open(ctx, nil, connectors.Credentials{})
	require.NoError(t, err)
	rows, err := sess.Preview(ctx, source.PreviewRequest{Table: "t", Limit: 2})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "t", f.LastPreview.Table)
	f.PreviewErr = errors.New("denied")
	_, err = sess.Preview(ctx, source.PreviewRequest{})
	require.ErrorIs(t, err, f.PreviewErr)
}
