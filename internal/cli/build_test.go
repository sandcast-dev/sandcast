package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/engine"
)

func TestBuildVerifyEndToEnd(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	require.Equal(t, 0, ta.run("build", "--verify", "--json"), ta.stderr.String())
	var rep buildReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, "localhost/demo:latest", rep.Image)
	assert.Equal(t, ".sandcast/build/openshell/Containerfile", rep.Containerfile)
	require.NotNil(t, rep.Verify)
	assert.Zero(t, rep.Verify.Failed)
	assert.Positive(t, rep.Verify.Passed)

	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	for _, f := range []string{"Containerfile", "Containerfile.final", "build.log", "profiles/index.yaml", "profiles/go-proxy.yaml", "profiles/github.yaml", "verify.yaml"} {
		_, err := os.Stat(filepath.Join(dir, f))
		assert.NoError(t, err, f)
	}
	assert.Equal(t, 2, ta.eng.CountOp("build"))

	// verify alone re-reads the plan.
	require.Equal(t, 0, ta.run("verify"))
	assert.Contains(t, ta.stdout.String(), fmt.Sprintf(": %d passed, 0 failed\n", rep.Verify.Passed))

	// profiles lint on the adapted set.
	require.Equal(t, 0, ta.run("profiles", "lint"), ta.stdout.String())
	assert.Contains(t, ta.stdout.String(), "0 diagnostic(s)")
}

func TestBuildFailureExit1NoRetry(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = spec.Log.Write([]byte("RUN false\nError: exit status 1\n"))
		return errors.New("exit status 1")
	}
	assert.Equal(t, ExitFailure, ta.run("build"))
	assert.Equal(t, 1, ta.eng.CountOp("build"))
	assert.Contains(t, ta.stdout.String(), "build failed")
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "build.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "RUN false")
	assert.Contains(t, ta.stderr.String(), "last ", "the log tail is printed")
	assert.Contains(t, ta.stderr.String(), "  Error: exit status 1")
}

func TestBuildVerifyFailureExit1(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.failing["go"] = true
	assert.Equal(t, ExitFailure, ta.run("build", "--verify"))
	assert.Contains(t, ta.stdout.String(), "FAIL")
	assert.Contains(t, ta.stdout.String(), "1 failed")
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "verify.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "failed: 1")
}

func TestVerifyStandaloneExitCodes(t *testing.T) {
	ta := builtProject(t)
	ta.eng.failing["go"] = true
	runs := ta.eng.CountOp("run")
	assert.Equal(t, ExitFailure, ta.run("verify"), ta.stderr.String())
	assert.Greater(t, ta.eng.CountOp("run"), runs, "the checks ran in the image")
	assert.Regexp(t, `(?m)^go\s+FAIL \(`, ta.stdout.String())
	assert.Contains(t, ta.stdout.String(), ", 1 failed\n")
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "verify.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "failed: 1")

	// No build yet: no plan to verify against.
	fresh := newTestApp(t)
	fresh.project(t)
	assert.Equal(t, ExitUsage, fresh.run("verify"))
	assert.Contains(t, fresh.stderr.String(), `run "sandcast build --target openshell" first`)
	assert.Zero(t, fresh.eng.CountOp("run"), "nothing runs without a plan")
}

func TestVerifyRefusesWhileABuildRuns(t *testing.T) {
	ta := builtProject(t)
	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	unlock, err := build.LockDir(dir)
	require.NoError(t, err)
	defer unlock()
	runs := ta.eng.CountOp("run")
	assert.Equal(t, ExitFailure, ta.run("verify"))
	assert.Contains(t, ta.stderr.String(), "another sandcast build is running for target openshell in "+dir+"; wait for it to finish")
	assert.Equal(t, runs, ta.eng.CountOp("run"), "no check runs while a build holds the directory")
	assert.NoFileExists(t, filepath.Join(dir, "verify.yaml"))
}

func TestBuildUnresolvedExit2(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: mystery\n", 1))
	assert.Equal(t, ExitUsage, ta.run("build"))
	assert.Contains(t, ta.stderr.String(), "unresolved: mystery")
	assert.Zero(t, ta.eng.CountOp("build"))
}

