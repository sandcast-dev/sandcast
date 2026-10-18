package build

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/shell"
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
		// As plan.Build leaves it: the relocation env is image env, which
		// makes the plan carry the session environment script.
		p.Image.Env = []plan.EnvVar{{Name: "ZDOTDIR", Value: plan.ZDotDir}}
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
	assert.Contains(t, got, "for f in /etc/zsh/zshenv /etc/zshenv; do")
	// The zshenv block: the export line, the session environment and the
	// disabled new-user wizard, between markers so a rebuild replaces it.
	assert.Contains(t, got, shell.ZshEnvBegin+"\nexport ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1 SHELL=/bin/zsh\n[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\nzsh-newuser-install() { :; }\n"+shell.ZshEnvEnd+"\n")
	assert.Contains(t, got, "COPY shell/sandcast-env.sh /etc/profile.d/sandcast-env.sh\n")
	assert.Contains(t, got, "install -d /opt/sandcast/zdotdir /etc/zsh")
	assert.Contains(t, got, "COPY shell/sandcast.zsh /opt/sandcast/zdotdir/.zshrc")
	assert.Contains(t, got, `CMD ["/bin/zsh", "-l"]`)
	assert.NotContains(t, got, "COPY shell/sandcast.zsh /etc/zsh/zshrc.d", "the old in-home-chained location is gone")
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
	// The build bakes the absolute path of the image's bash into the
	// launcher, found on the system directories only, so the launcher never
	// resolves bash through a PATH that a curated rc extended with $HOME/bin.
	assert.Contains(t, got, "bash_bin=$(PATH=/usr/local/bin:/usr/bin:/bin command -v bash)\n")
	assert.Contains(t, got, `sed -i "s|^exec bash |exec $bash_bin |" /opt/sandcast/shell-login`+"\n")
	assert.Contains(t, got, `grep -q "^exec $bash_bin " /opt/sandcast/shell-login`+"\n", "the build fails when the substitution did not happen")
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

