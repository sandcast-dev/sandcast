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

func TestIsCredentialName(t *testing.T) {
	assert.True(t, IsCredentialName(".env"))
	assert.True(t, IsCredentialName(".config/app/token.conf"))
	assert.True(t, IsCredentialName("id_rsa.pem"))
	assert.False(t, IsCredentialName(".gitconfig"))
	assert.False(t, IsCredentialName(".tmux.conf"))
}

func TestHomeDotfileAllowed(t *testing.T) {
	for _, ok := range []string{".gitconfig", ".config/git/config", ".tmux.conf", ".inputrc", ".config/starship.toml", ".config/tool/settings.toml", "./.gitconfig"} {
		assert.True(t, HomeDotfileAllowed(ok), ok)
	}
	for _, no := range []string{".aws/credentials", ".netrc", ".ssh/config", ".bash_history", "notes.txt", ".config", ".config/../.ssh/id_rsa", "..", "."} {
		assert.False(t, HomeDotfileAllowed(no), no)
	}
}

func TestDotfileDenied(t *testing.T) {
	for _, bad := range []string{
		".ssh/config", ".aws/credentials", ".gnupg/pubring.kbx", ".kube/config", ".docker/config.json",
		".config/gh/hosts.yml", ".config/gh", ".netrc", ".bash_history", ".zsh_history", ".config/x/.python_history",
		"sub/.ssh/id_rsa", ".config/tool/api_token", "conf/deploy.key", ".env",
	} {
		assert.NotEmpty(t, DotfileDenied(bad), bad)
	}
	for _, ok := range []string{".gitconfig", ".config/git/config", ".config/ghostty/config", "conf/tool.conf", ".tmux.conf"} {
		assert.Empty(t, DotfileDenied(ok), ok)
	}
}
