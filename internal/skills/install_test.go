package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/harness"
)

// dirAdapter installs skills under <home>/skills or <project>/skills.
type dirAdapter struct{}

func (dirAdapter) Name() string                                                      { return "test-harness" }
func (dirAdapter) ComponentKey() string                                              { return "test-harness" }
func (dirAdapter) Installed(harness.Env) (bool, string)                              { return true, "/bin/true" }
func (dirAdapter) Discover(context.Context, harness.Env) (*harness.Discovery, error) { return nil, nil }
func (dirAdapter) Scaffold(harness.ScaffoldInput) (*harness.Scaffold, error)         { return nil, nil }
func (dirAdapter) SkillFileName() string                                             { return "SKILL.md" }
func (dirAdapter) SkillInstallDir(s harness.Scope, env harness.Env) (string, error) {
	if s == harness.ScopeProject {
		return filepath.Join(env.Project, "skills"), nil
	}
	return filepath.Join(env.Home, "skills"), nil
}

func bundleFS(version string) fstest.MapFS {
	skill := func(name string) []byte {
		return []byte("---\nname: sandcast-" + name + "\ndescription: test\nsandcast_version: " + version + "\n---\n\n# " + name + "\n")
	}
	return fstest.MapFS{
		"VERSION":             {Data: []byte(version + "\n")},
		"capture/SKILL.md":    {Data: skill("capture")},
		"capture/notes/a.txt": {Data: []byte("extra file")},
		"build/SKILL.md":      {Data: skill("build")},
		"README.md":           {Data: []byte("not a skill")},
	}
}

func testEnv(t *testing.T) harness.Env {
	return harness.Env{Home: t.TempDir(), Project: t.TempDir()}
}

func TestLoadBundle(t *testing.T) {
	b, err := Load(bundleFS("0.1.0"))
	require.NoError(t, err)
	assert.Equal(t, "0.1.0", b.Version)
	require.Len(t, b.Skills, 2)
	assert.Equal(t, "build", b.Skills[0].Dir)
	assert.Equal(t, "sandcast-capture", b.Skills[1].Meta.Name)
}

func TestInstallAndReinstall(t *testing.T) {
	env := testEnv(t)
	b, err := Load(bundleFS("0.1.0"))
	require.NoError(t, err)
	res, err := Install(b, dirAdapter{}, harness.ScopeUser, env, false)
	require.NoError(t, err)
	require.Len(t, res, 2)
	assert.Equal(t, ActionInstalled, res[0].Action)
	_, err = os.Stat(filepath.Join(env.Home, "skills", "sandcast-capture", "SKILL.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(env.Home, "skills", "sandcast-capture", "notes", "a.txt"))
	require.NoError(t, err)

	res, err = Install(b, dirAdapter{}, harness.ScopeUser, env, false)
	require.NoError(t, err)
	assert.Equal(t, ActionUnchanged, res[0].Action)

	newer, err := Load(bundleFS("0.2.0"))
	require.NoError(t, err)
	res, err = Install(newer, dirAdapter{}, harness.ScopeUser, env, false)
	require.NoError(t, err)
	assert.Equal(t, ActionUpdated, res[0].Action)

	res, err = Install(b, dirAdapter{}, harness.ScopeUser, env, false)
	require.NoError(t, err)
	assert.Equal(t, ActionSkipped, res[0].Action, "no silent downgrade")
	res, err = Install(b, dirAdapter{}, harness.ScopeUser, env, true)
	require.NoError(t, err)
	assert.Equal(t, ActionUpdated, res[0].Action)
}

func TestInstallProjectScope(t *testing.T) {
	env := testEnv(t)
	b, err := Load(bundleFS("0.1.0"))
	require.NoError(t, err)
	_, err = Install(b, dirAdapter{}, harness.ScopeProject, env, false)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(env.Project, "skills", "sandcast-build", "SKILL.md"))
	require.NoError(t, err)
}
