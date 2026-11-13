// Package webui serves the built React application from the Go binary.
//
// Development uses the Vite dev server, which proxies /v1 to meridian-server.
// Production runs `npm run build`, which writes directly into ./dist; the next
// `go build` embeds those assets. The committed placeholder index.html only
// keeps a plain `go build` working before the first frontend build.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// Handler serves embedded static files and delegates API paths to api. Unknown
// paths fall back to index.html so the SPA can own client-side routes.
func Handler(api http.Handler) http.Handler {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return api
	}
	index, _ := fs.ReadFile(sub, "index.html")
	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			api.ServeHTTP(w, r)
			return
		}
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" || clean == "." {
			serveIndex(w, r, index)
			return
		}
		if f, err := sub.Open(clean); err == nil {
			_ = f.Close()
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, index)
	})
}

func isAPIPath(p string) bool {
	switch {
	case strings.HasPrefix(p, "/v1/"), strings.HasPrefix(p, "/ml/"):
		return true
	case p == "/status", strings.HasPrefix(p, "/status/"):
		return true
	case p == "/policies", strings.HasPrefix(p, "/policies/"):
		return true
	case p == "/healthz":
		return true
	}
	return false
}

func serveIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
}
