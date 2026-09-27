package jobs

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func TestOptions_AC3_ValidateAndDefaults(t *testing.T) {
	o, err := ParseOptions(nil)
	require.NoError(t, err)
	require.Equal(t, DefaultOptions(), o)
	require.NoError(t, o.Validate())

	// Omitted fields keep their defaults; explicit zeros are kept.
	o, err = ParseOptions(json.RawMessage(`{"batchSize":500,"errorPolicy":{"maxErrors":0}}`))
	require.NoError(t, err)
	require.Equal(t, 500, o.BatchSize)
	require.Equal(t, 64, o.WriteConcurrency)
	require.EqualValues(t, 0, o.ErrorPolicy.MaxErrors)
	require.InDelta(t, 0.01, o.ErrorPolicy.MaxErrorRatio, 1e-9, "sibling fields keep their defaults")

	o, err = ParseOptions(json.RawMessage(`{"batchSize":50,"writeConcurrency":1000,"readParallelism":"20","durability":"eventually","writeMode":"merge"}`))
	require.NoError(t, err)
	verr := o.Validate()
	require.Error(t, verr)
	p := problem.From(verr)
	require.Equal(t, 422, p.Status)
	var fields []string
	for _, fe := range p.FieldErrors {
		fields = append(fields, fe.Field)
	}
	require.ElementsMatch(t, []string{"batchSize", "writeConcurrency", "readParallelism", "durability", "writeMode"}, fields)

	fixed := 8
	o = DefaultOptions()
	o.ReadParallelism, o.MaxDocsPerSecond = "8", &fixed
	require.NoError(t, o.Validate())
	require.Equal(t, 8, o.Parallelism())
	require.Equal(t, 0, DefaultOptions().Parallelism(), "auto")

	_, err = ParseOptions(json.RawMessage(`{"batchSize":"lots"}`))
	require.Equal(t, 400, problem.From(err).Status)
}

func TestOptions_TableConcurrency_DefaultThreeRangeOneToSix(t *testing.T) {
	require.Equal(t, 3, DefaultOptions().TableConcurrency, "three mappings migrate at the same time by default")
	for n, ok := range map[int]bool{0: false, 1: true, 3: true, 6: true, 7: false} {
		o := DefaultOptions()
		o.TableConcurrency = n
		if ok {
			require.NoError(t, o.Validate(), n)
		} else {
			require.Error(t, o.Validate(), n)
			require.Equal(t, "tableConcurrency", problem.From(o.Validate()).FieldErrors[0].Field)
		}
	}
}

func TestOptions_ReadParallelism_AutoOrOneToEight(t *testing.T) {
	for v, ok := range map[string]bool{"auto": true, "1": true, "8": true, "0": false, "9": false, "16": false} {
		o := DefaultOptions()
		o.ReadParallelism = v
		if ok {
			require.NoError(t, o.Validate(), v)
		} else {
			require.Error(t, o.Validate(), v)
		}
	}
}
