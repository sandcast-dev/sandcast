package build

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/plan"
)

// shellKindPlan returns a minimal OpenShell plan whose shell resolves to kind.
func shellKindPlan(t *testing.T, kind string) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		SchemaVersion: 1, SandcastVersion: "dev", Target: "openshell",
		Platform:   plan.Selected{Value: "linux/arm64", Source: "host"},
		Engine:     plan.EngineInfo{Name: "podman", Source: "detected"},
		BaseImage:  plan.BaseImage{Reference: "img", Digest: testDigest},
		FinalUser:  plan.FinalUser{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/sandbox", Created: true},
		Image:      plan.ImageSetup{Env: []plan.EnvVar{}, Dirs: []plan.Dir{}},
		Unresolved: []plan.Unresolved{},
		Conflicts:  nil,
	}
	switch kind {
	case "zsh":
		p.Shell = plan.Shell{Program: "zsh", RCPath: plan.ZshRCPath, RC: "alias ll='ls -l'\n"}
	case "bash":
		p.Shell = plan.Shell{Program: "bash", RCPath: plan.BashRCPath, RC: "alias ll='ls -l'\n"}
	default:
		p.Shell = plan.Shell{Program: "none"}
		p.ImageFacts = plan.ImageFacts{Shells: []string{"/bin/sh"}}
	}
	return p
}

// TestShellSectionZshRelocates checks the zsh path sets ZDOTDIR in /etc/zshenv
// and bakes the curated config inside the ZDOTDIR tree, never under $HOME
// (spec 003 US1/US2).
func TestShellSectionZshRelocates(t *testing.T) {
	got, err := Assemble(shellKindPlan(t, "zsh"))
	require.NoError(t, err)
	assert.Contains(t, got, "export ZDOTDIR=/opt/sandcast/zdotdir")
	// Debian and Ubuntu build zsh with /etc/zsh as its global rc directory and
	// never read /etc/zshenv; other builds read /etc/zshenv. Both are written
	// so ZDOTDIR is set even when a runtime clears the image ENV (env -i).
	assert.Contains(t, got, ">> /etc/zsh/zshenv")
	assert.Contains(t, got, ">> /etc/zshenv")
	assert.Contains(t, got, "install -d /opt/sandcast/zdotdir /etc/zsh")
	assert.Contains(t, got, "COPY shell/sandcast.zsh /opt/sandcast/zdotdir/.zshrc")
	assert.Contains(t, got, `CMD ["/bin/zsh", "-l"]`)
	assert.NotContains(t, got, "/etc/zsh/zshrc.d", "the old in-home-chained location is gone")
}

// TestShellSectionBashLauncher checks bash is launched with no profile files
// and the baked rcfile in place of ~/.bashrc, so a workspace ~/.bashrc is
// never sourced (US2). --norc must be absent: it makes bash ignore --rcfile.
func TestShellSectionBashLauncher(t *testing.T) {
	got, err := Assemble(shellKindPlan(t, "bash"))
	require.NoError(t, err)
	assert.Contains(t, got, "exec bash --noprofile --rcfile /opt/sandcast/bashrc")
	assert.NotContains(t, got, "--norc --rcfile", "the launcher must not pass --norc")
	assert.Contains(t, got, "cat > /opt/sandcast/shell-login")
	assert.Contains(t, got, "COPY shell/sandcast.sh /opt/sandcast/bashrc")
	// The launcher, not /bin/bash, is the login shell and the CMD.
	assert.Contains(t, got, "-s /opt/sandcast/shell-login")
	assert.Contains(t, got, `CMD ["/opt/sandcast/shell-login"]`)
	assert.NotContains(t, got, `CMD ["/bin/bash"`)
}