// TestPasswdShellFallbackRewritesEntry runs the sed expression of the
// usermod-less fallback with the host sed over sample passwd lines: the
// shell field of the final user's line is replaced, every other field and
// every other line is kept, and a "." in the user name matches itself only.
func TestPasswdShellFallbackRewritesEntry(t *testing.T) {
	sed, err := exec.LookPath("sed")
	if err != nil {
		t.Skip("no sed")
	}
	sedLine := regexp.MustCompile(`(?m)^\s*sed -i ('[^\n]*') /etc/passwd$`)
	for _, user := range []string{"sandbox", "sand.box"} {
		p := shellKindPlan(t, "bash")
		p.FinalUser.Name = user
		p.FinalUser.Created = false
		got, err := shellSection(p)
		require.NoError(t, err)
		m := sedLine.FindStringSubmatch(got)
		require.Len(t, m, 2, "the passwd fallback is rendered:\n%s", got)
		expr := strings.Trim(m[1], "'")
		passwd := "root:x:0:0:root:/root:/bin/bash\n" +
			user + ":x:1000:1000::/sandbox:/bin/bash\n" +
			"sandxbox:x:1001:1001::/home/sandxbox:/bin/bash\n" +
			user + "2:x:1002:1002::/home/x:/bin/sh\n"
		cmd := exec.Command(sed, "-e", expr)
		cmd.Stdin = strings.NewReader(passwd)
		out, err := cmd.Output()
		require.NoError(t, err, user)
		assert.Equal(t, "root:x:0:0:root:/root:/bin/bash\n"+
			user+":x:1000:1000::/sandbox:"+plan.ShellLoginPath+"\n"+
			"sandxbox:x:1001:1001::/home/sandxbox:/bin/bash\n"+
			user+"2:x:1002:1002::/home/x:/bin/sh\n", string(out), user)
	}
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
		assert.Contains(t, got, `{ cat "$hook"; cat "$sysrc.sandcast.rest"; } > "$sysrc.sandcast"`, "%s: the hook is prepended", kind)
		// A previous block is removed by the cleanup at the start of the
		// section, not by the installer, which only prepends.
		assert.NotContains(t, got, "replacing the shell hook block", kind)
		assert.Less(t, strings.Index(got, `sandcast_legacy_unhook "$f"`), strings.Index(got, "cat > "+plan.LoginHookPath), "%s: a previous block is removed before this build's hooks are written", kind)
		assert.Contains(t, got, top, kind)
	}
	zsh, err := shellSection(shellKindPlan(t, "zsh"))
	require.NoError(t, err)
	// The relocation variables reach the hooks through the session
	// environment script, which both hooks source before they replace the
	// shell (and the zshenv block sources too): one export list.
	envLine := "[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\n"
	assert.Equal(t, 3, strings.Count(zsh, envLine), "the zshenv block, the login hook and the bashrc hook source the session environment")
	assert.NotContains(t, zsh, "export ZDOTDIR=/opt/sandcast/zdotdir\n", "no second export list in the hooks")
	assert.Contains(t, zsh, "exec /bin/zsh -l -i\n", "an interactive login bash becomes a login zsh")
	// The curated zsh owns SHELL, so a $SHELL spawner (tmux, an editor's
	// terminal) starts zsh and not the nested bare bash: both hooks and the
	// global zshenv export it; a bash image leaves SHELL alone.
	assert.Equal(t, 2, strings.Count(zsh, "export SHELL=/bin/zsh\n"), "the login hook and the bashrc hook export SHELL")
	assert.Contains(t, zsh, "SANDCAST_SESSION=1 SHELL=/bin/zsh\n", "the global zshenv exports SHELL")
	// The opt-out of the switch to zsh keeps bash without startup files.
	assert.Equal(t, 2, strings.Count(zsh, `if [ -n "${SANDCAST_NO_SHELL_SWITCH-}" ]; then`+"\n"), "both hooks honor the opt-out")
	bash, err := shellSection(shellKindPlan(t, "bash"))
	require.NoError(t, err)
	assert.NotContains(t, bash, "export SHELL=")
	assert.NotContains(t, bash, "SANDCAST_NO_SHELL_SWITCH", "a bash image has nothing to switch")
	assert.NotContains(t, bash, "COPY shell/sandcast-env.sh", "no session environment without mise, image env or extra PATH directories")
	assert.NotContains(t, bash, envLine)

	p := shellKindPlan(t, "zsh")
	p.Target = "container"
	got, err := shellSection(p)
	require.NoError(t, err)
	assert.NotContains(t, got, "cat > "+plan.LoginHookPath, "the container target gets no hooks")
	assert.NotContains(t, got, "hook=/tmp/sandcast-bashrc-hook")
}

// runInstallScript runs one of the install snippets with the host sh and the
// given variables, returning its stderr.
func runInstallScript(t *testing.T, script string, vars map[string]string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	var pre strings.Builder
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		pre.WriteString(k + "=" + shell.Quote(vars[k]) + "\n")
	}
	cmd := exec.Command(sh, "-eu", "-c", pre.String()+script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), stderr.String())
	return stderr.String()
}

