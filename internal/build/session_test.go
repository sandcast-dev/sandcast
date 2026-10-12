package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/testutil"
)

// sessionPlan is testPlan with a mise component, which the session script
// needs for the MISE_* variables and the shims on PATH.
func sessionPlan(t *testing.T) *plan.Plan {
	t.Helper()
	return testPlan(t)
}

func TestSessionScriptGolden(t *testing.T) {
	got := SessionScript(sessionPlan(t))
	testutil.Golden(t, "testdata/sandcast-session.sh.golden", []byte(got))
	assert.Contains(t, got, "export GOPATH='/opt/go'\n")
	assert.Contains(t, got, "export GREETING='hello world'\n")
	assert.Contains(t, got, "export MISE_DATA_DIR='/opt/mise'\n")
	assert.Contains(t, got, plan.MiseShims)
	assert.Equal(t, got, SessionScript(sessionPlan(t)), "deterministic")
}

func TestSessionScriptQuotesValues(t *testing.T) {
	p := sessionPlan(t)
	p.Image.Env = []plan.EnvVar{{Name: "QUOTED", Value: `it's $HOME`}}
	got := SessionScript(p)
	assert.Contains(t, got, `export QUOTED='it'\''s $HOME'`)
}

func TestSessionScriptWithoutMise(t *testing.T) {
	p := sessionPlan(t)
	p.Components = nil
	got := SessionScript(p)
	assert.Contains(t, got, "export GOPATH='/opt/go'\n")
	assert.NotContains(t, got, "MISE_")
	assert.NotContains(t, got, "export PATH")
}

// The script is sourced by sh, bash and zsh. Run it with sh from an
// environment as cleared as an OpenShell session and check what it exports
// and that sourcing it twice keeps one shims entry.
func TestSessionScriptRestoresClearedEnvironment(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	file := filepath.Join(t.TempDir(), "session.sh")
	require.NoError(t, os.WriteFile(file, []byte(SessionScript(sessionPlan(t))), 0o644))
	cmd := exec.Command(sh, "-c", `. "$1"; . "$1"; printf '%s\n%s\n%s\n' "$PATH" "$MISE_DATA_DIR" "$GREETING"`, "sh", file)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/sandbox"}
	out, err := cmd.Output()
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, plan.MiseShims+":/usr/local/bin:/usr/bin:/bin", lines[0])
	assert.Equal(t, plan.MiseDataDir, lines[1])
	assert.Equal(t, "hello world", lines[2])
}

func TestAssembleSessionSection(t *testing.T) {
	got, err := Assemble(testPlan(t))
	require.NoError(t, err)
	shell := strings.Index(got, "# 60-shell")
	session := strings.Index(got, "# 65-session")
	policy := strings.Index(got, "# 80-policy")
	require.GreaterOrEqual(t, session, 0)
	assert.True(t, shell < session && session < policy, "65-session sits between 60-shell and 80-policy")
	assert.Contains(t, got, "COPY "+SessionContextFile+" "+SessionEnvPath+"\n")
	assert.Contains(t, got, "rc=/etc/zsh/zshenv && if [ -f /etc/zshenv ]; then rc=/etc/zshenv; fi")
	assert.Contains(t, got, "'[ -r "+SessionEnvPath+" ] && . "+SessionEnvPath+"'")
	assert.Contains(t, got, "zsh-newuser-install() { :; }")
	assert.Contains(t, got, "for rc in /etc/bash.bashrc /etc/bashrc", "openshell + zsh switches interactive sessions to zsh")
	assert.Contains(t, got, "SANDCAST_NO_ZSH")
	assert.NotContains(t, got, "sandcast-mise.sh")
}

func TestAssembleSessionSectionSwitchOnlyForOpenShellZsh(t *testing.T) {
	p := testPlan(t)
	p.Target = "container"
	p.Policy = ""
	got, err := Assemble(p)
	require.NoError(t, err)
	assert.Contains(t, got, "# 65-session", "the container target keeps the session environment")
	assert.NotContains(t, got, "SANDCAST_NO_ZSH", "the container target leaves the shell choice to the caller")

	p = testPlan(t)
	p.Shell = plan.Shell{Program: "bash", RCPath: plan.BashRCPath, RC: "alias l=ls\n"}
	got, err = Assemble(p)
	require.NoError(t, err)
	assert.NotContains(t, got, "SANDCAST_NO_ZSH")
}

func TestAssembleWithoutSessionNeeds(t *testing.T) {
	p := testPlan(t)
	p.Image.Env = nil
	p.Components = nil
	p.Shell = plan.Shell{Program: "none"}
	got, err := Assemble(p)
	require.NoError(t, err)
	assert.NotContains(t, got, "# 65-session")
}

// The bashrc hook must leave non-interactive shells alone: OpenShell runs
// commands with "bash -lc", and those have to stay in bash.
func TestZshSwitchHookIgnoresNonInteractiveShells(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	out, err := exec.Command(sh, "-c", zshSwitchHook+"\necho stayed").CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "stayed\n", string(out))
	assert.NotContains(t, zshSwitchHook, "'", "the hook is written with printf '%s' and must not contain a single quote")
}

func TestWriteContextSessionFile(t *testing.T) {
	dir := t.TempDir()
	p := testPlan(t)
	require.NoError(t, writeContext(dir, p, "FROM x\n"))
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(SessionContextFile)))
	require.NoError(t, err)
	assert.Equal(t, SessionScript(p), string(data))

	p.Image.Env = nil
	p.Components = nil
	p.Shell = plan.Shell{Program: "none"}
	require.NoError(t, writeContext(dir, p, "FROM x\n"))
	_, err = os.Stat(filepath.Join(dir, filepath.FromSlash(SessionContextFile)))
	assert.ErrorIs(t, err, os.ErrNotExist, "a stale session file is removed")
}
