package app

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// webApp serves the built web client (web/dist) from the same origin as the API, so the
// session cookie works without CORS. Paths that are not files fall back to index.html for the
// client-side router. Vite fingerprints everything under /assets, so those cache for a year;
// index.html is always revalidated so a deploy shows up on the next load.
func webApp(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if p != "/" {
			if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}
