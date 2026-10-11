package claudecode

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/harness"
)

func TestSkillInstallDir(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	env := harness.Env{Home: home, Project: project}
	tests := []struct {
		name  string
		scope harness.Scope
		env   harness.Env
		want  string
		err   string
	}{
		{name: "user", scope: harness.ScopeUser, env: env, want: filepath.Join(home, ".claude", "skills")},
		{name: "project", scope: harness.ScopeProject, env: env, want: filepath.Join(project, ".claude", "skills")},
		{name: "user without home", scope: harness.ScopeUser, env: harness.Env{Project: project}, err: "home directory unknown"},
		{name: "project without project", scope: harness.ScopeProject, env: harness.Env{Home: home}, err: "project directory unknown"},
		{name: "unknown scope", scope: harness.Scope("global"), env: env, err: `unknown skill scope "global"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Adapter{}.SkillInstallDir(tt.scope, tt.env)
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAdapterIdentity(t *testing.T) {
	a := Adapter{}
	assert.Equal(t, "claude-code", a.Name())
	assert.Equal(t, "claude-code", a.ComponentKey())
	assert.Equal(t, "SKILL.md", a.SkillFileName())

	registered, err := harness.Get("claude-code")
	require.NoError(t, err)
	assert.Equal(t, a, registered)
}

func TestInstalled(t *testing.T) {
	tests := []struct {
		name     string
		lookPath func(string) (string, error)
		ok       bool
		path     string
	}{
		{
			name: "on path",
			lookPath: func(bin string) (string, error) {
				if bin != "claude" {
					return "", errors.New("unexpected binary " + bin)
				}
				return "/usr/local/bin/claude", nil
			},
			ok:   true,
			path: "/usr/local/bin/claude",
		},
		{name: "missing", lookPath: func(string) (string, error) { return "", errors.New("not found") }},
		{name: "no lookup", lookPath: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, path := Adapter{}.Installed(harness.Env{LookPath: tt.lookPath})
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.path, path)
		})
	}
}
