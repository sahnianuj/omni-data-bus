package directory

import (
	"crypto/sha1" //nolint:gosec // sharding, not security
	"encoding/hex"
	"path"
	"strings"

	"omnidatabus/internal/pathcodec"
)

// Ext is the document file extension: .json, or .json.gz with gzip compression.
func (c Config) Ext() string {
	if c.Compression == "gzip" {
		return ".json.gz"
	}
	return ".json"
}

// DocPath is where the document with key lands, relative to the target folder, with "/" as the
// separator (OC-039). It's a pure function of the key and the config, so anyone holding the key
// can find the file:
//
//	flat      orders/1001.json
//	sharded   orders/<sha1[0:2]>/<sha1[2:4]>/1001.json
//	key-path  orders/2026/10/1001.json   (key 2026::10::1001)
//
// hashed reports a name too long to keep (pathcodec): it's only findable through the manifest.
func (c Config) DocPath(folder, key string) (rel string, hashed bool) {
	folder = strings.Trim(folder, "/")
	switch c.Layout {
	case "key-path":
		segs := strings.Split(key, pathcodec.Separator)
		parts := make([]string, 0, len(segs)+1)
		parts = append(parts, folder)
		for i, s := range segs {
			name, h := pathcodec.SegmentName(s)
			hashed = hashed || h
			if i == len(segs)-1 {
				name += c.Ext()
			}
			parts = append(parts, name)
		}
		return path.Join(parts...), hashed
	case "sharded":
		name, h := pathcodec.Encode(key)
		sum := sha1.Sum([]byte(pathcodec.NFC(key))) //nolint:gosec // sharding, not security
		x := hex.EncodeToString(sum[:2])
		return path.Join(folder, x[:2], x[2:4], name+c.Ext()), h
	default:
		name, h := pathcodec.Encode(key)
		return path.Join(folder, name+c.Ext()), h
	}
}