// TestHookInstallReplacesPreviousBlock runs the cleanup's unhook and the
// bashrc hook install snippet on a system bashrc that carries the block of
// a previous build with other targets, as the 60-shell section does on a
// base image that sandcast itself built: the previous block is removed with
// a build log line and the new one prepended, so only the new targets
// remain, the distribution's content follows once, and a second round is
// idempotent. A first install prepends without a notice, and a missing file
// is created.
func TestHookInstallReplacesPreviousBlock(t *testing.T) {
	dir := t.TempDir()
	sysrc := filepath.Join(dir, "bash.bashrc")
	hook := filepath.Join(dir, "hook")
	launcher := shell.HookTarget{Program: plan.ShellLoginPath}
	oldBlock := shell.BashrcHook(nil, shell.HookTargets{Top: launcher, TopLogin: launcher, Nested: launcher}, "")
	newBlock := shell.BashrcHook([]shell.Export{{Name: "SHELL", Value: shell.ZshProgram}}, shell.HookTargets{Top: shell.HookTarget{Program: "/bin/zsh"}, TopLogin: shell.HookTarget{Program: "/bin/zsh", Args: "-l"}, Nested: shell.PlainBash}, "")
	distro := "# System-wide .bashrc file for interactive bash(1) shells.\n[ -z \"$PS1\" ] && return\nshopt -s checkwinsize\n"
	vars := map[string]string{"sysrc": sysrc, "hook": hook}
	round := legacyUnhookFunc + "sandcast_legacy_unhook \"$sysrc\"\n" + hookInstallScript
	require.NoError(t, os.WriteFile(hook, []byte(newBlock), 0o644))

	require.NoError(t, os.WriteFile(sysrc, []byte(oldBlock+distro), 0o644))
	stderr := runInstallScript(t, round, vars)
	got, err := os.ReadFile(sysrc)
	require.NoError(t, err)
	assert.Equal(t, newBlock+distro, string(got), "the previous block is replaced by the new one")
	assert.NotContains(t, string(got), plan.ShellLoginPath, "no old target remains")
	assert.Equal(t, 1, strings.Count(string(got), shell.HookBegin))
	assert.Equal(t, "sandcast: removing the shell hook block of a previous sandcast build from "+sysrc+"\n", stderr)

	stderr = runInstallScript(t, round, vars)
	got, err = os.ReadFile(sysrc)
	require.NoError(t, err)
	assert.Equal(t, newBlock+distro, string(got), "a second round is idempotent")
	assert.Contains(t, stderr, "removing the shell hook block")

	require.NoError(t, os.WriteFile(sysrc, []byte(distro), 0o644))
	stderr = runInstallScript(t, round, vars)
	got, err = os.ReadFile(sysrc)
	require.NoError(t, err)
	assert.Equal(t, newBlock+distro, string(got), "a first install prepends")
	assert.Empty(t, stderr)

	require.NoError(t, os.Remove(sysrc))
	runInstallScript(t, round, vars)
	got, err = os.ReadFile(sysrc)
	require.NoError(t, err)
	assert.Equal(t, newBlock, string(got), "a missing file is created")
	assert.NoFileExists(t, sysrc+".sandcast.rest")
}

// TestZshEnvInstallReplacesPreviousBlock runs the zshenv install snippet on
// a file that carries the block of a previous sandcast build, or the bare
// export line of a build before the block existed: both are replaced by the
// current block, other content is kept, the build log says so, a second run
// is idempotent, and a missing file is created.
func TestZshEnvInstallReplacesPreviousBlock(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "zshenv")
	blockFile := filepath.Join(dir, "block")
	oldLine := "export ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1\n"
	oldBlock := shell.ZshEnvBlock(plan.ZDotDir, "", shell.ZshEnvCurated)
	newBlock := shell.ZshEnvBlock(plan.ZDotDir, plan.SessionEnvPath, shell.ZshEnvCurated)
	require.NoError(t, os.WriteFile(blockFile, []byte(newBlock), 0o644))
	distro := "# /etc/zsh/zshenv: system-wide .zshenv file for zsh(1).\nskip_global_compinit=1\n"
	vars := map[string]string{"f": f, "zshenv_block": blockFile, "zshenv_begin": shell.ZshEnvBegin, "zshenv_end": shell.ZshEnvEnd}

	for name, previous := range map[string]string{"block": oldBlock, "bare line": oldLine} {
		require.NoError(t, os.WriteFile(f, []byte(distro+previous), 0o644))
		stderr := runInstallScript(t, zshEnvInstallScript, vars)
		got, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.Equal(t, distro+newBlock, string(got), "%s: the previous block is replaced", name)
		assert.Contains(t, stderr, "replacing the zshenv block of a previous sandcast build in "+f, name)
	}

	runInstallScript(t, zshEnvInstallScript, vars)
	got, err := os.ReadFile(f)
	require.NoError(t, err)
	assert.Equal(t, distro+newBlock, string(got), "a second install is idempotent")
	assert.Equal(t, 1, strings.Count(string(got), shell.ZshEnvBegin))

	require.NoError(t, os.Remove(f))
	stderr := runInstallScript(t, zshEnvInstallScript, vars)
	got, err = os.ReadFile(f)
	require.NoError(t, err)
	assert.Equal(t, newBlock, string(got), "a missing file is created")
	assert.Empty(t, stderr)
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
		{Source: plan.DotfileSourceAllowlist, HostPath: "~/.config/starship.toml", RelocatedTo: plan.XDGConfigDir + "/starship.toml", Env: map[string]string{"STARSHIP_CONFIG": plan.XDGConfigDir + "/starship.toml"}, Content: "add_newline=false\n"},
	}
	got, err := Assemble(p)
	require.NoError(t, err)
	assert.Contains(t, got, "# 62-dotfiles")
	assert.Contains(t, got, "COPY dotfiles/etc%2Fgitconfig /etc/gitconfig")
	assert.Contains(t, got, "COPY dotfiles/etc%2Fxdg%2Fstarship.toml /etc/xdg/starship.toml")
	assert.Equal(t, "dotfiles/etc%2Fgitconfig", DotfileContextFile("/etc/gitconfig"))
	// No dotfile target may be under the overmount home.
	for _, d := range p.Dotfiles {
		assert.False(t, plan.UnderOvermount(plan.DefaultOvermountHome, d.RelocatedTo))
	}
	// The per-dotfile relocation variable is restored after the runtime
	// cleared the image ENV through the session environment script, which
	// carries the image env (plan.Build merges the relocation env into it)
	// and which both hooks source, for the zsh kind and the bash kind alike.
	for _, kind := range []string{"zsh", "bash"} {
		k := shellKindPlan(t, kind)
		k.Dotfiles = p.Dotfiles
		k.Image.Env = append(k.Image.Env, plan.EnvVar{Name: "STARSHIP_CONFIG", Value: plan.XDGConfigDir + "/starship.toml"})
		got, err = Assemble(k)
		require.NoError(t, err)
		assert.Contains(t, plan.SessionEnvScriptFor(k), "export STARSHIP_CONFIG='/etc/xdg/starship.toml'\n", kind)
		assert.Contains(t, got, "COPY shell/sandcast-env.sh /etc/profile.d/sandcast-env.sh\n", kind)
		assert.Equal(t, 3, strings.Count(got, "[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\n"), "%s: the zshenv block or the launcher, the login hook and the bashrc hook each source the session environment once", kind)
		assert.NotContains(t, got, "export STARSHIP_CONFIG=", kind)
	}
}

