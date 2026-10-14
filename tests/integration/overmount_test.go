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

// zshOvermountManifest builds a zsh image with a baked alias and three
// dotfiles: git's two configuration files (merged into /etc/gitconfig) and
// tmux. The dotfile sources are project-relative files the test writes
// (dotfile sources must stay inside the home bounds or the project), so the
// run is deterministic and never reads the developer's real home.
func zshOvermountManifest(image, gitconfig, gitXDG, tmuxconf string) string {
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
  - path: ` + gitXDG + `
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

// runEngine executes a command and returns combined output and the exit error.
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
	for _, rc := range []string{".zshrc", ".zprofile", ".zshenv", ".zlogin", ".zlogout", ".bashrc", ".bash_profile", ".bash_login", ".bash_logout", ".profile"} {
		require.NoError(t, os.WriteFile(filepath.Join(ws, rc), []byte("touch /tmp/pwned-"+strings.TrimPrefix(rc, ".")+"\n"), 0o644))
	}
	return ws
}

// openShellSpawnScript simulates how OpenShell v0.1.x starts shells in a
// sandbox (boundary_exec.rs and the default agent): it runs /bin/bash after
// clearing the environment, with -i under a pty for a terminal session, with
// -lc or -c for a command, and as -l for the default agent. None of these
// uses the image CMD, the passwd shell or the image ENV. script(1) provides
// the pty. Every case prints RESULT <case> pwned=[...] with the sentinels the
// planted workspace files left behind, then clears them; the first case is a
// positive control that sources the workspace .bashrc on purpose.
const openShellSpawnScript = `#!/bin/sh
E="env -i HOME=/sandbox USER=sandbox PATH=/usr/local/bin:/usr/bin:/bin TERM=xterm-256color"
report() { echo "RESULT $1 pwned=[$(ls /tmp | grep '^pwned-' | tr '\n' ' ')]"; rm -f /tmp/pwned-*; }
rm -f /tmp/pwned-*
$E /bin/bash -c '. /sandbox/.bashrc'
report control
printf 'alias myll; echo "ZDOTDIR=[$ZDOTDIR]"; exit\n' | script -qec "$E /bin/bash -i" /dev/null 2>&1 | tr -d '\r' | sed 's/^/OUT pty-i /'
report pty-i
$E /bin/bash -lc 'echo "LC 0=$0 @=$*"; exit 3' zero a b | sed 's/^/OUT lc /'
$E /bin/bash -lc 'exit 3'; echo "OUT lc rc=$?"
report lc
$E /bin/bash -c 'echo C-OK' | sed 's/^/OUT c /'
report c
printf 'alias myll; echo "ZDOTDIR=[$ZDOTDIR]"; exit\n' | script -qec "$E /bin/bash -l" /dev/null 2>&1 | tr -d '\r' | sed 's/^/OUT pty-l /'
report pty-l
printf 'echo FROM-STDIN; exit 4\n' | $E /bin/bash -l | sed 's/^/OUT pipe-l /'
printf 'exit 4\n' | $E /bin/bash -l; echo "OUT pipe-l rc=$?"
report pipe-l
printf 'bash -i -c "echo NESTED-BASH=\\$BASH_VERSION"; exit\n' | script -qec "$E /bin/bash -i" /dev/null 2>&1 | tr -d '\r' | sed 's/^/OUT nested /'
report nested
$E /bin/sh -lc 'echo "SH-HOME=$HOME"; exit 6' | sed 's/^/OUT sh-lc /'
$E /bin/sh -lc 'exit 6'; echo "OUT sh-lc rc=$?"
report sh-lc
env -i SSH_CLIENT='192.0.2.1 1 22' HOME=/sandbox PATH=/usr/bin:/bin /bin/bash -c 'echo SSH-OK' | sed 's/^/OUT ssh /'
report ssh
if [ -x /opt/sandcast/shell-login ]; then
  printf 'echo "GUARD=[${SANDCAST_SHELL_PID-unset}]"; alias myll; exit\n' | script -qec "$E /opt/sandcast/shell-login -c 'exec bash -i'" /dev/null 2>&1 | tr -d '\r' | sed 's/^/OUT launcher-c /'
  report launcher-c
fi
`

