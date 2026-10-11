package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/engine"
)

func TestScriptBoundsEachCheck(t *testing.T) {
	s := Script([]Request{{Key: "go", Bin: "go", Command: "go version"}}, ScriptOptions{})
	assert.Contains(t, s, "_tmax="+strconv.Itoa(int(CheckTimeout/time.Second))+"\n")
	assert.Contains(t, s, `timeout "$_tmax" /bin/sh -c "$1"`, "each check runs under timeout(1) when the image has one")
	assert.Contains(t, s, `head -c "$_max"; cat >/dev/null;`, "each check's output is bounded before it reaches a variable")
	assert.Contains(t, s, `"timeout":%s`, "the check line says whether timeout(1) stopped the check")
	assert.Contains(t, s, "_check 'go' 'go' 'go version'\n", "the check call format is unchanged")
}

// TestScriptReportsTimedOutCheck runs the script on the host with a one
// second limit: a hanging check is stopped and marked, the next check still
// runs.
func TestScriptReportsTimedOutCheck(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("no timeout(1) on the host")
	}
	reqs := []Request{
		{Key: "a-hangs", Command: "echo started; sleep 30"},
		{Key: "b-fails", Command: "echo bye; exit 3"},
	}
	script := strings.Replace(Script(reqs, ScriptOptions{}), "_tmax="+strconv.Itoa(int(CheckTimeout/time.Second))+"\n", "_tmax=1\n", 1)
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_, checks, err := Parse([]byte(runShell(t, sh, script)))
			require.NoError(t, err)
			assert.Less(t, time.Since(start), 20*time.Second, "the hanging check is stopped")
			require.Len(t, checks, 2)
			assert.True(t, checks[0].Timeout, "a-hangs")
			assert.Equal(t, "started", checks[0].Out, "output up to the timeout is kept")
			assert.False(t, checks[1].Timeout, "b-fails")
			assert.Equal(t, 3, checks[1].Exit)
			assert.Equal(t, "bye", checks[1].Out)
		})
	}
}

// TestScriptRunsWithoutTimeout runs the script with a PATH that has no
// timeout(1): the checks run unbounded and are never marked.
func TestScriptRunsWithoutTimeout(t *testing.T) {
	script := Script([]Request{{Key: "plain", Command: "echo hello"}}, ScriptOptions{})
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			_, checks, err := Parse([]byte(runShell(t, sh, script, "PATH="+t.TempDir())))
			require.NoError(t, err)
			require.Len(t, checks, 1)
			assert.False(t, checks[0].Timeout)
			assert.Equal(t, "hello", checks[0].Out)
		})
	}
}

// TestScriptBoundsCapturedOutput runs the script with a head(1) wrapper on
// PATH: every check's output passes through head -c, so a check that prints
// megabytes never lands in a shell variable, and the check keeps its own
// exit status because the rest of its output is drained, not cut off.
func TestScriptBoundsCapturedOutput(t *testing.T) {
	realHead, err := exec.LookPath("head")
	if err != nil {
		t.Skip("no head(1) on the host")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "head.log")
	writeFile(t, filepath.Join(bin, "head"), "#!/bin/sh\necho \"$*\" >> \""+log+"\"\nexec "+realHead+" \"$@\"\n")
	require.NoError(t, os.Chmod(filepath.Join(bin, "head"), 0o755))
	reqs := []Request{
		// About 1 MB, then a distinct exit code: SIGPIPE would turn it into 141.
		{Key: "flood", Command: `i=0; while [ $i -lt 1000 ]; do printf '%01022d\n' $i; i=$((i+1)); done; exit 7`},
		{Key: "short", Command: "echo hello 1.2.3"},
		{Key: "trailing", Command: `printf 'out\n\n\n'; exit 2`},
		{Key: "silent", Command: "exit 4"},
	}
	script := Script(reqs, ScriptOptions{})
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(log, nil, 0o644))
			out := runShell(t, sh, script, "PATH="+bin+":"+os.Getenv("PATH"))
			_, checks, err := Parse([]byte(out))
			require.NoError(t, err)
			require.Len(t, checks, 4)
			byKey := map[string]rawCheck{}
			for _, c := range checks {
				byKey[c.Key] = c
			}
			assert.Equal(t, 7, byKey["flood"].Exit, "the drained check keeps its exit status")
			assert.Len(t, byKey["flood"].Out, MaxOutput)
			assert.True(t, strings.HasPrefix(byKey["flood"].Out, strings.Repeat("0", 1022)+"\n"), "the output starts at the beginning")
			assert.Equal(t, rawCheck{Key: "short", Exit: 0, Out: "hello 1.2.3"}, byKey["short"])
			assert.Equal(t, rawCheck{Key: "trailing", Exit: 2, Out: "out"}, byKey["trailing"], "trailing newlines are dropped as before")
			assert.Equal(t, rawCheck{Key: "silent", Exit: 4}, byKey["silent"])
			assert.Less(t, len(out), 4*MaxOutput+2048, "the probe output stays small")
			calls, err := os.ReadFile(log)
			require.NoError(t, err)
			assert.Equal(t, len(reqs), strings.Count(string(calls), "-c "+strconv.Itoa(MaxOutput)+"\n"), "every check runs through head -c: %q", calls)
		})
	}
}

