package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/probe"
)

func testPlan() *plan.Plan {
	return &plan.Plan{Components: []plan.Component{
		{Key: "go", Bins: []string{"go"}, Check: plan.Check{Command: "go version", Version: "1.25"}},
		{Key: "zsh", Bins: []string{"zsh"}, Check: plan.Check{Command: "zsh --version"}},
		{Key: "broken", Bins: []string{"broken"}, Check: plan.Check{Command: "command -v broken"}},
		{Key: "nocheck"},
	}}
}

// checkEngine answers the check script by looking at which commands it
// contains, emitting JSON check lines like the real script.
func checkEngine(lines map[string]string) *engine.Fake {
	return &engine.Fake{RunFunc: func(spec engine.RunSpec) ([]byte, []byte, error) {
		var out strings.Builder
		for key, line := range lines {
			if strings.Contains(spec.Script, key) {
				out.WriteString(line + "\n")
			}
		}
		return []byte(out.String()), nil, nil
	}}
}

func TestRequests(t *testing.T) {
	reqs := Requests(testPlan())
	require.Len(t, reqs, 3)
	assert.Equal(t, "broken", reqs[0].Key)
	assert.Equal(t, "go", reqs[1].Key)
	assert.Equal(t, "1.25", reqs[1].Selector)
}

func TestRunReportsEveryResultThenFails(t *testing.T) {
	eng := checkEngine(map[string]string{
		"go version":    `{"type":"check","key":"go","exit":0,"path":"/opt/mise/shims/go","out":"go version go1.25.1 linux/arm64"}`,
		"zsh --version": `{"type":"check","key":"zsh","exit":0,"path":"/usr/bin/zsh","out":"zsh 5.9 (aarch64-unknown-linux-gnu)"}`,
		"broken":        `{"type":"check","key":"broken","exit":1,"path":"","out":""}`,
	})
	r, err := Run(context.Background(), eng, "localhost/demo:latest", "linux/arm64", testPlan())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrChecksFailed))
	require.NotNil(t, r)
	assert.Equal(t, 2, r.Passed)
	assert.Equal(t, 1, r.Failed)
	require.Len(t, r.Results, 3)
	assert.Equal(t, Result{ComponentKey: "broken", Condition: ConditionImage, OK: false, Status: "missing", Output: ""}, r.Results[0])
	assert.Equal(t, "1.25.1", r.Results[1].Version)
	assert.True(t, strings.HasPrefix(r.Image.Digest, "sha256:"))
	assert.Equal(t, 1, eng.CountOp("run"), "all checks run in one container")

	path := filepath.Join(t.TempDir(), FileName)
	require.NoError(t, r.Write(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "failed: 1")
}

func TestRunAllPass(t *testing.T) {
	eng := checkEngine(map[string]string{
		"go version":    `{"type":"check","key":"go","exit":0,"path":"/x","out":"go version go1.25.1"}`,
		"zsh --version": `{"type":"check","key":"zsh","exit":0,"path":"/x","out":"zsh 5.9"}`,
		"broken":        `{"type":"check","key":"broken","exit":0,"path":"/x","out":"/usr/local/bin/broken"}`,
	})
	r, err := Run(context.Background(), eng, "img", "", testPlan())
	require.NoError(t, err)
	assert.Equal(t, 3, r.Passed)
}

func TestRunIncompatibleVersionFails(t *testing.T) {
	eng := checkEngine(map[string]string{
		"go version":    `{"type":"check","key":"go","exit":0,"path":"/x","out":"go version go1.24.3"}`,
		"zsh --version": `{"type":"check","key":"zsh","exit":0,"path":"/x","out":"zsh 5.9"}`,
		"broken":        `{"type":"check","key":"broken","exit":0,"path":"/x","out":"x"}`,
	})
	r, err := Run(context.Background(), eng, "img", "", testPlan())
	require.Error(t, err)
	assert.Equal(t, "incompatible", r.Results[1].Status)
}

