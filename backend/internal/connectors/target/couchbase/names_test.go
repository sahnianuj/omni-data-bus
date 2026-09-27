package couchbase

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

type nameCase struct {
	Name  string `json:"name"`
	Valid bool   `json:"valid"`
}

type fixture struct {
	Bucket     []nameCase `json:"bucket"`
	Scope      []nameCase `json:"scope"`
	Collection []nameCase `json:"collection"`
	BucketSpec []struct {
		Spec  target.BucketSpec `json:"spec"`
		Valid bool              `json:"valid"`
		Field string            `json:"field"`
	} `json:"bucketSpec"`
	MaxExpirySec []struct {
		Value int64 `json:"value"`
		Valid bool  `json:"valid"`
	} `json:"maxExpirySec"`
}

// The same fixture drives web/src/features/targets/names.test.ts.
func TestNames_AC1_SharedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/keyspace-names.json")
	require.NoError(t, err)
	var fx fixture
	require.NoError(t, json.Unmarshal(raw, &fx))
	require.NotEmpty(t, fx.Bucket)

	for _, c := range fx.Bucket {
		require.Equal(t, c.Valid, ValidBucketName(c.Name), "bucket %q", c.Name)
	}
	for _, c := range fx.Scope {
		require.Equal(t, c.Valid, ValidateScopeName(c.Name) == nil, "scope %q", c.Name)
	}
	for _, c := range fx.Collection {
		require.Equal(t, c.Valid, ValidateCollectionSpec(target.CollectionSpec{Name: c.Name}) == nil, "collection %q", c.Name)
	}
	for _, c := range fx.BucketSpec {
		err := ValidateBucketSpec(c.Spec)
		require.Equal(t, c.Valid, err == nil, "bucket spec %+v: %v", c.Spec, err)
		if !c.Valid {
			require.Equal(t, c.Field, problem.From(err).FieldErrors[0].Field)
		}
	}
	for _, c := range fx.MaxExpirySec {
		require.Equal(t, c.Valid, ValidateCollectionSpec(target.CollectionSpec{Name: "c", MaxExpirySec: c.Value}) == nil, "ttl %d", c.Value)
	}
}

func TestBucketSpec_AC1_MagmaMinQuota(t *testing.T) {
	spec := target.BucketSpec{Name: "big", RAMQuotaMB: 1023, BucketType: "couchbase", StorageBackend: "magma"}
	require.Equal(t, "ramQuotaMB", problem.From(ValidateBucketSpec(spec)).FieldErrors[0].Field)
	spec.RAMQuotaMB = 1024
	require.NoError(t, ValidateBucketSpec(spec))
}
