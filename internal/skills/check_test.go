package skills

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/harness"
)

func TestCheck(t *testing.T) {
	env := testEnv(t)
	b, err := Load(bundleFS("0.2.0"))
	require.NoError(t, err)

	rep, err := Check(b, dirAdapter{}, env)
	require.NoError(t, err)
	assert.False(t, rep.OK)
	assert.Equal(t, StatusMissing, rep.Results[0].Status)
	assert.Contains(t, rep.Message(), "sandcast skills install --harness test-harness")

	old, err := Load(bundleFS("0.1.0"))
	require.NoError(t, err)
	_, err = Install(old, dirAdapter{}, harness.ScopeUser, env, false)
	require.NoError(t, err)
	rep, err = Check(b, dirAdapter{}, env)
	require.NoError(t, err)
	assert.False(t, rep.OK)
	assert.Equal(t, StatusOutdated, rep.Results[0].Status)
	assert.Contains(t, rep.Message(), "update the skill")

	newer, err := Load(bundleFS("0.3.0"))
	require.NoError(t, err)
	_, err = Install(newer, dirAdapter{}, harness.ScopeProject, env, false)
	require.NoError(t, err)
	rep, err = Check(b, dirAdapter{}, env)
	require.NoError(t, err)
	assert.Equal(t, StatusNewer, rep.Results[0].Status, "project scope wins")
	assert.Contains(t, rep.Message(), "update the sandcast binary")

	_, err = Install(b, dirAdapter{}, harness.ScopeProject, env, true)
	require.NoError(t, err)
	rep, err = Check(b, dirAdapter{}, env)
	require.NoError(t, err)
	assert.True(t, rep.OK)
	assert.Empty(t, rep.Message())
}

func TestParseFrontmatter(t *testing.T) {
	fm, err := ParseFrontmatter([]byte("---\nname: x\nsandcast_version: 1.0.0\n---\nbody"))
	require.NoError(t, err)
	assert.Equal(t, "x", fm.Name)
	_, err = ParseFrontmatter([]byte("no frontmatter"))
	require.Error(t, err)
	_, err = ParseFrontmatter([]byte("---\nname: x\n"))
	require.Error(t, err)
}
