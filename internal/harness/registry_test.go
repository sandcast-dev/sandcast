package harness

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubAdapter struct{ name string }

func (s stubAdapter) Name() string                                      { return s.name }
func (s stubAdapter) ComponentKey() string                              { return s.name }
func (s stubAdapter) Installed(Env) (bool, string)                      { return false, "" }
func (s stubAdapter) Discover(context.Context, Env) (*Discovery, error) { return &Discovery{}, nil }
func (s stubAdapter) Scaffold(ScaffoldInput) (*Scaffold, error)         { return &Scaffold{}, nil }
func (s stubAdapter) SkillInstallDir(Scope, Env) (string, error)        { return "", nil }
func (s stubAdapter) SkillFileName() string                             { return "SKILL.md" }

func TestRegistry(t *testing.T) {
	saved := registry
	t.Cleanup(func() { registry = saved })
	registry = map[string]Adapter{}

	Register(stubAdapter{name: "zeta"})
	Register(stubAdapter{name: "alpha"})
	assert.Equal(t, []string{"alpha", "zeta"}, Names())
	a, err := Get("alpha")
	require.NoError(t, err)
	assert.Equal(t, "alpha", a.Name())
	_, err = Get("nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alpha")
}
