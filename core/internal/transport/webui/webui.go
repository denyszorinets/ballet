// Package webui serves the web UI: its public runtime configuration
// (/config.json) and, optionally, the static SPA bundle with a fallback to
// index.html for client-side routes. The bundle is a directory, or embedded
// in the binary when built with the bindata tag (see Bundled).
package webui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Config configures the web UI.
type Config struct {
	OIDCIssuer string // issuer the SPA logs in with
	ClientID   string // public OIDC client of the SPA
	Assets     fs.FS  // built SPA (web/build); nil: not served
}

type configJSON struct {
	OIDC struct {
		Issuer   string `json:"issuer"`
		ClientID string `json:"client_id"`
	} `json:"oidc"`
}

// Register mounts /config.json and, with Assets set, the SPA on mux.
func Register(mux *http.ServeMux, c Config) {
	var cfg configJSON
	cfg.OIDC.Issuer, cfg.OIDC.ClientID = c.OIDCIssuer, c.ClientID
	body, _ := json.Marshal(cfg) // plain strings cannot fail
	mux.HandleFunc("GET /config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	if c.Assets != nil {
		// "/" rather than "GET /": the latter would conflict with routes
		// registered for every method on a narrower path.
		mux.Handle("/", spa(c.Assets))
	}
}

// spa serves files from assets; paths without a file extension that do
// not exist are client-side routes and get index.html.
func spa(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		_, err := fs.Stat(assets, strings.TrimPrefix(clean, "/"))
		if errors.Is(err, fs.ErrNotExist) && !strings.Contains(path.Base(clean), ".") {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, assets, "index.html")
			return
		}
		if strings.HasPrefix(clean, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
