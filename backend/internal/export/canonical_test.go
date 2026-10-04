package export

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// RFC 8785 §3.2.2 and §3.2.3 examples, plus the number vectors of Appendix B.
func TestCanonicalJSON_AC1_JCSVectors(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"rfc example", `{
  "numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
  "string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
  "literals": [null, true, false]
}`, `{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€$\u000f\nA'B\"\\\\\"/"}`},
		{
			"utf16 key order", `{"\u20ac":"Euro Sign","\r":"Carriage Return","\ufb33":"Hebrew Letter Dalet With Dagesh","1":"One","\ud83d\ude00":"Emoji: Grinning Face","\u0080":"Control","\u00f6":"Latin Small Letter O With Diaeresis"}`,
			"{\"\\r\":\"Carriage Return\",\"1\":\"One\",\"\u0080\":\"Control\",\"\u00f6\":\"Latin Small Letter O With Diaeresis\",\"\u20ac\":\"Euro Sign\",\"\U0001F600\":\"Emoji: Grinning Face\",\"\ufb33\":\"Hebrew Letter Dalet With Dagesh\"}",
		},
		{"zero", `[0, -0, 0.0]`, `[0,0,0]`},
		{"integers", `[1, -1, 9007199254740992, 1e20, 1e21]`, `[1,-1,9007199254740992,100000000000000000000,1e+21]`},
		{"small", `[1e-7, 1.5e-7, 0.000001]`, `[1e-7,1.5e-7,0.000001]`},
		{"max double", `[1.7976931348623157e308, 5e-324]`, `[1.7976931348623157e+308,5e-324]`},
		{"whitespace and nesting", "{ \"b\" : [ {\"z\":1, \"a\":2} ], \"a\" : {} }", `{"a":{},"b":[{"a":2,"z":1}]}`},
		{"html is not escaped", `{"x":"<a&b>\u2028"}`, "{\"x\":\"<a&b>\u2028\"}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CanonicalJSON([]byte(c.in))
			require.NoError(t, err)
			require.Equal(t, c.want, string(got))
		})
	}
	_, err := CanonicalJSON([]byte(`{"a":1} {}`))
	require.Error(t, err, "trailing data")
}
