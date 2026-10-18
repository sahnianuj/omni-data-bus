package httpapi

import (
	"net/http"
	"strings"

	"omnidatabus/internal/legal"
)

// Licenses serves GET /licenses.txt (OC-189): LICENSE, NOTICE and THIRD_PARTY_NOTICES.md as one
// plain-text page, from the binary itself, so the app's About can link it on any network.
// Public: licence texts are meant to be read by anyone who gets the software.
func Licenses(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	for i, f := range legal.Files() {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.Repeat("=", 78) + "\n" + f.Name + "\n" + strings.Repeat("=", 78) + "\n\n")
		b.Write(f.Data)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))
}
