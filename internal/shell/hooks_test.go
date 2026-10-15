package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTargets = HookTargets{
	Top:      HookTarget{Program: "/bin/zsh"},
	TopLogin: HookTarget{Program: "/bin/zsh", Args: "-l"},
	Nested:   PlainBash,
}

var testExports = []Export{{Name: "ZDOTDIR", Value: "/opt/sandcast/zdotdir"}, {Name: "WEIRD", Value: "a b'c"}}

// parses checks that a shell parses script without running it.
func parses(t *testing.T, shell, script string) {
	t.Helper()
	path, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("no %s", shell)
	}
	out, err := exec.Command(path, "-n", "-c", script).CombinedOutput()
	require.NoError(t, err, "%s -n: %s\n%s", shell, out, script)
}

// TestBashrcHook checks the block prepended to the system bashrc: it is
// delimited, recognizes the launcher's bash by the pid guard, leaves login
// shells to the profile hook, redirects interactive shells (nested ones to
// the nested target), keeps a -c command with its arguments, and replaces a
// non-interactive (sshd) bash with one that reads no startup file.
func TestBashrcHook(t *testing.T) {
	got := BashrcHook(testExports, testTargets)
	assert.True(t, strings.HasPrefix(got, HookBegin+"\n"))
	assert.True(t, strings.HasSuffix(got, HookEnd+"\n"))
	assert.Contains(t, got, `if [ "${SANDCAST_SHELL_PID-}" = "$$" ]; then`)
	assert.Contains(t, got, "unset SANDCAST_SHELL_PID")
	assert.Contains(t, got, "elif ! shopt -q login_shell; then")
	assert.Contains(t, got, "export ZDOTDIR=/opt/sandcast/zdotdir\n")
	assert.Contains(t, got, `export WEIRD='a b'\''c'`, "values are quoted")
	assert.Contains(t, got, `exec /bin/zsh -i -c "$BASH_EXECUTION_STRING" "$0" "$@"`)
	assert.Contains(t, got, "exec /bin/zsh -i\n")
	assert.Contains(t, got, "shopt -s execfail\n", "a failed exec returns so the fallback runs")
	assert.Contains(t, got, "sandcast: cannot execute /bin/zsh", "the fallback names the target")
	assert.Contains(t, got, `exec "$BASH" --noprofile --norc -i`+"\n", "a nested bash stays bash")
	assert.Contains(t, got, `set -- "$@" -c "$BASH_EXECUTION_STRING" "$0"`)
	assert.Contains(t, got, `set -- "$@" -o "$_sandcast_opt"`, "set -o options are forwarded")
	assert.Contains(t, got, `set -- "$@" -O "$_sandcast_opt"`, "shopt options are forwarded")
	assert.Contains(t, got, `exec "$BASH" --noprofile --norc "$@"`)
	assert.Less(t, strings.Index(got, "SANDCAST_SESSION-}"), strings.Index(got, "SANDCAST_SESSION=1"), "the nested check runs before the session is marked")
	parses(t, "bash", got)

	// When the nested target is the top target the session check is left out.
	launcher := HookTarget{Program: "/opt/sandcast/shell-login"}
	same := BashrcHook(nil, HookTargets{Top: launcher, TopLogin: launcher, Nested: launcher})
	assert.NotContains(t, same, "SANDCAST_SESSION-}")
	assert.NotContains(t, same, "export ZDOTDIR")
	parses(t, "bash", same)
}

