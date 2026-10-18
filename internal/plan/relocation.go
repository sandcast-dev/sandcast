package plan

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/redact"
	"github.com/rhuss/sandcast/internal/shell"
)

// DotfileInput is one dotfile the pipeline read from the host for the plan to
// resolve. The plan redacts Content, picks the relocation target and emits the
// notice; the pipeline supplies the host facing values (spec 003 US3).
type DotfileInput struct {
	// HostPath is the manifest path the entry came from. It selects the
	// relocation target (relocateDotfile) and is shown in the notices.
	HostPath string
	// Content is the raw host file content, redacted by the plan before it is
	// baked.
	Content string
	// FromAllowlist is true when capture proposed the entry from the bounded
	// host allowlist (R4), false for an explicit manifest path (FR-006).
	FromAllowlist bool
}

// ShellKind is the interactive shell the image relocates for: "zsh", "bash"
// or "sh" (the minimal safe default for anything else, FR-011). It follows
// the same precedence the created user's login shell uses: the manifest shell
// program, then an explicit zsh component, then bash reported by the base
// image, else sh.
func ShellKind(p *Plan) string {
	switch p.Shell.Program {
	case "zsh":
		return "zsh"
	case "bash":
		return "bash"
	}
	for _, c := range p.Components {
		if c.Key == "zsh" {
			return "zsh"
		}
	}
	for _, s := range p.ImageFacts.Shells {
		if strings.HasSuffix(s, "/bash") {
			return "bash"
		}
	}
	return "sh"
}

// HasZsh reports whether the image has zsh: in the base image or planned as
// a component.
func HasZsh(p *Plan) bool {
	for _, s := range p.ImageFacts.Shells {
		if path.Base(s) == "zsh" {
			return true
		}
	}
	return p.Component("zsh") != nil
}

// WritesZshEnv reports whether the build writes the sandcast block to the
// global zshenv files (ZshEnvPaths): for the zsh kind, and for any other
// kind when the image has zsh, so a zsh the user starts gets the session
// environment and never its new-user wizard (spec 002). The build and the
// overmount guard (bakedPaths) read this one predicate.
func WritesZshEnv(p *Plan) bool {
	return ShellKind(p) == "zsh" || HasZsh(p)
}

// ZshEnvRelocates reports whether that block also moves zsh's startup files
// to ZDotDir: always for the zsh kind, and for another kind on the OpenShell
// target, where the home is the mounted workspace and a zsh the user starts
// must never read a .zshrc there (spec 003 FR-002). On the container target
// such a zsh keeps the image home's own startup files (FR-040).
func ZshEnvRelocates(p *Plan) bool {
	return WritesZshEnv(p) && (ShellKind(p) == "zsh" || p.Target == manifest.TargetOpenShell)
}

// RelocationEnv returns the relocation environment variables the image sets so
// that every process (not only interactive shells) reads baked configuration:
// ZDOTDIR for zsh plus the per-dotfile variables. The result is sorted by name
// so the plan and Containerfile stay byte-identical across rebuilds (FR-004,
// research R3).
func RelocationEnv(p *Plan) []EnvVar {
	env := map[string]string{}
	if ShellKind(p) == "zsh" {
		env["ZDOTDIR"] = ZDotDir
	}
	for _, d := range p.Dotfiles {
		for k, v := range d.Env {
			if _, ok := env[k]; !ok {
				env[k] = v
			}
		}
	}
	return sortedEnv(env)
}

// mergeRelocationEnv sets the relocation variables in setup.Env, then
// re-sorts. Unlike the first-wins merge of component and harness env, the
// relocation value always wins: a component or harness that sets ZDOTDIR,
// STARSHIP_CONFIG or INPUTRC to another value would point the shell or the
// tool back at a file in the overmounted home (spec 003 FR-002, FR-008). It
// returns one notice per replaced value, in name order.
func mergeRelocationEnv(setup *ImageSetup, relocation []EnvVar) []string {
	env := envMap(setup.Env)
	var notices []string
	for _, e := range relocation {
		if prev, ok := env[e.Name]; ok && prev != e.Value {
			notices = append(notices, fmt.Sprintf(
				"component or harness env %s=%s is replaced by the relocation value %s, so the configuration is not read from the overmounted home",
				e.Name, prev, e.Value))
		}
		env[e.Name] = e.Value
	}
	setup.Env = sortedEnv(env)
	sort.Strings(notices)
	return notices
}

