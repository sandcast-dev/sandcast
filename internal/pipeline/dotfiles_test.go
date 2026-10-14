package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
	out, notices := dotfileInputs(dotfileEnv(home, t.TempDir()), mdotfiles("~/.config/nvim/does-not-exist"))
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

// skippedWith asserts that dotfileInputs bakes nothing for paths and reports
// one notice per path containing want.
func skippedWith(t *testing.T, env Env, want string, paths ...string) {
	t.Helper()
	out, notices := dotfileInputs(env, mdotfiles(paths...))
	assert.Empty(t, out, "nothing is baked for %v", paths)
	require.Len(t, notices, len(paths), "%v", notices)
	for _, n := range notices {
		assert.Contains(t, n, want)
	}
}

// TestDotfileInputsHomeBounded checks a committed manifest cannot pull host
// secrets into the image: a home-relative dotfile must be an allowlist entry
// or lie in one of the allowed ~/.config entries (the configuration of a
// known tool), so the credential stores under ~/.config (sops age keys,
// 1Password, gcloud, gh) and any other ~/.config file are refused by the
// bound, and credential and history names are refused even inside an
// allowed entry.
func TestDotfileInputsHomeBounded(t *testing.T) {
	home := t.TempDir()
	for _, f := range []string{
		".aws/credentials", ".netrc", ".kube/config", ".docker/config.json", ".ssh/config", ".bash_history", "notes.txt",
		".config/sops/age/keys.txt", ".config/op/config", ".config/gcloud/legacy_credentials/a/adc.json", ".config/gh/hosts.yml",
		".config/tool/settings.toml", ".config/GH/hosts.yml", ".config/Nvim/init.lua",
	} {
		writeFile(t, filepath.Join(home, f), "secret\n")
	}
	env := dotfileEnv(home, t.TempDir())
	skippedWith(t, env, "only the dotfile allowlist", "~/.aws/credentials", "~/.netrc", "~/.kube/config", "~/.docker/config.json", "~/.ssh/config", "~/.bash_history", "~/notes.txt", "~/.config/../.ssh/config",
		"~/.config/sops/age/keys.txt", "~/.config/op/config", "~/.config/gcloud/legacy_credentials/a/adc.json", "~/.config/gh/hosts.yml",
		"~/.config/tool/settings.toml", "~/.config/GH/hosts.yml", "~/.config/Nvim/init.lua")
	_, notices := dotfileInputs(env, mdotfiles("~/.config/tool/settings.toml"))
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "these ~/.config entries: git/, starship.toml, nvim/", "the notice names the bound")

	for _, f := range []string{".config/nvim/api_token", ".config/nvim/.zsh_history", ".config/git/server.pem", ".config/helix/secrets/x.toml"} {
		writeFile(t, filepath.Join(home, f), "secret\n")
	}
	skippedWith(t, env, "credential", "~/.config/nvim/api_token", "~/.config/nvim/.zsh_history", "~/.config/git/server.pem", "~/.config/helix/secrets/x.toml")

	writeFile(t, filepath.Join(home, ".config", "nvim", "lua", "init.lua"), "vim.o.number = true\n")
	writeFile(t, filepath.Join(home, ".config", "ghostty", "config"), "theme = dark\n")
	out, notices := dotfileInputs(env, mdotfiles("~/.config/nvim/lua/init.lua", "~/.config/ghostty/config"))
	require.Empty(t, notices)
	require.Len(t, out, 2)
	assert.Equal(t, "vim.o.number = true\n", out[0].Content)
	assert.Equal(t, "theme = dark\n", out[1].Content)
}

// TestDotfileInputsHomeSymlinkToCredentialSkipped checks the denylist also
// applies to the resolved path: an allowed name that links to a credential
// store inside the home is refused.
func TestDotfileInputsHomeSymlinkToCredentialSkipped(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".ssh", "config"), "Host x\n")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "nvim"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(home, ".ssh", "config"), filepath.Join(home, ".config", "nvim", "innocent.lua")))
	skippedWith(t, dotfileEnv(home, t.TempDir()), "credential", "~/.config/nvim/innocent.lua")
}

// TestDotfileInputsAbsoluteOutsideProjectSkipped checks an absolute path is
// baked only when it resolves inside the project.
func TestDotfileInputsAbsoluteOutsideProjectSkipped(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "hosts")
	writeFile(t, outside, "127.0.0.1 localhost\n")
	skippedWith(t, dotfileEnv(t.TempDir(), t.TempDir()), "outside the project", outside)
}

// TestDotfileInputsProjectCredentialSkipped checks the denylist covers
// project files too.
func TestDotfileInputsProjectCredentialSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aws", "credentials"), "k\n")
	writeFile(t, filepath.Join(root, "conf", "deploy.key"), "k\n")
	skippedWith(t, dotfileEnv(t.TempDir(), root), "credential", ".aws/credentials", "conf/deploy.key")
}

// TestDotfileInputsTildeUserSkipped checks "~user" forms are refused rather
// than resolved one way and checked another (FINDING-23).
func TestDotfileInputsTildeUserSkipped(t *testing.T) {
	skippedWith(t, dotfileEnv(t.TempDir(), t.TempDir()), "only ~/ names the home", "~root/.gitconfig")
}

// TestDotfileInputsNotRegularOrTooLargeSkipped checks only regular files up
// to the size cap are read: a FIFO would block the plan forever and a huge
// file would be embedded in plan.yaml (FINDING-25).
func TestDotfileInputsNotRegularOrTooLargeSkipped(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "pipe.conf"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dir.conf"), 0o755))
	writeFile(t, filepath.Join(root, "big.conf"), strings.Repeat("x", manifest.MaxDotfileSize+1))
	writeFile(t, filepath.Join(root, "ok.conf"), strings.Repeat("x", manifest.MaxDotfileSize))
	env := dotfileEnv(t.TempDir(), root)

	done := make(chan struct{})
	go func() {
		defer close(done)
		skippedWith(t, env, "not a regular file", "pipe.conf", "dir.conf")
		skippedWith(t, env, "larger than 256 KiB", "big.conf")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO dotfile blocked")
	}
	out, notices := dotfileInputs(env, mdotfiles("ok.conf"))
	require.Empty(t, notices)
	require.Len(t, out, 1)
}

// TestDotfileInputsResolveErrorsAreDistinct checks a symlink loop is not
// reported as a missing file (FINDING-24) and that an unresolvable home fails
// closed.
func TestDotfileInputsResolveErrorsAreDistinct(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Symlink("loop.conf", filepath.Join(root, "loop.conf")))
	out, notices := dotfileInputs(dotfileEnv(t.TempDir(), root), mdotfiles("loop.conf"))
	assert.Empty(t, out)
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "could not be resolved")
	assert.NotContains(t, notices[0], "does not exist")

	skippedWith(t, dotfileEnv(filepath.Join(t.TempDir(), "missing-home"), root), "home directory", "~/.gitconfig")
}
