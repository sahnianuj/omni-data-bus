package crypto

import "log/slog"

const masked = "***"

// Secret holds a plaintext credential in memory. It never prints, logs or marshals its value;
// call Reveal only at the point of use (e.g. building a driver config).
type Secret string

// Reveal returns the plaintext.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer (%v, %s, %+v).
func (Secret) String() string { return masked }

// GoString implements fmt.GoStringer (%#v).
func (Secret) GoString() string { return masked }

// MarshalJSON implements json.Marshaler.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + masked + `"`), nil }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(masked) }
