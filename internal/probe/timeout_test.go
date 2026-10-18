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
	assert.Contains(t, s, `timeout "$_tmax" /bin/sh -c "$1"`, "a check runs under a coreutils timeout(1) when the image has one")
	assert.Contains(t, s, `head -c "$_max"; cat >/dev/null;`, "each check's output is bounded before it reaches a variable")
	assert.Contains(t, s, `"timeout":%s`, "the check line says whether the check was stopped at its time limit")
	assert.Contains(t, s, "_check 'go' 'go' 'go version'\n", "the check call format is unchanged")
}

// TestScriptReportsTimedOutCheck runs the script on the host with a one
// second limit and the host's timeout(1), or under busybox its own applets:
// a hanging check is stopped and marked, also when the process that hangs is
// a child the check's shell waits for, or when the check traps SIGTERM and
// exits with its own status. A child that a finished check leaves behind,
// even one that ignores SIGTERM, is killed and does not hold the output
// open. The next check still runs.
func TestScriptReportsTimedOutCheck(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("no timeout(1) on the host")
	}
	reqs := []Request{
		{Key: "a-hangs", Command: "echo started; sleep 30"},
		{Key: "b-child-hangs", Command: "sleep 30 & echo started; wait"},
		{Key: "c-traps-term", Command: "trap 'echo got; exit 1' TERM; echo armed; sleep 30 & wait"},
		{Key: "d-leaves-child", Command: "sleep 30 & echo started"},
		{Key: "e-leaves-term-proof-child", Command: "trap '' TERM; sleep 30 & echo started"},
		{Key: "f-fails", Command: "echo bye; exit 3"},
	}
	script := withTimeLimit(Script(reqs, ScriptOptions{}), 1)
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_, checks, err := Parse([]byte(runShell(t, sh, script)))
			require.NoError(t, err)
			assert.Less(t, time.Since(start), 20*time.Second, "every check ends within its limit")
			byKey := checksByKey(t, checks, len(reqs))
			for _, key := range []string{"a-hangs", "b-child-hangs"} {
				assert.True(t, byKey[key].Timeout, key)
				assert.Equal(t, "started", byKey[key].Out, "%s: output up to the timeout is kept", key)
			}
			assert.True(t, byKey["c-traps-term"].Timeout, "a check that traps SIGTERM is still marked")
			assert.Equal(t, "armed\ngot", byKey["c-traps-term"].Out, "the check's TERM trap ran")
			for _, key := range []string{"d-leaves-child", "e-leaves-term-proof-child"} {
				assert.Equal(t, rawCheck{Key: key, Exit: 0, Out: "started"}, byKey[key], "%s: the leftover child is killed", key)
			}
			assert.Equal(t, rawCheck{Key: "f-fails", Exit: 3, Out: "bye"}, byKey["f-fails"])
		})
	}
}