// TestDotfileContextFileInjective checks two distinct targets never share a
// build-context file. The previous encoding (underscore doubled, slash to
// underscore) mapped /x/a/_b and /x/a_/b to the same file, so one dotfile's
// content was baked to both destinations.
func TestDotfileContextFileInjective(t *testing.T) {
	targets := []string{
		"/x/a/_b", "/x/a_/b", "/x/a__b", "/x/a/b", "/x/a_b", "/x_/a/b", "/x/_a/b",
		"/x/a%2Fb", "/x/a%/b", "/x/a/%b", "/x/a%25b", "/etc/gitconfig", "/etc%2Fgitconfig",
	}
	seen := map[string]string{}
	for _, tg := range targets {
		f := DotfileContextFile(tg)
		require.True(t, strings.HasPrefix(f, DotfileContextDir+"/"), f)
		assert.NotContains(t, strings.TrimPrefix(f, DotfileContextDir+"/"), "/", "one flat file per target: %s", f)
		if prev, dup := seen[f]; dup {
			t.Errorf("targets %s and %s share the context file %s", prev, tg, f)
		}
		seen[f] = tg
	}
}

// TestWriteContextStagesEachDotfile checks writeContext stages one file per
// baked dotfile with its own content, even for targets that differ only by
// where a '/' and a '_' sit, and removes stale staged files.
func TestWriteContextStagesEachDotfile(t *testing.T) {
	p := shellKindPlan(t, "zsh")
	p.Dotfiles = []plan.DotfileSelection{
		{Source: plan.DotfileSourceManifest, HostPath: "~/.config/a/_b", RelocatedTo: "/etc/xdg/a/_b", Content: "first\n"},
		{Source: plan.DotfileSourceManifest, HostPath: "~/.config/a_/b", RelocatedTo: "/etc/xdg/a_/b", Content: "second\n"},
	}
	dir := t.TempDir()
	stale := filepath.Join(dir, DotfileContextDir, "stale")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o644))
	cf, err := Assemble(p)
	require.NoError(t, err)
	require.NoError(t, writeContext(dir, p, cf))
	for _, d := range p.Dotfiles {
		rel := DotfileContextFile(d.RelocatedTo)
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		require.NoError(t, err, rel)
		assert.Equal(t, d.Content, string(got), "%s carries its own content", d.RelocatedTo)
		assert.Contains(t, cf, "COPY "+rel+" "+d.RelocatedTo+"\n")
	}
	entries, err := os.ReadDir(filepath.Join(dir, DotfileContextDir))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "one staged file per dotfile, the stale one removed")
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
			assert.NotContains(t, line, plan.DefaultOvermountHome+"/",
				"kind %s: no generated instruction may touch a path under the overmount home: %q", kind, line)
		}
		// None of the shells may read a $HOME-relative rc file.
		assert.NotContains(t, got, "$HOME/.", kind)
		assert.NotContains(t, got, "~/.", kind)
	}
}

