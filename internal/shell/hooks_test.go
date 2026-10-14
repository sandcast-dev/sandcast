package shell

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTargets = HookTargets{Top: "/bin/zsh", TopLogin: "/bin/zsh -l", Nested: `"$BASH" --noprofile --norc`}

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
	assert.Contains(t, got, `exec "$BASH" --noprofile --norc -i`+"\n", "a nested bash stays bash")
	assert.Contains(t, got, `exec "$BASH" $_sandcast_posix --noprofile --norc -c "$BASH_EXECUTION_STRING" "$0" "$@"`)
	assert.Less(t, strings.Index(got, "SANDCAST_SESSION-}"), strings.Index(got, "SANDCAST_SESSION=1"), "the nested check runs before the session is marked")
	parses(t, "bash", got)

	// When the nested target is the top target the session check is left out.
	same := BashrcHook(nil, HookTargets{Top: "/opt/sandcast/shell-login", TopLogin: "/opt/sandcast/shell-login", Nested: "/opt/sandcast/shell-login"})
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
	assert.Contains(t, got, `exec "$BASH" $_sandcast_posix --noprofile --norc -s "$@"`)
	assert.Contains(t, got, `exec "$BASH" $_sandcast_posix --noprofile --norc "$0" "$@"`)
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
