package probe

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/engine"
)

const (
	testRef      = "quay.io/example/base:1"
	testPlatform = "linux/arm64"
)

var (
	testDigest = "sha256:" + strings.Repeat("ab", 32)
	testNow    = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
)

// image answers probe runs like a base image: the facts of factsOutput and
// one check line per _check call in the script, from checks.
type image struct {
	checks map[string]rawCheck
	exit   error
	stderr string
	broken bool
}

func (im *image) run(spec engine.RunSpec) ([]byte, []byte, error) {
	if im.broken {
		return []byte("garbage\n"), []byte(im.stderr), im.exit
	}
	var b strings.Builder
	if strings.Contains(spec.Script, "os-release") {
		b.WriteString(factsOutput)
	}
	for _, key := range scriptKeys(spec.Script) {
		c := im.checks[key]
		c.Key = key
		line, err := json.Marshal(struct {
			Type string `json:"type"`
			rawCheck
		}{"check", c})
		if err != nil {
			return nil, nil, err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return []byte(b.String()), []byte(im.stderr), im.exit
}

// scriptKeys returns the component keys of the _check calls in script.
func scriptKeys(script string) []string {
	var keys []string
	for line := range strings.SplitSeq(script, "\n") {
		if rest, ok := strings.CutPrefix(line, "_check '"); ok {
			keys = append(keys, rest[:strings.IndexByte(rest, '\'')])
		}
	}
	return keys
}

func newFake(im *image) *engine.Fake {
	return &engine.Fake{
		Digests: map[string]string{testRef: testDigest},
		RunFunc: im.run,
	}
}

func baseImage() *image {
	return &image{checks: map[string]rawCheck{
		"go":   {Exit: 0, Path: "/usr/local/go/bin/go", Out: "/usr/local/go/bin/go\ngo version go1.24.3 linux/arm64"},
		"git":  {Exit: 0, Path: "/usr/bin/git", Out: "git version 2.47.3"},
		"node": {Exit: 127, Out: "sh: 1: node: not found"},
	}}
}

func req(key, selector string) Request {
	c := &component.Component{
		Key:       key,
		Installer: component.Installer{Mise: &component.MiseInstaller{Tool: key, Version: selector}},
		Check:     component.Check{Command: "command -v " + key + " && " + key + " --version"},
	}
	return RequestFor(c, "")
}

func opts(t *testing.T) Options {
	t.Helper()
	return Options{Cache: &Cache{Dir: t.TempDir()}, Now: func() time.Time { return testNow }}
}

func lastScript(t *testing.T, f *engine.Fake) string {
	t.Helper()
	calls := f.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Op == "run" {
			return calls[i].Run.Script
		}
	}
	t.Fatal("no run call")
	return ""
}

func TestProbeFirstRunThenCached(t *testing.T) {
	ctx := context.Background()
	f := newFake(baseImage())
	o := opts(t)
	reqs := []Request{req("node", "22"), req("go", "1.25"), req("git", "")}

	first, err := Probe(ctx, f, testRef, testPlatform, reqs, o)
	require.NoError(t, err)
	assert.True(t, first.ContainerStarted)
	assert.False(t, first.FactsFromCache)
	assert.Equal(t, []string{"git", "go", "node"}, first.Probed)
	assert.Empty(t, first.Notices)
	assert.Equal(t, 1, f.CountOp("run"))
	script := lastScript(t, f)
	assert.Contains(t, script, "os-release")
	assert.Equal(t, []string{"git", "go", "node"}, scriptKeys(script), "sorted by key")
	run := f.Calls()[len(f.Calls())-1].Run
	assert.Equal(t, engine.RunSpec{Image: testRef, Platform: testPlatform, Entrypoint: "/bin/sh", Script: script}, *run)

	assert.Equal(t, Facts{
		Digest: testDigest, Reference: testRef, Platform: testPlatform,
		OS:             OS{ID: "debian", Name: "Debian GNU/Linux", Version: "13"},
		PackageManager: "apt-get",
		User:           User{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/home/sandbox", Shell: "/bin/bash"},
		Shells:         []string{"/bin/sh", "/bin/bash", "/usr/bin/bash"},
		ProbedAt:       "2026-10-08T12:00:00Z",
	}, first.Facts)
	assert.Equal(t, CheckResult{
		ComponentKey: "go", CheckHash: reqs[1].Hash, Status: StatusIncompatible,
		Path: "/usr/local/go/bin/go", Version: "1.24.3", Output: "/usr/local/go/bin/go\ngo version go1.24.3 linux/arm64",
	}, first.Checks["go"])
	assert.Equal(t, StatusPresent, first.Checks["git"].Status)
	assert.Equal(t, "2.47.3", first.Checks["git"].Version)
	assert.Equal(t, StatusMissing, first.Checks["node"].Status)
	assert.Equal(t, 127, first.Checks["node"].Exit)

	// SC-004: the same request set on the same image starts no container.
	second, err := Probe(ctx, f, testRef, testPlatform, reqs, Options{Cache: o.Cache, Now: func() time.Time { return testNow.Add(time.Hour) }})
	require.NoError(t, err)
	assert.Equal(t, 1, f.CountOp("run"))
	assert.False(t, second.ContainerStarted)
	assert.True(t, second.FactsFromCache)
	assert.Empty(t, second.Probed)
	assert.Equal(t, first.Facts, second.Facts)
	assert.Equal(t, first.Checks, second.Checks)
}

func TestProbePartial(t *testing.T) {
	ctx := context.Background()
	f := newFake(baseImage())
	o := opts(t)
	_, err := Probe(ctx, f, testRef, testPlatform, []Request{req("go", "")}, o)
	require.NoError(t, err)

	tests := []struct {
		name   string
		reqs   []Request
		probed []string
		status map[string]Status
	}{
		{
			name:   "added component",
			reqs:   []Request{req("go", ""), req("node", "")},
			probed: []string{"node"},
			status: map[string]Status{"go": StatusPresent, "node": StatusMissing},
		},
		{
			name:   "changed selector",
			reqs:   []Request{req("go", "1.25"), req("node", "")},
			probed: []string{"go"},
			status: map[string]Status{"go": StatusIncompatible, "node": StatusMissing},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := f.CountOp("run")
			res, err := Probe(ctx, f, testRef, testPlatform, tt.reqs, o)
			require.NoError(t, err)
			assert.Equal(t, runs+1, f.CountOp("run"))
			assert.True(t, res.ContainerStarted)
			assert.True(t, res.FactsFromCache)
			assert.Equal(t, tt.probed, res.Probed)
			script := lastScript(t, f)
			assert.Equal(t, tt.probed, scriptKeys(script), "only the missing checks run")
			assert.NotContains(t, script, "os-release", "no facts sections in a partial probe")
			assert.NotContains(t, script, `"type":"user"`)
			for key, st := range tt.status {
				assert.Equal(t, st, res.Checks[key].Status, key)
			}
		})
	}
}

func TestProbeNoCache(t *testing.T) {
	ctx := context.Background()
	f := newFake(baseImage())
	o := opts(t)
	reqs := []Request{req("go", ""), req("git", "")}
	_, err := Probe(ctx, f, testRef, testPlatform, reqs, o)
	require.NoError(t, err)

	o.NoCache = true
	res, err := Probe(ctx, f, testRef, testPlatform, reqs, o)
	require.NoError(t, err)
	assert.Equal(t, 2, f.CountOp("run"))
	assert.False(t, res.FactsFromCache)
	assert.Equal(t, []string{"git", "go"}, res.Probed)
	script := lastScript(t, f)
	assert.Contains(t, script, "os-release")
	assert.Equal(t, []string{"git", "go"}, scriptKeys(script))
}

func TestProbeFactsOnly(t *testing.T) {
	ctx := context.Background()
	f := newFake(baseImage())
	o := opts(t)
	reqs := []Request{req("go", "")}
	_, err := Probe(ctx, f, testRef, testPlatform, reqs, o)
	require.NoError(t, err)
	factsFile, err := o.Cache.factsPath(testDigest, testPlatform)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(factsFile, []byte("{corrupt"), 0o644))

	res, err := Probe(ctx, f, testRef, testPlatform, reqs, o)
	require.NoError(t, err)
	assert.True(t, res.ContainerStarted)
	assert.False(t, res.FactsFromCache)
	assert.Empty(t, res.Probed)
	script := lastScript(t, f)
	assert.Contains(t, script, "os-release")
	assert.Empty(t, scriptKeys(script))
	assert.Equal(t, StatusPresent, res.Checks["go"].Status)
}