// TestShellSectionsCreateRelocationPrefix checks the bash and sh sections
// create /opt/sandcast before they write into it. The base images have no
// /opt/sandcast, and only a baked harness used to create it, so a bash image
// without a curated rc (bash chosen from the image facts) failed to build
// with "cannot create /opt/sandcast/bashrc: Directory nonexistent".
func TestShellSectionsCreateRelocationPrefix(t *testing.T) {
	noRC := shellKindPlan(t, "sh")
	noRC.ImageFacts = plan.ImageFacts{Shells: []string{"/bin/sh", "/bin/bash"}}
	require.Equal(t, "bash", plan.ShellKind(noRC))
	for name, p := range map[string]*plan.Plan{
		"bash with rc":    shellKindPlan(t, "bash"),
		"bash without rc": noRC,
		"sh":              shellKindPlan(t, "sh"),
	} {
		got, err := shellSection(p)
		require.NoError(t, err, name)
		mk := strings.Index(got, "install -d "+plan.RelocationPrefix)
		require.GreaterOrEqual(t, mk, 0, "%s: the section creates %s:\n%s", name, plan.RelocationPrefix, got)
		first := strings.Index(got, plan.RelocationPrefix+"/")
		require.GreaterOrEqual(t, first, 0, name)
		assert.Less(t, mk, first, "%s: %s is created before anything is written into it:\n%s", name, plan.RelocationPrefix, got)
	}
	got, err := shellSection(noRC)
	require.NoError(t, err)
	assert.Contains(t, got, ": > "+plan.BashRCPath, "without a curated rc the bash rcfile is created empty")
}

// TestShellSectionSetsKeptUserLoginShell checks the OpenShell image makes the
// launcher (or zsh) the final user's passwd login shell even when the user
// is kept from the base image. The community base keeps sandbox with
// /bin/bash, and su - or any login through the passwd shell then sourced
// the workspace ~/.bash_profile.
func TestShellSectionSetsKeptUserLoginShell(t *testing.T) {
	for kind, want := range map[string]string{"zsh": "/bin/zsh", "bash": plan.ShellLoginPath, "sh": plan.ShLoginPath} {
		p := shellKindPlan(t, kind)
		p.FinalUser.Created = false
		got, err := shellSection(p)
		require.NoError(t, err, kind)
		assert.Contains(t, got, "usermod -s "+want+" sandbox", kind)
		assert.Contains(t, got, "PATH=\"$PATH:/usr/sbin:/sbin\"", "%s: usermod lives in sbin, which the community base PATH lacks", kind)
		assert.Contains(t, got, ">> /etc/shells", kind)
		assert.Contains(t, got, "/etc/passwd", "%s: a passwd fallback for images without usermod", kind)
	}
	// The container target keeps the base image user untouched.
	p := shellKindPlan(t, "bash")
	p.Target = "container"
	got, err := shellSection(p)
	require.NoError(t, err)
	assert.NotContains(t, got, "usermod")
}

// TestShellSectionBakesSpawnHooks checks OpenShell images get the hooks for
// shells the runtime starts directly (bash -i, bash -lc, bash -l after
// clearing the environment): the profile.d login hook, the redirect home
// with its .profile, and the bashrc hook prepended to the system bashrc the
// image's bash was compiled with. The targets follow the shell kind.
func TestShellSectionBakesSpawnHooks(t *testing.T) {
	for kind, top := range map[string]string{"zsh": "exec /bin/zsh -i\n", "bash": "exec /opt/sandcast/shell-login -i\n", "sh": `exec "$BASH" --noprofile --norc -i` + "\n"} {
		got, err := shellSection(shellKindPlan(t, kind))
		require.NoError(t, err, kind)
		assert.Contains(t, got, "cat > "+plan.LoginHookPath+" <<'SANDCAST_HOOK'", kind)
		assert.Contains(t, got, "cat > "+plan.RedirectHome+"/.profile <<'SANDCAST_HOOK'", kind)
		assert.Contains(t, got, "for f in /etc/bash.bashrc /etc/bash/bashrc; do", kind)
		assert.Contains(t, got, `grep -qsF "$f" "$bash_bin"`, "%s: the system bashrc is the one compiled into bash", kind)
		assert.Contains(t, got, "{ cat /tmp/sandcast-bashrc-hook; cat \"$sysrc\"", "%s: the hook is prepended", kind)
		assert.Contains(t, got, top, kind)
	}
	zsh, err := shellSection(shellKindPlan(t, "zsh"))
	require.NoError(t, err)
	assert.Contains(t, zsh, "export ZDOTDIR=/opt/sandcast/zdotdir\n", "the hooks restore the relocation variables")
	assert.Contains(t, zsh, "exec /bin/zsh -l -i\n", "an interactive login bash becomes a login zsh")

	p := shellKindPlan(t, "zsh")
	p.Target = "container"
	got, err := shellSection(p)
	require.NoError(t, err)
	assert.NotContains(t, got, plan.LoginHookPath, "the container target gets no hooks")
}

