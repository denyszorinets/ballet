package httpapi_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/api"
	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
)

var (
	specOnce   sync.Once
	spec       *openapi3.T
	specRouter routers.Router
	specErr    error
)

func loadSpec(t *testing.T) (*openapi3.T, routers.Router) {
	t.Helper()
	specOnce.Do(func() {
		loader := openapi3.NewLoader()
		spec, specErr = loader.LoadFromData(api.OpenAPI)
		if specErr != nil {
			return
		}
		if specErr = spec.Validate(loader.Context); specErr != nil {
			return
		}
		specRouter, specErr = gorillamux.NewRouter(spec)
	})
	require.NoError(t, specErr)
	return spec, specRouter
}

// contract wraps an API handler and fails the test when a response — or a
// request the server accepted — does not conform to the OpenAPI spec.
func contract(t *testing.T, next http.Handler) http.Handler {
	_, router := loadSpec(t)
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, MultiError: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)

		route, params, err := router.FindRoute(r)
		if err != nil {
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusUnauthorized {
				t.Errorf("contract: %s %s is served (%d) but not in the OpenAPI spec", r.Method, r.URL.Path, rec.Code)
			}
		} else {
			reqIn := &openapi3filter.RequestValidationInput{Request: r.Clone(r.Context()), PathParams: params, Route: route, Options: opts}
			reqIn.Request.Body = io.NopCloser(bytes.NewReader(body))
			if rec.Code < 300 {
				if err := openapi3filter.ValidateRequest(r.Context(), reqIn); err != nil {
					t.Errorf("contract: accepted request %s %s violates the spec: %v", r.Method, r.URL.Path, err)
				}
			}
			resIn := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: reqIn, Status: rec.Code, Header: rec.Header(),
				Body: io.NopCloser(bytes.NewReader(rec.Body.Bytes())), Options: opts,
			}
			if err := openapi3filter.ValidateResponse(r.Context(), resIn); err != nil {
				t.Errorf("contract: response %d of %s %s violates the spec: %v", rec.Code, r.Method, r.URL.Path, err)
			}
		}

		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
}

func TestContract_RegisteredRoutesMatchSpec(t *testing.T) {
	s, _ := loadSpec(t)
	var inSpec []string
	for path, item := range s.Paths.Map() {
		for method := range item.Operations() {
			inSpec = append(inSpec, method+" "+path)
		}
	}
	routes := httpapi.Register(http.NewServeMux(), httpapi.Deps{Authenticate: testUser})
	sort.Strings(inSpec)
	sort.Strings(routes)

	assert.Equal(t, inSpec, routes, "every route must be documented in core/api/openapi.yaml and vice versa")
}

func TestContract_SpecIsServedPublicly(t *testing.T) {
	api := newAPI(t, testUser, allow{})
	rec := httptest.NewRecorder()

	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/openapi.yaml", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "openapi: 3.0.3")
}