// TestLoginHook checks the profile.d hook: bash login shells are replaced
// (interactive ones by the curated shell, others by a bash without startup
// files that keeps the command, script or stdin), other POSIX shells get
// HOME redirected, and zsh only gets the exports. It must parse as POSIX sh
// because dash and busybox ash source it too.
func TestLoginHook(t *testing.T) {
	got := LoginHook(testExports, testTargets, "/opt/sandcast/home")
	assert.Contains(t, got, "export ZDOTDIR=/opt/sandcast/zdotdir\n")
	assert.Contains(t, got, "if shopt -q login_shell 2>/dev/null; then")
	assert.Contains(t, got, "exec /bin/zsh -l -i\n")
	assert.Contains(t, got, `set -- "$@" -s ;;`)
	assert.Contains(t, got, `set -- "$@" -- "$0" ;;`, "a script is passed after --")
	assert.Contains(t, got, `"$BASH" | "-$BASH" | "${BASH##*/}" | "-${BASH##*/}" | sh | -sh)`, "stdin is recognized by the invoking name")
	assert.Contains(t, got, `exec "$BASH" --noprofile --norc "$@"`)
	assert.Contains(t, got, `elif [ -z "${ZSH_VERSION-}" ] && [ -n "${HOME-}" ] && [ "$HOME" != /opt/sandcast/home ]; then`)
	assert.Contains(t, got, "HOME=/opt/sandcast/home\n")
	parses(t, "sh", got)
	parses(t, "bash", got)
}

// TestRedirectHomeProfile checks the baked .profile restores HOME.
func TestRedirectHomeProfile(t *testing.T) {
	got := RedirectHomeProfile()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	out, err := exec.Command(sh, "-c", `SANDCAST_HOME=/real; HOME=/opt/sandcast/home`+"\n"+got+`echo "$HOME ${SANDCAST_HOME-unset}"`).CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "/real unset\n", string(out))
}

// hostBash returns a bash 4 or newer found on the PATH, or skips the test:
// bash 3.2 (the macOS /bin/bash) binds $0 and "$@" only after the startup
// files, so the hooks cannot be exercised with it.
func hostBash(t *testing.T) string {
	t.Helper()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, "bash")
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			continue
		}
		out, err := exec.Command(p, "-c", "echo ${BASH_VERSINFO[0]}").Output()
		if err != nil {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 4 {
			return p
		}
	}
	t.Skip("no bash 4 or newer on the PATH")
	return ""
}

// hookFixture is a temp tree for running the hooks: a home whose startup
// and logout files print PWNED markers, and stub targets that print their
// name, arguments and the exported variables, then exit 5.
type hookFixture struct {
	dir, home, bash string
	targets         HookTargets
}

func newHookFixture(t *testing.T) *hookFixture {
	t.Helper()
	f := &hookFixture{dir: t.TempDir(), bash: hostBash(t)}
	f.home = filepath.Join(f.dir, "home")
	require.NoError(t, os.MkdirAll(f.home, 0o755))
	for _, name := range []string{".bashrc", ".bash_profile", ".bash_login", ".profile", ".bash_logout"} {
		require.NoError(t, os.WriteFile(filepath.Join(f.home, name), []byte("echo PWNED-"+name+"\n"), 0o644))
	}
	stub := func(name string) HookTarget {
		p := filepath.Join(f.dir, name)
		require.NoError(t, os.WriteFile(p, []byte(`#!/bin/sh
echo "STUB-`+name+` ARGV=[$*] ZDOTDIR=[${ZDOTDIR-}] SESSION=[${SANDCAST_SESSION-}]"
exit 5
`), 0o755))
		return HookTarget{Program: p}
	}
	f.targets = HookTargets{Top: stub("top"), TopLogin: HookTarget{Program: stub("toplogin").Program, Args: "-l"}, Nested: stub("nested")}
	return f
}

// unexecutable returns a target that exists but cannot be executed.
func (f *hookFixture) unexecutable(t *testing.T) HookTarget {
	t.Helper()
	p := filepath.Join(f.dir, "noexec")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho NOEXEC-RAN\n"), 0o644))
	return HookTarget{Program: p}
}

func (f *hookFixture) env(extra ...string) []string {
	return append([]string{"HOME=" + f.home, "PATH=" + filepath.Dir(f.bash) + ":/usr/bin:/bin", "TERM=dumb"}, extra...)
}

// run executes cmd and returns its combined output and exit code.
func run(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee, "%v: %s", cmd.Args, out)
	return string(out), ee.ExitCode()
}

