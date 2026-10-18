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

func TestSafeAbsPath(t *testing.T) {
	for _, ok := range []string{"/sandbox", "/etc/gitconfig", "/etc/xdg/a_b/c-d.toml", "/x/.hidden", "/x/..y"} {
		assert.True(t, SafeAbsPath(ok), ok)
	}
	for _, no := range []string{"", "/", "sandbox", "/sandbox/", "//sandbox", "/a/./b", "/a/../b", "/..", "/.", "/a b", "/a;b", "/a$(id)", "/a\nb", "/a'b"} {
		assert.False(t, SafeAbsPath(no), "%q", no)
	}
}

func TestZshEnvExport(t *testing.T) {
	assert.Equal(t, "export ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1 SHELL=/bin/zsh", ZshEnvExport("/opt/sandcast/zdotdir", true))
	assert.Equal(t, "export ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1", ZshEnvExport("/opt/sandcast/zdotdir", false), "a curated shell other than zsh keeps the launcher as SHELL")
}

// TestZshEnvBlockModes checks the three modes of the global zshenv block:
// every block sources the session environment and disables the new-user
// wizard; the relocate mode adds the ZDOTDIR export without SHELL, the
// curated mode the export with SHELL, and the session-only mode no export.
func TestZshEnvBlockModes(t *testing.T) {
	tail := "[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\nzsh-newuser-install() { :; }\n" + ZshEnvEnd + "\n"
	assert.Equal(t, ZshEnvBegin+"\n"+tail, ZshEnvBlock("/opt/sandcast/zdotdir", "/etc/profile.d/sandcast-env.sh", ZshEnvSessionOnly))
	assert.Equal(t, ZshEnvBegin+"\nexport ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1\n"+tail, ZshEnvBlock("/opt/sandcast/zdotdir", "/etc/profile.d/sandcast-env.sh", ZshEnvRelocate))
	assert.Equal(t, ZshEnvBegin+"\nexport ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1 SHELL=/bin/zsh\n"+tail, ZshEnvBlock("/opt/sandcast/zdotdir", "/etc/profile.d/sandcast-env.sh", ZshEnvCurated))
	assert.Equal(t, ZshEnvBegin+"\nzsh-newuser-install() { :; }\n"+ZshEnvEnd+"\n", ZshEnvBlock("/opt/sandcast/zdotdir", "", ZshEnvSessionOnly), "no session script, no source line")
}

// TestLoginLauncher checks the baked bash launcher closes the workspace
// startup-file vector: it reads no profile file, uses the explicit baked
// rcfile in place of ~/.bashrc, and forwards its arguments (spec 003 US2).
// --norc must be absent because it disables --rcfile.
func TestLoginLauncher(t *testing.T) {
	got := LoginLauncher("/opt/sandcast/bashrc", "")
	assert.True(t, strings.HasPrefix(got, "#!/bin/sh\n"), "has a shebang")
	assert.Contains(t, got, "--noprofile")
	assert.NotContains(t, got, "--norc", "--norc makes bash ignore --rcfile")
	assert.Contains(t, got, "--rcfile /opt/sandcast/bashrc")
	assert.Contains(t, got, `"$@"`, "forwards arguments")
	assert.Contains(t, got, "SANDCAST_SHELL_PID=$$\n", "the bashrc hook recognizes this bash by its pid")
	assert.Contains(t, got, "export SANDCAST_SHELL_PID\n")
	assert.Contains(t, got, "unset SANDCAST_SHELL_PID\n", "a non-interactive bash gets no guard")
	assert.Contains(t, got, "export SANDCAST_SESSION\n")
	assert.Contains(t, got, "exec bash")
	assert.Contains(t, got, "--login) continue ;;", "the login options are filtered")
	// The launcher must be runnable: a POSIX shell parses it without error.
	if sh, err := exec.LookPath("sh"); err == nil {
		require.NoError(t, exec.Command(sh, "-n", "-c", got).Run(), "launcher is valid sh")
	}
}

// launcherHome returns a temp home with planted startup and logout files that
// each print a PWNED marker, so a test can assert none of them ran.
func launcherHome(t *testing.T, dir string) string {
	t.Helper()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	for _, f := range []string{".bashrc", ".bash_profile", ".bash_login", ".profile", ".bash_logout"} {
		require.NoError(t, os.WriteFile(filepath.Join(home, f), []byte("echo PWNED-"+f+"\n"), 0o644))
	}
	return home
}

