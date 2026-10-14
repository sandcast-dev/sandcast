package capture

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAllowlisted(t *testing.T) {
	assert.True(t, IsAllowlisted("~/.gitconfig"))
	assert.True(t, IsAllowlisted(".gitconfig"))
	assert.True(t, IsAllowlisted("~/.config/starship.toml"))
	assert.False(t, IsAllowlisted("~/.config/nvim/init.lua"))
	assert.False(t, IsAllowlisted("/etc/hosts"))
}

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

func TestIsCredentialName(t *testing.T) {
	assert.True(t, isCredentialName(".env"))
	assert.True(t, isCredentialName(".config/app/token.conf"))
	assert.True(t, isCredentialName("id_rsa.pem"))
	assert.False(t, isCredentialName(".gitconfig"))
	assert.False(t, isCredentialName(".tmux.conf"))
}