// TestLoginHookRuns sources the login hook from a planted ~/.bash_profile
// (the hook lives in /etc/profile.d in the image, which runs before that
// file, and sourcing it from the profile itself shows the same thing: the
// shell is replaced before the rest of the profile runs) and checks every
// non-interactive form of a login bash: a -c command with $0 and its
// arguments, the standard input, a script (also one named like an option,
// and through a renamed bash), and that -e, -u, -x and -o pipefail survive
// the replacement, with the exit status. No planted file ever prints.
func TestLoginHookRuns(t *testing.T) {
	f := newHookFixture(t)
	hook := filepath.Join(f.dir, "zz-sandcast-shell.sh")
	require.NoError(t, os.WriteFile(hook, []byte(LoginHook(testExports, f.targets, "/opt/sandcast/home")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.home, ".bash_profile"), []byte(". "+hook+"\necho PWNED-.bash_profile\n"), 0o644))
	script := filepath.Join(f.dir, "script.sh")
	require.NoError(t, os.WriteFile(script, []byte(`echo "SCRIPT 0=$0 1=$1 2=$2"; exit 7`+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "-x"), []byte("echo SCRIPT-DASH-X; exit 8\n"), 0o644))
	shebang := filepath.Join(f.dir, "le.sh")
	require.NoError(t, os.WriteFile(shebang, []byte("#!"+f.bash+" -le\nfalse\necho X-SCRIPT\nexit 3\n"), 0o755))
	bash5 := filepath.Join(f.dir, "bash5")
	require.NoError(t, os.Symlink(f.bash, bash5))

	for _, tc := range []struct {
		name    string
		path    string
		args    []string
		stdin   string
		dir     string
		code    int
		want    []string
		absent  []string
		logwant string
	}{
		{name: "command", args: []string{"-l", "-c", `echo "RAN 0=$0 1=$1 2=$2"; exit 3`, "zero", "a", "b"}, code: 3, want: []string{"RAN 0=zero 1=a 2=b"}},
		{name: "-e", args: []string{"-le", "-c", "false; echo X; exit 3"}, code: 1, absent: []string{"X"}},
		{name: "pipefail", args: []string{"-l", "-o", "pipefail", "-c", "false | true"}, code: 1},
		// The host /etc/profile chain is not -u clean (the macOS /etc/bashrc
		// reads $PS1), so this case keeps it out with --noprofile and sources
		// the hook from the command, which the replacement runs again as a
		// non-login shell, where the hook does nothing.
		{name: "-u", args: []string{"--noprofile", "-lu", "-c", ". " + hook + `; echo "[${UNSET_VAR}]"`}, code: 127, want: []string{"unbound variable"}, absent: []string{"[]"}},
		{name: "posix", args: []string{"-l", "-o", "posix", "-c", `case :$SHELLOPTS: in *:posix:*) echo POSIX-KEPT ;; esac`}, want: []string{"POSIX-KEPT"}},
		{name: "-x", args: []string{"-lx", "-c", "echo TRACE"}, want: []string{"+ echo TRACE", "TRACE"}},
		{name: "stdin", args: []string{"-l"}, stdin: "echo FROM-STDIN; exit 4\n", code: 4, want: []string{"FROM-STDIN"}},
		{name: "stdin renamed bash", path: bash5, args: []string{"-l"}, stdin: "echo FROM-STDIN-BASH5; exit 4\n", code: 4, want: []string{"FROM-STDIN-BASH5"}},
		{name: "script", args: []string{"-l", script, "x", "y"}, code: 7, want: []string{"SCRIPT 0=" + script + " 1=x 2=y"}},
		{name: "script named -x", args: []string{"-l", "--", "-x"}, dir: f.dir, code: 8, want: []string{"SCRIPT-DASH-X"}},
		{name: "shebang -le", path: shebang, code: 1, absent: []string{"X-SCRIPT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = f.bash
			}
			cmd := exec.Command(path, tc.args...)
			cmd.Env = f.env()
			cmd.Dir = tc.dir
			if tc.stdin != "" {
				cmd.Stdin = strings.NewReader(tc.stdin)
			}
			out, code := run(t, cmd)
			assert.NotContains(t, out, "PWNED", "no planted file runs: %s", out)
			assert.NotContains(t, out, "STUB-", "no interactive target runs: %s", out)
			assert.Equal(t, tc.code, code, "exit status: %s", out)
			for _, w := range tc.want {
				assert.Contains(t, out, w, out)
			}
			for _, a := range tc.absent {
				assert.NotContains(t, out, a, out)
			}
		})
	}

	// A renamed bash invoked by its bare name: $0 is the base name and $BASH
	// the PATH lookup, so the base name pattern recognizes the stdin case.
	cmd := exec.Command(bash5, "-l")
	cmd.Args = []string{"bash5", "-l"}
	cmd.Env = f.env("PATH=" + f.dir + ":" + filepath.Dir(f.bash) + ":/usr/bin:/bin")
	cmd.Stdin = strings.NewReader("echo FROM-STDIN-BARE; exit 4\n")
	out, code := run(t, cmd)
	assert.Equal(t, 4, code, out)
	assert.Contains(t, out, "FROM-STDIN-BARE", out)
	assert.NotContains(t, out, "PWNED", out)
}

