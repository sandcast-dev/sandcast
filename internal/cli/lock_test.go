package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/mise"
)

// fakeHostMise implements mise.Runner like a host mise: "mise lock" writes
// a complete entry for every tool of the workspace mise.toml (or the one
// tool argument) on every requested platform, except tools listed in fail,
// which end the run with a mise error.
type fakeHostMise struct {
	missing bool
	fail    map[string]bool
	calls   []lockRun
	locked  map[string]string // tool -> selector, entries written so far
}

// lockRun is one recorded mise lock call.
type lockRun struct {
	args   []string
	config string
}

func (f *fakeHostMise) Run(_ context.Context, dir string, _ []string, name string, args ...string) ([]byte, []byte, error) {
	if f.missing {
		return nil, nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	if len(args) == 1 && args[0] == "--version" {
		return []byte("2026.10.4 linux-arm64 (2026-10-08)\n"), nil, nil
	}
	if len(args) == 0 || args[0] != "lock" {
		return nil, nil, fmt.Errorf("unexpected mise %v", args)
	}
	config, err := os.ReadFile(filepath.Join(dir, "mise.toml"))
	if err != nil {
		return nil, nil, err
	}
	f.calls = append(f.calls, lockRun{args: slices.Clone(args), config: string(config)})
	var platforms []string
	only := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--platform":
			i++
			platforms = strings.Split(args[i], ",")
		case "--bump":
		default:
			only = args[i]
		}
	}
	tools := configTools(string(config))
	if only == "" {
		f.locked = map[string]string{}
	} else {
		tools = map[string]string{only: tools[only]}
	}
	var errs []string
	for _, tool := range slices.Sorted(maps.Keys(tools)) {
		if f.fail[tool] {
			errs = append(errs, "mise ERROR no versions found for "+tool)
		}
	}
	if len(errs) > 0 {
		return nil, []byte(strings.Join(errs, "\n") + "\n"), errors.New("exit status 1")
	}
	maps.Copy(f.locked, tools)
	return nil, nil, os.WriteFile(filepath.Join(dir, "mise.lock"), []byte(fakeLockfile(f.locked, platforms)), 0o644)
}

// configTools returns the [tools] table of a rendered mise.toml.
func configTools(config string) map[string]string {
	tools := map[string]string{}
	_, table, _ := strings.Cut(config, "[tools]\n")
	for _, line := range strings.Split(table, "\n") {
		key, value, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		if k, err := strconv.Unquote(key); err == nil {
			key = k
		}
		value, _ = strconv.Unquote(value)
		tools[key] = value
	}
	return tools
}

// fakeLockfile renders a lockfile v3 with a checksummed entry for every
// tool on every platform.
func fakeLockfile(tools map[string]string, platforms []string) string {
	var b strings.Builder
	b.WriteString("lockfile_version = 3\n")
	for _, tool := range slices.Sorted(maps.Keys(tools)) {
		fmt.Fprintf(&b, "\n[[tools.%s]]\nversion = \"1.0.0\"\nbackend = \"core:%s\"\nspecifiers = [%q]\n", tool, tool, tools[tool])
		for _, p := range slices.Sorted(slices.Values(platforms)) {
			sum := sha256.Sum256([]byte(tool + p))
			fmt.Fprintf(&b, "\n[tools.%s.\"platforms.%s\"]\nchecksum = \"sha256:%x\"\nurl = \"https://example.com/%s-%s.tar.gz\"\n", tool, p, sum, tool, p)
		}
	}
	return b.String()
}

func newLockApp(t *testing.T) (*testApp, *fakeHostMise) {
	t.Helper()
	ta := newTestApp(t)
	ta.project(t)
	fake := &fakeHostMise{fail: map[string]bool{}}
	ta.MiseRunner = fake
	return ta, fake
}

// lockPlatforms returns the --platform value of a recorded mise lock call.
func lockPlatforms(t *testing.T, r lockRun) string {
	t.Helper()
	i := slices.Index(r.args, "--platform")
	require.GreaterOrEqual(t, i, 0, "%v", r.args)
	return r.args[i+1]
}

func TestLockDefaultPlatform(t *testing.T) {
	ta, fake := newLockApp(t)
	require.Equal(t, 0, ta.run("lock"), ta.stderr.String())
	require.Len(t, fake.calls, 1)
	assert.Equal(t, "linux-arm64", lockPlatforms(t, fake.calls[0]), "host platform without a manifest platform")
	assert.Contains(t, ta.stdout.String(), ".sandcast/mise.lock written for [linux-arm64]: 1 tool(s) locked")

	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "base_image: nvidia-community-base\n", "base_image: nvidia-community-base\nplatform: linux/amd64\n", 1))
	fake.calls = nil
	require.Equal(t, 0, ta.run("lock"), ta.stderr.String())
	require.Len(t, fake.calls, 1)
	assert.Equal(t, "linux-x64", lockPlatforms(t, fake.calls[0]), "the manifest platform wins over the host")

	fake.calls = nil
	require.Equal(t, 0, ta.run("lock", "--platform", "linux/arm64", "--platform", "linux/amd64"), ta.stderr.String())
	require.Len(t, fake.calls, 1)
	assert.Equal(t, "linux-arm64,linux-x64", lockPlatforms(t, fake.calls[0]), "flags win over the manifest")

	lock, err := mise.ReadLock(filepath.Join(ta.dir, ".sandcast", "mise.lock"))
	require.NoError(t, err)
	goTool := []mise.Tool{{Key: "go", Tool: "go", Version: "latest"}}
	assert.Empty(t, lock.Check(goTool, "linux-arm64"))
	assert.Empty(t, lock.Check(goTool, "linux-x64"))

	assert.Equal(t, ExitUsage, ta.run("lock", "--platform", "darwin/arm64"))
}

