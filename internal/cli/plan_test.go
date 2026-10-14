package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/plan"
)

func TestProbe(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	require.Equal(t, 0, ta.run("probe", "--json"), ta.stderr.String())
	var rep probeReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, communityBase, rep.Reference)
	assert.Equal(t, "apt-get", rep.Facts.PackageManager)
	assert.True(t, rep.ContainerStarted)
	actions := map[string]string{}
	for _, d := range rep.Delta {
		actions[d.ComponentKey] = d.Action
	}
	assert.Equal(t, "skip", actions["git"])
	assert.Equal(t, "install", actions["go"])

	require.Equal(t, 0, ta.run("probe"))
	assert.Contains(t, ta.stderr.String(), "probe cache hit")
}

func TestProbeReferenceWithoutManifest(t *testing.T) {
	ta := newTestApp(t)
	require.Equal(t, 0, ta.run("probe", communityBase, "--json"), ta.stderr.String())
	var rep probeReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, communityBase, rep.Reference)
	assert.Empty(t, rep.Delta, "no manifest, no components to compare")
	assert.NotEmpty(t, rep.Facts.PackageManager)

	assert.Equal(t, ExitUsage, ta.run("probe"), "neither a manifest nor a reference")
}

func TestPlanWritesOutputsAndIsCached(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	require.Equal(t, 0, ta.run("plan"), ta.stderr.String())
	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	first, err := os.ReadFile(filepath.Join(dir, "plan.yaml"))
	require.NoError(t, err)
	for _, f := range []string{"mise/mise.toml", "mise/mise.lock", "policy.yaml"} {
		_, err := os.Stat(filepath.Join(dir, f))
		assert.NoError(t, err, f)
	}
	assert.Contains(t, ta.stdout.String(), "wrote .sandcast/build/openshell/plan.yaml")
	runs := ta.eng.CountOp("run")

	require.Equal(t, 0, ta.run("plan"))
	assert.Equal(t, runs, ta.eng.CountOp("run"), "second plan starts no container (SC-004)")
	assert.Contains(t, ta.stderr.String(), "probe cache hit")
	second, err := os.ReadFile(filepath.Join(dir, "plan.yaml"))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "byte-identical plans (SC-002)")

	require.Equal(t, 0, ta.run("plan", "--json"))
	var p plan.Plan
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &p))
	assert.Equal(t, "host", p.Platform.Source)
}

func TestPlanPlatformPrecedence(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "base_image: nvidia-community-base\n", "base_image: nvidia-community-base\nplatform: linux/arm64\n", 1))
	require.Equal(t, 0, ta.run("plan", "--json"))
	var p plan.Plan
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &p))
	assert.Equal(t, plan.Selected{Value: "linux/arm64", Source: "manifest"}, p.Platform)

	// The flag wins; the lockfile has no linux-x64 entry, so plan exits 2
	// without writing.
	require.NoError(t, os.RemoveAll(filepath.Join(ta.dir, ".sandcast", "build")))
	assert.Equal(t, ExitUsage, ta.run("plan", "--platform", "linux/amd64"))
	assert.Contains(t, ta.stderr.String(), "linux-x64")
	_, err := os.Stat(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "plan.yaml"))
	assert.True(t, os.IsNotExist(err), "no partial output")
	assert.Equal(t, ExitUsage, ta.run("plan", "--platform", "darwin/arm64"))
}

func TestPlanConflictsExit2WithoutOutput(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/components/rg-a.yaml", "key: rg-a\nname: a\ninstaller: {script: {run: \"true\"}}\ncheck: {command: rg --version}\nprovides: [rg]\n")
	ta.write(t, ".sandcast/components/rg-b.yaml", "key: rg-b\nname: b\ninstaller: {script: {run: \"true\"}}\ncheck: {command: rg --version}\nprovides: [rg]\n")
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: rg-a\n  - key: rg-b\n", 1))
	assert.Equal(t, ExitUsage, ta.run("plan"))
	assert.Contains(t, ta.stderr.String(), "binary rg")
	_, err := os.Stat(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "plan.yaml"))
	assert.True(t, os.IsNotExist(err))
}

