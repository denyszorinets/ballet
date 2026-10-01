package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/health"
)

func ok(context.Context) error      { return nil }
func failing(context.Context) error { return errors.New("database locked") }

func readiness(t *testing.T, checks ...health.Check) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	health.Readiness(checks...).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return rec.Code, body
}

func TestReadiness_ReadyWhenAllChecksPass(t *testing.T) {
	code, body := readiness(t, health.Check{Name: "db", Func: ok})

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ready", body["status"])
	assert.Equal(t, map[string]any{"db": "ok"}, body["checks"])
}

func TestReadiness_UnavailableWhenAnyCheckFails(t *testing.T) {
	code, body := readiness(t, health.Check{Name: "db", Func: failing}, health.Check{Name: "cache", Func: ok})

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "not ready", body["status"])
	// Failure details are logged, not exposed.
	assert.Equal(t, map[string]any{"db": "failing", "cache": "ok"}, body["checks"])
}

func TestReadiness_ReadyWithoutChecks(t *testing.T) {
	code, _ := readiness(t)

	assert.Equal(t, http.StatusOK, code)
}
