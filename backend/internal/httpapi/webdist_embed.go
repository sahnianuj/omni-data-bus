//go:build embedspa

package httpapi

import (
	"embed"
	"io/fs"
)

//go:embed all:webdist
var webdist embed.FS

// EmbeddedSPA returns the web app built into the binary (`make build`).
func EmbeddedSPA() fs.FS {
	sub, err := fs.Sub(webdist, "webdist")
	if err != nil {
		return nil
	}
	return sub
}
