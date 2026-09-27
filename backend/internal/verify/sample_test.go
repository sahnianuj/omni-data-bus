package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// shop: 100 source rows {id, total}; documents in "Couchbase" keyed order::<id>.
func checkerFixture() (Checker, map[string][]byte, map[int32][]any) {
	rows := map[int32][]any{}
	docs := map[string][]byte{}
	build := func(row []any) (string, []byte) {
		return fmt.Sprintf("order::%d", row[0]), fmt.Appendf(nil, `{"id":%d,"total":%s}`, row[0], row[1])
	}
	for i := int32(1); i <= 100; i++ {
		rows[i] = []any{i, fmt.Sprintf("%d.50", i)}
		k, d := build(rows[i])
		docs[k] = d
	}
	c := Checker{
		Read: func(_ context.Context, keys [][]any) ([][]any, error) {
			var out [][]any
			for _, k := range keys {
				if r, ok := rows[k[0].(int32)]; ok {
					out = append(out, r)
				}
			}
			return out, nil
		},
		KeyOf: func(row []any) []any { return []any{row[0]} },
		Transform: func(_ context.Context, rs [][]any) ([]Built, error) {
			out := make([]Built, len(rs))
			for i, r := range rs {
				if strings.HasPrefix(r[1].(string), "x") {
					out[i].Err = errors.New("parse failed")
					continue
				}
				k, d := build(r)
				out[i] = Built{Key: k, Doc: d}
			}
			return out, nil
		},
		Target: func(_ context.Context, keys []string) (map[string][]byte, error) {
			out := map[string][]byte{}
			for _, k := range keys {
				if d, ok := docs[k]; ok {
					out[k] = d
				}
			}
			return out, nil
		},
	}
	return c, docs, rows
}

func samples() []Sample {
	var out []Sample
	for i := int32(1); i <= 100; i++ {
		out = append(out, Sample{SourceKey: []any{i}, DocKey: fmt.Sprintf("order::%d", i)})
	}
	return out
}

func TestVerifySample_AC3_Outcomes(t *testing.T) {
	c, docs, _ := checkerFixture()
	docs["order::7"] = []byte(`{"total":99, "id":7}`) // modified in Couchbase
	delete(docs, "order::42")                         // deleted in Couchbase
	res, err := c.Check(context.Background(), samples())
	require.NoError(t, err)
	require.Equal(t, 100, res.Checked)
	require.Equal(t, 98, res.Matched)
	require.Equal(t, 1, res.Mismatched)
	require.Equal(t, 1, res.Missing)
	require.Equal(t, []string{"order::42"}, res.MissingKeys)
	require.Equal(t, "order::7", res.Mismatches[0].Key)
	require.Equal(t, []Diff{{Path: "$.total", Expected: jsonNum("7.50"), Actual: jsonNum("99")}}, res.Mismatches[0].Diffs)

	r := MappingResult{Counts: Counts{Reconciled: true}, Sample: &res}
	r.Decide()
	require.Equal(t, Failed, r.Result, "a missing document fails")
}

func TestVerifySample_SourceChangedAndDecisions(t *testing.T) {
	c, _, rows := checkerFixture()
	delete(rows, 3)                // deleted at the source since
	rows[4] = []any{int32(4), "x"} // no longer transforms
	res, err := c.Check(context.Background(), samples())
	require.NoError(t, err)
	require.Equal(t, 2, res.SourceChanged)
	require.Equal(t, 98, res.Matched)
	r := MappingResult{Counts: Counts{Reconciled: true}, Sample: &res}
	r.Decide()
	require.Equal(t, PassedWithWarnings, r.Result)

	ok := MappingResult{Counts: Counts{Reconciled: true, Warnings: []string{CodePartial}}, Sample: &SampleResult{Checked: 100, Matched: 100}}
	ok.Decide()
	require.Equal(t, Passed, ok.Result, "a partial distinct count isn't a warning")
	coll := MappingResult{Counts: Counts{Reconciled: true, Warnings: []string{CodeKeyCollisions}}}
	coll.Decide()
	require.Equal(t, PassedWithWarnings, coll.Result)
	unbalanced := MappingResult{Counts: Counts{Reconciled: false}}
	unbalanced.Decide()
	require.Equal(t, Failed, unbalanced.Result)
	mism := MappingResult{Counts: Counts{Reconciled: true}, Sample: &SampleResult{Checked: 100, Matched: 98, Mismatched: 2}}
	mism.Decide()
	require.Equal(t, Failed, mism.Result, "more than 1% mismatched")
	errd := MappingResult{Error: "can't read back"}
	errd.Decide()
	require.Equal(t, PassedWithWarnings, errd.Result)

	empty, err := c.Check(context.Background(), nil)
	require.NoError(t, err)
	require.Zero(t, empty.Checked)
	c.Read = func(context.Context, [][]any) ([][]any, error) { return nil, errors.New("down") }
	_, err = c.Check(context.Background(), samples())
	require.ErrorContains(t, err, "re-read")
}

func jsonNum(s string) any { return plain(jsonNumber(s)) }

func jsonNumber(s string) json.Number { return json.Number(s) }