// TestWorkDirFollowsOvermountHome checks the WORKDIR and the workspace
// directory come from the plan's overmount home, the one value the guard
// uses too: a base image whose workspace is mounted
// at /workspace gets that WORKDIR, a plan without the field gets /sandbox,
// and the container target keeps the user's home.
func TestWorkDirFollowsOvermountHome(t *testing.T) {
	p := shellKindPlan(t, "bash")
	p.BaseImage.OvermountHome = "/workspace"
	p.FinalUser.Home = "/workspace"
	p.Image.Dirs = []plan.Dir{{Path: "/workspace", Owner: "user"}, {Path: "/opt/tool", Owner: "user"}}
	got, err := Assemble(p)
	require.NoError(t, err)
	assert.Contains(t, got, "RUN install -d -o sandbox -g sandbox /workspace /opt/tool\n", "the workspace dir is created once")
	assert.Contains(t, got, "WORKDIR /workspace\n")
	assert.NotContains(t, got, "/sandbox")

	got, err = Assemble(shellKindPlan(t, "bash"))
	require.NoError(t, err)
	assert.Contains(t, got, "WORKDIR /sandbox\n", "the default when the plan records no overmount home")

	p = shellKindPlan(t, "bash")
	p.Target = "container"
	p.FinalUser = plan.FinalUser{Name: "root", Group: "root", Home: "/root"}
	p.Image.Dirs = []plan.Dir{{Path: "/sandbox", Owner: "root"}}
	got, err = Assemble(p)
	require.NoError(t, err)
	assert.Contains(t, got, "WORKDIR /root\n")
	assert.Contains(t, got, "RUN install -d /sandbox\n", "the container target has no workspace dir to skip")
}

// TestZshEnvBlockPerKindAndTarget checks the global zshenv block of an image
// whose curated shell is not zsh but which has zsh (spec 002 kept, spec 003
// reconciled): on the OpenShell target it relocates zsh's startup files to
// the ZDOTDIR tree and marks the session, but leaves SHELL alone (the
// launcher is the passwd shell); on the container target, where the home is
// never overmounted, it only sources the session environment and disables
// the new-user wizard, so a zsh keeps the image home's own startup files.
// The zsh kind exports all three on both targets.
func TestZshEnvBlockPerKindAndTarget(t *testing.T) {
	withZsh := func(kind, target string) *plan.Plan {
		p := shellKindPlan(t, kind)
		p.Target = target
		p.ImageFacts.Shells = append(p.ImageFacts.Shells, "/usr/bin/zsh")
		if target == "container" {
			p.FinalUser.Created = false
		}
		return p
	}
	block := func(p *plan.Plan) string {
		t.Helper()
		got, err := shellSection(p)
		require.NoError(t, err)
		const open, close = "<<'SANDCAST_ZSHENV'\n", "\nSANDCAST_ZSHENV\n"
		i := strings.Index(got, open)
		require.GreaterOrEqual(t, i, 0, "a zshenv block:\n%s", got)
		i += len(open)
		j := strings.Index(got[i:], close)
		require.GreaterOrEqual(t, j, 0, got)
		return got[i : i+j]
	}
	envLine := "[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\n"

	bashOpenShell := withZsh("bash", "openshell")
	bashOpenShell.Image.Env = []plan.EnvVar{{Name: "GOPATH", Value: "/opt/go"}}
	assert.True(t, plan.WritesZshEnv(bashOpenShell))
	assert.True(t, plan.ZshEnvRelocates(bashOpenShell))
	assert.Equal(t, shell.ZshEnvBegin+"\nexport ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1\n"+envLine+"zsh-newuser-install() { :; }\n"+shell.ZshEnvEnd, block(bashOpenShell))

	bashContainer := withZsh("bash", "container")
	assert.True(t, plan.WritesZshEnv(bashContainer))
	assert.False(t, plan.ZshEnvRelocates(bashContainer))
	assert.Equal(t, shell.ZshEnvBegin+"\nzsh-newuser-install() { :; }\n"+shell.ZshEnvEnd, block(bashContainer), "no session script without mise, env or extra PATH directories on the container target")

	zshContainer := withZsh("zsh", "container")
	assert.True(t, plan.ZshEnvRelocates(zshContainer))
	assert.Contains(t, block(zshContainer), "export ZDOTDIR=/opt/sandcast/zdotdir SANDCAST_SESSION=1 SHELL=/bin/zsh\n")

	noZsh := shellKindPlan(t, "bash")
	assert.False(t, plan.WritesZshEnv(noZsh))
	got, err := shellSection(noZsh)
	require.NoError(t, err)
	assert.NotContains(t, got, shell.ZshEnvBegin)
}