// startupEnvVars name a file or directory a shell or tool reads its startup
// configuration from. A final image value under the overmount home would make
// it read a workspace file (BASH_ENV for every non-interactive bash, ENV for
// interactive POSIX shells, ZDOTDIR for zsh), so the guard refuses it.
var startupEnvVars = []string{"BASH_ENV", "ENV", "ZDOTDIR"}

// relocateDotfile maps a host dotfile path to the image path it is baked to
// and the environment variables that point the tool at it (research R5).
// Every target is outside the home: a system path the tool reads natively
// (/etc/gitconfig, /etc/tmux.conf, /etc/xdg in the default XDG_CONFIG_DIRS),
// or a baked file named by a tool-specific variable (STARSHIP_CONFIG,
// INPUTRC). No dotfile sets XDG_CONFIG_HOME: an image-wide value would point
// every tool that writes its configuration there (gh, helm, glab) at a
// root-owned tree. resolveDotfiles refuses a target that is not
// a clean, safe absolute path.
func relocateDotfile(hostPath string) (target string, env map[string]string) {
	norm := normHostPath(hostPath)
	base := path.Base(norm)
	switch {
	case base == ".gitconfig" || base == "gitconfig" || gitConfigXDG(norm):
		// git reads /etc/gitconfig as system config natively, no env needed.
		// Both git configuration files go there; resolveDotfiles merges them.
		return GitConfigPath, nil
	case base == "starship.toml":
		t := XDGConfigDir + "/starship.toml"
		return t, map[string]string{"STARSHIP_CONFIG": t}
	case base == ".tmux.conf" || base == "tmux.conf":
		// tmux reads /etc/tmux.conf natively before any user file.
		return TmuxConfPath, nil
	case base == ".inputrc" || base == "inputrc":
		return InputRCPath, map[string]string{"INPUTRC": InputRCPath}
	default:
		// Generic: the system XDG directory, keeping the part below a
		// .config/ segment wherever it sits in the source path (~/.config/x,
		// or a project copy such as dotfiles/.config/x), so a tool that
		// searches XDG_CONFIG_DIRS finds it. Without such a segment the
		// whole path is kept, with a leading dot dropped (.editorconfig
		// becomes editorconfig).
		rel := norm
		if i := strings.Index("/"+norm, "/.config/"); i >= 0 {
			rel = norm[i+len(".config/"):]
		}
		rel = strings.TrimPrefix(strings.TrimPrefix(rel, "."), "/")
		return XDGConfigDir + "/" + rel, nil
	}
}

// normHostPath is the manifest path relocateDotfile classifies: without the
// "~/" prefix, cleaned.
func normHostPath(hostPath string) string {
	return path.Clean(strings.TrimPrefix(hostPath, "~/"))
}

// gitConfigXDG reports whether a normalized host path is git's XDG
// configuration: ~/.config/git/config, or a copy that keeps the
// .config/git/config layout (a dotfiles directory in the project). Every
// other path that relocates to GitConfigPath is a ~/.gitconfig.
func gitConfigXDG(norm string) bool {
	return norm == ".config/git/config" || strings.HasSuffix(norm, "/.config/git/config")
}

// InputRCInclude keeps the system readline defaults in the baked inputrc:
// INPUTRC replaces /etc/inputrc as well as ~/.inputrc.
const InputRCInclude = "$include /etc/inputrc\n"

