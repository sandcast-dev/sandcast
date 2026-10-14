package shell

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuote(t *testing.T) {
	assert.Equal(t, `'it'\''s'`, Quote("it's"))
	assert.Equal(t, `''`, Quote(""))
	assert.Equal(t, "plain-value_1.0", QuoteIfNeeded("plain-value_1.0"))
	assert.Equal(t, `'a b'`, QuoteIfNeeded("a b"))
	assert.Equal(t, `'$HOME'`, QuoteIfNeeded("$HOME"))
}

func TestZshEnvExport(t *testing.T) {
	assert.Equal(t, "export ZDOTDIR=/opt/sandcast/zdotdir", ZshEnvExport("/opt/sandcast/zdotdir"))
}

// TestLoginLauncher checks the baked bash launcher closes the workspace
// startup-file vector: it reads no profile file, uses the explicit baked
// rcfile in place of ~/.bashrc, and forwards its arguments (spec 003 US2).
// --norc must be absent because it disables --rcfile.
func TestLoginLauncher(t *testing.T) {
	got := LoginLauncher("/opt/sandcast/bashrc")
	assert.True(t, strings.HasPrefix(got, "#!/bin/sh\n"), "has a shebang")
	assert.Contains(t, got, "--noprofile")
	assert.NotContains(t, got, "--norc", "--norc makes bash ignore --rcfile")
	assert.Contains(t, got, "--rcfile /opt/sandcast/bashrc")
	assert.Contains(t, got, `"$@"`, "forwards arguments")
	assert.Contains(t, got, "exec bash")
	// The launcher must be runnable: a POSIX shell parses it without error.
	if sh, err := exec.LookPath("sh"); err == nil {
		require.NoError(t, exec.Command(sh, "-n", "-c", got).Run(), "launcher is valid sh")
	}
}

// TestLoginLauncherLoadsRCFile runs the generated launcher with the host bash
// and checks the behavior, not just the flags: the baked rcfile is executed and
// a ~/.bashrc in $HOME is not. bash ignores --rcfile whenever --norc is given,
// so a launcher that passes both leaves the shell unconfigured (spec 003 US1).
func TestLoginLauncherLoadsRCFile(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	for _, f := range []string{".bashrc", ".bash_profile", ".profile"} {
		require.NoError(t, os.WriteFile(filepath.Join(home, f), []byte("echo PWNED-"+f+"\n"), 0o644))
	}
	rc := filepath.Join(dir, "bashrc")
	require.NoError(t, os.WriteFile(rc, []byte("echo RCFILE_LOADED\nalias myll='ls -l'\n"), 0o644))
	launcher := filepath.Join(dir, "shell-login")
	require.NoError(t, os.WriteFile(launcher, []byte(LoginLauncher(rc)), 0o755))

	cmd := exec.Command(sh, launcher, "-i", "-c", "alias myll; echo CMD_RAN")
	cmd.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(bash) + ":/usr/bin:/bin", "TERM=dumb"}
	out, _ := cmd.CombinedOutput()
	assert.Contains(t, string(out), "RCFILE_LOADED", "the baked rcfile is executed: %s", out)
	assert.Contains(t, string(out), "myll='ls -l'", "the baked alias is active: %s", out)
	assert.Contains(t, string(out), "CMD_RAN", "the forwarded -c command runs: %s", out)
	assert.NotContains(t, string(out), "PWNED", "no startup file in $HOME runs: %s", out)
}

// TestJSONHelpersBuiltinsOnly runs _jsafe with an empty PATH, so any use of
// an external command such as sed or tr fails the test.
func TestJSONHelpersBuiltinsOnly(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, v := range []string{"plain", `quo"te`, `back\slash`, "tab\there", "new\nline", "cr\rx", "bell\x07", "ünïcode", ""} {
		cmd := exec.Command(sh, "-c", JSONHelpers+`printf '"'; _jsafe "$1"; printf '"'`, "sh", v)
		cmd.Env = []string{"PATH="}
		out, err := cmd.Output()
		require.NoError(t, err, "value %q", v)
		var got string
		require.NoError(t, json.Unmarshal(out, &got), "output %q", out)
		assert.Equal(t, v, got)
	}
}