// TestShellSectionMinimalDefault checks a login shell that is neither zsh nor
// bash gets a baked /etc/profile.d default plus a non-login sh launcher, so a
// workspace .profile is never sourced (FR-011, FR-002/SC-002, research R2).
func TestShellSectionMinimalDefault(t *testing.T) {
	got, err := Assemble(shellKindPlan(t, "sh"))
	require.NoError(t, err)
	assert.Contains(t, got, "cat > /etc/profile.d/sandcast.sh")
	// The launcher sets $ENV and execs a non-login sh (no -l), so neither
	// /etc/profile nor a workspace .profile is read on login.
	assert.Contains(t, got, "cat > /opt/sandcast/sh-login")
	assert.Contains(t, got, `exec /bin/sh "$@"`)
	assert.Contains(t, got, "-s /opt/sandcast/sh-login")
	assert.Contains(t, got, `CMD ["/opt/sandcast/sh-login"]`)
	assert.NotContains(t, got, `CMD ["/bin/sh", "-l"]`)
}

// TestDotfilesSectionBakesToRelocationTargets checks baked dotfiles are
// copied to their relocation targets outside the home (spec 003 US3).
func TestDotfilesSectionBakesToRelocationTargets(t *testing.T) {
	p := shellKindPlan(t, "zsh")
	p.Dotfiles = []plan.DotfileSelection{
		{Source: plan.DotfileSourceAllowlist, HostPath: "~/.gitconfig", RelocatedTo: "/etc/gitconfig", Content: "[user]\n"},
		{Source: plan.DotfileSourceAllowlist, HostPath: "~/.config/starship.toml", RelocatedTo: plan.XDGConfigHome + "/starship.toml", Env: map[string]string{"STARSHIP_CONFIG": plan.XDGConfigHome + "/starship.toml"}, Content: "add_newline=false\n"},
	}
	got, err := Assemble(p)
	require.NoError(t, err)
	assert.Contains(t, got, "# 62-dotfiles")
	assert.Contains(t, got, "COPY dotfiles/etc_gitconfig /etc/gitconfig")
	assert.Contains(t, got, "COPY dotfiles/opt_sandcast_xdg_starship.toml /opt/sandcast/xdg/starship.toml")
	assert.Equal(t, "dotfiles/etc_gitconfig", DotfileContextFile("/etc/gitconfig"))
	// No dotfile target may be under the overmount home.
	for _, d := range p.Dotfiles {
		assert.False(t, plan.UnderOvermount(plan.OvermountHome, d.RelocatedTo))
	}
}

// TestNoGeneratedFileSourcesTheHome is the core US2 guarantee: across every
// shell kind, no instruction in the generated Containerfile sources, copies to
// or launches from a path under the overmounted home (/sandbox), so a startup
// file in the mounted workspace is never auto-run.
func TestNoGeneratedFileSourcesTheHome(t *testing.T) {
	for _, kind := range []string{"zsh", "bash", "sh"} {
		got, err := Assemble(shellKindPlan(t, kind))
		require.NoError(t, err, kind)
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			assert.NotContains(t, line, plan.OvermountHome+"/",
				"kind %s: no generated instruction may touch a path under the overmount home: %q", kind, line)
		}
		// None of the shells may read a $HOME-relative rc file.
		assert.NotContains(t, got, "$HOME/.", kind)
		assert.NotContains(t, got, "~/.", kind)
	}
}