// TestScriptBoundsFloodUnderTimeout floods output until timeout(1) stops the
// check: the line is still marked as a timeout and keeps the first
// MaxOutput bytes.
func TestScriptBoundsFloodUnderTimeout(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("no timeout(1) on the host")
	}
	if _, err := exec.LookPath("head"); err != nil {
		t.Skip("no head(1) on the host")
	}
	reqs := []Request{{Key: "flood", Command: `while :; do printf '%01022d\n' 1; done`}}
	script := strings.Replace(Script(reqs, ScriptOptions{}), "_tmax="+strconv.Itoa(int(CheckTimeout/time.Second))+"\n", "_tmax=1\n", 1)
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			_, checks, err := Parse([]byte(runShell(t, sh, script)))
			require.NoError(t, err)
			require.Len(t, checks, 1)
			assert.True(t, checks[0].Timeout)
			assert.Contains(t, []int{124, 143}, checks[0].Exit)
			assert.Len(t, checks[0].Out, MaxOutput)
		})
	}
}

// checkLine renders one check line as the probe script prints it.
func checkLine(t *testing.T, c rawCheck) string {
	t.Helper()
	line, err := json.Marshal(struct {
		Type string `json:"type"`
		rawCheck
	}{"check", c})
	require.NoError(t, err)
	return string(line) + "\n"
}

func TestRunChecksReportsTimedOutCheck(t *testing.T) {
	f := &engine.Fake{RunFunc: func(spec engine.RunSpec) ([]byte, []byte, error) {
		return []byte(checkLine(t, rawCheck{Key: "go", Exit: 0, Out: "go version go1.25.1"}) +
			checkLine(t, rawCheck{Key: "node", Exit: 124, Out: "downloading", Timeout: true})), nil, nil
	}}
	got, err := RunChecks(context.Background(), f, "localhost/demo:latest", "", []Request{req("go", ""), req("node", "")})
	require.NoError(t, err)
	assert.Equal(t, StatusPresent, got["go"].Status)
	node := got["node"]
	assert.Equal(t, StatusTimeout, node.Status)
	assert.True(t, strings.HasPrefix(node.Output, "timeout: the check did not finish within "+CheckTimeout.String()), node.Output)
	assert.Contains(t, node.Output, "downloading", "partial output is kept")
	assert.Equal(t, 124, node.Exit)
}

func TestProbeDoesNotCacheTimedOutCheck(t *testing.T) {
	timedOut := true
	f := &engine.Fake{
		Digests: map[string]string{testRef: testDigest},
		RunFunc: func(spec engine.RunSpec) ([]byte, []byte, error) {
			var b strings.Builder
			if strings.Contains(spec.Script, "os-release") {
				b.WriteString(factsOutput)
			}
			for _, key := range scriptKeys(spec.Script) {
				c := rawCheck{Key: key, Exit: 0, Out: key + " 1.0.0"}
				if key == "node" && timedOut {
					c = rawCheck{Key: key, Exit: 124, Timeout: true}
				}
				b.WriteString(checkLine(t, c))
			}
			return []byte(b.String()), nil, nil
		},
	}
	o := opts(t)
	res, err := Probe(context.Background(), f, testRef, testPlatform, []Request{req("go", ""), req("node", "")}, o)
	require.NoError(t, err)
	assert.Equal(t, StatusTimeout, res.Checks["node"].Status)
	assert.Equal(t, StatusPresent, res.Checks["go"].Status)

	// The next probe runs the timed-out check again; go comes from the cache.
	timedOut = false
	res, err = Probe(context.Background(), f, testRef, testPlatform, []Request{req("go", ""), req("node", "")}, o)
	require.NoError(t, err)
	assert.Equal(t, []string{"node"}, res.Probed, "a timeout is not cached")
	assert.Equal(t, StatusPresent, res.Checks["node"].Status)
}

// hangingEngine prints the check lines of the keys in done, then blocks
// until its context ends, like a container whose next check hangs in an
// image without timeout(1).
type hangingEngine struct {
	*engine.Fake
	t    *testing.T
	done []string
}

