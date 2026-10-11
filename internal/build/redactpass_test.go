package build

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/profile"
)

func TestRedactPass(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "profiles"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Containerfile"), []byte("FROM x\nRUN echo ok\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profiles", "a.yaml"), []byte("id: a\nbinaries: [/usr/bin/a]\n"), 0o644))
	require.NoError(t, RedactPass(dir))

	token := "ghp_" + strings.Repeat("A", 36)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build.log"), []byte("cloning with "+token+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profiles", "b.yaml"), []byte("id: b\nenv:\n  GITHUB_TOKEN: "+token+"\n"), 0o644))
	err := RedactPass(dir)
	var se *SecretError
	require.True(t, errors.As(err, &se))
	joined := strings.Join(se.Findings, "\n")
	assert.Contains(t, joined, "build.log")
	assert.Contains(t, joined, "profiles/b.yaml env.GITHUB_TOKEN")
	assert.NotContains(t, joined, token)
}

func TestRedactPassSkipsOnlyNamedTopLevelEntries(t *testing.T) {
	dir := t.TempDir()
	token := "ghp_" + strings.Repeat("C", 36)
	for _, f := range []string{"build.log", "profiles/a.yaml", "harness/x/build.log"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("t: "+token+"\n"), 0o644))
	}
	err := RedactPass(dir, "build.log", "profiles")
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	joined := strings.Join(se.Findings, "\n")
	assert.Contains(t, joined, "harness/x/build.log", "a skipped name only applies at the top level")
	assert.NotContains(t, joined, "profiles/a.yaml")
	assert.NotContains(t, strings.ReplaceAll(joined, "harness/x/build.log", ""), "build.log")
}

// TestRedactPassAcceptsEveryShippedProfile adapts every vendored and overlay
// profile and every shipped component, policy and registry file, and checks
// that the output scan finds nothing: the scan must never fail a build on
// sandcast's own content.
func TestRedactPassAcceptsEveryShippedProfile(t *testing.T) {
	set, _, err := profile.Resolve(sandcast.UpstreamProfiles, sandcast.OverlayProfiles, nil)
	require.NoError(t, err)
	binaries := map[string][]string{}
	for _, id := range set.IDs() {
		binaries[id] = []string{"/usr/bin/tool"}
	}
	for _, suffix := range []bool{false, true} {
		adapted, err := profile.Adapt(set, binaries, profile.AdaptOptions{
			Suffix: suffix, ImageLabel: "demo",
			Image: profile.ImageRef{Name: "localhost/demo", Tag: "latest", Digest: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64"},
		})
		require.NoError(t, err)
		dir := t.TempDir()
		require.NoError(t, adapted.WriteDir(filepath.Join(dir, ProfilesDirName)))
		assert.NoError(t, RedactPass(dir), "suffix=%v", suffix)
	}

	dir := t.TempDir()
	for _, fsys := range []fs.FS{sandcast.UpstreamProfiles, sandcast.OverlayProfiles, sandcast.Components, sandcast.Policy, sandcast.BaseImages} {
		require.NoError(t, fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			target := filepath.Join(dir, strings.ReplaceAll(p, "/", "_")+"-"+filepath.Base(p))
			return os.WriteFile(target, data, 0o644)
		}))
	}
	assert.NoError(t, RedactPass(dir), "shipped files")
}