func TestLockUnionsTargets(t *testing.T) {
	ta, fake := newLockApp(t)
	ta.write(t, ".sandcast/components/ostool.yaml", "key: ostool\nname: ostool\ninstaller: {mise: {tool: ostool, version: \"1.2\"}}\ncheck: {command: ostool --version}\ntargets: [openshell]\n")
	ta.write(t, ".sandcast/components/ctool.yaml", "key: ctool\nname: ctool\ninstaller: {mise: {tool: ctool}}\ncheck: {command: ctool --version}\ntargets: [container]\n")
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: ostool\n  - key: ctool\n", 1))

	require.Equal(t, 0, ta.run("lock", "--json"), ta.stderr.String())
	require.Len(t, fake.calls, 1)
	assert.Equal(t, map[string]string{"go": "latest", "ostool": "1.2", "ctool": "latest"}, configTools(fake.calls[0].config),
		"tools of both declared targets in one lock run")
	var rep lockReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, []string{"ctool", "go", "ostool"}, rep.Locked)
}

func TestLockIncludesHarnessAgent(t *testing.T) {
	ta, fake := newLockApp(t)
	ta.write(t, ".sandcast/manifest.yaml", testManifest+"harnesses:\n  - name: claude-code\n")

	require.Equal(t, 0, ta.run("lock", "--json"), ta.stderr.String())
	require.Len(t, fake.calls, 1)
	assert.Equal(t, map[string]string{"go": "latest", "claude": "latest"}, configTools(fake.calls[0].config),
		"the claude-code harness adds the agent the build installs")
	var rep lockReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, []string{"claude", "go"}, rep.Locked)

	lock, err := mise.ReadLock(filepath.Join(ta.dir, ".sandcast", "mise.lock"))
	require.NoError(t, err)
	assert.Empty(t, lock.Check([]mise.Tool{{Key: "claude-code", Tool: "claude", Version: "latest"}}, "linux-arm64"),
		"a locked build finds the agent in mise.lock")
}

func TestLockBumpAndJSON(t *testing.T) {
	ta, fake := newLockApp(t)
	require.Equal(t, 0, ta.run("lock", "--bump", "--json"), ta.stderr.String())
	require.Len(t, fake.calls, 1)
	assert.Contains(t, fake.calls[0].args, "--bump")

	var raw map[string]any
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &raw))
	assert.Equal(t, map[string]any{
		"lockfile":  ".sandcast/mise.lock",
		"platforms": []any{"linux-arm64"},
		"locked":    []any{"go"},
		"failed":    []any{},
		"changed":   true,
	}, raw)

	// The same inputs again leave the lockfile alone.
	fake.calls = nil
	require.Equal(t, 0, ta.run("lock"), ta.stderr.String())
	assert.NotContains(t, fake.calls[0].args, "--bump")
	assert.Contains(t, ta.stdout.String(), ".sandcast/mise.lock unchanged")
}

func TestLockMiseMissingExit3(t *testing.T) {
	ta, fake := newLockApp(t)
	fake.missing = true
	before, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "mise.lock"))
	require.NoError(t, err)
	assert.Equal(t, ExitTool, ta.run("lock"))
	assert.Contains(t, ta.stderr.String(), "mise not found on PATH")
	after, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "mise.lock"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestLockToolFailureExit1(t *testing.T) {
	ta, fake := newLockApp(t)
	ta.write(t, ".sandcast/components/ostool.yaml", "key: ostool\nname: ostool\ninstaller: {mise: {tool: ostool}}\ncheck: {command: ostool --version}\n")
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: ostool\n", 1))
	fake.fail["ostool"] = true

	assert.Equal(t, ExitFailure, ta.run("lock"))
	assert.Contains(t, ta.stdout.String(), "1 tool(s) locked")
	assert.Contains(t, ta.stdout.String(), "failed: component ostool (mise tool ostool): no versions found for ostool")

	require.Equal(t, ExitFailure, ta.run("lock", "--json"))
	var rep lockReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, []string{"go"}, rep.Locked)
	require.Len(t, rep.Failed, 1)
	assert.Equal(t, mise.ToolError{Key: "ostool", Tool: "ostool", Platform: "linux-arm64", Reason: "no versions found for ostool"}, rep.Failed[0])

	lock, err := mise.ReadLock(filepath.Join(ta.dir, ".sandcast", "mise.lock"))
	require.NoError(t, err)
	assert.Empty(t, lock.Check([]mise.Tool{{Key: "go", Tool: "go", Version: "latest"}}, "linux-arm64"), "the tools that locked are written")
	assert.Equal(t, "", lock.Version("ostool"))
}

func TestLockNothingToLock(t *testing.T) {
	ta, fake := newLockApp(t)
	ta.write(t, ".sandcast/manifest.yaml", "schema_version: 1\nname: demo\nbase_image: nvidia-community-base\ncomponents: []\ntargets:\n  openshell:\n    image: localhost/demo\n")
	require.Equal(t, 0, ta.run("lock"), ta.stderr.String())
	assert.Contains(t, ta.stdout.String(), "nothing to lock")
	assert.Empty(t, fake.calls)
}
