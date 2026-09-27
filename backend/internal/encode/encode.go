// Package encode turns source values into JSON-ready values following the type normalization
// rules (architecture §6.4). The data preview (OC-011) and the migration pipeline (OC-020)
// share it, so a preview shows exactly what a migration will write.
package encode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"omnidatabus/internal/schema"
)

// maxSafeDigits is the precision a float64 represents exactly; wider numerics become strings.
const maxSafeDigits = 15

// Options adjust encodings (OC-020). The zero value is the default behaviour.
type Options struct {
	// NumericMode: "" / "auto" (number when the declared precision is ≤ 15, else string),
	// "number" or "string".
	NumericMode string `json:"numericMode,omitempty"`
	// BigintAsString: "" / "auto" (string only beyond ±2^53−1), "always" or "never".
	BigintAsString string `json:"bigintAsString,omitempty"`
	// AssumeTimezone "UTC" writes timestamp (without zone) values with a Z suffix.
	AssumeTimezone string `json:"assumeTimezone,omitempty"`
}

const maxSafeInt = 1<<53 - 1

// Value encodes one value of col with the default options. The result is nil, bool, int64,
// float64, json.Number, string, a JSON value (json/jsonb), or a []any of encoded elements.
func Value(v any, col schema.Column) any { return ValueWith(v, col, Options{}) }

// ValueWith encodes one value of col.
func ValueWith(v any, col schema.Column, opts Options) any {
	if v == nil {
		return nil
	}
	if col.JSONType == schema.TypeAny {
		return jsonValue(v)
	}
	if col.IsArray {
		items, ok := v.([]any)
		if !ok {
			return fmt.Sprint(v)
		}
		elem := schema.Column{UDTName: strings.TrimPrefix(col.UDTName, "_"), JSONType: col.ElementType}
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = ValueWith(it, elem, opts)
		}
		return out
	}
	switch x := v.(type) {
	case int64:
		return bigint(x, col, opts)
	case bool, string:
		if s, ok := x.(string); ok && col.UDTName == "bpchar" {
			return strings.TrimRight(s, " ") // char(n) is right-trimmed
		}
		return x
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		// Widen through float32's shortest decimal form, so 0.1 stays 0.1 (not 0.10000000149…).
		f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(x), 'g', -1, 32), 64)
		return finite(f)
	case float64:
		return finite(x)
	case pgtype.Numeric:
		switch opts.NumericMode {
		case "number":
			wide := 1000
			return numeric(x, &wide, true)
		case "string":
			wide := 1000
			return numeric(x, &wide, false)
		}
		return numeric(x, col.NumericPrecision, false)
	case time.Time:
		ts := timestamp(x, col.UDTName)
		if col.UDTName == "timestamp" && strings.EqualFold(opts.AssumeTimezone, "UTC") {
			ts += "Z"
		}
		return ts
	case [16]byte:
		return uuidString(x)
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	case pgtype.Time:
		return clock(x.Microseconds)
	case pgtype.Interval:
		return Interval(x)
	case netip.Prefix:
		if x.Bits() == x.Addr().BitLen() && col.UDTName == "inet" {
			return x.Addr().String() // a host address prints without /32
		}
		return x.String()
	case netip.Addr:
		return x.String()
	case net.HardwareAddr:
		return x.String()
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

// bigint keeps int8 values as numbers unless they exceed what JavaScript (and many JSON
// readers) represent exactly, or the options say otherwise.
func bigint(x int64, col schema.Column, opts Options) any {
	if col.UDTName != "int8" {
		return x
	}
	switch opts.BigintAsString {
	case "always":
		return strconv.FormatInt(x, 10)
	case "never":
		return x
	}
	if x > maxSafeInt || x < -maxSafeInt {
		return strconv.FormatInt(x, 10)
	}
	return x
}

// jsonValue decodes json/jsonb read as text with UseNumber, so large numbers stay exact
// (pgx's own decoding goes through float64). Already-decoded values pass through.
func jsonValue(v any) any {
	var raw []byte
	switch x := v.(type) {
	case string:
		raw = []byte(x)
	case []byte:
		raw = x
	default:
		return v
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return string(raw)
	}
	return out
}

// finite maps NaN and ±Inf (not representable in JSON) to null.
func finite(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

// numeric is a number when the column's declared precision is ≤ 15 digits, otherwise its exact
// decimal string, so a column always encodes to one JSON type and wide values never lose
// digits. Unconstrained numeric (no declared precision) decides per value.
func numeric(n pgtype.Numeric, precision *int, forceNumber bool) any {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite {
		return nil
	}
	raw, err := n.Value()
	if err != nil {
		return nil
	}
	s, _ := raw.(string)
	fits := significantDigits(s) <= maxSafeDigits
	if precision != nil {
		fits = *precision <= maxSafeDigits
	}
	if forceNumber {
		return json.Number(s) // exact digits, written as a JSON number
	}
	if fits {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}

func significantDigits(s string) int {
	s = strings.TrimLeft(strings.TrimPrefix(s, "-"), "0.")
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	s = strings.Replace(s, ".", "", 1)
	return len(strings.TrimRight(s, "0"))
}

func timestamp(t time.Time, udt string) string {
	switch udt {
	case "date":
		return t.Format("2006-01-02")
	case "timestamp":
		return t.Format("2006-01-02T15:04:05.999999") // no zone: a wall-clock time
	}
	return t.UTC().Format("2006-01-02T15:04:05.999999Z07:00") // timestamptz → UTC with Z
}

func uuidString(b [16]byte) string {
	const hex = "0123456789abcdef"
	var out [36]byte
	j := 0
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j], out[j+1] = hex[c>>4], hex[c&0xf]
		j += 2
	}
	return string(out[:])
}

// clock formats microseconds since midnight as HH:MM:SS[.ffffff].
func clock(us int64) string {
	h, us := us/3_600_000_000, us%3_600_000_000
	m, us := us/60_000_000, us%60_000_000
	s, us := us/1_000_000, us%1_000_000
	out := fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	if us > 0 {
		out += strings.TrimRight(fmt.Sprintf(".%06d", us), "0")
	}
	return out
}

// Interval formats an interval as an ISO-8601 duration, e.g. P1Y6M3DT4H5M6.5S.
func Interval(iv pgtype.Interval) string {
	var b strings.Builder
	b.WriteString("P")
	years, months := iv.Months/12, iv.Months%12
	for _, part := range []struct {
		n    int64
		unit string
	}{{int64(years), "Y"}, {int64(months), "M"}, {int64(iv.Days), "D"}} {
		if part.n != 0 {
			fmt.Fprintf(&b, "%d%s", part.n, part.unit)
		}
	}
	us := iv.Microseconds
	if us != 0 {
		b.WriteString("T")
		h, rest := us/3_600_000_000, us%3_600_000_000
		m, rest := rest/60_000_000, rest%60_000_000
		if h != 0 {
			fmt.Fprintf(&b, "%dH", h)
		}
		if m != 0 {
			fmt.Fprintf(&b, "%dM", m)
		}
		if rest != 0 {
			sec := strconv.FormatFloat(float64(rest)/1e6, 'f', -1, 64)
			fmt.Fprintf(&b, "%sS", sec)
		}
	}
	if b.Len() == 1 {
		return "PT0S"
	}
	return b.String()
}
