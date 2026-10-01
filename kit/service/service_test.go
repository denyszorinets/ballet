package service_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/health"
	"github.com/denyszorinets/ballet/kit/service"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

func TestService_ServesOperationalEndpointsAndRoutes(t *testing.T) {
	var logs bytes.Buffer
	cfg := service.DefaultConfig(":0")
	svc, err := service.New("core", cfg, &logs)
	require.NoError(t, err)

	svc.AddReadinessCheck(health.Check{Name: "db", Func: func(context.Context) error { return nil }})
	svc.Mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("pong")) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.Serve(ctx, ln) }()

	code, body := get(t, base+"/healthz")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"service":"core"`)

	code, body = get(t, base+"/readyz")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"db":"ok"`)

	code, body = get(t, base+"/metrics")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "go_goroutines")
	assert.Contains(t, body, `ballet_build_info{service="core"`)

	code, body = get(t, base+"/api/ping")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "pong", body)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
	assert.True(t, strings.Contains(logs.String(), `"msg":"service started"`))
	assert.True(t, strings.Contains(logs.String(), `"msg":"service stopped"`))
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*service.Config)
		ok     bool
	}{
		{name: "defaults are valid", mutate: func(*service.Config) {}, ok: true},
		{name: "empty address", mutate: func(c *service.Config) { c.Server.Addr = "" }},
		{name: "non-positive shutdown timeout", mutate: func(c *service.Config) { c.Server.ShutdownTimeout = 0 }},
		{name: "unknown log level", mutate: func(c *service.Config) { c.Log.Level = "loud" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := service.DefaultConfig(":8080")
			tt.mutate(&cfg)

			err := cfg.Validate()

			if tt.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
