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