func TestProbePlatformsAreSeparate(t *testing.T) {
	ctx := context.Background()
	f := newFake(baseImage())
	o := opts(t)
	reqs := []Request{req("go", "")}
	for _, p := range []string{"linux/arm64", "linux/amd64", "linux/arm64", "linux/amd64"} {
		res, err := Probe(ctx, f, testRef, p, reqs, o)
		require.NoError(t, err)
		assert.Equal(t, p, res.Facts.Platform)
	}
	assert.Equal(t, 2, f.CountOp("run"), "one run per platform, same digest")
}

func TestProbeEmptyPlatformUsesImagePlatform(t *testing.T) {
	ctx := context.Background()
	f := newFake(baseImage())
	f.Platforms = map[string]string{testRef: "linux/amd64"}
	res, err := Probe(ctx, f, testRef, "", []Request{req("go", "")}, opts(t))
	require.NoError(t, err)
	assert.Equal(t, "linux/amd64", res.Facts.Platform)
	assert.Equal(t, "linux/amd64", lastRun(t, f).Platform)
}

func lastRun(t *testing.T, f *engine.Fake) engine.RunSpec {
	t.Helper()
	calls := f.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Op == "run" {
			return *calls[i].Run
		}
	}
	t.Fatal("no run call")
	return engine.RunSpec{}
}

