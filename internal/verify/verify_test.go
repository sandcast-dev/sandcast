package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/plan"
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
	assert.Equal(t, Result{ComponentKey: "broken", OK: false, Status: "missing", Output: ""}, r.Results[0])
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

// sessionPlan is an OpenShell plan whose shell program and components are
// also checked under the conditions of an OpenShell session.
func sessionPlan() *plan.Plan {
	return &plan.Plan{
		Target:     "openshell",
		FinalUser:  plan.FinalUser{Name: "sandbox", Home: "/sandbox"},
		ImageFacts: plan.ImageFacts{Shells: []string{"/bin/bash", "/bin/sh"}},
		Shell:      plan.Shell{Program: "zsh", Components: []string{"starship"}},
		Components: []plan.Component{
			{Key: "go", Bins: []string{"go"}, Check: plan.Check{Command: "go version"}},
			{Key: "starship", Bins: []string{"starship"}, Check: plan.Check{Command: "starship --version"}},
			{Key: "zsh", Bins: []string{"zsh"}, Check: plan.Check{Command: "zsh --version"}},
		},
	}
}

func TestRequestsAddSessionChecks(t *testing.T) {
	reqs := Requests(sessionPlan())
	keys := []string{}
	byKey := map[string]string{}
	for _, r := range reqs {
		keys = append(keys, r.Key)
		byKey[r.Key] = r.Command
	}
	assert.Equal(t, []string{"go", "starship", "starship.session", "zsh", "zsh.session"}, keys)
	assert.Equal(t, "env -i HOME=/sandbox USER=sandbox TERM=dumb PATH=/usr/local/bin:/usr/bin:/bin /bin/zsh -lc 'starship --version'", byKey["starship.session"])
	assert.Contains(t, byKey["zsh.session"], "grep -qs SANDCAST_NO_ZSH /etc/bash.bashrc /etc/bashrc", "the bash to zsh switch is installed")
	assert.Contains(t, byKey["zsh.session"], "/bin/zsh -lc 'zsh --version'")
}

func TestRequestsSessionShellFollowsOpenShell(t *testing.T) {
	p := sessionPlan()
	p.Shell.Program = "bash"
	p.Components[2] = plan.Component{Key: "bash", Bins: []string{"bash"}, Check: plan.Check{Command: "bash --version"}}
	byKey := map[string]string{}
	for _, r := range Requests(p) {
		byKey[r.Key] = r.Command
	}
	assert.Contains(t, byKey["starship.session"], "/bin/bash -lc 'starship --version'", "OpenShell starts bash")
	assert.Contains(t, byKey["bash.session"], "/bin/bash -lc 'bash --version'")
	assert.NotContains(t, byKey["bash.session"], "SANDCAST_NO_ZSH")

	p.ImageFacts.Shells = []string{"/bin/sh"}
	p.Shell.Program = "none"
	p.Components = p.Components[:2]
	byKey = map[string]string{}
	for _, r := range Requests(p) {
		byKey[r.Key] = r.Command
	}
	assert.Contains(t, byKey["starship.session"], "/bin/sh -lc 'starship --version'")
}

func TestRequestsNoSessionChecksForContainerTarget(t *testing.T) {
	p := sessionPlan()
	p.Target = "container"
	for _, r := range Requests(p) {
		assert.False(t, strings.HasSuffix(r.Key, SessionSuffix), "the container target keeps the image ENV: %s", r.Key)
	}
}

func TestRunReportsSessionResults(t *testing.T) {
	lines := []string{
		`{"type":"check","key":"go","exit":0,"path":"/x","out":"go version go1.25.1"}`,
		`{"type":"check","key":"starship","exit":0,"path":"/x","out":"starship 1.26.0"}`,
		`{"type":"check","key":"starship.session","exit":127,"path":"","out":"zsh:1: command not found: starship"}`,
		`{"type":"check","key":"zsh","exit":0,"path":"/x","out":"zsh 5.9"}`,
		`{"type":"check","key":"zsh.session","exit":0,"path":"","out":"zsh 5.9"}`,
	}
	eng := &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) {
		return []byte(strings.Join(lines, "\n") + "\n"), nil, nil
	}}
	r, err := Run(context.Background(), eng, "img", "", sessionPlan())
	require.ErrorIs(t, err, ErrChecksFailed)
	assert.Contains(t, err.Error(), "starship (session)")
	require.Len(t, r.Results, 5)
	var session []Result
	for _, res := range r.Results {
		if res.Mode == ModeSession {
			session = append(session, res)
		}
	}
	require.Len(t, session, 2)
	assert.Equal(t, "starship", session[0].ComponentKey, "the component key stays the plain key")
	assert.False(t, session[0].OK)
	assert.Equal(t, "zsh", session[1].ComponentKey)
	assert.True(t, session[1].OK)
	assert.Equal(t, 4, r.Passed)
	assert.Equal(t, 1, r.Failed)
	assert.Empty(t, r.Results[0].Mode, "image checks have no mode")
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
