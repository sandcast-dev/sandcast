package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
)

// writeFile creates a regular file with content, making parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func dotfileEnv(home, root string) Env {
	return Env{Home: home, Layout: fsutil.Layout{Root: root}}
}

func mdotfiles(paths ...string) *manifest.Manifest {
	m := &manifest.Manifest{}
	for _, p := range paths {
		m.Dotfiles = append(m.Dotfiles, manifest.Dotfile{Path: p})
	}
	return m
}

// TestDotfileInputsAllowlistAndExplicit checks the host-reading path tags an
// allowlisted home dotfile as from the allowlist and an explicit absolute path
// as explicit, reading each one's content (spec 003 FR-005/FR-006, US3).
func TestDotfileInputsAllowlistAndExplicit(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\temail = a@b.c\n")
	abs := filepath.Join(root, "custom.conf")
	writeFile(t, abs, "key = value\n")

	out, notices := dotfileInputs(dotfileEnv(home, root), mdotfiles("~/.gitconfig", abs))
	require.Empty(t, notices)
	require.Len(t, out, 2)

	byPath := map[string]bool{}
	for _, in := range out {
		byPath[in.HostPath] = in.FromAllowlist
	}
	assert.True(t, byPath["~/.gitconfig"], "~/.gitconfig is tagged as from the allowlist")
	assert.False(t, byPath[abs], "an explicit absolute path is not from the allowlist")
}

// TestDotfileInputsMissingSkipped checks an explicit dotfile that does not
// exist on the host is reported and skipped rather than failing the plan
// (spec 003 Edge Cases).
func TestDotfileInputsMissingSkipped(t *testing.T) {
	home := t.TempDir()
	out, notices := dotfileInputs(dotfileEnv(home, t.TempDir()), mdotfiles("~/.does-not-exist"))
	assert.Empty(t, out)
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "does not exist")
}

// TestDotfileInputsUnresolvedSkipped checks a home-relative dotfile with no
// known home is reported and skipped.
func TestDotfileInputsUnresolvedSkipped(t *testing.T) {
	out, notices := dotfileInputs(dotfileEnv("", ""), mdotfiles("~/.gitconfig"))
	assert.Empty(t, out)
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "could not be resolved")
}

// TestDotfileInputsHomeSymlinkEscapeSkipped checks a home-relative dotfile that
// is a symlink resolving outside the home is skipped with a notice (spec 003
// Edge Cases, R6).
func TestDotfileInputsHomeSymlinkEscapeSkipped(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret")
	writeFile(t, target, "secret\n")
	require.NoError(t, os.Symlink(target, filepath.Join(home, ".gitconfig")))

	out, notices := dotfileInputs(dotfileEnv(home, t.TempDir()), mdotfiles("~/.gitconfig"))
	assert.Empty(t, out, "the escaping symlink is not baked")
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "outside the home")
}

// TestDotfileInputsProjectSymlinkEscapeSkipped checks a project-relative
// dotfile that resolves outside the project root is skipped with a notice,
// matching the skill-copy containment rule (spec 003 Edge Cases, R6).
func TestDotfileInputsProjectSymlinkEscapeSkipped(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret")
	writeFile(t, target, "secret\n")
	require.NoError(t, os.Symlink(target, filepath.Join(root, "evil.conf")))

	out, notices := dotfileInputs(dotfileEnv(t.TempDir(), root), mdotfiles("evil.conf"))
	assert.Empty(t, out, "the escaping project symlink is not baked")
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "outside the project")
}

// TestDotfileInputsProjectRelativeInsideKept checks a project-relative dotfile
// that stays inside the project root is read and baked.
func TestDotfileInputsProjectRelativeInsideKept(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "tool.conf"), "ok\n")
	out, notices := dotfileInputs(dotfileEnv(t.TempDir(), root), mdotfiles("sub/tool.conf"))
	require.Empty(t, notices)
	require.Len(t, out, 1)
	assert.Equal(t, "sub/tool.conf", out[0].HostPath)
	assert.False(t, out[0].FromAllowlist)
	assert.Equal(t, "ok\n", out[0].Content)
}