// resolveDotfiles maps the dotfile inputs to baked selections, redacting each
// one's content so no credential enters the image (FR-007). On a collision
// (two inputs relocate to the same target) the explicit manifest entry wins
// over an allowlist proposal, deterministically, and a notice reports it
// (spec Edge Case). git configuration is the exception: git reads both its
// XDG file and ~/.gitconfig, so one of each is merged into /etc/gitconfig in
// git's order rather than one being dropped (mergeGitConfig). It returns the
// selections sorted by target, one relocated notice per baked source path,
// and the skip, dedup and merge notices. Whether a target lies under the
// overmount home is checked once, by the overmount guard (bakedPaths).
func resolveDotfiles(inputs []DotfileInput) ([]DotfileSelection, []RelocationNotice, []string) {
	// slot is where a dotfile goes: its target, and for /etc/gitconfig which
	// of git's two files it is, since those are merged, not deduplicated.
	type slot struct {
		target string
		xdgGit bool
	}
	bySlot := map[slot]DotfileSelection{}
	var extra []string
	for _, in := range inputs {
		target, env := relocateDotfile(in.HostPath)
		// A target that is not a safe path would let a crafted dotfile path
		// inject into the generated COPY and RUN instructions, or escape its
		// tree with a "." or ".." segment, so it is skipped
		// (spec 003 US3, constitution IV).
		if !shell.SafeAbsPath(target) {
			extra = append(extra, fmt.Sprintf(
				"dotfile %s relocates to an unsafe image path %q (only letters, digits, '.', '_' and '-' are allowed in a path segment, and no segment may be . or ..); skipped",
				in.HostPath, target))
			continue
		}
		content, _ := redact.Replace(in.Content)
		if target == InputRCPath && !strings.HasPrefix(content, InputRCInclude) {
			content = InputRCInclude + content
		}
		source := DotfileSourceManifest
		if in.FromAllowlist {
			source = DotfileSourceAllowlist
		}
		sel := DotfileSelection{Source: source, HostPath: in.HostPath, RelocatedTo: target, Env: env, Content: content}
		k := slot{target: target, xdgGit: target == GitConfigPath && gitConfigXDG(normHostPath(in.HostPath))}
		prev, clash := bySlot[k]
		if !clash {
			bySlot[k] = sel
			continue
		}
		winner := dedupDotfiles(prev, sel)
		bySlot[k] = winner
		extra = append(extra, fmt.Sprintf(
			"dotfiles %s and %s both relocate to %s; %s is used (%s source wins)",
			prev.HostPath, sel.HostPath, target, winner.HostPath, winner.Source))
	}
	xdgSlot, globalSlot := slot{GitConfigPath, true}, slot{GitConfigPath, false}
	if xdg, ok := bySlot[xdgSlot]; ok {
		if global, ok := bySlot[globalSlot]; ok {
			delete(bySlot, xdgSlot)
			bySlot[globalSlot] = mergeGitConfig(xdg, global)
			extra = append(extra, fmt.Sprintf(
				"dotfiles %s and %s are both git configuration; they are merged into %s in git's order, %s last so its values win",
				xdg.HostPath, global.HostPath, GitConfigPath, global.HostPath))
		}
	}
	sels := make([]DotfileSelection, 0, len(bySlot))
	for _, s := range bySlot {
		sels = append(sels, s)
	}
	sort.Slice(sels, func(i, j int) bool { return sels[i].RelocatedTo < sels[j].RelocatedTo })
	var notices []RelocationNotice
	for _, s := range sels {
		for _, hp := range append(slices.Clone(s.MergedFrom), s.HostPath) {
			notices = append(notices, RelocationNotice{Kind: NoticeRelocated, Subject: hp, To: s.RelocatedTo})
		}
	}
	sort.Strings(extra)
	return sels, notices, extra
}

// mergeGitConfig concatenates git's XDG configuration (first) and a
// ~/.gitconfig (last) into one /etc/gitconfig. git reads both, the XDG file
// before ~/.gitconfig, so a key set in both takes the ~/.gitconfig value; the
// concatenation keeps that, because git lets the last value of a key win
// within one file as well. Each part starts with a comment that
// names its source.
func mergeGitConfig(first, last DotfileSelection) DotfileSelection {
	merged := last
	merged.MergedFrom = []string{first.HostPath}
	merged.Content = gitConfigPart(first) + gitConfigPart(last)
	return merged
}

func gitConfigPart(s DotfileSelection) string {
	c := s.Content
	if c != "" && !strings.HasSuffix(c, "\n") {
		c += "\n"
	}
	return "# sandcast: from " + s.HostPath + "\n" + c
}

// dedupDotfiles picks the winner of two dotfiles that relocate to the same
// target: an explicit manifest entry beats an allowlist proposal; otherwise
// the smaller host path wins, so the choice is deterministic.
func dedupDotfiles(a, b DotfileSelection) DotfileSelection {
	switch {
	case a.Source == DotfileSourceManifest && b.Source != DotfileSourceManifest:
		return a
	case b.Source == DotfileSourceManifest && a.Source != DotfileSourceManifest:
		return b
	case a.HostPath <= b.HostPath:
		return a
	default:
		return b
	}
}

// bakedPath is one image path the plan writes or declares, with what it is
// for the guard's error message.
type bakedPath struct {
	path, what string
	// mountPointOK allows the path to be the overmount home itself: the
	// workspace directory, which the build creates and the policy grants.
	mountPointOK bool
}

