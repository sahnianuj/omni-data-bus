package docschema

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/schema"
)

func customerTable() *schema.Table {
	return &schema.Table{Name: "customer", Columns: []schema.Column{
		{Name: "customer_id", Ordinal: 1, UDTName: "int4", JSONType: schema.TypeInteger, IsPrimaryKey: true},
		{Name: "first_name", Ordinal: 2, UDTName: "text", JSONType: schema.TypeString, Nullable: true},
		{Name: "billing_street", Ordinal: 3, UDTName: "text", JSONType: schema.TypeString, Nullable: true},
		{Name: "billing_city", Ordinal: 4, UDTName: "text", JSONType: schema.TypeString, Nullable: true},
		{Name: "created_at", Ordinal: 5, UDTName: "timestamptz", JSONType: schema.TypeDatetime},
		{Name: "tags", Ordinal: 6, UDTName: "_text", JSONType: schema.TypeArray, ElementType: schema.TypeString, IsArray: true, Nullable: true},
		{Name: "external_ref", Ordinal: 7, UDTName: "uuid", JSONType: schema.TypeString, Nullable: true},
	}}
}

type shape struct {
	Name     string
	Type     FieldType
	Required bool
	Format   string
	Children []shape
}

func shapeOf(fs []DocField) []shape {
	out := []shape{}
	for _, f := range fs {
		out = append(out, shape{Name: f.Name, Type: f.Type, Required: f.Required, Format: f.Format, Children: shapeOf(f.Children)})
	}
	return out
}

func TestFromTable_AC1_CamelCaseAndTypes(t *testing.T) {
	s := FromTable(customerTable(), GenerateOptions{})
	require.Equal(t, SourceGenerated, s.Source)
	require.Empty(t, Validate(&s))
	require.Equal(t, []shape{
		{Name: "customerId", Type: Integer, Required: true, Children: []shape{}},
		{Name: "firstName", Type: String, Children: []shape{}},
		{Name: "billingStreet", Type: String, Children: []shape{}},
		{Name: "billingCity", Type: String, Children: []shape{}},
		{Name: "createdAt", Type: Datetime, Required: true, Format: "iso8601", Children: []shape{}},
		{Name: "tags", Type: Array, Children: []shape{}},
		{Name: "externalRef", Type: String, Format: "uuid", Children: []shape{}},
	}, shapeOf(s.Root.Children))
	require.Equal(t, String, s.Root.Children[5].Items.Type, "array<string>")
}

func TestFromTable_AC1_NestByPrefix(t *testing.T) {
	s := FromTable(customerTable(), GenerateOptions{NestByPrefix: true})
	require.Empty(t, Validate(&s))
	names := []string{}
	for _, f := range s.Root.Children {
		names = append(names, f.Name)
	}
	// customer_id, first_name, created_at and external_ref share no prefix with another column.
	require.Equal(t, []string{"customerId", "firstName", "billing", "createdAt", "tags", "externalRef"}, names)
	billing := s.Root.Children[2]
	require.Equal(t, Object, billing.Type)
	require.Equal(t, []shape{{Name: "street", Type: String, Children: []shape{}}, {Name: "city", Type: String, Children: []shape{}}}, shapeOf(billing.Children))
}

func TestFromTable_AC1_PreserveNaming(t *testing.T) {
	s := FromTable(customerTable(), GenerateOptions{Naming: NamingPreserve})
	for i, c := range customerTable().Columns {
		require.Equal(t, c.Name, s.Root.Children[i].Name)
	}
	snake := FromTable(&schema.Table{Columns: []schema.Column{{Name: "orderID", JSONType: schema.TypeInteger}}}, GenerateOptions{Naming: NamingSnake})
	require.Equal(t, "order_id", snake.Root.Children[0].Name)
	nested := FromTable(customerTable(), GenerateOptions{Naming: NamingPreserve, NestByPrefix: true})
	require.Equal(t, "street", nested.Root.Children[2].Children[0].Name)
}

func TestFromTable_DedupesCollidingNames(t *testing.T) {
	s := FromTable(&schema.Table{Columns: []schema.Column{
		{Name: "Order_Id", JSONType: schema.TypeInteger},
		{Name: "order_id", JSONType: schema.TypeInteger},
		{Name: "stamp", UDTName: "date", JSONType: schema.TypeDatetime},
		{Name: "doc", JSONType: schema.TypeAny},
		{Name: "flag", JSONType: schema.TypeBoolean},
		{Name: "amount", JSONType: schema.TypeNumber},
	}}, GenerateOptions{})
	require.Equal(t, "orderId", s.Root.Children[0].Name)
	require.Equal(t, "orderId2", s.Root.Children[1].Name)
	require.Equal(t, "date", s.Root.Children[2].Format)
	require.Equal(t, Any, s.Root.Children[3].Type)
	require.Empty(t, Validate(&s))
}
