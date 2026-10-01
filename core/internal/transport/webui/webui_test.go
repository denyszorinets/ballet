package webui_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/transport/webui"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestConfig_ExposesOIDCSettingsForTheSPA(t *testing.T) {
	mux := http.NewServeMux()
	webui.Register(mux, webui.Config{OIDCIssuer: "https://idp.example/realms/ballet", ClientID: "ballet-web"})

	rec := get(mux, "/config.json")

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]string{"issuer": "https://idp.example/realms/ballet", "client_id": "ballet-web"}, body["oidc"])
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestStatic_ServesFilesAndFallsBackToIndex(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "_app"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "_app", "x.js"), []byte("js"), 0o644))
	mux := http.NewServeMux()
	webui.Register(mux, webui.Config{Dir: dir, ClientID: "ballet-web"})

	assert.Equal(t, "js", get(mux, "/_app/x.js").Body.String())
	assert.Contains(t, get(mux, "/").Body.String(), "app")
	deep := get(mux, "/projects/WEB")
	assert.Equal(t, http.StatusOK, deep.Code)
	assert.Contains(t, deep.Body.String(), "app", "client-side routes fall back to index.html")
	assert.Equal(t, http.StatusNotFound, get(mux, "/_app/missing.js").Code, "missing assets are not masked")
}

func TestStatic_DisabledWithoutDir(t *testing.T) {
	mux := http.NewServeMux()
	webui.Register(mux, webui.Config{ClientID: "ballet-web"})

	assert.Equal(t, http.StatusNotFound, get(mux, "/").Code)
}
