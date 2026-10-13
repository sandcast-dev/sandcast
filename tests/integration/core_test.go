//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const coreManifest = `schema_version: 1
name: sc-core
base_image: nvidia-community-base
components:
  - key: go
  - key: python
  - key: uv
  - key: starship
  - key: fzf
  - key: lsd
shell:
  program: zsh
  rc: |
    autoload -Uz compinit && compinit -i -D
    [[ "$TERM" != "dumb" ]] && eval "$(starship init zsh)"
    alias ls=lsd
  components: [starship, lsd]
targets:
  openshell:
    image: localhost/sandcast-it-core
  container:
    image: localhost/sandcast-it-core-plain
`

type verifyFile struct {
	Results []struct {
		ComponentKey string `yaml:"component_key"`
		OK           bool   `yaml:"ok"`
	} `yaml:"results"`
	Passed int `yaml:"passed"`
	Failed int `yaml:"failed"`
}

func readVerify(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var v verifyFile
	require.NoError(t, yaml.Unmarshal(data, &v))
	out := map[string]bool{}
	for _, r := range v.Results {
		out[r.ComponentKey] = r.OK
	}
	return out
}

// TestCoreFlow covers quickstart scenario 1 on the reference fixture:
// lock, probe, plan twice (byte-identical, cached), build, verify, plus the
// performance goals of plan.md and the container target (US5 scenario 2).
func TestCoreFlow(t *testing.T) {
	engine := requireEngine(t)
	requireMise(t)
	dir := project(t, coreManifest)
	removeImages(t, engine, "localhost/sandcast-it-core:latest", "localhost/sandcast-it-core-plain:latest")
	platform := hostPlatform()

	require.Zero(t, sandcast(t, dir, nil, "lock", "--platform", platform).code)
	r := sandcast(t, dir, nil, "plan")
	require.Zero(t, r.code, "first plan pulls and probes")
	assert.NotContains(t, r.stderr, "probe cache hit", "the test's own cache starts empty")
	planPath := filepath.Join(dir, ".sandcast", "build", "openshell", "plan.yaml")
	first, err := os.ReadFile(planPath)
	require.NoError(t, err)

	start := time.Now()
	r = sandcast(t, dir, nil, "plan")
	cached := time.Since(start)
	require.Zero(t, r.code)
	assert.Contains(t, r.stderr, "probe cache hit")
	second, err := os.ReadFile(planPath)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "plans must be byte-identical (SC-002)")
	t.Logf("cached plan took %s", cached)
	assert.Less(t, cached, 2*time.Second, "a plan on a cached probe completes in under 2 seconds")

	start = time.Now()
	require.Zero(t, sandcast(t, dir, nil, "probe", "--no-cache").code)
	full := time.Since(start)
	t.Logf("full probe of the pulled image took %s", full)
	assert.Less(t, full, 60*time.Second, "a full probe of a pulled image completes in under 60 seconds")

	r = sandcast(t, dir, nil, "build", "--verify")
	require.Zero(t, r.code, "build --verify (SC-001)")
	openshell := readVerify(t, filepath.Join(dir, ".sandcast", "build", "openshell", "verify.yaml"))
	for _, k := range []string{"go", "python", "uv", "starship", "fzf", "lsd", "zsh"} {
		assert.True(t, openshell[k], "%s verified in the openshell image", k)
	}
	idx, err := os.ReadFile(filepath.Join(dir, ".sandcast", "build", "openshell", "profiles", "index.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(idx), "upstream_id: go-proxy")
	assert.Contains(t, string(idx), "upstream_id: pypi")
	// A tool the base image already ships under the workspace (/sandbox),
	// which the sandbox can write, flags its profile (FR-022). Binaries that
	// sandcast installs under /opt/mise never do.
	var index profilesIndex
	require.NoError(t, yaml.Unmarshal(idx, &index))
	for _, p := range index.Profiles {
		if !p.Flagged {
			continue
		}
		assert.NotEqual(t, "go-proxy", p.UpstreamID, "go is installed by sandcast")
		assert.Contains(t, p.FlagReason, "paths the sandbox can write", p.UpstreamID)
		assert.Contains(t, p.FlagReason, " /sandbox/", "%s: only base image binaries under the workspace are flagged", p.UpstreamID)
	}
	require.Zero(t, sandcast(t, dir, nil, "profiles", "lint").code)
	readBackImage(t, engine, dir, "localhost/sandcast-it-core:latest")

	// US5: the same manifest builds a plain container image.
	r = sandcast(t, dir, nil, "build", "--target", "container", "--verify")
	require.Zero(t, r.code, "container target build --verify")
	container := readVerify(t, filepath.Join(dir, ".sandcast", "build", "container", "verify.yaml"))
	for _, k := range []string{"go", "python", "uv", "starship", "fzf", "lsd", "zsh"} {
		assert.True(t, container[k], "%s verified in the container image", k)
	}
	domains, err := os.ReadFile(filepath.Join(dir, ".sandcast", "build", "container", "allowed-domains.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(domains), "proxy.golang.org\n")
	assert.Contains(t, string(domains), "pypi.org\n")
	cf, err := os.ReadFile(filepath.Join(dir, ".sandcast", "build", "container", "Containerfile"))
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(cf), "/etc/openshell/policy.yaml"))
}

// profilesIndex is the part of an adapted set index.yaml the tests read.
type profilesIndex struct {
	Profiles []struct {
		UpstreamID string   `yaml:"upstream_id"`
		AdaptedID  string   `yaml:"adapted_id"`
		Binaries   []string `yaml:"binaries"`
		Flagged    bool     `yaml:"flagged"`
		FlagReason string   `yaml:"flag_reason"`
	} `yaml:"profiles"`
}

// readBackImage checks what the built image itself carries: the
// profiles-index label, the adapted set that profiles export reads out of
// the image without the build directory, and every non-glob binary path of
// that set.
func readBackImage(t *testing.T, engine, dir, image string) {
	t.Helper()
	out, err := exec.Command(engine, "image", "inspect", "--format", `{{index .Config.Labels "io.sandcast.profiles-index"}}`, image).Output()
	require.NoError(t, err)
	var label struct {
		IDs     []string `json:"ids"`
		Flagged []string `json:"flagged"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(out), &label), "label value %q", out)
	assert.Contains(t, label.IDs, "go-proxy")
	assert.Contains(t, label.IDs, "pypi")
	assert.NotContains(t, label.Flagged, "go-proxy", "go is installed by sandcast")

	profiles := filepath.Join(dir, ".sandcast", "build", "openshell", "profiles")
	require.NoError(t, os.RemoveAll(profiles))
	require.Zero(t, sandcast(t, dir, nil, "profiles", "export", "--suffix").code)
	data, err := os.ReadFile(filepath.Join(profiles, "index.yaml"))
	require.NoError(t, err, "profiles export rewrites the index from the image")
	var idx profilesIndex
	require.NoError(t, yaml.Unmarshal(data, &idx))
	adapted := map[string]string{}
	var bins []string
	for _, p := range idx.Profiles {
		adapted[p.UpstreamID] = p.AdaptedID
		for _, b := range p.Binaries {
			if !strings.ContainsAny(b, "*?[") {
				bins = append(bins, b)
			}
		}
	}
	assert.Equal(t, "go-proxy-sc-core", adapted["go-proxy"])
	assert.Equal(t, "pypi-sc-core", adapted["pypi"])

	// One container checks every path and names each one that fails.
	require.NotEmpty(t, bins)
	var script strings.Builder
	script.WriteString("rc=0\n")
	for _, b := range bins {
		q := "'" + strings.ReplaceAll(b, "'", `'\''`) + "'"
		fmt.Fprintf(&script, "test -x %s || { echo \"not executable: \"%s; rc=1; }\n", q, q)
	}
	script.WriteString("exit $rc\n")
	run, err := exec.Command(engine, "run", "--rm", "--entrypoint", "/bin/sh", image, "-c", script.String()).CombinedOutput()
	assert.NoError(t, err, "profile binaries missing from %s:\n%s", image, run)
	t.Logf("%d profile binaries are executable in %s", len(bins), image)
}

