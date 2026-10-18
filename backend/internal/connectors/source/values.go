package source

import (
	"context"
	"fmt"

	"omnidatabus/internal/schema"
)

// DistinctValues reads up to n distinct non-null values of a column, as text (a variant
// discriminator, OC-171), through the session's preview read: one row per value. Errors and
// unknown columns give nothing.
func DistinctValues(ctx context.Context, sess Session, schemaName string, t *schema.Table, column string, n int) []string {
	c := t.Column(column)
	if c == nil || sess == nil {
		return nil
	}
	rows, err := sess.Preview(ctx, PreviewRequest{Schema: schemaName, Table: t.Name, Columns: []schema.Column{*c}, DistinctOn: []string{column}, OrderBy: []string{column}, Limit: n})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r) > 0 && r[0] != nil {
			out = append(out, fmt.Sprint(r[0]))
		}
	}
	return out
}