// TestScriptWatchdogBranch forces the setsid watchdog that the script picks
// when timeout(1) is missing or does not stop a whole process group
// (busybox): a fake timeout(1) that rejects --version, and setsid(1) from
// the host or a perl stand-in. On the GitHub Ubuntu runner only the busybox
// subtests reach this branch through busybox's own applets; this test runs it
// on every host.
func TestScriptWatchdogBranch(t *testing.T) {
	tools := newStandInTools(t)
	sh := pathResolvingShells(t)
	flags := `printf '[%s][%s]' "$_to" "$_sg"`

	t.Run("selection", func(t *testing.T) {
		for name, argv := range sh {
			assert.Equal(t, "[1][1]", runShell(t, argv, helpersPrelude()+flags, "PATH="+tools.dir(t, "timeout", "setsid", "sleep")),
				"%s: busybox-like timeout(1) and setsid(1) select the watchdog", name)
			assert.Equal(t, "[][1]", runShell(t, argv, helpersPrelude()+flags, "PATH="+tools.dir(t, "setsid", "sleep")),
				"%s: the watchdog does not need timeout(1)", name)
			assert.Equal(t, "[1][]", runShell(t, argv, helpersPrelude()+flags, "PATH="+tools.dir(t, "timeout", "sleep")),
				"%s: no setsid(1), no watchdog", name)
			assert.Equal(t, "[1][]", runShell(t, argv, helpersPrelude()+flags, "PATH="+tools.dir(t, "timeout", "setsid")),
				"%s: no sleep(1), no watchdog that could never fire", name)
			if _, ok := tools.tools["coreutils-timeout"]; ok {
				assert.Equal(t, "[1][]", runShell(t, argv, helpersPrelude()+flags, "PATH="+tools.dir(t, "timeout=coreutils-timeout", "setsid", "sleep")),
					"%s: a coreutils timeout(1) keeps the timeout path although setsid(1) exists", name)
			}
		}
		if _, ok := tools.tools["coreutils-timeout"]; !ok {
			t.Log("coreutils selection case skipped: the host timeout(1) is not coreutils")
		}
	})

	reqs := []Request{
		{Key: "a-hangs", Command: "echo started; sleep 30"},
		{Key: "b-child-hangs", Command: "sleep 30 & echo started; wait"},
		{Key: "c-traps-term", Command: "trap 'echo got; exit 1' TERM; echo armed; sleep 30 & wait"},
		{Key: "d-leaves-child", Command: "sleep 30 & echo started"},
		{Key: "e-leaves-term-proof-child", Command: "trap '' TERM; sleep 30 & echo started"},
		{Key: "f-fails", Command: "echo bye; exit 3"},
	}
	script := withTimeLimit(Script(reqs, ScriptOptions{}), 1)
	for _, path := range []struct{ name, dir string }{
		{"with-timeout", tools.dir(t, "timeout", "setsid", "sleep", "head", "cat")},
		{"without-timeout", tools.dir(t, "setsid", "sleep", "head", "cat")},
	} {
		for name, argv := range sh {
			t.Run(path.name+"/"+name, func(t *testing.T) {
				start := time.Now()
				_, checks, err := Parse([]byte(runShell(t, argv, script, "PATH="+path.dir)))
				require.NoError(t, err)
				assert.Less(t, time.Since(start), 15*time.Second, "every check ends within its limit")
				byKey := checksByKey(t, checks, len(reqs))
				for key, out := range map[string]string{"a-hangs": "started", "b-child-hangs": "started", "c-traps-term": "armed\ngot"} {
					assert.Equal(t, rawCheck{Key: key, Exit: 143, Out: out, Timeout: true}, byKey[key], "%s is stopped and marked", key)
				}
				for _, key := range []string{"d-leaves-child", "e-leaves-term-proof-child"} {
					assert.Equal(t, rawCheck{Key: key, Exit: 0, Out: "started"}, byKey[key],
						"%s: a child left behind by a finished check is killed and does not hold the output open", key)
				}
				assert.Equal(t, rawCheck{Key: "f-fails", Exit: 3, Out: "bye"}, byKey["f-fails"])
			})
		}
	}
}

// helpersPrelude is the start of a probe script up to its helper functions,
// with a one second limit, for tests that inspect the script's choices.
func helpersPrelude() string {
	return "_max=" + strconv.Itoa(MaxOutput) + "\n_tmax=1\n" + scriptHelpers
}

// withTimeLimit sets the script's per-check limit to secs seconds.
func withTimeLimit(script string, secs int) string {
	return strings.Replace(script, "_tmax="+strconv.Itoa(int(CheckTimeout/time.Second))+"\n", "_tmax="+strconv.Itoa(secs)+"\n", 1)
}

// checksByKey indexes the parsed check lines by key and requires n of them.
func checksByKey(t *testing.T, checks []rawCheck, n int) map[string]rawCheck {
	t.Helper()
	require.Len(t, checks, n)
	byKey := map[string]rawCheck{}
	for _, c := range checks {
		byKey[c.Key] = c
	}
	return byKey
}

// pathResolvingShells returns the host shells that look commands up through
// PATH. A busybox shell that prefers its own applets would bypass the
// stand-in tools of TestScriptWatchdogBranch and the PATH of
// TestScriptRunsWithoutTimeout.
func pathResolvingShells(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for name, argv := range shells(t) {
		if onPath(t, argv, "sleep") {
			out[name] = argv
		}
	}
	if len(out) == 0 {
		t.Skip("no host shell looks commands up through PATH")
	}
	return out
}

// standInTools holds the tools for TestScriptWatchdogBranch by name, and
// builds PATH directories that contain exactly the named ones.
type standInTools struct {
	tools map[string]string
}

func newStandInTools(t *testing.T) standInTools {
	t.Helper()
	bin := t.TempDir()
	tools := map[string]string{}
	// busybox-like timeout(1): accepts "timeout SECS CMD", rejects --version.
	// The watchdog branch never uses it to stop a check.
	tools["timeout"] = filepath.Join(bin, "timeout")
	writeFile(t, tools["timeout"], "#!/bin/sh\ncase $1 in --version) exit 1 ;; esac\nshift\nexec \"$@\"\n")
	require.NoError(t, os.Chmod(tools["timeout"], 0o755))
	if p, err := exec.LookPath("timeout"); err == nil && exec.Command(p, "--version").Run() == nil {
		tools["coreutils-timeout"] = p
	}
	if p, err := exec.LookPath("setsid"); err == nil {
		tools["setsid"] = p
	} else if perl, err := exec.LookPath("perl"); err == nil {
		// setsid(1) without a fork, as util-linux and busybox behave for a
		// process that is not a group leader.
		tools["setsid"] = filepath.Join(bin, "setsid")
		writeFile(t, tools["setsid"], "#!"+perl+"\nuse POSIX ();\nPOSIX::setsid() or die \"setsid: $!\\n\";\nexec { $ARGV[0] } @ARGV or die \"setsid: $ARGV[0]: $!\\n\";\n")
		require.NoError(t, os.Chmod(tools["setsid"], 0o755))
	} else {
		t.Skip("neither setsid(1) nor perl on the host")
	}
	for _, name := range []string{"sleep", "head", "cat"} {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("no %s(1) on the host", name)
		}
		tools[name] = p
	}
	return standInTools{tools: tools}
}