// bakedPaths lists every image path the plan writes or declares, so the
// overmount guard can assert none lands under the home (FR-008, SC-004): the
// shell configuration, launchers and hooks, each dotfile target (this is the
// only overmount check for dotfiles), the component and harness directories,
// and the policy paths (component and harness read_only and read_write,
// fragment trees, /opt/sandcast and /opt/mise).
func bakedPaths(p *Plan, policyPaths []string) []bakedPath {
	var shellFiles []string
	if WritesZshEnv(p) {
		shellFiles = append(shellFiles, ZshEnvPaths...)
	}
	if ZshEnvRelocates(p) {
		shellFiles = append(shellFiles, ZDotDir)
	}
	switch ShellKind(p) {
	case "zsh":
		if p.Shell.RCPath != "" {
			shellFiles = append(shellFiles, p.Shell.RCPath)
		}
	case "bash":
		// The bash launcher and its rcfile are always baked (the rcfile is
		// created empty when there is no curated content), so both are listed
		// unconditionally.
		shellFiles = append(shellFiles, ShellLoginPath, BashRCPath)
	default:
		shellFiles = append(shellFiles, ProfileScriptPath, ShLoginPath)
	}
	if p.Target == manifest.TargetOpenShell {
		// The shell hooks for shells OpenShell starts directly.
		shellFiles = append(shellFiles, LoginHookPath, RedirectHome, RedirectHome+"/.profile")
		shellFiles = append(shellFiles, SystemBashrcPaths...)
	}
	out := make([]bakedPath, 0, len(shellFiles)+len(p.Dotfiles)+len(p.Image.Dirs)+len(policyPaths))
	for _, f := range shellFiles {
		out = append(out, bakedPath{path: f, what: "shell configuration"})
	}
	for _, d := range p.Dotfiles {
		out = append(out, bakedPath{path: d.RelocatedTo, what: "dotfile " + d.HostPath})
	}
	for _, d := range p.Image.Dirs {
		out = append(out, bakedPath{path: d.Path, what: "directory of a component or harness", mountPointOK: true})
	}
	for _, pp := range policyPaths {
		out = append(out, bakedPath{path: pp, what: "policy path of a component, harness or fragment", mountPointOK: true})
	}
	return out
}

// checkOvermountGuard fails when any path the plan bakes or declares
// (bakedPaths) would resolve under the overmount home, where it is hidden
// once the workspace is mounted, or when the final image value of a
// relocation or startup-file variable points under it, which a component or
// harness env can cause (FR-002, FR-008, SC-004). Such a value must be a
// plain absolute path: bash expands BASH_ENV, dash expands ENV and readline
// tilde-expands INPUTRC at run time, so "$HOME/.bashrc" or "~/.shrc" names a
// workspace file although it does not start with the overmount home.
// policyPaths are the image policy paths besides the pinned defaults.
func checkOvermountGuard(p *Plan, overmount string, policyPaths []string) error {
	if overmount == "" {
		return nil
	}
	for _, bp := range bakedPaths(p, policyPaths) {
		clean := path.Clean(bp.path)
		if bp.mountPointOK && clean == overmount {
			continue
		}
		if UnderOvermount(overmount, clean) {
			return fmt.Errorf("%s (%s) lies under the overmount home %s and would be hidden once the workspace is mounted; use a path outside it",
				bp.path, bp.what, overmount)
		}
	}
	guarded := map[string]bool{}
	for _, n := range startupEnvVars {
		guarded[n] = true
	}
	for _, e := range RelocationEnv(p) {
		guarded[e.Name] = true
	}
	for _, e := range p.Image.Env {
		if !guarded[e.Name] {
			continue
		}
		if UnderOvermount(overmount, path.Clean(e.Value)) {
			return fmt.Errorf("image env %s=%s points under the overmount home %s, so a shell or tool would read its configuration from the mounted workspace; remove it from the component or harness",
				e.Name, e.Value, overmount)
		}
		if !shell.SafeAbsPath(e.Value) {
			return fmt.Errorf("image env %s=%s is not a plain absolute path; the shell expands it at run time, so it could name a file in the mounted workspace; use an absolute path outside the overmount home %s in the component or harness",
				e.Name, e.Value, overmount)
		}
	}
	return nil
}
