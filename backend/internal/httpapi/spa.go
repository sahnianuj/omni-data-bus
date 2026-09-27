package httpapi

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"omnidatabus/internal/problem"
)

// SPA serves the built web app from fsys: hashed assets are cached forever and every other
// non-API path falls back to index.html so client-side routes work on reload.
func SPA(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if strings.HasPrefix(p, "api/") {
			problem.Write(w, r, problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "No such API route."))
			return
		}
		if p != "" && p != "index.html" {
			if _, err := fs.Stat(fsys, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			} else if !errors.Is(err, fs.ErrNotExist) {
				problem.Write(w, r, err)
				return
			}
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			problem.Write(w, r, problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "Web app is not built into this binary."))
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
