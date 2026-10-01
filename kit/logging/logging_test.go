package logging_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/logging"
)

func TestNew_WritesJSONWithServiceAttribute(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(&buf, "core", "info")
	require.NoError(t, err)

	logger.Info("started", "addr", ":8080")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "started", rec["msg"])
	assert.Equal(t, "core", rec["service"])
	assert.Equal(t, ":8080", rec["addr"])
	assert.Equal(t, "INFO", rec["level"])
}

func TestNew_FiltersBelowConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(&buf, "core", "warn")
	require.NoError(t, err)

	logger.Info("hidden")
	logger.Warn("shown")

	assert.NotContains(t, buf.String(), "hidden")
	assert.Contains(t, buf.String(), "shown")
}

func TestNew_RejectsUnknownLevel(t *testing.T) {
	_, err := logging.New(&bytes.Buffer{}, "core", "loud")

	assert.ErrorContains(t, err, "loud")
}