func TestRunReportsTimedOutCheck(t *testing.T) {
	eng := checkEngine(map[string]string{
		"go version":    `{"type":"check","key":"go","exit":0,"path":"/x","out":"go version go1.25.1"}`,
		"zsh --version": `{"type":"check","key":"zsh","exit":124,"path":"/x","out":"","timeout":true}`,
		"broken":        `{"type":"check","key":"broken","exit":0,"path":"/x","out":"x"}`,
	})
	r, err := Run(context.Background(), eng, "img", "", testPlan())
	require.ErrorIs(t, err, ErrChecksFailed)
	require.Len(t, r.Results, 3, "every component is reported")
	zsh := r.Results[2]
	assert.Equal(t, "zsh", zsh.ComponentKey)
	assert.False(t, zsh.OK)
	assert.Equal(t, "timeout", zsh.Status, "a timeout is told apart from a missing tool")
	assert.Contains(t, zsh.Output, "did not finish within")
	assert.Equal(t, 2, r.Passed)
}

func TestReportWriteRefusesCredentialInOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	r := &Report{Results: []Result{{ComponentKey: "gh", Status: "present", Output: "token ghp_" + strings.Repeat("J", 36)}}}
	err := r.Write(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not written")
	assert.NotContains(t, err.Error(), strings.Repeat("J", 36))
	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func openshellPlan(program string) *plan.Plan {
	p := testPlan()
	p.Target = manifest.TargetOpenShell
	p.FinalUser = plan.FinalUser{Name: "sandbox", Home: "/sandbox"}
	p.Shell = plan.Shell{Program: program}
	if program == "zsh" {
		p.Shell.RCPath = plan.ZshRCPath
	}
	return p
}

func TestSessionRequests(t *testing.T) {
	assert.Empty(t, SessionRequests(testPlan()), "no session checks for the container target")

	keys := func(reqs []probe.Request) []string {
		out := make([]string, len(reqs))
		for i, r := range reqs {
			out[i] = r.Key
		}
		return out
	}
	bash := SessionRequests(openshellPlan("bash"))
	assert.Equal(t, []string{
		"session.bash-interactive.broken", "session.bash-interactive.go", "session.bash-interactive.zsh",
		"session.bash-login.broken", "session.bash-login.go", "session.bash-login.zsh",
		"session.bash.startup",
	}, keys(bash))
	assert.Contains(t, bash[4].Command, "env -i HOME=/sandbox USER=sandbox TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin bash -lc ")
	assert.Contains(t, bash[4].Command, "go version' sandcast-session $_sb;", "the binaries that resolve in the image environment are the session shell's arguments")
	assert.Equal(t, "1.25", bash[4].Selector, "the session check keeps the component's expectations")
	assert.Contains(t, bash[1].Command, "| env -i HOME=/sandbox USER=sandbox TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin bash -i -s -- $_sb 2>/dev/null")

	zsh := SessionRequests(openshellPlan("zsh"))
	assert.Contains(t, keys(zsh), "session.zsh.go")
	assert.Contains(t, keys(zsh), "session.zsh.startup")
	assert.Len(t, zsh, 10)
	for _, r := range zsh {
		if r.Key == "session.zsh.go" {
			assert.Contains(t, r.Command, "zsh -il -c", "a login zsh, as the login hook starts it")
		}
		if r.Key == "session.zsh.startup" {
			assert.Contains(t, r.Command, "zsh -il -c")
			assert.Contains(t, r.Command, plan.CuratedRCLoadedVar)
		}
	}

	// The zsh checks key on the shell kind the hooks use, not on
	// shell.program alone: a zsh component without a shell section (testPlan
	// has one) is a zsh image, whose interactive sessions the hooks hand to
	// zsh, so it gets the zsh checks and the zsh startup check.
	zshComponent := openshellPlan("")
	require.Equal(t, "zsh", plan.ShellKind(zshComponent))
	component := SessionRequests(zshComponent)
	assert.Contains(t, keys(component), "session.zsh.go")
	assert.Contains(t, keys(component), "session.zsh.startup")
	assert.NotContains(t, keys(component), "session.bash.startup")
	assert.Len(t, component, 10)
}

// TestSessionRequestsRunLocally runs the generated session commands with
// the host's shells, so their quoting is exercised for real.
func TestSessionRequestsRunLocally(t *testing.T) {
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Skipf("no %s", sh)
		}
	}
	p := openshellPlan("zsh")
	p.FinalUser.Home = t.TempDir()
	p.Components = []plan.Component{{Key: "sh", Bins: []string{"sh"}, Check: plan.Check{Command: `command -v sh && echo "it's" >&2`}}}
	for _, r := range SessionRequests(p) {
		if r.Key == "session.zsh.startup" {
			continue
		}
		out, err := exec.Command("/bin/sh", "-c", r.Command).CombinedOutput()
		require.NoError(t, err, "%s: %s", r.Key, out)
		assert.Contains(t, string(out), "/sh", r.Key)
		assert.Contains(t, string(out), "it's", "%s keeps the check's stderr", r.Key)
	}
}

