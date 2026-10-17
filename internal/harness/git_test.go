package harness

import (
	"context"
	"os/exec"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitArgs(t *testing.T) {
	assert.Equal(t, []string{"-C", "/repo", "-c", "core.quotepath=off", "-c", "color.ui=false", "-c", "core.fsmonitor=false", "--no-pager", "remote", "get-url", "origin"},
		gitArgs("/repo", []string{"remote", "get-url", "origin"}))
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.fsmonitor'='true'")
	env := gitEnv()
	assert.True(t, slices.Contains(env, "GIT_TERMINAL_PROMPT=0"))
	for _, kv := range env {
		assert.NotRegexp(t, `^GIT_(DIR|CONFIG_PARAMETERS)=`, kv, "repository-location and config variables are dropped")
	}
	assert.True(t, slices.Contains(env, "LC_ALL=C"))
}

func TestExecGitRun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	g := ExecGit{}
	_, err := g.Run(context.Background(), dir, "init", "-q")
	require.NoError(t, err)
	out, err := g.Run(context.Background(), dir, "rev-parse", "--is-inside-work-tree")
	require.NoError(t, err)
	assert.Equal(t, "true", out)
	_, err = g.Run(context.Background(), dir, "remote", "get-url", "origin")
	assert.Error(t, err)
	_, err = ExecGit{Bin: "sandcast-no-such-git"}.Run(context.Background(), dir, "status")
	assert.Error(t, err)
}