// dir returns a new directory with a link to each named tool, for use as
// the only PATH entry. A name of the form link=tool links the tool under
// another name.
func (s standInTools) dir(t *testing.T, names ...string) string {
	t.Helper()
	d := t.TempDir()
	for _, n := range names {
		link, tool, ok := strings.Cut(n, "=")
		if !ok {
			tool = link
		}
		require.NoError(t, os.Symlink(s.tools[tool], filepath.Join(d, link)))
	}
	return d
}

// TestScriptRunsWithoutTimeout runs the script with a PATH that has neither
// timeout(1) nor setsid(1): the checks run unbounded and are never marked,
// not even when a check exits 124 or 143 by itself.
func TestScriptRunsWithoutTimeout(t *testing.T) {
	reqs := []Request{
		{Key: "plain", Command: "echo hello"},
		{Key: "exits-124", Command: "exit 124"},
		{Key: "exits-143", Command: "exit 143"},
	}
	script := Script(reqs, ScriptOptions{})
	pathShells := pathResolvingShells(t)
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			_, checks, err := Parse([]byte(runShell(t, sh, script, "PATH="+t.TempDir())))
			require.NoError(t, err)
			byKey := checksByKey(t, checks, len(reqs))
			assert.Equal(t, rawCheck{Key: "plain", Out: "hello"}, byKey["plain"])
			if _, ok := pathShells[name]; !ok {
				// busybox's own timeout applet still bounds and marks checks.
				return
			}
			assert.Equal(t, rawCheck{Key: "exits-124", Exit: 124}, byKey["exits-124"], "without a time limit 124 is no timeout")
			assert.Equal(t, rawCheck{Key: "exits-143", Exit: 143}, byKey["exits-143"], "without a time limit 143 is no timeout")
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
			byKey := checksByKey(t, checks, len(reqs))
			assert.Equal(t, 7, byKey["flood"].Exit, "the drained check keeps its exit status")
			assert.Len(t, byKey["flood"].Out, MaxOutput)
			assert.True(t, strings.HasPrefix(byKey["flood"].Out, strings.Repeat("0", 1022)+"\n"), "the output starts at the beginning")
			assert.Equal(t, rawCheck{Key: "short", Exit: 0, Out: "hello 1.2.3"}, byKey["short"])
			assert.Equal(t, rawCheck{Key: "trailing", Exit: 2, Out: "out"}, byKey["trailing"], "trailing newlines are dropped as before")
			assert.Equal(t, rawCheck{Key: "silent", Exit: 4}, byKey["silent"])
			assert.Less(t, len(out), 4*MaxOutput+2048, "the probe output stays small")
			if !onPath(t, sh, "head") {
				// busybox built with the standalone shell runs its own head
				// applet and never reaches the wrapper. The bounds above
				// still hold; check that the script chose the head -c path.
				assert.Equal(t, "[1]", runShell(t, sh, helpersPrelude()+`printf '[%s]' "$_hd"`),
					"the shell's own head applet selects the head -c path")
				t.Log("head call count skipped: the shell runs its own head applet")
				return
			}
			calls, err := os.ReadFile(log)
			require.NoError(t, err)
			assert.Equal(t, len(reqs), strings.Count(string(calls), "-c "+strconv.Itoa(MaxOutput)+"\n"), "every check runs through head -c: %q", calls)
		})
	}
}

// TestScriptBoundsFloodUnderTimeout floods output until the time limit stops
// the check: the line is still marked as a timeout and keeps the first
// MaxOutput bytes.
func TestScriptBoundsFloodUnderTimeout(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("no timeout(1) on the host")
	}
	if _, err := exec.LookPath("head"); err != nil {
		t.Skip("no head(1) on the host")
	}
	reqs := []Request{{Key: "flood", Command: `while :; do printf '%01022d\n' 1; done`}}
	script := withTimeLimit(Script(reqs, ScriptOptions{}), 1)
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
// image that has neither timeout(1) nor the tools for the script's watchdog.
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

// onPath reports whether the shell resolves cmd through PATH; a busybox
// standalone shell prefers its own applet and prints the bare name.
func onPath(t *testing.T, sh []string, cmd string) bool {
	t.Helper()
	out := runShell(t, sh, "command -v "+cmd)
	return strings.HasPrefix(strings.TrimSpace(out), "/")
}
