//go:build integration || gateway

// Package integration holds build-tagged end-to-end tests that drive the
// sandcast binary against a real container engine (tag integration) and a
// real OpenShell gateway (tag gateway). Run them with make test-integration
// and make test-gateway; both build the binary first.
package integration

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// binary returns the sandcast binary built by make.
func binary(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("../../sandcast")
	require.NoError(t, err)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sandcast binary not found at %s: run make build (make test-integration does)", path)
	}
	return path
}

// toolsRequired reports whether a missing engine or host mise fails the
// test instead of skipping it: in CI, and wherever SANDCAST_REQUIRE_TOOLS=1.
func toolsRequired() bool {
	if os.Getenv("SANDCAST_REQUIRE_TOOLS") == "1" {
		return true
	}
	ci := os.Getenv("CI")
	return ci != "" && ci != "0" && ci != "false"
}

// missingTool skips the test, or fails it when toolsRequired.
func missingTool(t *testing.T, msg string) {
	t.Helper()
	if toolsRequired() {
		t.Fatal(msg + " (required: CI or SANDCAST_REQUIRE_TOOLS=1 is set)")
	}
	t.Skip(msg + "; skipping integration test")
}

// requireEngine returns podman or docker, whichever is usable first.
func requireEngine(t *testing.T) string {
	t.Helper()
	for _, e := range []string{"podman", "docker"} {
		if _, err := exec.LookPath(e); err != nil {
			continue
		}
		if err := exec.Command(e, "info").Run(); err == nil {
			return e
		}
	}
	missingTool(t, "no usable container engine (podman or docker)")
	return ""
}

// requireMise checks for the host mise needed by sandcast lock.
func requireMise(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mise"); err != nil {
		missingTool(t, "mise not on PATH; sandcast lock needs it")
	}
}

// isolated maps a test to its SANDCAST_CACHE_DIR and SANDCAST_CONFIG_DIR.
var isolated sync.Map

// isolatedEnv returns the cache and configuration directories of t, created
// on first use, so that runs neither read nor write the developer's probe
// cache, component registry cache or base image registry.
func isolatedEnv(t *testing.T) []string {
	t.Helper()
	if env, ok := isolated.Load(t); ok {
		return env.([]string)
	}
	env := []string{"SANDCAST_CACHE_DIR=" + t.TempDir(), "SANDCAST_CONFIG_DIR=" + t.TempDir()}
	isolated.Store(t, env)
	t.Cleanup(func() { isolated.Delete(t) })
	return env
}

// hostPlatform is linux/<host arch>.
func hostPlatform() string { return "linux/" + runtime.GOARCH }

// result is one sandcast invocation.
type result struct {
	stdout, stderr string
	code           int
}

// sandcast runs the binary in dir with the test's own cache and
// configuration directories and extra environment variables.
func sandcast(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), isolatedEnv(t)...), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr := &exec.ExitError{}
		if ok := asExit(err, &exitErr); !ok {
			t.Fatalf("run sandcast %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	t.Logf("sandcast %s (exit %d)\n%s%s", strings.Join(args, " "), code, tail(stdout.String()), tail(stderr.String()))
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 30 {
		lines = append([]string{"..."}, lines[len(lines)-30:]...)
	}
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// project copies the reference fixture into a temp directory and writes the
// manifest. The image name is unique per test to keep runs independent.
func project(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	src := "../fixtures/reference-project"
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".sandcast"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".sandcast", "manifest.yaml"), []byte(manifest), 0o644))
	return dir
}

// removeImages deletes the images a test built.
func removeImages(t *testing.T, engine string, refs ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, r := range refs {
			_ = exec.Command(engine, "rmi", "-f", r).Run()
		}
	})
}
