package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeaturesAPI_Lifecycle(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	code, body := call(t, api, "POST", "/api/v1/organizations", "alice", `{"key":"acme","name":"Acme"}`)
	require.Equal(t, http.StatusCreated, code, body)
	code, body = call(t, api, "POST", "/api/v1/organizations/acme/projects", "alice", `{"key":"WEB","name":"Web"}`)
	require.Equal(t, http.StatusCreated, code, body)

	code, f := call(t, api, "POST", "/api/v1/organizations/acme/features", "alice",
		`{"title":"Invoice export","description":"CSV.","projects":["WEB"],"reason":"Asked by finance"}`)
	require.Equal(t, http.StatusCreated, code, f)
	assert.Equal(t, "F-1", f["key"])
	assert.Equal(t, "planned", f["status"])
	assert.Equal(t, []any{"WEB"}, f["projects"])

	code, f = call(t, api, "PATCH", "/api/v1/organizations/acme/features/F-1", "alice",
		`{"version":1,"description":"CSV and PDF.","status":"live","reason":"PDF added"}`)
	require.Equal(t, http.StatusOK, code, f)
	assert.EqualValues(t, 2, f["version"])

	code, body = call(t, api, "PATCH", "/api/v1/organizations/acme/features/F-1", "alice", `{"version":1,"title":"x"}`)
	assert.Equal(t, http.StatusConflict, code, body)

	code, _ = call(t, api, "POST", "/api/v1/organizations/acme/features", "alice", `{"title":"Scheduled export"}`)
	require.Equal(t, http.StatusCreated, code)
	code, l := call(t, api, "POST", "/api/v1/organizations/acme/feature-links", "alice",
		`{"from":"F-2","to":"F-1","type":"derived_from"}`)
	require.Equal(t, http.StatusCreated, code, l)

	code, d := call(t, api, "GET", "/api/v1/organizations/acme/features/F-1", "alice", "")
	require.Equal(t, http.StatusOK, code, d)
	assert.Len(t, d["links"], 1)

	code, revs := call(t, api, "GET", "/api/v1/organizations/acme/features/F-1/revisions", "alice", "")
	require.Equal(t, http.StatusOK, code, revs)
	items := revs["items"].([]any)
	require.Len(t, items, 2)
	assert.Equal(t, "PDF added", items[0].(map[string]any)["reason"])

	code, list := call(t, api, "GET", "/api/v1/organizations/acme/features?status=live", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, list["items"], 1)

	code, g := call(t, api, "GET", "/api/v1/organizations/acme/feature-graph", "alice", "")
	require.Equal(t, http.StatusOK, code, g)
	assert.Len(t, g["features"], 2)
	assert.Len(t, g["links"], 1)
	assert.Len(t, g["changes"], 4)

	code, g = call(t, api, "GET", "/api/v1/organizations/acme/feature-graph?at=2000-01-01T00:00:00Z", "alice", "")
	require.Equal(t, http.StatusOK, code, g)
	assert.Empty(t, g["features"], "nothing existed then")

	code, body = call(t, api, "GET", "/api/v1/organizations/acme/feature-graph?at=yesterday", "alice", "")
	assert.Equal(t, http.StatusBadRequest, code, body)

	code, _ = call(t, api, "DELETE", "/api/v1/organizations/acme/feature-links/"+l["id"].(string), "alice", "")
	assert.Equal(t, http.StatusNoContent, code)
	code, d = call(t, api, "GET", "/api/v1/organizations/acme/features/F-1", "alice", "")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, d["links"])
}

func TestFeaturesAPI_PolicyAndReviews(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	code, o := call(t, api, "POST", "/api/v1/organizations", "alice", `{"key":"acme","name":"Acme"}`)
	require.Equal(t, http.StatusCreated, code, o)
	assert.Equal(t, "direct", o["feature_policy"])

	code, o = call(t, api, "PATCH", "/api/v1/organizations/acme", "alice", `{"feature_policy":"proposal","version":1}`)
	require.Equal(t, http.StatusOK, code, o)
	assert.Equal(t, "proposal", o["feature_policy"])
	assert.Equal(t, "Acme", o["name"], "name unchanged")
	code, _ = call(t, api, "PATCH", "/api/v1/organizations/acme", "alice", `{"feature_policy":"never","version":2}`)
	assert.Equal(t, http.StatusBadRequest, code)

	code, q := call(t, api, "GET", "/api/v1/organizations/acme/feature-reviews", "alice", "")
	require.Equal(t, http.StatusOK, code, q)
	assert.Empty(t, q["items"])

	code, _ = call(t, api, "POST", "/api/v1/organizations/acme/features", "alice", `{"title":"Export"}`)
	require.Equal(t, http.StatusCreated, code)
	code, body := call(t, api, "POST", "/api/v1/organizations/acme/features/F-1/revisions/1/review", "alice", `{"action":"confirm"}`)
	assert.Equal(t, http.StatusConflict, code, body, "human revisions need no review")
	code, body = call(t, api, "POST", "/api/v1/organizations/acme/features/F-1/revisions/x/review", "alice", `{"action":"confirm"}`)
	assert.Equal(t, http.StatusBadRequest, code, body)
}