// TestSessionChecksDeclaredBins runs the session commands with the host's
// shells (FR-010): a binary that resolves in the image environment but not
// through the session PATH fails every session check with exit 127; a
// binary that does not resolve in the image environment either is skipped
// with a note, which Run moves out of the output into Result.Notes.
func TestSessionChecksDeclaredBins(t *testing.T) {
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Skipf("no %s", sh)
		}
	}
	imageOnly := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(imageOnly, "sandcast-image-only"), []byte("#!/bin/sh\n"), 0o755))
	imageEnv := append(os.Environ(), "PATH="+imageOnly+":"+os.Getenv("PATH"))
	run := func(c plan.Component) map[string]string {
		p := openshellPlan("zsh")
		p.FinalUser.Home = t.TempDir()
		p.Components = []plan.Component{c}
		out := map[string]string{}
		for _, r := range SessionRequests(p) {
			if r.Key == "session.zsh.startup" {
				continue
			}
			cmd := exec.Command("/bin/sh", "-c", r.Command)
			cmd.Env = imageEnv
			b, err := cmd.CombinedOutput()
			code := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				require.NoError(t, err)
			}
			out[r.Key] = fmt.Sprintf("%d\n%s", code, b)
		}
		require.Len(t, out, 3)
		return out
	}
	for key, res := range run(plan.Component{Key: "tool", Bins: []string{"sh", "sandcast-image-only"}, Check: plan.Check{Command: "command -v sh"}}) {
		assert.True(t, strings.HasPrefix(res, "127\n"), "%s: %s", key, res)
		assert.Contains(t, res, "sandcast-image-only does not resolve through the session PATH", key)
	}
	for key, res := range run(plan.Component{Key: "tool", Bins: []string{"sh", "sandcast-nowhere"}, Check: plan.Check{Command: "command -v sh"}}) {
		assert.True(t, strings.HasPrefix(res, "0\n"), "%s: %s", key, res)
		assert.Contains(t, res, NotCheckedPrefix+"sandcast-nowhere does not resolve in the image environment", key)
		out, notes := splitNotes(strings.TrimPrefix(res, "0\n"))
		assert.NotContains(t, out, NotCheckedPrefix)
		assert.Equal(t, []string{"sandcast-nowhere does not resolve in the image environment; not checked in the session"}, notes)
	}
	for key, res := range run(plan.Component{Key: "certs", Bins: []string{"certs"}, Check: plan.Check{Command: "true"}}) {
		assert.Equal(t, "0\n", res, "%s: a component without executables is checked by its own check, without a note", key)
	}
}

