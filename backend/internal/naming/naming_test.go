package naming

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWords(t *testing.T) {
	cases := map[string][]string{
		"customerID":    {"customer", "ID"},
		"Customer_Id":   {"Customer", "Id"},
		"CUSTOMER-ID":   {"CUSTOMER", "ID"},
		"addr2":         {"addr", "2"},
		"URLValue":      {"URL", "Value"},
		"billing.city":  {"billing", "city"},
		"Order Details": {"Order", "Details"},
		"":              nil,
	}
	for in, want := range cases {
		require.Equal(t, want, Words(in), in)
	}
}

// OC-015 AC1: TestToCamel_AC1_Cases.
func TestToCamel_AC1_Cases(t *testing.T) {
	cases := map[string]string{
		"order_ID2":     "orderId2",
		"URL":           "url",
		"already_camel": "alreadyCamel",
		"alreadyCamel":  "alreadyCamel",
		"first_name":    "firstName",
		"Order Details": "orderDetails",
		"created_at":    "createdAt",
	}
	for in, want := range cases {
		require.Equal(t, want, Camel(in), in)
	}
	require.Equal(t, "order_id", Snake("orderID"))
	require.Equal(t, "order_details", Snake("Order Details"))
	require.Equal(t, "", Camel(""))
}

// OC-016 AC5: TestDefaultTypeValue_AC5_Singularize.
func TestDefaultTypeValue_AC5_Singularize(t *testing.T) {
	cases := map[string]string{
		"customers": "customer", "categories": "category", "order_items": "order_item", "addresses": "address",
		"statuses": "status", "people": "person", "data": "data", "customer": "customer", "Order Details": "order_detail",
		"boxes": "box", "branches": "branch", "analysis": "analysis",
	}
	for in, want := range cases {
		require.Equal(t, want, SingularSnake(in), in)
	}
}

func TestCollectionName_OC014_AC1(t *testing.T) {
	cases := map[string]string{
		"Order Details":          "order_details",
		"customer":               "customer",
		"CustomerOrders":         "customer_orders",
		"_hidden":                "hidden",
		"%pct":                   "pct",
		"ümlaut Größe":           "mlaut_gr_e",
		strings.Repeat("a", 300): strings.Repeat("a", 251),
		"___":                    "collection",
		"order-items":            "order_items",
	}
	for in, want := range cases {
		require.Equal(t, want, CollectionName(in), in)
	}
}

func TestFolderName_OC068(t *testing.T) {
	require.Equal(t, "order_details", FolderName("Order Details"))
	require.Equal(t, "a_b", FolderName("a%b"))
	require.Equal(t, strings.Repeat("a", 64), FolderName(strings.Repeat("a", 300)))
}
