package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersion(t *testing.T) {
	ta := newTestApp(t)
	require.Equal(t, 0, ta.run("version"))
	assert.Contains(t, ta.stdout.String(), "v0.1.3")
	assert.Contains(t, ta.stdout.String(), "v2026.10.4")

	require.Equal(t, 0, ta.run("version", "--json"))
	var v map[string]any
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &v))
	assert.Equal(t, "v0.1.3", v["openshell"])
	assert.Equal(t, float64(1), v["manifest_schema_version"])
}

func TestUsageErrorsExit2(t *testing.T) {
	ta := newTestApp(t)
	assert.Equal(t, ExitUsage, ta.run("version", "--nope"))
	assert.Equal(t, ExitUsage, ta.run("version", "extra"))
	assert.Equal(t, ExitUsage, ta.run("plan"), "no manifest")
	assert.Contains(t, ta.stderr.String(), "sandcast init")
}

func TestNoEngineExit3(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.LookPath = func(string) (string, error) { return "", assert.AnError }
	assert.Equal(t, ExitTool, ta.run("plan"))
	assert.Contains(t, ta.stderr.String(), "podman")
}

func TestCodeOf(t *testing.T) {
	assert.Equal(t, 0, CodeOf(nil))
	assert.Equal(t, 1, CodeOf(assert.AnError))
	assert.Equal(t, 2, CodeOf(usageErr("x")))
	assert.Equal(t, 3, CodeOf(toolErr(assert.AnError)))
	assert.Equal(t, 1, CodeOf(silentErr{code: 1}))
}