// TestLoginLauncherDropsLogin runs the bash launcher with -l, --login and
// -ilc (an editor's environment probe, su -l, a tmux default-command) and a
// planted $HOME/.bash_logout. A login bash never reads --rcfile, so the
// curated configuration would be lost, and an interactive login bash runs
// the workspace ~/.bash_logout on exit. The launcher drops the login options
// so bash stays a non-login shell with the baked rcfile; arguments after the
// command string are the command's own and are kept.
func TestLoginLauncherDropsLogin(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	home := launcherHome(t, dir)
	rc := filepath.Join(dir, "bashrc")
	require.NoError(t, os.WriteFile(rc, []byte("alias myll='ls -l'\n"), 0o644))
	launcher := filepath.Join(dir, "shell-login")
	require.NoError(t, os.WriteFile(launcher, []byte(LoginLauncher(rc, "")), 0o755))

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-l", "-i", "-c", "alias myll; echo CMD"}, []string{"myll='ls -l'", "CMD"}},
		{[]string{"-ilc", "alias myll; echo CMD"}, []string{"myll='ls -l'", "CMD"}},
		{[]string{"--login", "-i", "-c", "alias myll; echo CMD"}, []string{"myll='ls -l'", "CMD"}},
		{[]string{"-o", "noglob", "-l", "-ic", "alias myll; echo CMD"}, []string{"myll='ls -l'", "CMD"}},
		{[]string{"-i", "-c", `echo "ARGS $0 $1"`, "-l", "--login"}, []string{"ARGS -l --login"}},
		{[]string{"-lc", "echo LC; exit 7"}, nil},
	} {
		cmd := exec.Command(sh, append([]string{launcher}, tc.args...)...)
		cmd.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(bash) + ":/usr/bin:/bin", "TERM=dumb"}
		out, err := cmd.CombinedOutput()
		assert.NotContains(t, string(out), "PWNED", "%v: no $HOME startup or logout file runs: %s", tc.args, out)
		for _, w := range tc.want {
			assert.Contains(t, string(out), w, "%v: %s", tc.args, out)
		}
		if tc.want == nil {
			var ee *exec.ExitError
			require.ErrorAs(t, err, &ee, "%v", tc.args)
			assert.Equal(t, 7, ee.ExitCode(), "%v: the exit status is kept", tc.args)
			assert.Contains(t, string(out), "LC", "%v: %s", tc.args, out)
		}
	}
}

// TestLoginLauncherGuardForInteractiveOnly checks the launcher exports the
// pid guard only for a bash that will be interactive (-i, or no -c and no
// script operand on a terminal) and removes it, an inherited value included,
// for every other form: -c, a script, -s or no operand with stdin piped. A
// guard that a non-interactive bash passes on would wave a later interactive
// bash with the same pid past the bashrc hook into the workspace ~/.bashrc.
func TestLoginLauncherGuardForInteractiveOnly(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	home := launcherHome(t, dir)
	rc := filepath.Join(dir, "bashrc")
	require.NoError(t, os.WriteFile(rc, []byte(""), 0o644))
	launcher := filepath.Join(dir, "shell-login")
	require.NoError(t, os.WriteFile(launcher, []byte(LoginLauncher(rc, "")), 0o755))
	const probe = `echo "GUARD=${SANDCAST_SHELL_PID-unset} PID=$$"`
	script := filepath.Join(dir, "probe.sh")
	require.NoError(t, os.WriteFile(script, []byte(probe+"\n"), 0o644))

	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		// guarded means the guard names the bash itself.
		guarded bool
	}{
		{"-i -c", []string{"-i", "-c", probe}, "", true},
		{"-ic", []string{"-ic", probe}, "", true},
		{"-i after --rcfile", []string{"--rcfile", rc, "-i", "-c", probe}, "", true},
		{"-c", []string{"-c", probe}, "", false},
		{"-lc", []string{"-lc", probe}, "", false},
		{"script operand", []string{script}, "", false},
		{"-- script", []string{"--", script}, "", false},
		{"-s piped", []string{"-s"}, probe + "\n", false},
		{"no operand piped", nil, probe + "\n", false},
	} {
		cmd := exec.Command(sh, append([]string{launcher}, tc.args...)...)
		// An inherited guard must be removed too.
		cmd.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(bash) + ":/usr/bin:/bin", "TERM=dumb", "SANDCAST_SHELL_PID=999999"}
		if tc.stdin != "" {
			cmd.Stdin = strings.NewReader(tc.stdin)
		}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", tc.name, out)
		assert.NotContains(t, string(out), "PWNED", "%s: %s", tc.name, out)
		line := ""
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "GUARD=") {
				line = l
			}
		}
		require.NotEmpty(t, line, "%s: the probe ran: %s", tc.name, out)
		f := strings.Fields(line)
		require.Len(t, f, 2, line)
		guard, pid := strings.TrimPrefix(f[0], "GUARD="), strings.TrimPrefix(f[1], "PID=")
		if tc.guarded {
			assert.Equal(t, pid, guard, "%s: the guard names the launcher's own bash", tc.name)
		} else {
			assert.Equal(t, "unset", guard, "%s: a non-interactive bash gets no guard", tc.name)
		}
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
	require.NoError(t, os.WriteFile(launcher, []byte(LoginLauncher(rc, "")), 0o755))

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
	got := ShLoginLauncher("/etc/profile.d/sandcast.sh", "")
	assert.True(t, strings.HasPrefix(got, "#!/bin/sh\n"), "has a shebang")
	assert.Contains(t, got, "ENV=/etc/profile.d/sandcast.sh\n")
	assert.Contains(t, got, "export ENV SANDCAST_SESSION\n")
	assert.Contains(t, got, `exec /bin/sh "$@"`+"\n")
	assert.NotContains(t, got, "exec /bin/sh -l", "never a login shell")
	assert.Contains(t, ShLoginLauncher("/opt/a b/profile", ""), "ENV='/opt/a b/profile'\n", "the profile path is quoted")
	if sh, err := exec.LookPath("sh"); err == nil {
		require.NoError(t, exec.Command(sh, "-n", "-c", got).Run(), "launcher is valid sh")
	}
}

