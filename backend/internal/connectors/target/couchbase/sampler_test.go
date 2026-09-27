package couchbase

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func TestSampler_AC4_PrefersRangeScan(t *testing.T) {
	f := admin()
	f.version = "7.6.2-3721-enterprise"
	f.scanDocs = []json.RawMessage{json.RawMessage(`{"a":1}`)}
	docs, method, err := browser(f, SelfManaged).SampleDocuments(ctx, "travel-sample", "inventory", "route", 50)
	require.NoError(t, err)
	require.Equal(t, "range-scan", method)
	require.Len(t, docs, 1)
	require.NotContains(t, f.calls, "Query")
}

func TestSampler_AC4_FallsBackToQuery(t *testing.T) {
	f := admin()
	f.version = "7.2.4-7070-enterprise"
	f.queryRows["SELECT RAW c FROM `travel-sample`.`inventory`.`route` AS c LIMIT 50"] = []json.RawMessage{json.RawMessage(`{"b":2}`)}
	docs, method, err := browser(f, SelfManaged).SampleDocuments(ctx, "travel-sample", "inventory", "route", 0)
	require.NoError(t, err)
	require.Equal(t, "query", method)
	require.Len(t, docs, 1)
	require.NotContains(t, f.calls, "SampleScan", "no range scans before 7.6")

	// A failing range scan on 7.6+ also falls back.
	f = admin()
	f.version = "8.0.3"
	f.scanErr = errors.New("feature not available")
	f.queryRows["SELECT RAW c"] = []json.RawMessage{json.RawMessage(`{"b":2}`)}
	_, method, err = browser(f, SelfManaged).SampleDocuments(ctx, "travel-sample", "inventory", "route", 10)
	require.NoError(t, err)
	require.Equal(t, "query", method)
}

func TestSampler_AC4_Unavailable(t *testing.T) {
	f := admin()
	f.version = "7.1.0"
	f.queryErr = errors.New("no query nodes")
	_, _, err := browser(f, SelfManaged).SampleDocuments(ctx, "travel-sample", "inventory", "route", 50)
	require.True(t, problem.HasCode(err, CodeSamplingUnavailable))
	require.Contains(t, problem.From(err).Detail, "7.6+")
	_, _, err = browser(f, SelfManaged).SampleDocuments(ctx, "bad`name", "s", "c", 5)
	require.True(t, problem.HasCode(err, problem.CodeValidation))
}
