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
		assert.Contains(t, got, "replacing the shell hook block of a previous sandcast build", "%s: a previous block is replaced, not kept", kind)
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
	assert.NotContains(t, bash, "sandcast-env.sh", "no session environment without mise, image env or extra PATH directories")

	p := shellKindPlan(t, "zsh")
	p.Target = "container"
	got, err := shellSection(p)
	require.NoError(t, err)
	assert.NotContains(t, got, plan.LoginHookPath, "the container target gets no hooks")
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

// TestHookInstallReplacesPreviousBlock runs the bashrc hook install snippet
// twice with different targets, as a build on a base image that sandcast
// itself built does: the block of the previous build is replaced, not kept,
// so only the new targets remain, the distribution's content follows once,
// the build log says so, and the result is idempotent. A first install
// prepends without a notice, and a missing file is created.
func TestHookInstallReplacesPreviousBlock(t *testing.T) {
	dir := t.TempDir()
	sysrc := filepath.Join(dir, "bash.bashrc")
	hook := filepath.Join(dir, "hook")
	launcher := shell.HookTarget{Program: plan.ShellLoginPath}
	oldBlock := shell.BashrcHook(nil, shell.HookTargets{Top: launcher, TopLogin: launcher, Nested: launcher}, "")
	newBlock := shell.BashrcHook([]shell.Export{{Name: "ZDOTDIR", Value: plan.ZDotDir}}, shell.HookTargets{Top: shell.HookTarget{Program: "/bin/zsh"}, TopLogin: shell.HookTarget{Program: "/bin/zsh", Args: "-l"}, Nested: shell.PlainBash}, "")
	distro := "# System-wide .bashrc file for interactive bash(1) shells.\n[ -z \"$PS1\" ] && return\nshopt -s checkwinsize\n"
	vars := map[string]string{"sysrc": sysrc, "hook": hook, "hook_begin": shell.HookBegin, "hook_end": shell.HookEnd}
	require.NoError(t, os.WriteFile(hook, []byte(newBlock), 0o644))

	require.NoError(t, os.WriteFile(sysrc, []byte(oldBlock+distro), 0o644))
	stderr := runInstallScript(t, hookInstallScript, vars)
	got, err := os.ReadFile(sysrc)
	require.NoError(t, err)
	assert.Equal(t, newBlock+distro, string(got), "the previous block is replaced by the new one")
	assert.NotContains(t, string(got), plan.ShellLoginPath, "no old target remains")
	assert.Equal(t, 1, strings.Count(string(got), shell.HookBegin))
	assert.Contains(t, stderr, "replacing the shell hook block of a previous sandcast build in "+sysrc)

	stderr = runInstallScript(t, hookInstallScript, vars)
	got, err = os.ReadFile(sysrc)
	require.NoError(t, err)
	assert.Equal(t, newBlock+distro, string(got), "a second install is idempotent")
	assert.Contains(t, stderr, "replacing")

	require.NoError(t, os.WriteFile(sysrc, []byte(distro), 0o644))
	stderr = runInstallScript(t, hookInstallScript, vars)
	got, err = os.ReadFile(sysrc)
	require.NoError(t, err)
	assert.Equal(t, newBlock+distro, string(got), "a first install prepends")
	assert.Empty(t, stderr)

	require.NoError(t, os.Remove(sysrc))
	runInstallScript(t, hookInstallScript, vars)
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
	oldBlock := shell.ZshEnvBlock(plan.ZDotDir, "")
	newBlock := shell.ZshEnvBlock(plan.ZDotDir, plan.SessionEnvPath)
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
		assert.GreaterOrEqual(t, strings.Count(got, "[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh\n"), 2, "%s: both hooks source the session environment", kind)
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
