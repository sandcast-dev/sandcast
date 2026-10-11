package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordCmd struct {
	bin    string
	args   [][]string
	stdout string
	stderr string
	err    error
}

func (r *recordCmd) Command(_ context.Context, bin string, args []string, out io.Writer) ([]byte, []byte, error) {
	r.bin = bin
	r.args = append(r.args, args)
	if r.err != nil {
		return nil, []byte(r.stderr), r.err
	}
	if out != nil {
		_, _ = io.WriteString(out, r.stdout)
		return nil, nil, nil
	}
	return []byte(r.stdout), nil, nil
}

// exitCode is an error with an exit code, like *exec.ExitError.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitCode) ExitCode() int { return int(e) }

// initCmd fails every run that has --init with the engine's error for a
// missing init binary and records all calls. exit defaults to 125.
type initCmd struct {
	mu     sync.Mutex
	stderr string
	exit   int
	calls  [][]string
}

func (c *initCmd) Command(_ context.Context, _ string, args []string, _ io.Writer) ([]byte, []byte, error) {
	c.mu.Lock()
	c.calls = append(c.calls, args)
	c.mu.Unlock()
	if slices.Contains(args, "--init") {
		code := c.exit
		if code == 0 {
			code = 125
		}
		return nil, []byte(c.stderr), exitCode(code)
	}
	return []byte("ok\n"), nil, nil
}

func (c *initCmd) withInit() (with, without int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.calls {
		if slices.Contains(a, "--init") {
			with++
		} else {
			without++
		}
	}
	return with, without
}

func TestRunFallsBackWithoutInit(t *testing.T) {
	// podman's create-time error exits 125; the docker CLI maps "executable
	// file not found" to 127, so the fallback must fire on both codes.
	cases := map[string]struct {
		stderr string
		exit   int
	}{
		"podman": {stderr: "Error: container-init binary not found on the host: stat /usr/libexec/podman/catatonit: no such file or directory\n", exit: 125},
		"docker": {stderr: "docker: Error response from daemon: exec: \"docker-init\": executable file not found in $PATH.\n", exit: 127},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := &initCmd{stderr: tc.stderr, exit: tc.exit}
			var notices []string
			var mu sync.Mutex
			e := &Exec{Bin: name, Kind: name, Cmd: cmd, Notice: func(format string, args ...any) {
				mu.Lock()
				defer mu.Unlock()
				notices = append(notices, fmt.Sprintf(format, args...))
			}}
			stdout, _, err := e.Run(context.Background(), RunSpec{Image: "img", Script: "true"})
			require.NoError(t, err, "a missing init binary is not a failure")
			assert.Equal(t, "ok\n", string(stdout))
			with, without := cmd.withInit()
			assert.Equal(t, 1, with)
			assert.Equal(t, 1, without, "the run is retried once without --init")

			// Later runs, also concurrent ones, skip --init at once.
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _, err := e.Run(context.Background(), RunSpec{Image: "img"})
					assert.NoError(t, err)
				}()
			}
			wg.Wait()
			with, without = cmd.withInit()
			assert.Equal(t, 1, with, "--init is tried only once per Exec")
			assert.Equal(t, 9, without)
			require.Len(t, notices, 1, "one notice per Exec")
			assert.Contains(t, notices[0], "without --init")
		})
	}
}

func TestRunKeepsOtherFailures(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		stderr string
	}{
		{name: "other engine error", err: exitCode(125), stderr: "Error: manifest unknown\n"},
		{name: "container exit", err: exitCode(1), stderr: "catatonit\n"},
		// A real "command not found" inside the container also exits 127, but
		// carries no init marker, so it must not trigger the --init retry.
		{name: "command not found in container", err: exitCode(127), stderr: "sh: nope: command not found\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recordCmd{err: tt.err, stderr: tt.stderr}
			e := &Exec{Bin: "podman", Kind: Podman, Cmd: rec}
			_, _, err := e.Run(context.Background(), RunSpec{Image: "img"})
			require.Error(t, err)
			assert.Len(t, rec.args, 1, "no retry")
			assert.Contains(t, rec.args[0], "--init")
		})
	}
}

