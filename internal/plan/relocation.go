package plan

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/redact"
)

// safeTargetRe matches an absolute image path built only from characters that
// are inert in both the shell and a Containerfile instruction. A relocation
// target that fails it would let a crafted dotfile path inject into the
// generated COPY/RUN instructions, so such a target is skipped (spec 003 US3,
// constitution IV).
var safeTargetRe = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)

// DotfileInput is one dotfile the pipeline read from the host for the plan to
// resolve. The plan redacts Content, picks the relocation target and emits the
// notice; the pipeline supplies the host facing values (spec 003 US3).
type DotfileInput struct {
	// HostPath is the manifest path the entry came from (kept for notices).
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
	out := make([]EnvVar, 0, len(env))
	for _, k := range sortedKeys(env) {
		out = append(out, EnvVar{Name: k, Value: env[k]})
	}
	return out
}

// mergeRelocationEnv sets the relocation variables in setup.Env, then
// re-sorts. Unlike the first-wins merge of component and harness env, the
// relocation value always wins: a component or harness that sets ZDOTDIR,
// STARSHIP_CONFIG or INPUTRC to another value would point the shell or the
// tool back at a file in the overmounted home (spec 003 FR-002, FR-008). It
// returns one notice per replaced value, in name order.
func mergeRelocationEnv(setup *ImageSetup, relocation []EnvVar) []string {
	env := map[string]string{}
	for _, e := range setup.Env {
		env[e.Name] = e.Value
	}
	var notices []string
	for _, e := range relocation {
		if prev, ok := env[e.Name]; ok && prev != e.Value {
			notices = append(notices, fmt.Sprintf(
				"component or harness env %s=%s is replaced by the relocation value %s, so the configuration is not read from the overmounted home",
				e.Name, prev, e.Value))
		}
		env[e.Name] = e.Value
	}
	setup.Env = make([]EnvVar, 0, len(env))
	for _, k := range sortedKeys(env) {
		setup.Env = append(setup.Env, EnvVar{Name: k, Value: env[k]})
	}
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
// Every target is outside the home: a system path the tool reads natively, a
// per-tool variable, or the XDG tree.
func relocateDotfile(hostPath string) (target string, env map[string]string) {
	norm := strings.TrimPrefix(hostPath, "~/")
	norm = strings.TrimPrefix(norm, "./")
	norm = path.Clean(norm)
	base := path.Base(norm)
	switch {
	case base == ".gitconfig" || norm == ".config/git/config" || base == "gitconfig":
		// git reads /etc/gitconfig as system config natively, no env needed.
		return GitConfigPath, nil
	case base == "starship.toml":
		t := XDGConfigHome + "/starship.toml"
		return t, map[string]string{"STARSHIP_CONFIG": t}
	case base == ".tmux.conf" || base == "tmux.conf":
		return XDGConfigHome + "/tmux/tmux.conf", map[string]string{"XDG_CONFIG_HOME": XDGConfigHome}
	case base == ".inputrc" || base == "inputrc":
		return InputRCPath, map[string]string{"INPUTRC": InputRCPath}
	default:
		// Generic: relocate under the XDG tree, preserving a .config/ subpath
		// so a tool that already reads XDG_CONFIG_HOME finds it.
		rel := strings.TrimPrefix(norm, ".config/")
		rel = strings.TrimPrefix(rel, "./")
		rel = strings.TrimPrefix(strings.TrimPrefix(rel, "."), "/")
		if rel == "" || rel == "." {
			rel = base
		}
		return XDGConfigHome + "/" + rel, map[string]string{"XDG_CONFIG_HOME": XDGConfigHome}
	}
}

// resolveDotfiles maps the dotfile inputs to baked selections, redacting each
// one's content so no credential enters the image (FR-007). On a collision
// (two inputs relocate to the same target) the explicit manifest entry wins
// over an allowlist proposal, deterministically, and a notice reports it
// (spec Edge Case). It returns the sorted selections, one relocated notice per
// selection, and any dedup notices.
func resolveDotfiles(inputs []DotfileInput, overmount string) ([]DotfileSelection, []RelocationNotice, []string, error) {
	byTarget := map[string]DotfileSelection{}
	var dedup []string
	for _, in := range inputs {
		target, env := relocateDotfile(in.HostPath)
		if UnderOvermount(overmount, target) {
			return nil, nil, nil, fmt.Errorf("dotfile %s would relocate to %s, under the overmount home %s", in.HostPath, target, overmount)
		}
		if !safeTargetRe.MatchString(target) {
			dedup = append(dedup, fmt.Sprintf(
				"dotfile %s relocates to an unsafe image path %q (only letters, digits, '.', '_' and '-' are allowed in a path segment); skipped",
				in.HostPath, target))
			continue
		}
		content, _ := redact.Replace(in.Content)
		source := DotfileSourceManifest
		if in.FromAllowlist {
			source = DotfileSourceAllowlist
		}
		sel := DotfileSelection{Source: source, HostPath: in.HostPath, RelocatedTo: target, Env: env, Content: content}
		prev, clash := byTarget[target]
		if !clash {
			byTarget[target] = sel
			continue
		}
		winner := dedupDotfiles(prev, sel)
		byTarget[target] = winner
		dedup = append(dedup, fmt.Sprintf(
			"dotfiles %s and %s both relocate to %s; %s is used (%s source wins)",
			prev.HostPath, sel.HostPath, target, winner.HostPath, winner.Source))
	}
	sels := make([]DotfileSelection, 0, len(byTarget))
	for _, s := range byTarget {
		sels = append(sels, s)
	}
	sort.Slice(sels, func(i, j int) bool { return sels[i].RelocatedTo < sels[j].RelocatedTo })
	notices := make([]RelocationNotice, 0, len(sels))
	for _, s := range sels {
		notices = append(notices, RelocationNotice{Kind: NoticeRelocated, Subject: s.HostPath, From: s.HostPath, To: s.RelocatedTo})
	}
	sort.Strings(dedup)
	return sels, notices, dedup, nil
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

// bakedPaths lists every image path sandcast writes for the interactive
// environment of p, so the overmount guard can assert none lands under the
// home (FR-008, SC-004).
func bakedPaths(p *Plan) []string {
	var out []string
	switch ShellKind(p) {
	case "zsh":
		out = append(out, ZshEnvPaths...)
		out = append(out, ZDotDir)
		if p.Shell.RCPath != "" {
			out = append(out, p.Shell.RCPath)
		}
	case "bash":
		// The bash launcher and its rcfile are always baked (the rcfile is
		// created empty when there is no curated content), so both are listed
		// unconditionally.
		out = append(out, ShellLoginPath, BashRCPath)
	default:
		out = append(out, ProfileScriptPath, ShLoginPath)
	}
	if p.Target == manifest.TargetOpenShell {
		// The shell hooks for shells OpenShell starts directly.
		out = append(out, LoginHookPath, RedirectHome, RedirectHome+"/.profile")
		out = append(out, SystemBashrcPaths...)
	}
	for _, d := range p.Dotfiles {
		out = append(out, d.RelocatedTo)
	}
	return out
}

// checkOvermountGuard fails when any path sandcast bakes for the interactive
// environment would resolve under the overmount home (all are fixed
// /opt/sandcast and /etc locations, so that is a programming error; defense
// in depth for SC-004), or when the final image value of a relocation or
// startup-file variable points under it, which a component or harness env
// can cause (FR-002, FR-008).
func checkOvermountGuard(p *Plan, overmount string) error {
	for _, bp := range bakedPaths(p) {
		if UnderOvermount(overmount, bp) {
			return fmt.Errorf("plan would bake %s under the overmount home %s, where it is hidden at runtime", bp, overmount)
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
		if guarded[e.Name] && UnderOvermount(overmount, path.Clean(e.Value)) {
			return fmt.Errorf("image env %s=%s points under the overmount home %s, so a shell or tool would read its configuration from the mounted workspace; remove it from the component or harness",
				e.Name, e.Value, overmount)
		}
	}
	return nil
}
