package preview

import (
	"context"
	"net/http"
	"sort"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
)

// WorstCase is the root row with the most rows of one relation (Rows), found for the preview.
type WorstCase struct {
	RelationID string
	Path       string
	Rows       int64
}

// Related looks up the rows' related rows the way a migration does (OC-021), reading at
// most Cap+1 per parent (OC-049), and counts every row of the arrays directly under
// the root, per root row (totals[i]: relation id → count), when the source can.
func Related(ctx context.Context, rs source.ReadSession, pl *plan.ExecutionPlan, spec *mapping.Spec, rows [][]any) ([]source.Related, map[int]source.RowFailure, []map[string]int64, error) {
	if rs == nil || !pl.HasRelations() || len(rows) == 0 {
		return nil, nil, nil, nil
	}
	rr, ok := rs.(source.RelatedReader)
	if !ok {
		return nil, nil, nil, nil
	}
	res := &related.Resolver{Plans: Capped(pl.Lookups()), Reader: rr, Concurrency: related.DefaultConcurrency}
	out, fails, err := res.Resolve(ctx, rows)
	if err != nil {
		return nil, nil, nil, err
	}
	totals := make([]map[string]int64, len(rows))
	counter, ok := rs.(source.RelatedCounter)
	if !ok {
		return out, fails, totals, nil
	}
	for _, l := range pl.Lookups() {
		if l.Depth != 1 || !l.Many {
			continue
		}
		if r := spec.RelationByID(l.ID); r == nil || r.LookupOnly || r.TargetFieldID == "" {
			continue
		}
		keys := make([][]any, len(rows))
		for i, row := range rows {
			for _, idx := range l.ParentIdx {
				keys[i] = append(keys[i], row[idx])
			}
		}
		counts, err := counter.CountRelated(ctx, l.Query, keys)
		if err != nil {
			return nil, nil, nil, err
		}
		for i := range rows {
			if i < len(counts) {
				if totals[i] == nil {
					totals[i] = map[string]int64{}
				}
				totals[i][l.ID] = counts[i]
			}
		}
	}
	return out, fails, totals, nil
}

// RowsByKey reads the driving table's row with these column values (the preview's sample
// navigation, OC-049).
func RowsByKey(ctx context.Context, rs source.ReadSession, drv mapping.TableRef, t *schema.Table, key map[string]any) ([][]any, error) {
	if rs == nil {
		return nil, nil
	}
	cols := make([]string, 0, len(key))
	for c := range key {
		if t.Column(c) == nil {
			return nil, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Unknown column", "Column "+c+" isn't in "+drv.String()+".")
		}
		cols = append(cols, c)
	}
	sort.Strings(cols)
	vals := make([]any, len(cols))
	for i, c := range cols {
		vals[i] = key[c]
	}
	rows, err := rs.ReadKeys(ctx, readAll(drv, t, cols), [][]any{vals})
	if len(rows) > 1 {
		rows = rows[:1]
	}
	return rows, err
}

// WorstCaseRows reads the root row with the most rows of relation relID (an array directly
// under the root), found with ORDER BY count DESC LIMIT 1 under a 5 s statement budget.
func WorstCaseRows(ctx context.Context, rs source.ReadSession, pl *plan.ExecutionPlan, spec *mapping.Spec, drv mapping.TableRef, t *schema.Table, relID string) ([][]any, *WorstCase, error) {
	notArray := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "No worst case",
		"The worst case is found for an array bound directly under the root.")
	r := spec.RelationByID(relID)
	var l *related.Plan
	for i := range pl.Lookups() {
		if pl.Lookups()[i].ID == relID {
			l = &pl.Lookups()[i]
		}
	}
	if r == nil || l == nil || l.Depth != 1 || !l.Many || r.TargetFieldID == "" {
		return nil, nil, notArray
	}
	wc := &WorstCase{RelationID: relID, Path: NodePaths(spec)[relID]}
	counter, ok := rs.(source.RelatedCounter)
	if !ok {
		return nil, wc, nil
	}
	tctx, cancel := context.WithTimeout(ctx, worstCaseTimeout)
	defer cancel()
	key, n, found, err := counter.TopRelated(tctx, l.Query)
	if err != nil || !found {
		return nil, wc, err
	}
	wc.Rows = n
	var cols []string
	for _, i := range l.ParentIdx {
		cols = append(cols, t.Columns[i].Name)
	}
	rows, err := rs.ReadKeys(ctx, readAll(drv, t, cols), [][]any{key})
	if len(rows) > 1 {
		rows = rows[:1]
	}
	return rows, wc, err
}

// worstCaseTimeout bounds the worst case's count query (OC-049).
const worstCaseTimeout = 5 * time.Second

// readAll reads every column of the rows with these key values, one row per distinct key (the
// first by primary key), so a grouped driving table gives one row per group (OC-063).
func readAll(drv mapping.TableRef, t *schema.Table, key []string) source.TableRead {
	project := make([]bool, len(t.Columns))
	for i := range project {
		project[i] = true
	}
	return source.TableRead{Schema: drv.Schema, Table: drv.Name, Columns: t.Columns, Key: key, Project: project, Distinct: true, Within: t.PrimaryKey}
}
