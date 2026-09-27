package transform

import "github.com/jackc/pgx/v5/pgtype"

func mustNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		panic(err)
	}
	return n
}
