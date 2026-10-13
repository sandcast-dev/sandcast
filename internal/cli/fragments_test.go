package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/plan"
)

func TestFragmentsWriteListAndBuildReuse(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: broken-tool\n", 1))

	ta.Stdin = strings.NewReader("USER root\nRUN false\nUSER sandbox\n")
	require.Equal(t, 0, ta.run("fragments", "write", "broken-tool"), ta.stderr.String())
	assert.Contains(t, ta.stdout.String(), ".sandcast/fragments/broken-tool.containerfile")

	require.Equal(t, 0, ta.run("fragments", "list", "--json"))
	var frags []build.Fragment
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &frags))
	require.Len(t, frags, 1)
	assert.Equal(t, "active", frags[0].Status)

	// The non-interactive plan uses the stored fragment.
	require.Equal(t, 0, ta.run("plan", "--json"))
	var p plan.Plan
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &p))
	c := p.Component("broken-tool")
	require.NotNil(t, c)
	assert.Equal(t, plan.KindFragment, c.InstallerKind)
	assert.Empty(t, p.Blocking())

	ta.Stdin = strings.NewReader("FROM ubuntu\n")
	assert.Equal(t, ExitUsage, ta.run("fragments", "write", "bad"))
	ta.Stdin = strings.NewReader("RUN echo ghp_" + strings.Repeat("k", 36) + "\n")
	assert.Equal(t, ExitUsage, ta.run("fragments", "write", "leaky"))
}

func TestPromoteAndLearnings(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	rc := filepath.Join(t.TempDir(), "frag")
	require.NoError(t, os.WriteFile(rc, []byte("USER root\nRUN install -m 0755 /dev/null /usr/local/bin/mytool\nUSER sandbox\n"), 0o644))
	require.Equal(t, 0, ta.run("fragments", "write", "mytool", "--file", rc))

	ta.Stdin = strings.NewReader("n\n")
	assert.Equal(t, ExitFailure, ta.run("promote", "mytool"))
	assert.Contains(t, ta.stdout.String(), "not promoted")
	_, err := os.Stat(filepath.Join(ta.dir, ".sandcast", "recipes", "mytool.yaml"))
	assert.True(t, os.IsNotExist(err))

	require.Equal(t, 0, ta.run("promote", "mytool", "--bins", "mytool", "--yes"), ta.stderr.String())
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "recipes", "mytool.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "promoted_from: fragments/mytool.containerfile")
	frag, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "fragments", "mytool.containerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(frag), "status=superseded")

	require.Equal(t, 0, ta.run("components", "show", "mytool"))
	assert.Contains(t, ta.stdout.String(), "project tier")

	require.Equal(t, 0, ta.run("learnings", "add", "--component", "mytool", "--attempt", "1", "--failure", "tar: not found", "--change", "nested path", "--outcome", "fixed", "--date", "2026-10-08"))
	list, err := build.LoadLearnings(filepath.Join(ta.dir, ".sandcast", "learnings.yaml"))
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "fixed", list[0].Outcome)
	assert.Equal(t, ExitUsage, ta.run("learnings", "add", "--component", "mytool", "--failure", "x", "--change", "y", "--outcome", "maybe"))
}

// TestPromoteDefaultsToDeclaredBins covers FR-018: without --bins and
// without a definition, promote takes the manifest entry's bins; --bins
// still wins.
func TestPromoteDefaultsToDeclaredBins(t *testing.T) {
	ta := newTestApp(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: helix\n    optional: true\n    bins: [hx]\n", 1))
	ta.write(t, ".sandcast/mise.lock", testLock)
	rc := filepath.Join(t.TempDir(), "frag")
	require.NoError(t, os.WriteFile(rc, []byte("USER root\nRUN install -m 0755 /dev/null /usr/local/bin/hx\nUSER sandbox\n"), 0o644))
	require.Equal(t, 0, ta.run("fragments", "write", "helix", "--file", rc), ta.stderr.String())
	require.Equal(t, 0, ta.run("promote", "helix", "--yes"), ta.stderr.String())
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "recipes", "helix.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "bins:\n      - hx\n")
	assert.NotContains(t, string(data), "- helix\n")

	ta = newTestApp(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: helix\n    optional: true\n    bins: [hx]\n", 1))
	require.Equal(t, 0, ta.run("fragments", "write", "helix", "--file", rc), ta.stderr.String())
	require.Equal(t, 0, ta.run("promote", "helix", "--bins", "helix-term", "--yes"), ta.stderr.String())
	data, err = os.ReadFile(filepath.Join(ta.dir, ".sandcast", "recipes", "helix.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "- helix-term\n", "--bins wins")
}
