// Package pathcodec turns a document key into a file name that is valid on Linux, macOS and
// Windows, and back (OC-039). It's mirrored in web/src/lib/pathcodec; both run the fixtures in
// testdata/pathcodec.json.
//
// Encoding: the key is normalized to NFC, then these bytes become %XX (upper-case hex): / \ : *
// ? " < > | % ~, control characters (0x00–0x1F, 0x7F), a leading . or -, and a trailing . or
// space. The first character of a Windows reserved name (CON, PRN, AUX, NUL, COM1–9, LPT1–9,
// with or without an extension) is encoded too. Other UTF-8 is kept. A name longer than MaxName
// bytes becomes "<first 200 bytes>~<16 hex of sha1(key)>": not reversible (Hashed), so it's
// listed in the manifest. "~" is always escaped elsewhere, so it only ever marks a hashed name.
package pathcodec

import (
	"crypto/sha1" //nolint:gosec // a name, not security
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Version is recorded in _odb/run.json so readers know how names were built.
const Version = 1

// Name limits: file names are at most 255 bytes; MaxName leaves room for ".json.gz".
const (
	MaxName    = 247
	hashedHead = 200
	hashLen    = 16
)

// Separator splits a key into folders in the key-path layout.
const Separator = "::"

func unsafe(b byte) bool {
	switch b {
	case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '%', '~':
		return true
	}
	return b < 0x20 || b == 0x7F
}

var reserved = func() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	for _, p := range []string{"COM", "LPT"} {
		for d := '1'; d <= '9'; d++ {
			m[p+string(d)] = true
		}
	}
	return m
}()

const hexDigits = "0123456789ABCDEF"

func esc(sb *strings.Builder, b byte) {
	sb.WriteByte('%')
	sb.WriteByte(hexDigits[b>>4])
	sb.WriteByte(hexDigits[b&0xF])
}

// NFC is the normalized key (what Decode returns).
func NFC(key string) string { return norm.NFC.String(key) }

// Encode returns key's file name (without extension) and whether it had to be hashed.
func Encode(key string) (name string, hashed bool) {
	k := NFC(key)
	var sb strings.Builder
	sb.Grow(len(k) + 8)
	base := k
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	isReserved := reserved[strings.ToUpper(base)]
	for i := 0; i < len(k); i++ {
		b := k[i]
		switch {
		case unsafe(b),
			i == 0 && (b == '.' || b == '-' || isReserved),
			i == len(k)-1 && (b == '.' || b == ' '):
			esc(&sb, b)
		default:
			sb.WriteByte(b)
		}
	}
	name = sb.String()
	if len(name) <= MaxName {
		return name, false
	}
	head := name[:hashedHead]
	// Don't cut an escape or a UTF-8 character in half.
	if i := strings.LastIndexByte(head, '%'); i >= hashedHead-2 {
		head = head[:i]
	}
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	sum := sha1.Sum([]byte(k)) //nolint:gosec // a name, not security
	return head + "~" + hex.EncodeToString(sum[:])[:hashLen], true
}

// Decode reverses Encode; ok is false for a hashed name or one that isn't an encoding.
func Decode(name string) (key string, ok bool) {
	if strings.Contains(name, "~") {
		return "", false
	}
	var sb strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c != '%' {
			sb.WriteByte(c)
			continue
		}
		if i+2 >= len(name) {
			return "", false
		}
		hi, lo := strings.IndexByte(hexDigits, name[i+1]), strings.IndexByte(hexDigits, name[i+2])
		if hi < 0 || lo < 0 {
			return "", false
		}
		sb.WriteByte(byte(hi<<4 | lo)) //nolint:gosec // two hex digits: 0..255
		i += 2
	}
	return sb.String(), true
}

// SegmentName encodes one key-path segment: like Encode, but an empty segment ("a::::b")
// becomes %00 so it's still a folder.
func SegmentName(seg string) (string, bool) {
	if seg == "" {
		return "%00", false
	}
	return Encode(seg)
}