// misePlan is an OpenShell plan in which mise installs go and a backend
// tool, and the package manager installs zsh.
func misePlan() *plan.Plan {
	p := openshellPlan("bash")
	p.Mise.TOML = "[settings]\nlocked = true\n\n[tools]\n\"aqua:jqlang/jq\" = \"latest\"\ngo = \"1.25\"\n"
	for i := range p.Components {
		switch p.Components[i].Key {
		case "go", "broken":
			p.Components[i].InstallerKind, p.Components[i].Action = "mise", plan.ActionInstall
		case "zsh":
			p.Components[i].InstallerKind, p.Components[i].Action = "package", plan.ActionInstall
		}
	}
	return p
}

func TestSessionMiseProjectRequest(t *testing.T) {
	var got []probe.Request
	for _, r := range SessionRequests(misePlan()) {
		if r.Key == sessionMiseProjectKey {
			got = append(got, r)
		}
	}
	require.Len(t, got, 1)
	r := got[0]
	assert.Equal(t, ConditionSessionBashLogin, condition(r.Key))
	assert.Equal(t, "broken", r.Bin, "the first mise component by key")
	assert.Contains(t, r.Command, "bash -lc ")
	assert.Contains(t, r.Command, `"aqua:jqlang/jq" = "`+miseProjectPin+`"`)
	assert.Contains(t, r.Command, "go "+miseProjectPin)
	assert.NotContains(t, r.Command, "aqua:jqlang/jq "+miseProjectPin, ".tool-versions names no backend tool")

	skipped := misePlan()
	for i := range skipped.Components {
		skipped.Components[i].Action = plan.ActionSkip
	}
	for _, r := range SessionRequests(skipped) {
		assert.NotEqual(t, sessionMiseProjectKey, r.Key, "no check when mise installs nothing")
	}
	for _, r := range SessionRequests(openshellPlan("bash")) {
		assert.NotEqual(t, sessionMiseProjectKey, r.Key, "no check without mise components")
	}
}

// TestMiseProjectCheckRunsLocally runs the wrapper with the host's sh and a
// fake shim that behaves like mise without the override settings: it warns
// and fails when it finds a project mise.toml.
func TestMiseProjectCheckRunsLocally(t *testing.T) {
	bin := t.TempDir()
	shim := "#!/bin/sh\n" +
		`if [ -z "$MISE_OVERRIDE_CONFIG_FILENAMES" ] && [ -f mise.toml ]; then` + "\n" +
		`  echo "mise WARN  Failed to resolve tool version list for go: go@0.0.0 is not in the lockfile" >&2` + "\n" +
		`  echo "mise ERROR No version is set for shim: go" >&2; exit 1` + "\n" +
		"fi\n" +
		`echo "mise WARN  mise version 2099.1.1 available" >&2` + "\n" +
		`echo "To update, run mise self-update" >&2` + "\n" +
		"echo go version go1.25.1; echo note >&2\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte(shim), 0o755))
	run := func(env ...string) (int, string) {
		dir := filepath.Join(t.TempDir(), "proj")
		cmd := exec.Command("/bin/sh", "-c", miseProjectCheck(dir, "go version", []string{"aqua:jqlang/jq", "go"}))
		cmd.Env = append(os.Environ(), append(env, "PATH="+bin+":"+os.Getenv("PATH"))...)
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), string(out)
		}
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(dir, ".tool-versions"))
		require.NoError(t, err)
		assert.Equal(t, "go "+miseProjectPin+"\n", string(data))
		return 0, string(out)
	}
	code, out := run("MISE_OVERRIDE_CONFIG_FILENAMES=")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "is not in the lockfile")

	code, out = run("MISE_OVERRIDE_CONFIG_FILENAMES=/etc/mise/mise.toml")
	assert.Equal(t, 0, code, "the update notice is not a failure: %s", out)
	assert.Contains(t, out, "go version go1.25.1")
	assert.Contains(t, out, "note", "the check's stderr is kept")
}

