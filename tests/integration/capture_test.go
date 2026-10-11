//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCaptureManifestBuilds covers SC-003: the manifest recorded from a
// capture of the reference fixture (tests/fixtures/reference-project/
// expected-manifest.yaml) locks, builds and verifies without a repair
// iteration, and contains no credential value (SC-009).
func TestCaptureManifestBuilds(t *testing.T) {
	engine := requireEngine(t)
	requireMise(t)
	data, err := os.ReadFile("../fixtures/reference-project/expected-manifest.yaml")
	require.NoError(t, err)
	manifest := strings.Replace(string(data), "image: localhost/reference-project", "image: localhost/sandcast-it-capture", 1)
	dir := project(t, manifest)
	removeImages(t, engine, "localhost/sandcast-it-capture:latest")

	require.Zero(t, sandcast(t, dir, nil, "manifest", "validate").code)
	require.Zero(t, sandcast(t, dir, nil, "lock", "--platform", hostPlatform()).code)
	require.Zero(t, sandcast(t, dir, nil, "build", "--verify").code, "the captured manifest builds without a repair iteration")

	verify := readVerify(t, filepath.Join(dir, ".sandcast", "build", "openshell", "verify.yaml"))
	for _, k := range []string{"go", "python", "uv", "starship", "fzf", "lsd", "zsh", "claude-code"} {
		assert.True(t, verify[k], "%s verified", k)
	}
	for _, f := range []string{".sandcast/manifest.yaml", ".sandcast/build/openshell/plan.yaml"} {
		content, err := os.ReadFile(filepath.Join(dir, f))
		require.NoError(t, err)
		for _, shape := range []string{"sk-ant-", "ghp_", "glpat-", "github_pat_"} {
			assert.NotContains(t, string(content), shape, "%s contains no credential (SC-009)", f)
		}
	}
}