// TestShLoginLauncherDropsLogin runs the sh launcher with the host sh and a
// planted $HOME/.profile. A caller that passes -l, --login or -lc (an
// editor's environment probe, su -l, a tmux default-command) must not turn
// the shell into a login shell, which would read the workspace .profile.
// Options after the command string are the command's own arguments and are
// kept.
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
	require.NoError(t, os.WriteFile(launcher, []byte(ShLoginLauncher(profile, "")), 0o755))

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

// TestLaunchersSourceSessionEnv checks both launchers source the session
// environment script before they start the shell, so a non-interactive run
// (su - user -c, $SHELL -c), which reads no system bashrc, still carries
// the exported settings and the shims first on PATH (spec 002 FR-006), and
// that the line is absent without a script path.
func TestLaunchersSourceSessionEnv(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	env := filepath.Join(dir, "sandcast-env.sh")
	require.NoError(t, os.WriteFile(env, []byte("export MISE_DATA_DIR='/opt/mise'\n_sandcast_path() { case \"${PATH:-}\" in /opt/mise/shims:*) ;; *) PATH=\"/opt/mise/shims${PATH:+:$PATH}\" ;; esac; export PATH; }\n_sandcast_path\n"), 0o644))
	rc := filepath.Join(dir, "bashrc")
	require.NoError(t, os.WriteFile(rc, []byte(""), 0o644))
	line := "[ -r " + env + " ] && . " + env + "\n"
	got := LoginLauncher(rc, env)
	assert.Contains(t, got, line)
	assert.Less(t, strings.Index(got, line), strings.Index(got, "_sandcast_opts="), "sourced before the option scan sets _sandcast_ variables")
	assert.NotContains(t, LoginLauncher(rc, ""), "] && . ")
	assert.Contains(t, ShLoginLauncher("/etc/profile.d/sandcast.sh", env), line)
	assert.NotContains(t, ShLoginLauncher("/etc/profile.d/sandcast.sh", ""), "] && . ")

	for name, text := range map[string]string{"shell-login": LoginLauncher(rc, env), "sh-login": ShLoginLauncher("/etc/profile.d/sandcast.sh", env)} {
		launcher := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(launcher, []byte(text), 0o755))
		cmd := exec.Command(sh, launcher, "-c", `echo "MISE=[$MISE_DATA_DIR] PATH=[$PATH]"`)
		cmd.Env = []string{"HOME=" + dir, "PATH=" + filepath.Dir(bash) + ":/usr/bin:/bin", "TERM=dumb"}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", name, out)
		assert.Equal(t, "MISE=[/opt/mise] PATH=[/opt/mise/shims:"+filepath.Dir(bash)+":/usr/bin:/bin]\n", string(out), name)
	}
}