func TestProbePullAndDigest(t *testing.T) {
	pullErr := errors.New("dial tcp: network is unreachable")
	tests := []struct {
		name    string
		setup   func(f *engine.Fake)
		offline bool
		err     string
		isErr   error
		notice  string
		pulls   int
	}{
		{
			name: "pull failure with local image",
			setup: func(f *engine.Fake) {
				f.PullErr = map[string]error{testRef: pullErr}
				f.Images = map[string]bool{testRef: true}
				f.Platforms = map[string]string{testRef: testPlatform}
			},
			notice: "pull of " + testRef + " failed, using the local image (linux/arm64): dial tcp: network is unreachable",
			pulls:  1,
		},
		{
			name: "pull failure without local image",
			setup: func(f *engine.Fake) {
				f.PullErr = map[string]error{testRef: pullErr}
			},
			err:   "pull " + testRef + ": dial tcp: network is unreachable (the image is not available locally)",
			isErr: pullErr,
			pulls: 1,
		},
		{
			name: "pull failure with local image of another platform",
			setup: func(f *engine.Fake) {
				f.PullErr = map[string]error{testRef: pullErr}
				f.Images = map[string]bool{testRef: true}
				f.Platforms = map[string]string{testRef: "linux/amd64"}
			},
			err:   "the local image is linux/amd64, not linux/arm64",
			isErr: pullErr,
			pulls: 1,
		},
		{
			name: "offline with local image",
			setup: func(f *engine.Fake) {
				f.Images = map[string]bool{testRef: true}
				f.Platforms = map[string]string{testRef: testPlatform}
			},
			offline: true,
			notice:  "offline: using the local image of " + testRef + " (linux/arm64)",
		},
		{
			name:    "offline without local image",
			offline: true,
			err:     "probe " + testRef + ": the host is offline and the image is not available locally",
		},
		{
			name:  "missing digest",
			setup: func(f *engine.Fake) { f.Digests = nil },
			err:   "probe " + testRef + ": read image digest",
			isErr: engine.ErrNoDigest,
			pulls: 1,
		},
		{
			name:  "malformed digest",
			setup: func(f *engine.Fake) { f.Digests = map[string]string{testRef: "sha256:../../etc"} },
			err:   "invalid image digest",
			pulls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(baseImage())
			if tt.setup != nil {
				tt.setup(f)
			}
			o := opts(t)
			o.Offline = tt.offline
			res, err := Probe(context.Background(), f, testRef, testPlatform, []Request{req("go", "")}, o)
			assert.Equal(t, tt.pulls, f.CountOp("pull"))
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				if tt.isErr != nil {
					assert.ErrorIs(t, err, tt.isErr)
				}
				assert.Zero(t, f.CountOp("run"), "no container on error")
				entries, _ := os.ReadDir(o.Cache.Dir)
				assert.Empty(t, entries, "no cache entry on error")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []string{tt.notice}, res.Notices)
			assert.Equal(t, StatusPresent, res.Checks["go"].Status)
		})
	}
}

func TestProbeContainerOutput(t *testing.T) {
	tests := []struct {
		name  string
		image *image
		err   string
	}{
		{
			name:  "non-zero exit with complete output",
			image: &image{checks: baseImage().checks, exit: errors.New("exit status 1")},
		},
		{
			name:  "unparseable output",
			image: &image{broken: true, stderr: "exec /bin/sh: no such file or directory"},
			err:   "probe " + testRef + ": probe output has no probe lines; stderr: exec /bin/sh: no such file or directory",
		},
		{
			name:  "run error and unparseable output",
			image: &image{broken: true, exit: errors.New("exit status 127"), stderr: "boom"},
			err:   "run probe container for " + testRef + ": exit status 127 (probe output has no probe lines); stderr: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Probe(context.Background(), newFake(tt.image), testRef, testPlatform, []Request{req("go", "")}, opts(t))
			if tt.err != "" {
				require.Error(t, err)
				assert.Equal(t, tt.err, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, StatusPresent, res.Checks["go"].Status)
		})
	}
}