func TestRunNoticeFallsBackToTrace(t *testing.T) {
	var traced []string
	e := &Exec{Bin: "podman", Kind: Podman, Cmd: &initCmd{stderr: "Error: container-init binary not found on the host\n"},
		Trace: func(format string, args ...any) { traced = append(traced, fmt.Sprintf(format, args...)) }}
	_, _, err := e.Run(context.Background(), RunSpec{Image: "img"})
	require.NoError(t, err)
	assert.True(t, slices.ContainsFunc(traced, func(s string) bool { return strings.Contains(s, "without --init") }), traced)
}

func TestPullArgs(t *testing.T) {
	assert.Equal(t, []string{"pull", "--platform", "linux/arm64", "x:1"}, pullArgs("x:1", "linux/arm64"))
	assert.Equal(t, []string{"pull", "x:1"}, pullArgs("x:1", ""))
}

func TestRunArgs(t *testing.T) {
	spec := RunSpec{
		Image: "img", Platform: "linux/amd64", User: "sandbox", Script: "echo hi",
		Env: map[string]string{"B": "2", "A": "1"},
	}
	assert.Equal(t, []string{
		"run", "--rm", "--init", "--platform", "linux/amd64", "--entrypoint", "/bin/sh", "--user", "sandbox",
		"--env", "A=1", "--env", "B=2", "img", "-c", "echo hi",
	}, runArgs(spec, true), "--init makes a forwarded SIGTERM stop the script, which as PID 1 would ignore it")
	assert.Equal(t, []string{
		"run", "--rm", "--platform", "linux/amd64", "--entrypoint", "/bin/sh", "--user", "sandbox",
		"--env", "A=1", "--env", "B=2", "img", "-c", "echo hi",
	}, runArgs(spec, false))
}

// blockingEngine runs until its context ends, like a container whose check
// hangs.
type blockingEngine struct{ *Fake }

func (e *blockingEngine) Run(ctx context.Context, spec RunSpec) ([]byte, []byte, error) {
	<-ctx.Done()
	return []byte("partial\n"), nil, fmt.Errorf("signal: terminated")
}

func TestRunWithinStopsAHangingContainer(t *testing.T) {
	eng := &blockingEngine{Fake: &Fake{}}
	stdout, _, err := RunWithin(context.Background(), eng, RunSpec{Image: "img"}, 20*time.Millisecond)
	require.ErrorIs(t, err, ErrRunTimeout)
	assert.Contains(t, err.Error(), "20ms")
	assert.Equal(t, "partial\n", string(stdout), "the output up to the deadline is kept")

	// A cancelled parent is an interrupt, not a timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = RunWithin(ctx, eng, RunSpec{Image: "img"}, time.Hour)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRunTimeout)

	// A run that finishes in time is passed through.
	f := &Fake{RunFunc: func(RunSpec) ([]byte, []byte, error) { return []byte("ok"), nil, nil }}
	stdout, _, err = RunWithin(context.Background(), f, RunSpec{Image: "img"}, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, "ok", string(stdout))
}

func TestBuildArgs(t *testing.T) {
	got := buildArgs(BuildSpec{
		ContextDir: "/ctx", Containerfile: "/ctx/Containerfile", Tags: []string{"a:1", "a:stage"},
		Platform: "linux/arm64", Labels: map[string]string{"z": "1", "a": "2"}, NoCache: true,
	})
	assert.Equal(t, []string{
		"build", "--platform", "linux/arm64", "-f", "/ctx/Containerfile", "-t", "a:1", "-t", "a:stage",
		"--label", "a=2", "--label", "z=1", "--no-cache", "/ctx",
	}, got)
}

func TestDigestArgsAndParse(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	assert.Equal(t, []string{"image", "inspect", "--format", "{{.Digest}}", "r"}, digestArgs(Podman, "r"))
	assert.Equal(t, []string{"image", "inspect", "--format", "{{json .RepoDigests}}", "r"}, digestArgs(Docker, "r"))

	got, err := parseDigest(Podman, []byte(d+"\n"))
	require.NoError(t, err)
	assert.Equal(t, d, got)

	got, err = parseDigest(Docker, []byte(`["repo@`+d+`"]`))
	require.NoError(t, err)
	assert.Equal(t, d, got)

	_, err = parseDigest(Podman, []byte("\n"))
	assert.ErrorIs(t, err, ErrNoDigest)
	_, err = parseDigest(Docker, []byte(`[]`))
	assert.ErrorIs(t, err, ErrNoDigest)
}