// TestShellSectionContainerKeptUserZshLoginShell checks the container target
// sets a kept final user's passwd shell to zsh for a zsh plan (spec 002
// FR-008), with the passwd fallback, and leaves the user alone otherwise
// (FR-040): the OpenShell target sets it for every kind.
func TestShellSectionContainerKeptUserZshLoginShell(t *testing.T) {
	p := shellKindPlan(t, "zsh")
	p.Target = "container"
	p.FinalUser = plan.FinalUser{Name: "root", Group: "root", Home: "/root"}
	got, err := shellSection(p)
	require.NoError(t, err)
	assert.Contains(t, got, "usermod -s /bin/zsh root")
	assert.Contains(t, got, `sed -i 's|^\(root:[^:]*:[^:]*:[^:]*:[^:]*:[^:]*:\).*$|\1/bin/zsh|' /etc/passwd`)
	assert.NotContains(t, got, "cat > "+plan.LoginHookPath, "no hooks on the container target")

	for _, kind := range []string{"bash", "sh"} {
		p := shellKindPlan(t, kind)
		p.Target = "container"
		p.FinalUser = plan.FinalUser{Name: "root", Group: "root", Home: "/root"}
		got, err := shellSection(p)
		require.NoError(t, err)
		assert.NotContains(t, got, "usermod", kind)
	}
}