func TestBuildContainerTarget(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.Digests["registry.fedoraproject.org/fedora:42"] = testDigest
	require.Equal(t, 0, ta.run("build", "--target", "container", "--verify"), ta.stderr.String())
	dir := filepath.Join(ta.dir, ".sandcast", "build", "container")
	domains, err := os.ReadFile(filepath.Join(dir, "allowed-domains.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(domains), "proxy.golang.org\n")
	assert.Contains(t, string(domains), "github.com\n")
	cf, err := os.ReadFile(filepath.Join(dir, "Containerfile"))
	require.NoError(t, err)
	assert.NotContains(t, string(cf), "/etc/openshell/policy.yaml")
	assert.NotContains(t, string(cf), "nftables")
	_, err = os.Stat(filepath.Join(dir, "policy.yaml"))
	assert.True(t, os.IsNotExist(err))
}

func TestBuildWithClaudeCodeHarness(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", testManifest+`harnesses:
  - name: claude-code
    mcp_servers:
      - name: docs
        url: https://docs.example.com/mcp
        env:
          DOCS_TOKEN: ${SANDCAST_SECRET_1}
`)
	ta.write(t, ".sandcast/mise.lock", testLock+`
[[tools.claude]]
version = "2.1.0"
backend = "aqua:anthropics/claude-code"
specifiers = ["latest"]

[tools.claude."platforms.linux-arm64"]
checksum = "sha256:9bf234ea70ffec9347fdf6b22ce4add51717d3386a38a441e8c8743fceb5eaee"
url = "https://example.com/claude-linux-arm64.tar.gz"
`)
	require.Equal(t, 0, ta.run("build", "--json"), ta.stderr.String())
	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	cf, err := os.ReadFile(filepath.Join(dir, "Containerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(cf), "# 70-harness")
	assert.Contains(t, string(cf), "CLAUDE_CONFIG_DIR")
	_, err = os.Stat(filepath.Join(dir, "profiles", "mcp-docs.yaml"))
	assert.NoError(t, err, "uncovered MCP endpoint gets a project-local profile")
	_, err = os.Stat(filepath.Join(dir, "profiles", "claude-code.yaml"))
	assert.NoError(t, err)
	pol, err := os.ReadFile(filepath.Join(dir, "policy.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(pol), "/opt/sandcast/claude-code")
}

func TestBuildInterruptedExit130KeepsDebugImage(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ctx, cancel := context.WithCancel(context.Background())
	ta.eng.BuildFunc = func(spec engine.BuildSpec) error {
		if strings.HasSuffix(spec.Containerfile, build.FinalContainerfileName) {
			cancel()
			return context.Canceled
		}
		return nil
	}
	code := ta.Run(ctx, []string{"--project", ta.dir, "build"})
	assert.Equal(t, ExitInterrupted, code)
	assert.Contains(t, ta.stderr.String(), "interrupted")
	assert.Equal(t, 1, ta.eng.CountOp("tag"), "the stage image is kept as the debug tag")
	assert.Contains(t, ta.eng.ImageNames(), "localhost/demo:sandcast-debug", "tagged although the context was cancelled")
	for _, name := range ta.eng.ImageNames() {
		assert.NotContains(t, name, "localhost/demo:sandcast-stage", "the per-run stage tag is removed")
	}
	assert.Contains(t, ta.stdout.String(), "stage image kept as localhost/demo:sandcast-debug")
}

func TestReadLineHonoursCancel(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	a := &App{Stdin: r}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.readLine(ctx)
	assert.ErrorIs(t, err, context.Canceled, "a prompt does not swallow Ctrl-C")

	a = &App{Stdin: strings.NewReader("yes\n")}
	line, err := a.readLine(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "yes\n", line)

	// A piped answer without a newline, and empty stdin, are read, not errors.
	line, err = (&App{Stdin: strings.NewReader("y")}).readLine(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "y", line)
	line, err = (&App{Stdin: strings.NewReader("")}).readLine(context.Background())
	require.NoError(t, err)
	assert.Empty(t, line)
}
