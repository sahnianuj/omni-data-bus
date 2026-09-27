package transform

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"omnidatabus/internal/docschema"
)

// Friendly pattern tokens (Java/ICU style) → Go layout pieces, longest first.
var patternTokens = []struct{ tok, layout string }{
	{"yyyy", "2006"},
	{"yy", "06"},
	{"MMMM", "January"},
	{"MMM", "Jan"},
	{"MM", "01"},
	{"M", "1"},
	{"dd", "02"},
	{"d", "2"},
	{"EEEE", "Monday"},
	{"EEE", "Mon"},
	{"HH", "15"},
	{"hh", "03"},
	{"h", "3"},
	{"mm", "04"},
	{"m", "4"},
	{"ss", "05"},
	{"s", "5"},
	{"SSSSSS", "000000"},
	{"SSS", "000"},
	{"a", "PM"},
	{"XXX", "Z07:00"},
	{"xxx", "-07:00"},
	{"Z", "-0700"},
}

// GoLayout converts a friendly pattern (yyyy-MM-dd'T'HH:mm) into a Go time layout. Text in
// single quotes is literal (” is a quote).
func GoLayout(pattern string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c == '\'' {
			if i+1 < len(pattern) && pattern[i+1] == '\'' {
				b.WriteByte('\'') // '' outside quotes: a literal quote
				i += 2
				continue
			}
			// Quoted literal text; '' inside it is a quote.
			j := i + 1
			closed := false
			for j < len(pattern) {
				if pattern[j] == '\'' {
					if j+1 < len(pattern) && pattern[j+1] == '\'' {
						b.WriteByte('\'')
						j += 2
						continue
					}
					closed = true
					break
				}
				b.WriteByte(pattern[j])
				j++
			}
			if !closed {
				return "", fmt.Errorf("unclosed quote in pattern %q", pattern)
			}
			i = j + 1
			continue
		}
		matched := false
		for _, t := range patternTokens {
			if strings.HasPrefix(pattern[i:], t.tok) {
				b.WriteString(t.layout)
				i += len(t.tok)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return "", fmt.Errorf("unknown pattern letter %q (quote literal text with '…')", c)
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}

var isoLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999", "2006-01-02T15:04", "2006-01-02",
}

func loadLocation(args map[string]any) (*time.Location, error) {
	tz, _ := argString(args, "timezone")
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q", tz)
	}
	return loc, nil
}

// asTime reads a datetime value: an ISO-8601 string, or epoch seconds/milliseconds.
func asTime(v any, layout string, loc *time.Location) (time.Time, bool) {
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if layout != "" {
			t, err := time.ParseInLocation(layout, s, loc)
			return t, err == nil
		}
		for _, l := range isoLayouts {
			if t, err := time.ParseInLocation(l, s, loc); err == nil {
				return t, true
			}
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return epoch(float64(n)), true
		}
	case int64:
		return epoch(float64(x)), true
	case float64:
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			return epoch(x), true
		}
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return epoch(f), true
		}
	case time.Time:
		return x, true
	}
	return time.Time{}, false
}

// epoch treats values over 1e11 as milliseconds (1e11 s is the year 5138).
func epoch(n float64) time.Time {
	if math.Abs(n) >= 1e11 {
		return time.UnixMilli(int64(n)).UTC()
	}
	sec, frac := math.Modf(n)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// ISO formats a datetime as RFC 3339 with trailing fractional zeros trimmed.
func ISO(t time.Time) string { return t.Format(time.RFC3339Nano) }

func compileToDatetime(args map[string]any, _ *compileEnv) (opFunc, error) {
	layout := ""
	if p, ok := argString(args, "inputFormat"); ok && p != "" {
		l, err := GoLayout(p)
		if err != nil {
			return nil, err
		}
		layout = l
	}
	loc, err := loadLocation(args)
	if err != nil {
		return nil, err
	}
	strict := argBool(args, "strict", false)
	return func(v any, ctx *stepCtx) (any, error) {
		if v == nil {
			return nil, nil
		}
		t, ok := asTime(v, layout, loc)
		if !ok {
			msg := fmt.Sprintf("can't read %q as a date/time", String(v))
			if strict {
				return nil, &stepErr{code: CodeParseFailed, msg: msg}
			}
			ctx.warn(CodeParseFailed, msg+"; wrote null")
			return nil, nil
		}
		return ISO(t.UTC()), nil
	}, nil
}

func compileFormatDatetime(args map[string]any, _ *compileEnv) (opFunc, error) {
	format, _ := argString(args, "format")
	if format == "" {
		format = "iso8601"
	}
	loc, err := loadLocation(args)
	if err != nil {
		return nil, err
	}
	var layout string
	switch format {
	case "iso8601", "date", "epoch_ms", "epoch_s":
	case "custom":
		p, _ := argString(args, "pattern")
		if p == "" {
			return nil, fmt.Errorf("a custom format needs a pattern, e.g. yyyy-MM-dd HH:mm")
		}
		if layout, err = GoLayout(p); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("format must be iso8601, date, epoch_ms, epoch_s or custom")
	}
	return func(v any, ctx *stepCtx) (any, error) {
		if v == nil {
			return nil, nil
		}
		t, ok := asTime(v, "", time.UTC)
		if !ok {
			ctx.warn(CodeParseFailed, fmt.Sprintf("can't read %q as a date/time; wrote null", String(v)))
			return nil, nil
		}
		t = t.In(loc)
		switch format {
		case "date":
			return t.Format("2006-01-02"), nil
		case "epoch_ms":
			return t.UnixMilli(), nil
		case "epoch_s":
			return t.Unix(), nil
		case "custom":
			return t.Format(layout), nil
		}
		return ISO(t), nil
	}, nil
}

func formatDatetimeType(_ docschema.FieldType, args map[string]any) docschema.FieldType {
	switch f, _ := argString(args, "format"); f {
	case "epoch_ms", "epoch_s":
		return docschema.Integer
	case "custom":
		return docschema.String
	}
	return docschema.Datetime
}
