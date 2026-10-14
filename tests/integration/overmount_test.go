//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// zshOvermountManifest builds a zsh image with a baked alias and two dotfiles
// (git and tmux). The dotfile sources are absolute paths the test writes, so
// the run is deterministic and never reads the developer's real home.
func zshOvermountManifest(image, gitconfig, tmuxconf string) string {
	return `schema_version: 1
name: sc-overmount
base_image: nvidia-community-base
components:
  - key: git
shell:
  program: zsh
  rc: |
    alias myll='ls -l'
    export SANDCAST_OVERMOUNT_OK=1
dotfiles:
  - path: ` + gitconfig + `
  - path: ` + tmuxconf + `
targets:
  openshell:
    image: ` + image + `
`
}

func bashOvermountManifest(image string) string {
	return `schema_version: 1
name: sc-overmount-bash
base_image: nvidia-community-base
shell:
  program: bash
  rc: |
    alias myll='ls -l'
    export SANDCAST_OVERMOUNT_OK=1
targets:
  openshell:
    image: ` + image + `
`
}

// run executes a command and returns combined output and the exit error.
func runEngine(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	t.Logf("%s %s\n%s", name, strings.Join(args, " "), out)
	return string(out), err
}

// workspaceWithPlantedRC creates a host directory to mount over the home that
// contains hostile startup files; each writes a sentinel when sourced.
func workspaceWithPlantedRC(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	for _, rc := range []string{".zshrc", ".zprofile", ".zshenv", ".bashrc", ".bash_profile", ".profile"} {
		require.NoError(t, os.WriteFile(filepath.Join(ws, rc), []byte("touch /tmp/pwned-"+strings.TrimPrefix(rc, ".")+"\n"), 0o644))
	}
	return ws
}

// TestOvermountZshSurvives covers quickstart scenarios 1, 2 and 3 for zsh: the
// baked configuration loads under a workspace volume mounted over the home, a
// planted workspace ~/.zshrc never runs, and a baked git dotfile is in effect.
// It also checks the read-only-home case (spec Edge Case).
func TestOvermountZshSurvives(t *testing.T) {
	engine := requireEngine(t)
	// The manifest names the repository; the build tags it :latest. A tag in
	// targets.*.image would make the pass-1 stage reference invalid.
	repo := "localhost/sandcast-it-overmount-zsh"
	image := repo + ":latest"
	removeImages(t, engine, image)

	src := t.TempDir()
	gitconfig := filepath.Join(src, ".gitconfig")
	require.NoError(t, os.WriteFile(gitconfig, []byte("[user]\n\temail = captured@example.com\n"), 0o644))
	tmuxconf := filepath.Join(src, ".tmux.conf")
	require.NoError(t, os.WriteFile(tmuxconf, []byte("set -g history-limit 99999\n"), 0o644))

	dir := project(t, zshOvermountManifest(repo, gitconfig, tmuxconf))
	require.Zero(t, sandcast(t, dir, nil, "build").code, "image builds")

	ws := workspaceWithPlantedRC(t)

	// Scenario 1 + 2: the baked zsh config loads and the planted ~/.zshrc does
	// not run, even though /sandbox is mounted over the home.
	script := `test -f /tmp/pwned-zshrc && echo PWNED || echo SAFE; echo "ZDOTDIR=$ZDOTDIR"; alias myll`
	out, err := runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "/bin/zsh", image, "-ic", script)
	require.NoError(t, err, out)
	assert.Contains(t, out, "SAFE", "the planted workspace ~/.zshrc must not run")
	assert.NotContains(t, out, "PWNED")
	assert.Contains(t, out, "ZDOTDIR=/opt/sandcast/zdotdir")
	assert.Contains(t, out, "myll='ls -l'", "the baked alias is active")

	// Scenario 3: the baked git dotfile is read from /etc/gitconfig.
	out, err = runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "git", image, "config", "--get", "user.email")
	require.NoError(t, err, out)
	assert.Contains(t, out, "captured@example.com")

	// Edge case: the home is mounted read-only; the baked config still loads.
	out, err = runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox:ro", "--entrypoint", "/bin/zsh", image, "-ic", `echo "ZDOTDIR=$ZDOTDIR"; alias myll`)
	require.NoError(t, err, out)
	assert.Contains(t, out, "ZDOTDIR=/opt/sandcast/zdotdir")
	assert.Contains(t, out, "myll='ls -l'")
}

// TestOvermountBashWithoutRCBuilds covers a manifest with no shell section on
// the default base image: the plan picks bash from the image facts and bakes
// an empty rcfile and the launcher under /opt/sandcast, which the base image
// does not have. The build must create the directory itself.
func TestOvermountBashWithoutRCBuilds(t *testing.T) {
	engine := requireEngine(t)
	repo := "localhost/sandcast-it-overmount-norc"
	image := repo + ":latest"
	removeImages(t, engine, image)

	dir := project(t, `schema_version: 1
name: sc-overmount-norc
base_image: nvidia-community-base
targets:
  openshell:
    image: `+repo+`
`)
	require.Zero(t, sandcast(t, dir, nil, "build").code, "an image with bash from the image facts and no curated rc builds")

	out, err := runEngine(t, engine, "run", "--rm", "--entrypoint", "/bin/sh", image, "-c", "test -x /opt/sandcast/shell-login && test -f /opt/sandcast/bashrc && echo BAKED")
	require.NoError(t, err, out)
	assert.Contains(t, out, "BAKED")
}

// TestOvermountBashSurvives covers scenarios 1 and 2 for bash: the baked
// rcfile loads through the launcher and a planted workspace ~/.bashrc never
// runs under the overmount.
func TestOvermountBashSurvives(t *testing.T) {
	engine := requireEngine(t)
	repo := "localhost/sandcast-it-overmount-bash"
	image := repo + ":latest"
	removeImages(t, engine, image)

	dir := project(t, bashOvermountManifest(repo))
	require.Zero(t, sandcast(t, dir, nil, "build").code, "image builds")

	ws := workspaceWithPlantedRC(t)
	script := `test -f /tmp/pwned-bashrc && echo PWNED || echo SAFE; alias myll`
	// The launcher is the image CMD; with --entrypoint it receives -i -c as
	// arguments and forwards them to bash --noprofile --rcfile. Without
	// --entrypoint, podman would replace the CMD and run "-i" as the command.
	out, err := runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "/opt/sandcast/shell-login", image, "-i", "-c", script)
	require.NoError(t, err, out)
	assert.Contains(t, out, "SAFE", "the planted workspace ~/.bashrc must not run")
	assert.NotContains(t, out, "PWNED")
	assert.Contains(t, out, "myll='ls -l'", "the baked rcfile is active")
}