// TestBrokenFragmentFailsOnce covers quickstart scenario 3: a stored
// fragment that fails ends the build with exit 1, keeps the Containerfile
// and build.log, and is not retried.
func TestBrokenFragmentFailsOnce(t *testing.T) {
	engine := requireEngine(t)
	dir := project(t, `schema_version: 1
name: sc-broken
base_image: nvidia-community-base
components:
  - key: broken-tool
targets:
  openshell:
    image: localhost/sandcast-it-broken
`)
	removeImages(t, engine, "localhost/sandcast-it-broken:sandcast-debug")
	writeFragment(t, dir, "broken-tool", "USER root\nRUN false\nUSER sandbox\n")
	r := sandcast(t, dir, nil, "build")
	assert.Equal(t, 1, r.code)
	b := filepath.Join(dir, ".sandcast", "build", "openshell")
	log, err := os.ReadFile(filepath.Join(b, "build.log"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(log), "### sandcast pass 1"), "no retry")
	_, err = os.Stat(filepath.Join(b, "Containerfile"))
	assert.NoError(t, err)
}

func writeFragment(t *testing.T, dir, key, body string) {
	t.Helper()
	cmd := sandcastCmd(t, dir, "fragments", "write", key)
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// TestSessionEnvironment covers FR-006 to FR-010 and FR-025 under podman:
// a zsh image on the community base is started with the environment
// OpenShell gives a session (env -i with HOME, USER, TERM and the default
// PATH, bash as the started program) and must resolve every component
// binary in a login bash, an interactive bash and an interactive zsh, start
// zsh without stderr output, switch to zsh only with a terminal, and fail
// verify once its zshenv block is removed (SC-003, SC-004).
func TestSessionEnvironment(t *testing.T) {
	engine := requireEngine(t)
	requireMise(t)
	img := "localhost/sandcast-it-session:latest"
	broken := "localhost/sandcast-it-session-broken:latest"
	dir := project(t, sessionManifest("sc-session", "localhost/sandcast-it-session"))
	removeImages(t, engine, img, broken)
	require.Zero(t, sandcast(t, dir, nil, "lock", "--platform", hostPlatform()).code)
	r := sandcast(t, dir, nil, "build", "--verify")
	require.Zero(t, r.code, "build --verify runs the session checks and passes")
	assert.Contains(t, r.stdout, "session checks")
	assert.Contains(t, r.stdout, "session.zsh.startup")

	run := func(stdin string, tty bool, args ...string) (string, string, error) {
		t.Helper()
		full := []string{"run", "--rm"}
		if stdin != "" {
			full = append(full, "-i")
		}
		if tty {
			full = append(full, "-t")
		}
		full = append(full, "--entrypoint", "/usr/bin/env", img)
		full = append(full, sessionEnvArgs...)
		full = append(full, args...)
		cmd := exec.Command(engine, full...)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		t.Logf("%s %s\nstdout:\n%sstderr:\n%s", engine, strings.Join(args, " "), stdout.String(), stderr.String())
		return stdout.String(), stderr.String(), err
	}

	bins := planBins(t, dir, "openshell")
	var check strings.Builder
	for _, b := range bins {
		fmt.Fprintf(&check, "command -v %s || echo MISSING %s\n", b, b)
	}
	// exec: OpenShell runs bash -lc.
	out, _, err := run("", false, "/bin/bash", "-lc", check.String())
	require.NoError(t, err)
	assert.NotContains(t, out, "MISSING", "bash -lc resolves every binary")
	assert.Contains(t, out, "/opt/mise/shims/starship")
	out, _, err = run("", false, "/bin/bash", "-lc", `printf '%s\n' "$MISE_DATA_DIR" "$MISE_CONFIG_DIR"`)
	require.NoError(t, err)
	assert.Equal(t, "/opt/mise\n/etc/mise\n", out, "MISE_* settings in the session")

	// connect: OpenShell runs bash -i; without a terminal it must not switch.
	out, _, err = run(check.String()+"echo SHELL_IS=$0\n", false, "/bin/bash", "-i")
	require.NoError(t, err)
	assert.NotContains(t, out, "MISSING", "bash -i resolves every binary after ~/.bashrc reset PATH")
	assert.Contains(t, out, "SHELL_IS=/bin/bash", "no switch without a terminal")

	// zsh: every binary resolves and startup writes nothing to stderr.
	out, stderr, err := run("", false, "/bin/zsh", "-i", "-c", check.String())
	require.NoError(t, err)
	assert.NotContains(t, out, "MISSING")
	assert.Empty(t, stderr, "zsh -i startup writes nothing to stderr")
	out, _, err = run("", false, "/bin/zsh", "-i", "-c", "starship --version")
	require.NoError(t, err, "a mise tool runs in the session")
	assert.Contains(t, out, "starship")

	// With a terminal, an interactive bash switches to a zsh login shell:
	// script(1) gives bash a pseudo terminal and feeds it our commands. No
	// new-user wizard and no command-not-found output may appear.
	out, _, err = run("echo SWITCHED=$ZSH_VERSION\nexit\n", false, "/usr/bin/script", "-qec", "/bin/bash -i", "/dev/null")
	require.NoError(t, err)
	assert.Regexp(t, `SWITCHED=5\.`, out, "bash -i with a terminal switched to zsh")
	assert.NotContains(t, out, "zsh-newuser-install")
	assert.NotContains(t, out, "This is the Z Shell configuration function")
	assert.NotContains(t, out, "command not found")
	// The escape hatch keeps bash.
	out, _, err = run("echo SWITCHED=${ZSH_VERSION:-no}\nexit\n", false, "/usr/bin/script", "-qec", "env SANDCAST_NO_SHELL_SWITCH=1 /bin/bash -i", "/dev/null")
	require.NoError(t, err)
	assert.Contains(t, out, "SWITCHED=no")
	// Non-interactive bash with a terminal runs the command itself.
	out, _, err = run("", true, "/bin/bash", "-lc", "echo RAN=$0")
	require.NoError(t, err)
	assert.Contains(t, out, "RAN=/bin/bash")

	// The final user's login shell is zsh.
	out, _, err = run("", false, "/bin/sh", "-c", "getent passwd sandbox | cut -d: -f7")
	require.NoError(t, err)
	assert.Equal(t, "/bin/zsh\n", strings.ReplaceAll(out, "\r", ""))

	// SC-004: the same image without its zshenv block fails verify with
	// session.zsh entries, although the image-environment checks pass.
	build := exec.Command(engine, "build", "-t", broken, "-f", "-", dir)
	build.Stdin = strings.NewReader("FROM " + img + "\nUSER root\nRUN sed -i '/sandcast/d' /etc/zsh/zshenv\nUSER sandbox\n")
	bout, err := build.CombinedOutput()
	require.NoError(t, err, "%s", bout)
	r = sandcast(t, dir, nil, "verify", "--image", broken)
	assert.Equal(t, 1, r.code, "verify fails the broken session setup")
	results := readVerifyResults(t, filepath.Join(dir, ".sandcast", "build", "openshell", "verify.yaml"))
	failedSession := 0
	for key, res := range results {
		if !strings.HasPrefix(key, "session.") {
			assert.True(t, res.OK, "%s passes with the image environment", key)
			continue
		}
		if strings.HasPrefix(key, "session.zsh.") && !res.OK {
			failedSession++
			assert.Equal(t, "session:zsh", res.Condition)
		}
	}
	assert.Positive(t, failedSession, "session.zsh entries fail")
	assert.False(t, results["session.zsh.startup"].OK)
}

type verifyEntry struct {
	OK        bool
	Condition string
}

func readVerifyResults(t *testing.T, path string) map[string]verifyEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var v struct {
		Results []struct {
			ComponentKey string `yaml:"component_key"`
			Condition    string `yaml:"condition"`
			OK           bool   `yaml:"ok"`
		} `yaml:"results"`
	}
	require.NoError(t, yaml.Unmarshal(data, &v))
	out := map[string]verifyEntry{}
	for _, r := range v.Results {
		out[r.ComponentKey] = verifyEntry{OK: r.OK, Condition: r.Condition}
	}
	return out
}