func TestProbeMissingCheckLine(t *testing.T) {
	im := baseImage()
	f := newFake(im)
	f.RunFunc = func(spec engine.RunSpec) ([]byte, []byte, error) {
		return []byte(factsOutput), nil, nil
	}
	_, err := Probe(context.Background(), f, testRef, testPlatform, []Request{req("go", "")}, opts(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no check line for go")
}

func TestProbeRequestValidation(t *testing.T) {
	good := req("go", "")
	tests := []struct {
		name string
		reqs []Request
		err  string
	}{
		{name: "duplicate key", reqs: []Request{good, good}, err: "component go requested twice"},
		{name: "empty command", reqs: []Request{{Key: "x", Command: " ", Hash: good.Hash}}, err: "has no check command"},
		{name: "nul byte", reqs: []Request{{Key: "x", Command: "a\x00b", Hash: good.Hash}}, err: "NUL byte"},
		{name: "bad hash", reqs: []Request{{Key: "x", Command: "true"}}, err: "invalid check hash"},
		{name: "bad key", reqs: []Request{{Key: "../x", Command: "true", Hash: good.Hash}}, err: "invalid component key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(baseImage())
			_, err := Probe(context.Background(), f, testRef, testPlatform, tt.reqs, opts(t))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.err)
			assert.Empty(t, f.Calls(), "validation happens before any engine call")
		})
	}
}

func TestProbeCacheWriteFailureIsANotice(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	o := opts(t)
	o.Cache = &Cache{Dir: file}
	res, err := Probe(context.Background(), newFake(baseImage()), testRef, testPlatform, []Request{req("go", "")}, o)
	require.NoError(t, err)
	require.Len(t, res.Notices, 1)
	assert.Contains(t, res.Notices[0], "probe cache not updated")
	assert.Equal(t, StatusPresent, res.Checks["go"].Status)
}

func TestRequestFor(t *testing.T) {
	c := &component.Component{
		Key:       "python",
		Installer: component.Installer{Mise: &component.MiseInstaller{Tool: "python", Version: "3.12", Bins: []string{"python3", "pip3"}}},
		Check:     component.Check{Command: "python3 --version", Expect: component.Expect{Exit: 0, StdoutContains: "Python"}},
	}
	r := RequestFor(c, "")
	assert.Equal(t, Request{
		Key: "python", Command: "python3 --version", Bins: []string{"python3", "pip3"}, Selector: "3.12",
		StdoutContains: "Python", Hash: c.CheckHash("3.12"),
	}, r)
	pinned := RequestFor(c, "3.13")
	assert.Equal(t, "3.13", pinned.Selector)
	assert.Equal(t, c.CheckHash("3.13"), pinned.Hash)
	assert.NotEqual(t, r.Hash, pinned.Hash)
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		exit    int
		out     string
		status  Status
		version string
	}{
		{name: "present", req: Request{Selector: "1.25"}, out: "go version go1.25.1 linux/arm64\n", status: StatusPresent, version: "1.25.1"},
		{name: "present without selector", out: "hello", status: StatusPresent},
		{name: "latest accepts unknown version", req: Request{Selector: "latest"}, out: "hello", status: StatusPresent},
		{name: "incompatible", req: Request{Selector: ">=22"}, out: "v20.11.1", status: StatusIncompatible, version: "20.11.1"},
		{name: "no version with selector", req: Request{Selector: "1.25"}, out: "go version devel", status: StatusIncompatible},
		{name: "exit differs", exit: 127, out: "not found 1.2.3", status: StatusMissing},
		{name: "expected non-zero exit", req: Request{ExpectExit: 2}, exit: 2, status: StatusPresent},
		{name: "expected exit not met", req: Request{ExpectExit: 2}, exit: 0, status: StatusMissing},
		{name: "stdout_contains met", req: Request{StdoutContains: "Python"}, out: "/usr/bin/python3\nPython 3.12.3", status: StatusPresent, version: "3.12.3"},
		{name: "stdout_contains not met", req: Request{StdoutContains: "Python"}, out: "Something 3.12.3", status: StatusMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req.Key, tt.req.Hash = "k", "h"
			r := Evaluate(tt.req, tt.exit, "/bin/k", tt.out)
			assert.Equal(t, tt.status, r.Status)
			assert.Equal(t, tt.version, r.Version)
			assert.Equal(t, "k", r.ComponentKey)
			assert.Equal(t, "h", r.CheckHash)
			assert.Equal(t, "/bin/k", r.Path)
			assert.Equal(t, tt.exit, r.Exit)
			assert.Equal(t, strings.TrimRight(tt.out, "\n"), r.Output)
		})
	}
}