// TestLoginHookRunsInteractive checks an interactive login bash is handed
// to the TopLogin target with the exports, the -c command, $0 and its
// arguments; inside a curated session (SANDCAST_SESSION set) to the Nested
// target; and when the target cannot be executed, to a bash without
// startup files after one sandcast: line, so the session stays usable.
func TestLoginHookRunsInteractive(t *testing.T) {
	f := newHookFixture(t)
	write := func(targets HookTargets) {
		hook := filepath.Join(f.dir, "zz-sandcast-shell.sh")
		require.NoError(t, os.WriteFile(hook, []byte(LoginHook(testExports, targets, "/opt/sandcast/home")), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(f.home, ".bash_profile"), []byte(". "+hook+"\necho PWNED-.bash_profile\n"), 0o644))
	}
	write(f.targets)

	cmd := exec.Command(f.bash, "-l", "-i", "-c", "echo CMD", "zero", "a")
	cmd.Env = f.env()
	out, code := run(t, cmd)
	assert.Equal(t, 5, code, out)
	assert.Contains(t, out, "STUB-toplogin ARGV=[-l -i -c echo CMD zero a] ZDOTDIR=[/opt/sandcast/zdotdir] SESSION=[1]", out)
	assert.NotContains(t, out, "PWNED", out)

	cmd = exec.Command(f.bash, "-l", "-i", "-c", "echo CMD")
	cmd.Env = f.env("SANDCAST_SESSION=1")
	out, code = run(t, cmd)
	assert.Equal(t, 5, code, out)
	assert.Contains(t, out, "STUB-nested ARGV=[-i -c echo CMD ", out)
	assert.NotContains(t, out, "PWNED", out)

	write(HookTargets{Top: f.targets.Top, TopLogin: f.unexecutable(t), Nested: f.targets.Nested})
	cmd = exec.Command(f.bash, "-l", "-i", "-c", "echo FALLBACK-CMD; exit 2")
	cmd.Env = f.env()
	out, code = run(t, cmd)
	assert.Equal(t, 2, code, out)
	assert.Contains(t, out, "sandcast: cannot execute "+filepath.Join(f.dir, "noexec"), out)
	assert.Contains(t, out, "FALLBACK-CMD", out)
	assert.NotContains(t, out, "NOEXEC-RAN", out)
	assert.NotContains(t, out, "PWNED", out)
}

// TestLoginHookRedirectsPOSIXHome checks a POSIX shell that is not bash gets
// HOME pointed at the redirect home, with the real home kept for the baked
// .profile to restore.
func TestLoginHookRedirectsPOSIXHome(t *testing.T) {
	dash, err := exec.LookPath("dash")
	if err != nil {
		t.Skip("no dash")
	}
	hook := filepath.Join(t.TempDir(), "hook.sh")
	require.NoError(t, os.WriteFile(hook, []byte(LoginHook(testExports, testTargets, "/opt/sandcast/home")), 0o644))
	cmd := exec.Command(dash, "-c", ". "+hook+`; echo "HOME=$HOME REAL=$SANDCAST_HOME ZDOTDIR=$ZDOTDIR"`)
	cmd.Env = []string{"HOME=/real", "PATH=/usr/bin:/bin"}
	out, code := run(t, cmd)
	assert.Equal(t, 0, code, out)
	assert.Equal(t, "HOME=/opt/sandcast/home REAL=/real ZDOTDIR=/opt/sandcast/zdotdir\n", out)
}

