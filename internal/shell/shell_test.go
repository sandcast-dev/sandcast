package shell

import (
	"encoding/json"
	"os/exec"
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
// startup-file vector: it reads neither profile nor rc, uses the explicit
// baked rcfile, and forwards its arguments (spec 003 US2).
func TestLoginLauncher(t *testing.T) {
	got := LoginLauncher("/opt/sandcast/bashrc")
	assert.True(t, strings.HasPrefix(got, "#!/bin/sh\n"), "has a shebang")
	assert.Contains(t, got, "--noprofile")
	assert.Contains(t, got, "--norc")
	assert.Contains(t, got, "--rcfile /opt/sandcast/bashrc")
	assert.Contains(t, got, `"$@"`, "forwards arguments")
	assert.Contains(t, got, "exec bash")
	// The launcher must be runnable: a POSIX shell parses it without error.
	if sh, err := exec.LookPath("sh"); err == nil {
		require.NoError(t, exec.Command(sh, "-n", "-c", got).Run(), "launcher is valid sh")
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