// assertOpenShellSpawns runs openShellSpawnScript in image with the planted
// workspace ws mounted over the home and checks the spec 003 guarantees for
// every way OpenShell starts a shell: no workspace startup, profile or
// logout file runs (FR-002, SC-002), the curated configuration is active in
// interactive sessions (FR-001), and commands keep their output, arguments
// and exit status. alias is how the shell prints the curated alias; zdotdir
// is the expected ZDOTDIR in interactive sessions ("" for bash, which also
// selects the launcher cases: a bash image has /opt/sandcast/shell-login).
func assertOpenShellSpawns(t *testing.T, engine, image, ws, alias, zdotdir string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spawn.sh"), []byte(openShellSpawnScript), 0o755))
	out, err := runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "-v", dir+":/harness:ro", "--entrypoint", "/bin/sh", image, "/harness/spawn.sh")
	require.NoError(t, err, out)

	results := map[string]string{}
	lines := map[string][]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) >= 3 && f[0] == "RESULT":
			results[f[1]] = strings.TrimSpace(strings.TrimPrefix(strings.Join(f[2:], " "), "pwned="))
		case len(f) >= 2 && f[0] == "OUT":
			lines[f[1]] = append(lines[f[1]], strings.Join(f[2:], " "))
		}
	}
	has := func(c, want string) {
		t.Helper()
		assert.Contains(t, strings.Join(lines[c], "\n"), want, "case %s", c)
	}

	assert.Contains(t, results["control"], "pwned-bashrc", "positive control: sourcing a workspace file leaves its sentinel")
	cases := []string{"pty-i", "lc", "c", "pty-l", "pipe-l", "nested", "sh-lc", "ssh"}
	if zdotdir == "" {
		cases = append(cases, "launcher-c")
	}
	for _, c := range cases {
		require.Contains(t, results, c, "case %s ran", c)
		assert.Equal(t, "[]", results[c], "case %s: no workspace startup file may run", c)
	}
	has("pty-i", alias)
	has("pty-l", alias)
	if zdotdir != "" {
		has("pty-i", "ZDOTDIR=["+zdotdir+"]")
		has("pty-l", "ZDOTDIR=["+zdotdir+"]")
	}
	has("lc", "LC 0=zero @=a b")
	has("lc", "rc=3")
	has("c", "C-OK")
	has("pipe-l", "FROM-STDIN")
	has("pipe-l", "rc=4")
	has("nested", "NESTED-BASH=5.")
	has("sh-lc", "SH-HOME=/sandbox")
	has("sh-lc", "rc=6")
	has("ssh", "SSH-OK")
	if zdotdir == "" {
		// A non-interactive launcher run exports no pid guard, so an
		// interactive bash its command execs (same pid) is not waved past
		// the bashrc hook: it goes through the launcher and gets the rcfile.
		has("launcher-c", "GUARD=[unset]")
		has("launcher-c", alias)
	}
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

	dir := project(t, zshOvermountManifest(repo, "dotfiles-src/.gitconfig", "dotfiles-src/.config/git/config", "dotfiles-src/.tmux.conf"))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dotfiles-src", ".config", "git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dotfiles-src", ".gitconfig"), []byte("[user]\n\temail = captured@example.com\n\tname = from-gitconfig\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dotfiles-src", ".config", "git", "config"), []byte("[user]\n\tname = from-xdg\n[core]\n\tpager = less-from-xdg\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dotfiles-src", ".tmux.conf"), []byte("set -g history-limit 99999\n"), 0o644))
	require.Zero(t, sandcast(t, dir, nil, "build").code, "image builds")

	ws := workspaceWithPlantedRC(t)

	// Positive control: sourcing the planted files on purpose leaves their
	// sentinels, so the checks below can fire under this mount (the files
	// are readable and /tmp is writable as the image user).
	listPwned := `ls /tmp | grep '^pwned-' | sed 's/^/PWNED /'; true`
	out, err := runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "/bin/zsh", image,
		"-f", "-c", `. /sandbox/.zshenv; . /sandbox/.zshrc; `+listPwned)
	require.NoError(t, err, out)
	assert.Contains(t, out, "PWNED pwned-zshenv", "positive control")
	assert.Contains(t, out, "PWNED pwned-zshrc", "positive control")

	// The image ENV sets ZDOTDIR for every process, besides the global
	// zshenv that covers a cleared environment.
	out, err = runEngine(t, engine, "run", "--rm", "--entrypoint", "/usr/bin/env", image)
	require.NoError(t, err, out)
	assert.Contains(t, out, "ZDOTDIR=/opt/sandcast/zdotdir\n", "image ENV")

	// Scenario 1 + 2: the baked zsh config loads and no planted startup file
	// runs (.zshenv, .zshrc, .zprofile, .profile), even though /sandbox is
	// mounted over the home, in an interactive and in a login shell.
	for _, mode := range []string{"-ic", "-lic"} {
		out, err = runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "/bin/zsh", image, mode,
			`echo "ZDOTDIR=$ZDOTDIR"; alias myll; `+listPwned)
		require.NoError(t, err, out)
		assert.NotContains(t, out, "PWNED", "zsh %s: no workspace startup file may run", mode)
		assert.Contains(t, out, "ZDOTDIR=/opt/sandcast/zdotdir", mode)
		assert.Contains(t, out, "myll='ls -l'", "zsh %s: the baked alias is active", mode)
	}

	// Scenario 2 without the image ENV: a runtime that clears the environment
	// (OpenShell, su -l, sshd) starts zsh with no ZDOTDIR. The global zshenv
	// zsh actually reads (/etc/zsh/zshenv on Ubuntu) must set it, or zsh
	// sources the workspace ~/.zshenv and ~/.zshrc.
	for _, mode := range []string{"-ic", "-lc"} {
		out, err = runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "/usr/bin/env", image,
			"-i", "HOME=/sandbox", "USER=sandbox", "PATH=/usr/local/bin:/usr/bin:/bin", "TERM=xterm",
			"/bin/zsh", mode, `echo "ZDOTDIR=$ZDOTDIR"; ls /tmp | grep '^pwned-' | sed 's/^/PWNED /'; true`)
		require.NoError(t, err, out)
		assert.Contains(t, out, "ZDOTDIR=/opt/sandcast/zdotdir", "zsh %s with a cleared environment", mode)
		assert.NotContains(t, out, "PWNED", "zsh %s with a cleared environment runs no workspace startup file", mode)
	}

	// How OpenShell itself starts shells: /bin/bash after env_clear. The bash
	// hooks hand interactive sessions to the curated zsh.
	assertOpenShellSpawns(t, engine, image, ws, "myll='ls -l'", "/opt/sandcast/zdotdir")

	// Scenario 3: the baked git dotfiles are read from /etc/gitconfig. Both
	// of git's files are kept, merged in git's order, so a key only in one
	// of them is set and a key in both takes the ~/.gitconfig value
	// (FINDING-38).
	for key, want := range map[string]string{"user.email": "captured@example.com", "core.pager": "less-from-xdg", "user.name": "from-gitconfig"} {
		out, err = runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "git", image, "config", "--get", key)
		require.NoError(t, err, out)
		assert.Equal(t, want+"\n", out, key)
	}

	// The tmux dotfile is baked to /etc/tmux.conf, which tmux reads natively,
	// and no image-wide XDG_CONFIG_HOME points tools at a root-owned tree:
	// the sandbox user can still write its own XDG configuration.
	out, err = runEngine(t, engine, "run", "--rm", "-v", ws+":/sandbox", "--entrypoint", "/bin/sh", image, "-c",
		`cat /etc/tmux.conf; echo "XDG=[${XDG_CONFIG_HOME-unset}]"; mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/gh" && touch "${XDG_CONFIG_HOME:-$HOME/.config}/gh/config.yml" && echo XDG-WRITE-OK`)
	require.NoError(t, err, out)
	assert.Contains(t, out, "set -g history-limit 99999")
	assert.Contains(t, out, "XDG=[unset]")
	assert.Contains(t, out, "XDG-WRITE-OK")

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

	// The base image keeps its sandbox user with /bin/bash; the build makes
	// the launcher its passwd login shell, so a login through passwd (su -)
	// reads no workspace profile file.
	out, err = runEngine(t, engine, "run", "--rm", "--user", "root", "-v", ws+":/sandbox", "--entrypoint", "/bin/sh", image, "-c",
		`getent passwd sandbox; su - sandbox -c 'echo IN_SU'; ls /tmp | grep '^pwned-' | sed 's/^/PWNED /'; true`)
	require.NoError(t, err, out)
	assert.Contains(t, out, ":/sandbox:/opt/sandcast/shell-login", "the kept user's login shell is the launcher")
	assert.Contains(t, out, "IN_SU")
	assert.NotContains(t, out, "PWNED", "su - runs no workspace startup file")

	// How OpenShell itself starts shells: /bin/bash after env_clear.
	assertOpenShellSpawns(t, engine, image, ws, "alias myll='ls -l'", "")
}