func TestPlanUnknownTarget(t *testing.T) {
	ta := newTestApp(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  container:\n    image: localhost/demo-plain\n", "", 1))
	ta.write(t, ".sandcast/mise.lock", testLock)
	assert.Equal(t, ExitUsage, ta.run("plan", "--target", "container"))
	assert.Contains(t, ta.stderr.String(), "declares no container target")
}

// TestPlanRecordsDotfilesAndRelocations covers the manifest-to-plan flow for
// dotfiles from the CLI: project-relative sources are read, redacted,
// relocated (git's two files merged) and written to plan.yaml with one
// relocation per source, and sandcast build stages each one in the build
// context (FINDING-3).
func TestPlanRecordsDotfilesAndRelocations(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "targets:",
		"dotfiles:\n  - path: conf/.gitconfig\n  - path: conf/.config/git/config\n  - path: conf/.tmux.conf\ntargets:", 1))
	token := "ghp_" + strings.Repeat("a", 36)
	ta.write(t, "conf/.gitconfig", "[user]\n\temail = me@example.com\n[github]\n\ttoken = "+token+"\n")
	ta.write(t, "conf/.config/git/config", "[core]\n\tpager = less\n")
	ta.write(t, "conf/.tmux.conf", "set -g mouse on\n")

	p := planJSON(t, ta)
	require.Len(t, p.Dotfiles, 2)
	git, tmux := p.Dotfiles[0], p.Dotfiles[1]
	assert.Equal(t, plan.GitConfigPath, git.RelocatedTo)
	assert.Equal(t, "conf/.gitconfig", git.HostPath)
	assert.Equal(t, []string{"conf/.config/git/config"}, git.MergedFrom)
	assert.Contains(t, git.Content, "pager = less")
	assert.Contains(t, git.Content, "email = me@example.com")
	assert.NotContains(t, git.Content, token, "the credential is redacted")
	assert.Equal(t, plan.TmuxConfPath, tmux.RelocatedTo)
	assert.Equal(t, []plan.RelocationNotice{
		{Kind: plan.NoticeRelocated, Subject: "conf/.config/git/config", To: plan.GitConfigPath},
		{Kind: plan.NoticeRelocated, Subject: "conf/.gitconfig", To: plan.GitConfigPath},
		{Kind: plan.NoticeRelocated, Subject: "conf/.tmux.conf", To: plan.TmuxConfPath},
	}, p.Relocations)

	require.Equal(t, 0, ta.run("plan"), ta.stderr.String())
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "plan.yaml"))
	require.NoError(t, err)
	doc := string(data)
	assert.Contains(t, doc, "dotfiles:\n")
	assert.Contains(t, doc, "merged_from:\n")
	assert.Contains(t, doc, "relocated_to: /etc/gitconfig\n")
	assert.Contains(t, doc, "relocations:\n")
	assert.NotContains(t, doc, token)

	require.Equal(t, 0, ta.run("build"), ta.stderr.String())
	staged, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "dotfiles", "etc%2Fgitconfig"))
	require.NoError(t, err)
	assert.Equal(t, git.Content, string(staged), "the build context carries the redacted, merged content")
}

// TestPlanUsesRegistryOvermountHome checks a user registry entry's
// overmount_home reaches the plan (FINDING-28): the plan records it, and
// the Containerfile's WORKDIR follows it.
func TestPlanUsesRegistryOvermountHome(t *testing.T) {
	const ref = "quay.io/example/ws:1"
	ta := newTestApp(t)
	ta.project(t)
	require.NoError(t, os.WriteFile(filepath.Join(os.Getenv("SANDCAST_CONFIG_DIR"), "base-images.yaml"), []byte(
		"schema_version: 1\ntargets:\n  openshell:\n    - name: ws-base\n      ref: "+ref+"\n      overmount_home: /workspace\n"), 0o644))
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "base_image: nvidia-community-base\n", "base_image: ws-base\n", 1))
	ta.eng.Digests[ref] = "sha256:" + strings.Repeat("cd", 32)

	p := planJSON(t, ta)
	assert.Equal(t, "/workspace", p.BaseImage.OvermountHome)
	require.Equal(t, 0, ta.run("build"), ta.stderr.String())
	cf, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "Containerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(cf), "WORKDIR /workspace\n")

	// Without the field the default applies.
	ta.write(t, ".sandcast/manifest.yaml", testManifest)
	assert.Equal(t, plan.DefaultOvermountHome, planJSON(t, ta).BaseImage.OvermountHome)
}
