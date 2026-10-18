package capture

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/manifest"
)

func TestProposeDotfilesFindsAllowlistAndSkips(t *testing.T) {
	home := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	write(".gitconfig", "[user]\n\temail = a@b.c\n")
	write(".config/starship.toml", "add_newline = false\n")
	// .inputrc and .tmux.conf and .config/git/config are absent: skipped silently.

	props, skipped := ProposeDotfiles(home)
	var paths []string
	for _, p := range props {
		paths = append(paths, p.Path)
	}
	assert.Contains(t, paths, "~/.gitconfig")
	assert.Contains(t, paths, "~/.config/starship.toml")
	assert.NotContains(t, paths, "~/.tmux.conf")
	assert.Empty(t, skipped, "absent allowlist entries are skipped silently")
}

func TestProposeDotfilesSkipsSymlinkOutsideHome(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret-git")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(home, ".gitconfig")))

	props, skipped := ProposeDotfiles(home)
	assert.Empty(t, props, "a symlink leaving the home is not proposed")
	require.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "outside the home")
}

// TestProposeDotfilesResolvesParentSymlink checks a leaf that is a regular
// file below a symlinked parent (~/.config pointing out of the home) is not
// proposed: the pipeline resolves the whole path and would skip it later.
// A symlink that stays inside the home but lands in a credential store is
// not proposed either, for the same reason.
func TestProposeDotfilesResolvesParentSymlink(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(elsewhere, "starship.toml"), []byte("add_newline = false\n"), 0o644))
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(home, ".config")))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host x\n"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(home, ".ssh", "config"), filepath.Join(home, ".gitconfig")))

	props, skipped := ProposeDotfiles(home)
	assert.Empty(t, props, "nothing outside the bounds is proposed")
	require.Len(t, skipped, 2, "%v", skipped)
	assert.Contains(t, skipped[1], "~/.config/starship.toml resolves to")
	assert.Contains(t, skipped[1], "outside the home")
	assert.Contains(t, skipped[0], "~/.gitconfig")
	assert.Contains(t, skipped[0], "credential store .ssh")
}

// TestProposeDotfilesSkipsLargeFile checks the capture proposal applies the
// same size cap the pipeline reads with, so a proposal is never rejected
// later.
func TestProposeDotfilesSkipsLargeFile(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, ".inputrc"), make([]byte, manifest.MaxDotfileSize+1), 0o644))
	props, skipped := ProposeDotfiles(home)
	assert.Empty(t, props)
	require.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "larger than 256 KiB")
}

// TestProposeDotfilesUnresolvableHome checks a home that cannot be resolved
// is reported instead of proposing nothing silently.
func TestProposeDotfilesUnresolvableHome(t *testing.T) {
	props, skipped := ProposeDotfiles(filepath.Join(t.TempDir(), "missing"))
	assert.Empty(t, props)
	require.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "cannot be resolved")
}

// TestProposeDotfilesReportsUnreadableParent checks an allowlist entry
// below a directory that cannot be read is reported as skipped, not
// silently treated as absent: components match promises a notice for every
// entry found but not proposed. Root reads any directory, so the test skips
// there.
func TestProposeDotfilesReportsUnreadableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any directory")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, ".config")
	require.NoError(t, os.MkdirAll(cfg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfg, "starship.toml"), []byte("add_newline = false\n"), 0o644))
	require.NoError(t, os.Chmod(cfg, 0o000))
	t.Cleanup(func() { _ = os.Chmod(cfg, 0o755) })

	props, skipped := ProposeDotfiles(home)
	assert.Empty(t, props)
	require.Len(t, skipped, 2, "starship.toml and git/config below the unreadable ~/.config: %v", skipped)
	for _, s := range skipped {
		assert.Contains(t, s, "cannot be checked")
		assert.Contains(t, s, "permission denied")
	}
	assert.Contains(t, skipped[0], "~/.config/git/config")
	assert.Contains(t, skipped[1], "~/.config/starship.toml")
}
