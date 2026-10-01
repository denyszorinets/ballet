package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/config"
)

type serverSection struct {
	Addr            string        `toml:"addr"`
	ShutdownTimeout time.Duration `toml:"shutdown_timeout"`
}

type testConfig struct {
	Server  serverSection `toml:"server"`
	Debug   bool          `toml:"debug"`
	Workers int           `toml:"workers"`
	Ratio   float64       `toml:"ratio"`
}

func (c *testConfig) Validate() error {
	if c.Workers < 0 {
		return assert.AnError
	}
	return nil
}

func defaults() *testConfig {
	return &testConfig{Server: serverSection{Addr: ":8080", ShutdownTimeout: 10 * time.Second}, Workers: 1}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoad_UsesDefaultsWithoutFileOrEnv(t *testing.T) {
	cfg := defaults()

	require.NoError(t, config.Load("", "TEST", cfg))

	assert.Equal(t, defaults(), cfg)
}

func TestLoad_FileOverridesDefaults(t *testing.T) {
	path := writeFile(t, "workers = 4\n[server]\naddr = \":9000\"\nshutdown_timeout = \"3s\"\n")
	cfg := defaults()

	require.NoError(t, config.Load(path, "TEST", cfg))

	assert.Equal(t, ":9000", cfg.Server.Addr)
	assert.Equal(t, 3*time.Second, cfg.Server.ShutdownTimeout)
	assert.Equal(t, 4, cfg.Workers)
}

func TestLoad_EnvironmentOverridesFile(t *testing.T) {
	path := writeFile(t, "workers = 4\n[server]\naddr = \":9000\"\n")
	t.Setenv("TEST_SERVER_ADDR", ":7000")
	t.Setenv("TEST_SERVER_SHUTDOWN_TIMEOUT", "250ms")
	t.Setenv("TEST_DEBUG", "true")
	t.Setenv("TEST_WORKERS", "8")
	t.Setenv("TEST_RATIO", "0.5")
	cfg := defaults()

	require.NoError(t, config.Load(path, "TEST", cfg))

	assert.Equal(t, ":7000", cfg.Server.Addr)
	assert.Equal(t, 250*time.Millisecond, cfg.Server.ShutdownTimeout)
	assert.True(t, cfg.Debug)
	assert.Equal(t, 8, cfg.Workers)
	assert.InDelta(t, 0.5, cfg.Ratio, 1e-9)
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		env     map[string]string
		wantMsg string
	}{
		{name: "unknown key in file", file: "[server]\nadress = \":1\"\n", wantMsg: "server.adress"},
		{name: "malformed file", file: "workers = \n", wantMsg: "parse"},
		{name: "invalid env integer", env: map[string]string{"TEST_WORKERS": "many"}, wantMsg: "TEST_WORKERS"},
		{name: "invalid env duration", env: map[string]string{"TEST_SERVER_SHUTDOWN_TIMEOUT": "soon"}, wantMsg: "TEST_SERVER_SHUTDOWN_TIMEOUT"},
		{name: "validation failure", env: map[string]string{"TEST_WORKERS": "-1"}, wantMsg: "invalid configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := ""
			if tt.file != "" {
				path = writeFile(t, tt.file)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			err := config.Load(path, "TEST", defaults())

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}

func TestLoad_MissingFileIsAnError(t *testing.T) {
	err := config.Load(filepath.Join(t.TempDir(), "absent.toml"), "TEST", defaults())

	require.Error(t, err)
}

func TestEnvNames_ListsEveryKey(t *testing.T) {
	names := config.EnvNames("TEST", defaults())

	assert.Equal(t, []string{
		"TEST_SERVER_ADDR", "TEST_SERVER_SHUTDOWN_TIMEOUT", "TEST_DEBUG", "TEST_WORKERS", "TEST_RATIO",
	}, names)
}

type embeddedBase struct {
	Server serverSection `toml:"server"`
}

type serviceConfig struct {
	embeddedBase
	Name string `toml:"name"`
}

func TestLoad_PromotesEmbeddedStructFields(t *testing.T) {
	path := writeFile(t, "name = \"core\"\n[server]\naddr = \":9000\"\n")
	t.Setenv("TEST_SERVER_SHUTDOWN_TIMEOUT", "2s")
	cfg := &serviceConfig{}

	require.NoError(t, config.Load(path, "TEST", cfg))

	assert.Equal(t, "core", cfg.Name)
	assert.Equal(t, ":9000", cfg.Server.Addr)
	assert.Equal(t, 2*time.Second, cfg.Server.ShutdownTimeout)
	assert.Equal(t, []string{"TEST_SERVER_ADDR", "TEST_SERVER_SHUTDOWN_TIMEOUT", "TEST_NAME"}, config.EnvNames("TEST", cfg))
}
