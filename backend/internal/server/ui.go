package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:ui_dist
var uiFS embed.FS

// uiHandler serves the embedded Vue SPA. It does NOT inject credentials;
// the UI authenticates via /auth/onboard + /auth/login (session cookie).
func uiHandler() http.Handler {
	dist, err := fs.Sub(uiFS, "ui_dist")
	if err != nil {
		panic("failed to create sub FS for ui_dist: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(dist))
	indexHTML, _ := fs.ReadFile(dist, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/web")
		if path == "" {
			path = "/"
		}

		if strings.HasPrefix(path, "/assets") {
			r.URL.Path = path
			fileServer.ServeHTTP(w, r)
			return
		}

		if path != "/" {
			cleanPath := strings.TrimPrefix(path, "/")
			if f, err := dist.Open(cleanPath); err == nil {
				f.Close()
				r.URL.Path = path
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(indexHTML)
	})
}