// TestRunSessionFailureFailsVerify checks that session entries are reported
// with their condition and count toward the exit status, even when every
// image-condition check passes (FR-010, SC-004).
func TestRunSessionFailureFailsVerify(t *testing.T) {
	p := openshellPlan("zsh")
	eng := &engine.Fake{RunFunc: func(spec engine.RunSpec) ([]byte, []byte, error) {
		var out strings.Builder
		for _, m := range regexp.MustCompile(`(?m)^_check '([^']*)'`).FindAllStringSubmatch(spec.Script, -1) {
			key := m[1]
			exit, line := 0, "/usr/bin/x 1.25.1"
			if key == "session.zsh.startup" {
				exit, line = 1, "/etc/zsh/zshrc.d/sandcast.zsh:41: command not found: zoxide"
			}
			fmt.Fprintf(&out, `{"type":"check","key":%q,"exit":%d,"path":"","out":%q}`+"\n", key, exit, line)
		}
		return []byte(out.String()), nil, nil
	}}
	r, err := Run(context.Background(), eng, "localhost/demo:latest", "linux/arm64", p)
	require.ErrorIs(t, err, ErrChecksFailed)
	assert.Contains(t, err.Error(), "session.zsh.startup")
	assert.Equal(t, 1, r.Failed)
	assert.Equal(t, 1, eng.CountOp("run"), "session checks share the verify container")
	conditions := map[string]int{}
	for _, res := range r.Results {
		conditions[res.Condition]++
		if res.ComponentKey == "session.zsh.startup" {
			assert.False(t, res.OK)
			assert.Equal(t, ConditionSessionZsh, res.Condition)
			assert.Contains(t, res.Output, "command not found: zoxide")
			assert.True(t, res.IsSession())
		}
	}
	assert.Equal(t, map[string]int{ConditionImage: 3, ConditionSessionBashLogin: 3, ConditionSessionBashInter: 3, ConditionSessionZsh: 4}, conditions)
}

// TestBashStartupCheckRunsLocally runs session.bash.startup with the host's
// bash: stderr output from the startup files fails it, and bash's own
// notes about the missing terminal do not.
func TestBashStartupCheckRunsLocally(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	p := openshellPlan("bash")
	home := t.TempDir()
	p.FinalUser.Home = home
	var startup probe.Request
	for _, r := range SessionRequests(p) {
		if r.Key == "session.bash.startup" {
			startup = r
		}
	}
	require.NotEmpty(t, startup.Command)
	assert.Equal(t, ConditionSessionBashInter, condition(startup.Key))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte("echo broken-rc >&2\n"), 0o644))
	out, err := exec.Command("/bin/sh", "-c", startup.Command).CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "broken-rc")
	assert.NotContains(t, string(out), "no job control")
}

// TestZshStartupCheckRunsLocally runs session.zsh.startup with the host's
// zsh: the no-terminal zle note is ignored, other stderr output fails it.
func TestZshStartupCheckRunsLocally(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("no zsh")
	}
	p := openshellPlan("zsh")
	p.Shell.RCPath = ""
	home := t.TempDir()
	p.FinalUser.Home = home
	var startup probe.Request
	for _, r := range SessionRequests(p) {
		if r.Key == "session.zsh.startup" {
			startup = r
		}
	}
	require.NotEmpty(t, startup.Command)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zshrc"), []byte("setopt zle\n"), 0o644))
	out, err := exec.Command("/bin/sh", "-c", startup.Command).CombinedOutput()
	require.NoError(t, err, "the zle note alone passes: %s", out)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zshrc"), []byte("setopt zle\nno-such-command-sandcast\n"), 0o644))
	out, err = exec.Command("/bin/sh", "-c", startup.Command).CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "no-such-command-sandcast")
	assert.NotContains(t, string(out), "zle")
}