func TestExecUsesCommander(t *testing.T) {
	rec := &recordCmd{stdout: `{"io.sandcast.version":"dev"}`}
	e := &Exec{Bin: "podman", Kind: Podman, Cmd: rec}
	labels, err := e.Labels(context.Background(), "img")
	require.NoError(t, err)
	assert.Equal(t, "dev", labels["io.sandcast.version"])
	assert.Equal(t, "podman", rec.bin)

	var log strings.Builder
	require.NoError(t, e.Build(context.Background(), BuildSpec{ContextDir: "/c", Containerfile: "/c/F", Log: &log}))
	assert.Equal(t, rec.stdout, log.String())
	require.NoError(t, e.Tag(context.Background(), "a", "b"))
	assert.Equal(t, []string{"tag", "a", "b"}, rec.args[len(rec.args)-1])
}

func TestExecTracesCommandLines(t *testing.T) {
	var traced []string
	e := &Exec{Bin: "podman", Kind: Podman, Cmd: &recordCmd{}, Trace: func(format string, args ...any) {
		traced = append(traced, fmt.Sprintf(format, args...))
	}}
	require.NoError(t, e.Pull(context.Background(), "img", "linux/arm64"))
	require.NoError(t, e.Build(context.Background(), BuildSpec{ContextDir: "/c", Containerfile: "/c/F"}))
	require.Len(t, traced, 2)
	assert.Equal(t, "+ podman pull --platform linux/arm64 img", traced[0])
	assert.True(t, strings.HasPrefix(traced[1], "+ podman build "), traced[1])
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{Digests: map[string]string{"img": "sha256:x"}}
	ctx := context.Background()
	require.NoError(t, f.Pull(ctx, "img", "linux/arm64"))
	d, err := f.Digest(ctx, "img")
	require.NoError(t, err)
	assert.Equal(t, "sha256:x", d)
	_, err = f.Digest(ctx, "other")
	assert.ErrorIs(t, err, ErrNoDigest)
	require.NoError(t, f.Build(ctx, BuildSpec{Tags: []string{"out:1"}, Labels: map[string]string{"k": "v"}}))
	ok, err := f.ImageExists(ctx, "out:1")
	require.NoError(t, err)
	assert.True(t, ok)
	labels, err := f.Labels(ctx, "out:1")
	require.NoError(t, err)
	assert.Equal(t, "v", labels["k"])
	assert.Equal(t, 1, f.CountOp("pull"))
	assert.Equal(t, 1, f.CountOp("build"))
}

func TestWithStderr(t *testing.T) {
	base := errors.New("exit status 1")
	assert.Nil(t, WithStderr(nil, []byte("x")))
	assert.Equal(t, base, WithStderr(base, []byte("  \n")))
	got := WithStderr(base, []byte("boom\n"))
	assert.EqualError(t, got, "exit status 1: boom")
	assert.ErrorIs(t, got, base)
	already := fmt.Errorf("podman run: %w: boom", base)
	assert.Equal(t, already, WithStderr(already, []byte("boom")), "stderr is not repeated")
}

func TestExecErrorsCarryStderrOnce(t *testing.T) {
	e := &Exec{Bin: "podman", Kind: Podman, Cmd: &recordCmd{stderr: "Error: manifest unknown\n", err: errors.New("exit status 125")}}
	err := e.Pull(context.Background(), "img", "")
	require.Error(t, err)
	assert.Equal(t, "podman pull: exit status 125: Error: manifest unknown", err.Error())

	e = &Exec{Bin: "podman", Kind: Podman, Cmd: &recordCmd{err: errors.New("exit status 125")}}
	assert.EqualError(t, e.Pull(context.Background(), "img", ""), "podman pull: exit status 125", "no dangling separator without stderr")
}