// TestBashrcHookRuns runs the bashrc hook as the rcfile of an interactive
// bash: a top-level one is handed to the Top target with the exports, the
// -c command, $0 and its arguments; one inside a curated session to the
// Nested target; the launcher's own bash (the guard names its pid) is not
// replaced and the guard is removed; and a non-interactive bash -c that
// reads the file (sshd) is replaced by a bash without startup files that
// keeps the command, its arguments, -e and the exit status. An unexecutable
// target falls back to a bash without startup files.
func TestBashrcHookRuns(t *testing.T) {
	f := newHookFixture(t)
	hook := filepath.Join(f.dir, "bashrc-hook")
	require.NoError(t, os.WriteFile(hook, []byte(BashrcHook(testExports, f.targets)), 0o644))

	cmd := exec.Command(f.bash, "--rcfile", hook, "-i", "-c", "echo CMD; exit 9", "zero", "a")
	cmd.Env = f.env()
	out, code := run(t, cmd)
	assert.Equal(t, 5, code, out)
	assert.Contains(t, out, "STUB-top ARGV=[-i -c echo CMD; exit 9 zero a] ZDOTDIR=[/opt/sandcast/zdotdir] SESSION=[1]", out)

	cmd = exec.Command(f.bash, "--rcfile", hook, "-i", "-c", "echo CMD")
	cmd.Env = f.env("SANDCAST_SESSION=1")
	out, code = run(t, cmd)
	assert.Equal(t, 5, code, out)
	assert.Contains(t, out, "STUB-nested ARGV=[-i -c echo CMD ", out)

	// The guard: an outer bash exports its pid and execs the hooked bash, so
	// both share the pid, as the launcher and its bash do.
	cmd = exec.Command(f.bash, "-c", `SANDCAST_SHELL_PID=$$ exec "$1" --rcfile "$2" -i -c "$3"`, "_", f.bash, hook, `echo "GUARD-PASS G=[${SANDCAST_SHELL_PID-unset}]"`)
	cmd.Env = f.env()
	out, code = run(t, cmd)
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "GUARD-PASS G=[unset]", out)
	assert.NotContains(t, out, "STUB-", "the launcher's bash is not replaced: %s", out)

	// Non-interactive: the hook is sourced once (HOOKED marks the
	// replacement) as the sshd bash -c reads the system bashrc.
	probe := `if [ -z "${HOOKED-}" ]; then export HOOKED=1; . ` + hook + `; fi; echo "RAN 0=$0 1=$1 HOOKED=$HOOKED"; false; echo X; exit 6`
	cmd = exec.Command(f.bash, "-c", probe, "zero", "a")
	cmd.Env = f.env()
	out, code = run(t, cmd)
	assert.Equal(t, 6, code, out)
	assert.Contains(t, out, "RAN 0=zero 1=a HOOKED=1\nX\n", out)
	assert.NotContains(t, out, "STUB-", out)

	cmd = exec.Command(f.bash, "-e", "-c", probe, "zero", "a")
	cmd.Env = f.env()
	out, code = run(t, cmd)
	assert.Equal(t, 1, code, "-e survives the replacement: %s", out)
	assert.Contains(t, out, "RAN 0=zero 1=a HOOKED=1\n", out)
	assert.NotContains(t, out, "X\n", out)

	// An unexecutable target: one sandcast: line, then bash without startup
	// files runs the command.
	require.NoError(t, os.WriteFile(hook, []byte(BashrcHook(testExports, HookTargets{Top: f.unexecutable(t), TopLogin: f.targets.TopLogin, Nested: f.targets.Nested})), 0o644))
	cmd = exec.Command(f.bash, "--rcfile", hook, "-i", "-c", "echo FALLBACK-CMD; exit 2")
	cmd.Env = f.env()
	out, code = run(t, cmd)
	assert.Equal(t, 2, code, out)
	assert.Contains(t, out, "sandcast: cannot execute "+filepath.Join(f.dir, "noexec"), out)
	assert.Contains(t, out, "FALLBACK-CMD", out)
	assert.NotContains(t, out, "NOEXEC-RAN", out)
	assert.NotContains(t, out, "PWNED", out)
}