// TestLegacyCleanupStripsSpec002Layout runs the cleanup function of the
// 60-shell section on startup files that carry the blocks a build before
// this release appended (the spec 002 layout): the session environment
// block of the system bashrc and zshenv, the zsh switch hook, and the lines
// that sourced the curated rc from the system zshrc and bashrc. Each is
// removed, the rest of the file is kept, the build log names the file, and
// a file without them is left alone.
func TestLegacyCleanupStripsSpec002Layout(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}
	distroBashrc := "# System-wide .bashrc file for interactive bash(1) shells.\n[ -z \"$PS1\" ] && return\nshopt -s checkwinsize\n"
	bashrc := write("bash.bashrc", distroBashrc+
		"\n# sandcast: session environment\n[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\n"+
		"# sandcast: switch interactive terminal sessions to zsh\ncase $- in *i*) [ -t 0 ] && [ -t 1 ] && [ -z \"$ZSH_VERSION\" ] && [ -z \"$SANDCAST_NO_SHELL_SWITCH\" ] && [ -x /bin/zsh ] && SANDCAST_NO_SHELL_SWITCH=1 exec /bin/zsh -l ;; esac\n"+
		"\n[ -r /etc/profile.d/sandcast.sh ] && . /etc/profile.d/sandcast.sh\n")
	distroZshenv := "# /etc/zsh/zshenv: system-wide .zshenv file for zsh(1).\nskip_global_compinit=1\n"
	zshenv := write("zshenv", distroZshenv+"\n# sandcast: session environment\n[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\nzsh-newuser-install() { :; }\n")
	distroZshrc := "# /etc/zsh/zshrc: system-wide .zshrc file for zsh(1).\nREPORTTIME=10\n"
	zshrc := write("zshrc", distroZshrc+"\n[ -r /etc/zsh/zshrc.d/sandcast.zsh ] && . /etc/zsh/zshrc.d/sandcast.zsh\n")
	clean := write("clean", distroBashrc)

	for _, f := range []string{bashrc, zshenv, zshrc} {
		stderr := runInstallScript(t, legacyStripFunc+"sandcast_legacy_strip "+shell.Quote(f)+"\n", nil)
		assert.Contains(t, stderr, "removing the shell configuration of a previous sandcast build from "+f)
	}
	got, err := os.ReadFile(bashrc)
	require.NoError(t, err)
	assert.Equal(t, distroBashrc+"\n\n", string(got), "the bashrc keeps the distribution's content only")
	got, err = os.ReadFile(zshenv)
	require.NoError(t, err)
	assert.Equal(t, distroZshenv+"\n", string(got))
	got, err = os.ReadFile(zshrc)
	require.NoError(t, err)
	assert.Equal(t, distroZshrc+"\n", string(got))

	stderr := runInstallScript(t, legacyStripFunc+"sandcast_legacy_strip "+shell.Quote(clean)+"\nsandcast_legacy_strip "+shell.Quote(filepath.Join(dir, "absent"))+"\n", nil)
	assert.Empty(t, stderr, "a file without the old layout, or no file, is left alone")
	got, err = os.ReadFile(clean)
	require.NoError(t, err)
	assert.Equal(t, distroBashrc, string(got))

	// Every kind of 60-shell section runs the cleanup, on both targets,
	// before anything of this build is written. It also removes this
	// release's hooks (a container build from an OpenShell base image must
	// not keep them; an OpenShell build writes them again afterwards) and,
	// when this build installs no session environment script, a script of a
	// previous build.
	for _, kind := range []string{"zsh", "bash", "sh"} {
		for _, target := range []string{"openshell", "container"} {
			p := shellKindPlan(t, kind)
			p.Target = target
			got, err := shellSection(p)
			require.NoError(t, err, "%s %s", kind, target)
			i := strings.Index(got, "sandcast_legacy_strip() {")
			require.GreaterOrEqual(t, i, 0, "%s %s", kind, target)
			assert.Contains(t, got, "for f in /etc/bash.bashrc /etc/bash/bashrc; do\n  sandcast_legacy_unhook \"$f\"\ndone\n", "%s %s: the unhook list is the hook installer's list", kind, target)
			assert.Contains(t, got, "for f in /etc/zsh/zshrc.d/sandcast.zsh /etc/profile.d/sandcast.sh /etc/profile.d/zz-sandcast-shell.sh; do\n  sandcast_legacy_remove \"$f\"\ndone\n", "%s %s", kind, target)
			assert.Contains(t, got, "sandcast_legacy_remove /etc/profile.d/sandcast-mise.sh '# Generated by sandcast: keep mise shims on PATH'\n", "%s %s: the first layout's profile script goes only with its header", kind, target)
			assert.Less(t, i, strings.Index(got, "install -d /opt/sandcast"), "%s %s: the cleanup runs before this build's files are written", kind, target)
			if target == "openshell" {
				assert.Less(t, i, strings.Index(got, "cat > "+plan.LoginHookPath), "%s: the login hook is written after the cleanup", kind)
			}
			sessionRemove := "sandcast_legacy_remove /etc/profile.d/sandcast-env.sh '" + plan.SessionEnvHeader + "'\n"
			if NeedsSessionEnv(p) {
				assert.NotContains(t, got, sessionRemove, "%s %s: this build installs the script, which replaces a previous one", kind, target)
			} else {
				assert.Contains(t, got, sessionRemove, "%s %s: no script in this build, a previous one is removed", kind, target)
			}
		}
	}
}