func TestRunChecks(t *testing.T) {
	const built = "localhost/sandcast/demo:latest"
	reqs := []Request{req("node", "22"), req("go", "1.24"), req("rust", "")}
	tests := []struct {
		name  string
		image *image
		reqs  []Request
		want  map[string]Status
		runs  int
		err   string
	}{
		{
			name:  "evaluates every check, missing line is missing",
			image: baseImage(),
			reqs:  reqs,
			want:  map[string]Status{"go": StatusPresent, "node": StatusMissing, "rust": StatusMissing},
			runs:  1,
		},
		{
			name:  "no requests starts no container",
			image: baseImage(),
			want:  map[string]Status{},
		},
		{
			name:  "unparseable output",
			image: &image{broken: true, stderr: "killed"},
			reqs:  reqs,
			runs:  1,
			err:   "probe " + built + ": probe output has no probe lines; stderr: killed",
		},
		{
			name:  "invalid request",
			image: baseImage(),
			reqs:  []Request{{Key: "x", Command: ""}},
			err:   "has no check command",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			im := tt.image
			f := newFake(im)
			// rust produces no check line.
			f.RunFunc = func(spec engine.RunSpec) ([]byte, []byte, error) {
				out, stderr, err := im.run(engine.RunSpec{Script: strings.ReplaceAll(spec.Script, "_check 'rust'", "#")})
				if !im.broken {
					assert.Equal(t, engine.RunSpec{Image: built, Platform: "linux/amd64", Entrypoint: "/bin/sh", Script: spec.Script}, spec)
				}
				return out, stderr, err
			}
			got, err := RunChecks(context.Background(), f, built, "linux/amd64", tt.reqs)
			assert.Equal(t, tt.runs, f.CountOp("run"))
			assert.Equal(t, f.CountOp("run"), len(f.Calls()), "no pull, digest or other engine call")
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				return
			}
			require.NoError(t, err)
			status := map[string]Status{}
			for k, c := range got {
				status[k] = c.Status
			}
			assert.Equal(t, tt.want, status)
			if tt.runs > 0 {
				script := lastScript(t, f)
				assert.NotContains(t, script, "os-release", "no facts")
				assert.Equal(t, []string{"go", "node", "rust"}, scriptKeys(script))
				assert.Equal(t, CheckResult{ComponentKey: "rust", CheckHash: tt.reqs[2].Hash, Status: StatusMissing, Output: "no result", Exit: -1}, got["rust"])
				assert.Equal(t, "1.24.3", got["go"].Version)
			}
		})
	}
}

func TestRunChecksInterruptedRecordsNothing(t *testing.T) {
	im := baseImage()
	f := newFake(im)
	ctx, cancel := context.WithCancel(context.Background())
	f.RunFunc = func(spec engine.RunSpec) ([]byte, []byte, error) {
		// The container prints part of its output and is then interrupted.
		out, stderr, err := im.run(spec)
		cancel()
		return out, stderr, err
	}
	got, err := RunChecks(ctx, f, "localhost/sandcast/demo:latest", "linux/amd64", []Request{req("node", "22"), req("go", "1.24")})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, got, "unreached checks are not reported as missing")
}

func TestTail(t *testing.T) {
	assert.Equal(t, "", tail("", maxStderr), "empty stays empty")
	assert.Equal(t, "short", tail("short", maxStderr), "under the cap is returned whole")

	atCap := strings.Repeat("x", maxStderr)
	assert.Equal(t, atCap, tail(atCap, maxStderr), "exactly at the cap is not cut")

	over := strings.Repeat("a", 10) + strings.Repeat("b", maxStderr)
	got := tail(over, maxStderr)
	assert.True(t, strings.HasPrefix(got, "..."), "a cut result is marked")
	assert.Equal(t, len("...")+maxStderr, len(got), "a cut result keeps the last maxStderr bytes plus the marker")
	assert.Equal(t, strings.Repeat("b", maxStderr), strings.TrimPrefix(got, "..."), "the tail bytes are the most recent ones")
}
