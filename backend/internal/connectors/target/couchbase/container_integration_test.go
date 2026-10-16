//go:build integration

package couchbase

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tccouchbase "github.com/testcontainers/testcontainers-go/modules/couchbase"
)

// itContainer is the one Couchbase Server 8.0.3 container (bucket "sandbox") the package's
// integration tests share: starting one per test is slow enough on CI runners to blow the
// go test timeout.
type itContainer struct {
	connStr, user, pass string
	queryOnStdPort      bool
}

var (
	itOnce sync.Once
	itCB   itContainer
	itErr  error
)

// sharedContainer starts the container on first use. The tests never query the bucket
// through a primary index, so none is created: waiting for it is what used to hang.
func sharedContainer(t *testing.T) itContainer {
	t.Helper()
	itOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		c, err := tccouchbase.Run(ctx, "couchbase/server:enterprise-8.0.3",
			tccouchbase.WithBuckets(tccouchbase.NewBucket("sandbox").WithQuota(256).WithPrimaryIndex(false)))
		if c != nil {
			afterTests = append(afterTests, func() { _ = c.Terminate(context.Background()) })
		}
		if itErr = err; err != nil {
			return
		}
		if itCB.connStr, itErr = c.ConnectionString(ctx); itErr != nil {
			return
		}
		itCB.user, itCB.pass = c.Username(), c.Password()
		qp, err := c.MappedPort(ctx, "8093/tcp")
		if itErr = err; err != nil {
			return
		}
		itCB.queryOnStdPort = qp.Port() == "8093"
	})
	require.NoError(t, itErr, "start couchbase container")
	return itCB
}
