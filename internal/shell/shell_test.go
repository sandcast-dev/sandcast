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
	assert.Equal(t, "export ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1", ZshEnvExport("/opt/sandcast/zdotdir"))
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
	assert.Contains(t, got, "SANDCAST_SHELL_PID=$$\n", "the bashrc hook recognizes this bash by its pid")
	assert.Contains(t, got, "export SANDCAST_SHELL_PID SANDCAST_SESSION\n")
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

// TestShLoginLauncher checks the sh fallback launcher text: it points $ENV at
// the baked profile, exports it, and execs a non-login /bin/sh with the
// filtered arguments, and it is valid sh (spec 003 FR-002, FR-011).
func TestShLoginLauncher(t *testing.T) {
	got := ShLoginLauncher("/etc/profile.d/sandcast.sh")
	assert.True(t, strings.HasPrefix(got, "#!/bin/sh\n"), "has a shebang")
	assert.Contains(t, got, "ENV=/etc/profile.d/sandcast.sh\n")
	assert.Contains(t, got, "export ENV SANDCAST_SESSION\n")
	assert.Contains(t, got, `exec /bin/sh "$@"`+"\n")
	assert.NotContains(t, got, "exec /bin/sh -l", "never a login shell")
	assert.Contains(t, ShLoginLauncher("/opt/a b/profile"), "ENV='/opt/a b/profile'\n", "the profile path is quoted")
	if sh, err := exec.LookPath("sh"); err == nil {
		require.NoError(t, exec.Command(sh, "-n", "-c", got).Run(), "launcher is valid sh")
	}
}

// TestShLoginLauncherDropsLogin runs the sh launcher with the host sh and a
// planted $HOME/.profile. A caller that passes -l, --login or -lc (an
// editor's environment probe, su -l, a tmux default-command) must not turn
// the shell into a login shell, which would read the workspace .profile
// (FINDING-46). Options after the command string are the command's own
// arguments and are kept.
func TestShLoginLauncherDropsLogin(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".profile"), []byte("echo PWNED-profile\n"), 0o644))
	profile := filepath.Join(dir, "profile.sh")
	require.NoError(t, os.WriteFile(profile, []byte(""), 0o644))
	launcher := filepath.Join(dir, "sh-login")
	require.NoError(t, os.WriteFile(launcher, []byte(ShLoginLauncher(profile)), 0o755))

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-l", "-c", "echo CMD"}, "CMD"},
		{[]string{"-lc", "echo CMD"}, "CMD"},
		{[]string{"--login", "-c", "echo CMD"}, "CMD"},
		{[]string{"-el", "-c", "echo CMD"}, "CMD"},
		{[]string{"-o", "noglob", "-l", "-c", "echo CMD"}, "CMD"},
		{[]string{"-c", "echo CMD -l"}, "CMD -l"},
		{[]string{"-c", `echo "ARGS $0 $1"`, "-l", "--login"}, "ARGS -l --login"},
		{[]string{"-c", "exit 7"}, ""},
	} {
		cmd := exec.Command(sh, append([]string{launcher}, tc.args...)...)
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=dumb"}
		out, err := cmd.CombinedOutput()
		assert.NotContains(t, string(out), "PWNED", "%v: no $HOME/.profile runs: %s", tc.args, out)
		if tc.want == "" {
			var ee *exec.ExitError
			require.ErrorAs(t, err, &ee, "%v", tc.args)
			assert.Equal(t, 7, ee.ExitCode(), "%v: the exit status is kept", tc.args)
			continue
		}
		require.NoError(t, err, "%v: %s", tc.args, out)
		assert.Equal(t, tc.want+"\n", string(out), "%v", tc.args)
	}
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