// TestLockfileDecidesVersions reproduces issues #1 and #2 on the community
// base image, which ships gh, node, python and codex, with python first on
// PATH under /sandbox: the versions verify reports equal mise.lock, every
// differing component carries a notice, python is not taken from the
// workspace path, and no adapted profile names a binary under /sandbox
// (SC-001, SC-002).
func TestLockfileDecidesVersions(t *testing.T) {
	engine := requireEngine(t)
	requireMise(t)
	img := "localhost/sandcast-it-lock:latest"
	dir := project(t, `schema_version: 1
name: sc-lock
base_image: nvidia-community-base
components:
  - key: gh
  - key: node
  - key: python
  - key: codex
targets:
  openshell:
    image: localhost/sandcast-it-lock
`)
	removeImages(t, engine, img)
	require.Zero(t, sandcast(t, dir, nil, "lock", "--platform", hostPlatform()).code)
	r := sandcast(t, dir, nil, "build", "--verify", "--json")
	require.Zero(t, r.code, "build --verify")
	var rep struct {
		Notices []string `json:"notices"`
		Verify  struct {
			Results []struct {
				ComponentKey string `json:"component_key"`
				Condition    string `json:"condition"`
				Version      string `json:"version"`
				OK           bool   `json:"ok"`
			} `json:"results"`
		} `json:"verify"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &rep))

	locked := lockedVersions(t, filepath.Join(dir, ".sandcast", "mise.lock"))
	noticeFor := func(key string, kinds ...string) bool {
		for _, n := range rep.Notices {
			for _, k := range kinds {
				if strings.HasPrefix(n, k+": "+key+": ") {
					return true
				}
			}
		}
		return false
	}
	for _, key := range []string{"gh", "node", "python", "codex"} {
		want, ok := locked[key]
		require.True(t, ok, "%s is locked", key)
		found := false
		for _, res := range rep.Verify.Results {
			if res.ComponentKey == key && res.Condition == "image" {
				found = true
				assert.True(t, res.OK, key)
				assert.Equal(t, want, res.Version, "%s: verify reports the locked version (SC-001)", key)
			}
		}
		assert.True(t, found, "%s verified", key)
		assert.True(t, noticeFor(key, "lockfile-shadow", "unknown-version"), "%s: the base image version differs and a notice says so", key)
	}
	assert.True(t, noticeFor("python", "workspace-path"), "python's first PATH hit lies under /sandbox")

	// SC-002: no resolved binary under the workspace path.
	profiles := filepath.Join(dir, ".sandcast", "build", "openshell", "profiles")
	entries, err := os.ReadDir(profiles)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() == "index.yaml" || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(profiles, e.Name()))
		require.NoError(t, err)
		var p struct {
			Binaries []string `yaml:"binaries"`
		}
		require.NoError(t, yaml.Unmarshal(data, &p))
		for _, b := range p.Binaries {
			assert.False(t, strings.HasPrefix(b, "/sandbox/"), "%s lists %s", e.Name(), b)
		}
	}
	out, err := exec.Command(engine, "image", "inspect", "--format", `{{index .Config.Labels "io.sandcast.profiles-index"}}`, img).Output()
	require.NoError(t, err)
	var label struct {
		IDs     []string `json:"ids"`
		Flagged []string `json:"flagged"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(out), &label), "label %q", out)
	assert.Contains(t, label.IDs, "pypi")
	assert.NotContains(t, label.Flagged, "pypi", "the adapted pypi profile is not flagged (SC-002)")
}

// lockedVersions returns the first locked version of every tool in a mise
// lockfile, keyed by tool name.
func lockedVersions(t *testing.T, path string) map[string]string {
	t.Helper()
	var lock struct {
		Tools map[string][]struct {
			Version string `toml:"version"`
		} `toml:"tools"`
	}
	_, err := toml.DecodeFile(path, &lock)
	require.NoError(t, err)
	out := map[string]string{}
	for tool, entries := range lock.Tools {
		if len(entries) > 0 {
			out[tool] = entries[0].Version
		}
	}
	return out
}
