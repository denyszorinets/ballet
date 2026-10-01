// Package webui serves the web UI: its public runtime configuration
// (/config.json) and, optionally, the static SPA bundle with a fallback to
// index.html for client-side routes.
package webui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Config configures the web UI.
type Config struct {
	OIDCIssuer string // issuer the SPA logs in with
	ClientID   string // public OIDC client of the SPA
	Dir        string // built SPA (web/build); empty: not served
}

type configJSON struct {
	OIDC struct {
		Issuer   string `json:"issuer"`
		ClientID string `json:"client_id"`
	} `json:"oidc"`
}

// Register mounts /config.json and, with Dir set, the SPA on mux.
func Register(mux *http.ServeMux, c Config) {
	var cfg configJSON
	cfg.OIDC.Issuer, cfg.OIDC.ClientID = c.OIDCIssuer, c.ClientID
	body, _ := json.Marshal(cfg) // plain strings cannot fail
	mux.HandleFunc("GET /config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	if c.Dir != "" {
		mux.Handle("GET /", spa(c.Dir))
	}
}

// spa serves files from dir; paths without a file extension that do not
// exist are client-side routes and get index.html.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + r.URL.Path)
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean)))
		if errors.Is(err, fs.ErrNotExist) && !strings.Contains(path.Base(clean), ".") {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(clean, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
