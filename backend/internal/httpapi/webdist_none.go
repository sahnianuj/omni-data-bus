//go:build !embedspa

package httpapi

import "io/fs"

// EmbeddedSPA returns nil in development builds; Vite serves the web app on :5173.
func EmbeddedSPA() fs.FS { return nil }
