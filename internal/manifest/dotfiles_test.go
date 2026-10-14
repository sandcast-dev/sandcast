package manifest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAllowlisted(t *testing.T) {
	assert.True(t, IsAllowlisted("~/.gitconfig"))
	assert.True(t, IsAllowlisted(".gitconfig"))
	assert.True(t, IsAllowlisted("~/.config/starship.toml"))
	assert.False(t, IsAllowlisted("~/.config/nvim/init.lua"))
	assert.False(t, IsAllowlisted("/etc/hosts"))
}

// TestIsCredentialName checks every path segment is examined, not only the
// base name, and that case does not matter.
func TestIsCredentialName(t *testing.T) {
	for _, bad := range []string{
		".env", ".env.local", ".config/app/token.conf", "id_rsa.pem", "conf/deploy.key",
		".config/gcloud/legacy_credentials/a/adc.json", ".password-store/x.gpg", "secrets/plain.txt",
		".config/App/TOKENS/x", "Deploy.KEY",
	} {
		assert.True(t, IsCredentialName(bad), bad)
	}
	for _, ok := range []string{".gitconfig", ".tmux.conf", ".config/git/config", ".config/nvim/init.lua", "environment.conf"} {
		assert.False(t, IsCredentialName(ok), ok)
	}
}

// TestHomeDotfileAllowed checks the home bound: the allowlist and the
// ConfigDotfileAllowlist entries, and nothing else under ~/.config or the
// home. A differently spelled path is refused, not resolved.
func TestHomeDotfileAllowed(t *testing.T) {
	for _, ok := range []string{
		".gitconfig", ".config/git/config", ".tmux.conf", ".inputrc", ".config/starship.toml", "./.gitconfig",
		".config/git/ignore", ".config/nvim/init.lua", ".config/nvim/lua/plugins/init.lua", ".config/helix/config.toml",
		".config/tmux/tmux.conf", ".config/alacritty/alacritty.toml", ".config/kitty/kitty.conf", ".config/wezterm/wezterm.lua",
		".config/ghostty/config", ".config/bat/config", ".config/ripgrep/config",
	} {
		assert.True(t, HomeDotfileAllowed(ok), ok)
	}
	for _, no := range []string{
		".aws/credentials", ".netrc", ".ssh/config", ".bash_history", "notes.txt", ".config", ".config/../.ssh/id_rsa", "..", ".",
		".config/tool/settings.toml", ".config/sops/age/keys.txt", ".config/op/config", ".config/gcloud/legacy_credentials/a/adc.json",
		".config/gh/hosts.yml", ".config/GH/hosts.yml", ".config/nvimrc", ".config/nvim2/init.lua", ".config/Nvim/init.lua",
		".config/starship.toml.bak", "/.config/git/config",
	} {
		assert.False(t, HomeDotfileAllowed(no), no)
	}
	// The directory itself passes the path check (a later regular-file check
	// skips it), so a name below it is never refused for being a directory.
	assert.True(t, HomeDotfileAllowed(".config/git"))
	assert.Contains(t, HomeDotfileBound(), "~/.gitconfig")
	assert.Contains(t, HomeDotfileBound(), "these ~/.config entries: git/, starship.toml, nvim/", "entries are shown relative to ~/.config")
}

// TestDotfileDenied checks the denylist: the credential store directories
// (also the ~/.config ones), .netrc, history files and credential-looking
// segments, all without regard to case.
func TestDotfileDenied(t *testing.T) {
	for _, bad := range []string{
		".ssh/config", ".aws/credentials", ".gnupg/pubring.kbx", ".kube/config", ".docker/config.json",
		".config/gh/hosts.yml", ".config/gh", ".netrc", ".bash_history", ".zsh_history", ".config/x/.python_history",
		"sub/.ssh/id_rsa", ".config/tool/api_token", "conf/deploy.key", ".env",
		".config/sops/age/keys.txt", ".config/op/config", ".config/gcloud/legacy_credentials/a/adc.json",
		".config/containers/auth.json", ".config/rclone/rclone.conf", ".config/hcloud/cli.toml", ".config/doctl/config.yaml",
		".config/wrangler/config/default.toml", ".config/helm/registry/config.json",
		".config/GH/hosts.yml", ".CONFIG/Sops/age/keys.txt", ".SSH/config", "sub/.AWS/credentials", ".Netrc", ".BASH_HISTORY",
		".config/foo/Credentials-cache/x.json", "Secrets/x.txt",
	} {
		assert.NotEmpty(t, DotfileDenied(bad), bad)
	}
	for _, ok := range []string{".gitconfig", ".config/git/config", ".config/ghostty/config", "conf/tool.conf", ".tmux.conf", ".config/helm/repositories.yaml", "history-notes/readme.md"} {
		assert.Empty(t, DotfileDenied(ok), ok)
	}
	assert.Equal(t, "it is in the credential store .config/gh", DotfileDenied(".config/GH/hosts.yml"), "the store is named in lower case")
}