func (e *hangingEngine) Run(ctx context.Context, spec engine.RunSpec) ([]byte, []byte, error) {
	_, _, _ = e.Fake.Run(ctx, spec)
	var b strings.Builder
	if strings.Contains(spec.Script, "os-release") {
		b.WriteString(factsOutput)
	}
	for _, key := range e.done {
		b.WriteString(checkLine(e.t, rawCheck{Key: key, Exit: 0, Out: key + " 1.0.0"}))
	}
	b.WriteString(`{"type":"check","key":"cut`) // killed in the middle of a line
	<-ctx.Done()
	return []byte(b.String()), nil, fmt.Errorf("signal: terminated")
}

func shortDeadline(t *testing.T) {
	t.Helper()
	saved := runDeadline
	runDeadline = func(int) time.Duration { return 30 * time.Millisecond }
	t.Cleanup(func() { runDeadline = saved })
}

func TestRunChecksDeadlineMarksTheRunningCheck(t *testing.T) {
	shortDeadline(t)
	eng := &hangingEngine{Fake: &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) { return nil, nil, nil }}, t: t, done: []string{"a"}}
	reqs := []Request{req("a", ""), req("b", ""), req("c", "")}
	got, err := RunChecks(context.Background(), eng, "localhost/demo:latest", "", reqs)
	require.NoError(t, err, "a deadline still reports every check")
	assert.Equal(t, StatusPresent, got["a"].Status)
	assert.Equal(t, StatusTimeout, got["b"].Status, "b was running when the container was stopped")
	assert.Contains(t, got["b"].Output, "timeout")
	// c never ran: it is a timeout too, so nobody repairs an unchecked
	// component, and the output says it was not run.
	assert.Equal(t, CheckResult{ComponentKey: "c", CheckHash: reqs[2].Hash, Status: StatusTimeout, Exit: -1,
		Output: "not run: the container was stopped after 30ms while b was running"}, got["c"])
}

func TestRunChecksDeadlineBeforeAnyCheck(t *testing.T) {
	shortDeadline(t)
	eng := &hangingEngine{Fake: &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) { return nil, nil, nil }}, t: t}
	got, err := RunChecks(context.Background(), eng, "localhost/demo:latest", "", []Request{req("a", ""), req("b", "")})
	require.NoError(t, err)
	assert.Equal(t, StatusTimeout, got["a"].Status)
	assert.Equal(t, StatusTimeout, got["b"].Status)
	assert.Equal(t, "not run: the container was stopped after 30ms while a was running", got["b"].Output)
}

func TestProbeNoticesTimedOutCheck(t *testing.T) {
	f := &engine.Fake{
		Digests: map[string]string{testRef: testDigest},
		RunFunc: func(spec engine.RunSpec) ([]byte, []byte, error) {
			var b strings.Builder
			if strings.Contains(spec.Script, "os-release") {
				b.WriteString(factsOutput)
			}
			for _, key := range scriptKeys(spec.Script) {
				c := rawCheck{Key: key, Exit: 0, Out: key + " 1.0.0"}
				if key != "go" {
					c = rawCheck{Key: key, Exit: 124, Timeout: true}
				}
				b.WriteString(checkLine(t, c))
			}
			return []byte(b.String()), nil, nil
		},
	}
	res, err := Probe(context.Background(), f, testRef, testPlatform, []Request{req("node", ""), req("go", ""), req("cargo", "")}, opts(t))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"check of cargo did not finish within 2m0s; treated as missing; it runs again on the next plan",
		"check of node did not finish within 2m0s; treated as missing; it runs again on the next plan",
	}, res.Notices, "one notice per timed-out check, in key order")
}

func TestProbeDeadlineFailsWithTimeout(t *testing.T) {
	shortDeadline(t)
	eng := &hangingEngine{Fake: &engine.Fake{
		Digests: map[string]string{testRef: testDigest},
		RunFunc: func(engine.RunSpec) ([]byte, []byte, error) { return nil, nil, nil },
	}, t: t, done: []string{"a"}}
	_, err := Probe(context.Background(), eng, testRef, testPlatform, []Request{req("a", ""), req("b", "")}, opts(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, engine.ErrRunTimeout)
	assert.Contains(t, err.Error(), "the check of b did not finish")
}

func TestRunDeadlineGrowsWithChecks(t *testing.T) {
	assert.Greater(t, runDeadline(3), 3*CheckTimeout, "every check gets its full limit plus start-up time")
	assert.Greater(t, runDeadline(0), time.Duration(0))
}
