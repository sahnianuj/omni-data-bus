package automap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokens_AC1_CaseAndSeparators(t *testing.T) {
	d := DefaultDicts()
	for _, n := range []string{"customerID", "Customer_Id", "CUSTOMER-ID", "customer id", "customer.id"} {
		require.Equal(t, []string{"customer", "id"}, d.Tokens(n), n)
	}
	require.Equal(t, []string{"address", "2"}, d.Tokens("addr2"))
	require.Equal(t, []string{"order"}, d.Tokens("tbl_order_col"), "noise tokens dropped")
}

func TestTokens_AC1_AbbreviationExpansion(t *testing.T) {
	d := DefaultDicts()
	require.Equal(t, []string{"customer", "id"}, d.Tokens("cust_id"))
	require.Equal(t, []string{"order", "number"}, d.Tokens("order_no"))
	require.Equal(t, []string{"birth", "date"}, d.Tokens("dob"))
	require.Equal(t, []string{"first", "name"}, d.Tokens("fname"))
	require.Equal(t, []string{"postal", "code"}, d.Tokens("zip"))
}

func TestTokens_AC1_Singularize(t *testing.T) {
	d := DefaultDicts()
	require.Equal(t, []string{"customer", "id"}, d.Tokens("customerIds"))
	require.Equal(t, []string{"person"}, d.Tokens("people"))
	require.Equal(t, []string{"status"}, d.Tokens("statuses"))
	require.Equal(t, []string{"data"}, d.Tokens("data"))
	require.Equal(t, []string{"category"}, d.Tokens("categories"))
}

func TestTokens_AC1_TablePrefixStripped(t *testing.T) {
	d := DefaultDicts()
	vs := d.sourceVariants(SourceCol{Name: "cust_name", Table: "customer"})
	require.Len(t, vs, 2)
	require.Equal(t, "prefix-stripped", vs[1].kind)
	require.Equal(t, "name", vs[1].expanded)
	vs = d.sourceVariants(SourceCol{Name: "customer_email", Table: "customers"})
	require.Equal(t, "email", vs[1].expanded)
	vs = d.sourceVariants(SourceCol{Name: "customer", Table: "customer"})
	require.Len(t, vs, 1, "nothing left after the prefix")
	vs = d.sourceVariants(SourceCol{Name: "order_id", Table: "customer", IsPK: true})
	require.Equal(t, "pk-alias", vs[len(vs)-1].kind)
}

func TestDicts_AC2_NoTokenInTwoSynonymGroups(t *testing.T) {
	d := DefaultDicts()
	seen := map[string]int{}
	for gi, g := range d.Groups {
		for _, p := range g {
			prev, dup := seen[p]
			require.False(t, dup && prev != gi, "%q is in groups %d and %d", p, prev, gi)
			seen[p] = gi
		}
	}
	require.Greater(t, len(d.Abbreviations), 100)
	require.True(t, d.Synonyms("email", "email address"))
	require.False(t, d.Synonyms("email", "phone"))
}