// TestLegacyCleanupRemovesHooksAndSessionScript runs the cleanup functions
// of this release's layout: the hook block at the top of a system bashrc is
// removed and the rest kept, a file of a previous build is removed with a
// log line, the session environment script only when it carries sandcast's
// header, and a missing file or one without the block is left alone.
func TestLegacyCleanupRemovesHooksAndSessionScript(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}
	distro := "# System-wide .bashrc file for interactive bash(1) shells.\n[ -z \"$PS1\" ] && return\n"
	hooked := write("bash.bashrc", shell.BashrcHook(nil, hookTargets(shellKindPlan(t, "zsh")), plan.SessionEnvPath)+distro)
	clean := write("clean", distro)
	stderr := runInstallScript(t, legacyUnhookFunc+"sandcast_legacy_unhook "+shell.Quote(hooked)+"\nsandcast_legacy_unhook "+shell.Quote(clean)+"\nsandcast_legacy_unhook "+shell.Quote(filepath.Join(dir, "absent"))+"\n", nil)
	assert.Equal(t, "sandcast: removing the shell hook block of a previous sandcast build from "+hooked+"\n", stderr)
	got, err := os.ReadFile(hooked)
	require.NoError(t, err)
	assert.Equal(t, distro, string(got), "the hook block is removed, the distribution's content kept")
	got, err = os.ReadFile(clean)
	require.NoError(t, err)
	assert.Equal(t, distro, string(got))

	hook := write("zz-sandcast-shell.sh", "# Written by sandcast\n")
	generated := write("sandcast-env.sh", plan.SessionEnvScriptFor(shellKindPlan(t, "zsh")))
	foreign := write("other-env.sh", "# an administrator's script\nexport X=1\n")
	mentions := write("mentions.sh", "# not "+plan.SessionEnvHeader+"\nexport X=1\n")
	stderr = runInstallScript(t, legacyRemoveFunc+
		"sandcast_legacy_remove "+shell.Quote(hook)+"\n"+
		"sandcast_legacy_remove "+shell.Quote(generated)+" '"+plan.SessionEnvHeader+"'\n"+
		"sandcast_legacy_remove "+shell.Quote(foreign)+" '"+plan.SessionEnvHeader+"'\n"+
		"sandcast_legacy_remove "+shell.Quote(mentions)+" '"+plan.SessionEnvHeader+"'\n"+
		"sandcast_legacy_remove "+shell.Quote(filepath.Join(dir, "absent"))+"\n", nil)
	assert.Equal(t, "sandcast: removing "+hook+" of a previous sandcast build\nsandcast: removing "+generated+" of a previous sandcast build\n", stderr)
	assert.NoFileExists(t, hook)
	assert.NoFileExists(t, generated, "the script sandcast writes starts with its header")
	assert.FileExists(t, foreign, "a script of another origin at the same path is kept")
	assert.FileExists(t, mentions, "the header must start the first line, not only appear in it")
}

// TestNeedsSessionEnvForSandcastBuiltBase: a layered build over a base image
// that sandcast built, whose mise tools the delta keeps (skip), writes the
// session environment script again instead of removing the base's, on both
// targets, so login shells keep the MISE_* settings and the shims; the
// shims on the base image's PATH count the same way.
func TestNeedsSessionEnvForSandcastBuiltBase(t *testing.T) {
	for _, target := range []string{"container", "openshell"} {
		p := testPlan(t)
		p.Target = target
		p.Policy = ""
		p.Shell = plan.Shell{Program: "bash", RCPath: plan.BashRCPath, RC: "alias ll='ls -l'\n"}
		p.Image.Env = nil
		p.ImageFacts.Path = []string{"/usr/local/bin", "/usr/bin", "/bin"}
		p.Components = slices.DeleteFunc(p.Components, func(c plan.Component) bool { return c.Key == "zsh" })
		for i := range p.Components {
			if p.Components[i].Key == "go" {
				p.Components[i].Action = plan.ActionSkip
			}
		}
		assert.False(t, usesMise(p), target)
		assert.True(t, NeedsSessionEnv(p), "%s: a skipped mise component means the base provides mise", target)
		got, err := Assemble(p)
		require.NoError(t, err, target)
		assert.Contains(t, got, "COPY shell/sandcast-env.sh /etc/profile.d/sandcast-env.sh\n", target)
		assert.NotContains(t, got, "sandcast_legacy_remove /etc/profile.d/sandcast-env.sh", "%s: the base's script is replaced, not removed", target)

		p.Components = slices.DeleteFunc(p.Components, func(c plan.Component) bool { return c.Key == "go" })
		assert.False(t, NeedsSessionEnv(p) && target == "container", "%s: without mise and env, no script", target)
		p.ImageFacts.Path = []string{plan.MiseShimsDir, "/usr/local/bin", "/usr/bin", "/bin"}
		assert.True(t, NeedsSessionEnv(p), "%s: the shims on the base image's PATH", target)
	}
}
