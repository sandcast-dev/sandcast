package plan

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/mise"
	"github.com/rhuss/sandcast/internal/policy"
	"github.com/rhuss/sandcast/internal/probe"
	"github.com/rhuss/sandcast/internal/profile"
	"github.com/rhuss/sandcast/internal/version"
)

// Relocation layout: every file sandcast bakes for the interactive
// environment lives under this prefix or another /etc path, outside the
// workspace volume that OpenShell mounts over the home. See spec 003
// (FR-001, FR-003, FR-008) and research R1/R3.
const (
	// RelocationPrefix holds all image-baked shell and dotfile configuration,
	// outside the overmounted home.
	RelocationPrefix = "/opt/sandcast"
	// ZDotDir is the zsh ZDOTDIR tree. With ZDOTDIR set (in the global zshenv
	// and as image ENV) zsh reads its startup files here and never from
	// $HOME, so a workspace $HOME/.zshrc is never sourced (research R1, spec
	// US2).
	ZDotDir = RelocationPrefix + "/zdotdir"
	// ZshRCPath is the curated zsh configuration, baked inside the ZDOTDIR
	// tree rather than under /etc/zsh so it survives the overmount.
	ZshRCPath = ZDotDir + "/.zshrc"
	// BashRCPath is the curated bash configuration, loaded only through the
	// baked launcher with --rcfile (bash has no ZDOTDIR equivalent).
	BashRCPath = RelocationPrefix + "/bashrc"
	// ShellLoginPath is the bash login launcher: it execs bash with
	// --noprofile and the baked --rcfile in place of ~/.bashrc, so no $HOME
	// startup file in the mounted workspace is ever sourced (research R1,
	// spec US2).
	ShellLoginPath = RelocationPrefix + "/shell-login"
	// XDGConfigDir is where generic relocated dotfiles go: the system XDG
	// configuration directory, in the default XDG_CONFIG_DIRS search path.
	// sandcast never sets XDG_CONFIG_HOME image-wide, because tools write
	// their own configuration there and a root-owned baked tree would make
	// them fail (FINDING-39).
	XDGConfigDir = "/etc/xdg"
	// TmuxConfPath is tmux's system configuration, read natively outside the
	// home before any user file.
	TmuxConfPath = "/etc/tmux.conf"
	// GitConfigPath is git's system configuration, read natively outside the
	// home.
	GitConfigPath = "/etc/gitconfig"
	// InputRCPath is the baked readline configuration (pointed at by
	// INPUTRC). INPUTRC replaces /etc/inputrc as well as ~/.inputrc, so the
	// baked file starts with "$include /etc/inputrc" (InputRCInclude).
	InputRCPath = RelocationPrefix + "/inputrc"
	// ProfileScriptPath is the minimal safe default for the sh fallback and an
	// env fallback (research R2, FR-011).
	ProfileScriptPath = "/etc/profile.d/sandcast.sh"
	// ShLoginPath is the sh fallback launcher: it points $ENV at the baked
	// profile and execs a non-login /bin/sh, so no $HOME/.profile in the
	// mounted workspace is ever sourced (research R2, spec US2/FR-002).
	ShLoginPath = RelocationPrefix + "/sh-login"
	// LoginHookPath is the login shell hook. /etc/profile sources it last for
	// every login shell; it replaces a login bash before bash reads a profile
	// file from the home and redirects HOME for other POSIX shells (spec 003
	// FR-002, docs/guides/build.md).
	LoginHookPath = "/etc/profile.d/zz-sandcast-shell.sh"
	// RedirectHome is where the login hook points HOME for a POSIX login
	// shell other than bash, so the shell reads RedirectHome/.profile (which
	// restores HOME) instead of a workspace .profile.
	RedirectHome = RelocationPrefix + "/home"
	// OvermountHome is the path OpenShell mounts the workspace volume over at
	// runtime on the sandbox base images. Anything baked under it is hidden
	// once the volume is mounted (spec US4, FR-008/FR-009). It is a property
	// of the base image; the current base images use /sandbox.
	OvermountHome = "/sandbox"
)

// ZshEnvPaths are the global zshenv files sandcast sets ZDOTDIR in. zsh reads
// its global zshenv first and unconditionally, before any $HOME file, but the
// location is a build option: Debian and Ubuntu (both shipped OpenShell base
// images) use /etc/zsh/zshenv and never read /etc/zshenv, other builds use
// /etc/zshenv. sandcast writes both, so ZDOTDIR is set even when a runtime
// clears the image ENV (OpenShell starts shells after env_clear).
var ZshEnvPaths = []string{"/etc/zsh/zshenv", "/etc/zshenv"}

// SystemBashrcPaths are the system bashrc locations bash builds use (Debian
// and Ubuntu, then Alpine). The build prepends the bashrc hook to the one the
// image's bash was compiled with; bash builds without one (Fedora, RHEL, the
// official bash image) read no system file before the home .bashrc.
var SystemBashrcPaths = []string{"/etc/bash.bashrc", "/etc/bash/bashrc"}

// OvermountPrefix returns the overmount home prefix for a target, or "" when
// the target applies no sandbox mount. The container target keeps the base
// image home and is never overmounted (FR-040), so only the OpenShell target
// has a prefix.
func OvermountPrefix(target string) string {
	if target == manifest.TargetOpenShell {
		return OvermountHome
	}
	return ""
}

// UnderOvermount reports whether an absolute image path would resolve under
// the overmount home prefix (and therefore be hidden at runtime). An empty
// prefix (no overmount) is never under it.
func UnderOvermount(prefix, p string) bool {
	if prefix == "" {
		return false
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// LockPathRel is the committed lockfile path recorded in the plan.
const LockPathRel = ".sandcast/mise.lock"

// StoredFragment is a fragment file from .sandcast/fragments.
type StoredFragment struct {
	// Key is the component key.
	Key string
	// Source is the file path relative to the project root.
	Source string
	// Body is the Containerfile snippet without the header line.
	Body string
	// Superseded is true when the header says status=superseded.
	Superseded bool
}

// Inputs are everything a plan depends on. The same inputs always yield the
// same plan bytes (FR-026).
type Inputs struct {
	Manifest        *manifest.Manifest
	Target          string
	Platform        Selected
	Engine          EngineInfo
	BaseImage       BaseImage
	Probe           *probe.Result
	Wanted          []Wanted
	Profiles        profile.Set
	Lock            *mise.Lockfile
	PolicyDefaults  *policy.Defaults
	StoredFragments map[string]StoredFragment
	Harnesses       []Harness
	Notices         []string
	// Dotfiles are the non-shell dotfiles the pipeline read from the host for
	// the plan to redact, relocate and bake (spec 003 US3).
	Dotfiles []DotfileInput
}

// LockError reports lockfile gaps for the selected platform; plan exits 2.
type LockError struct {
	Platform string
	Errors   []mise.ToolError
}

func (e *LockError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, te := range e.Errors {
		parts = append(parts, te.Error())
	}
	return fmt.Sprintf("the lockfile does not cover %s:\n  %s", e.Platform, strings.Join(parts, "\n  "))
}

// ReferenceError reports a component that references an unknown profile id.
type ReferenceError struct {
	Problems []string
}

func (e *ReferenceError) Error() string {
	return "unresolved profile references:\n  " + strings.Join(e.Problems, "\n  ")
}

// ErrMissingCheck means the probe result lacks a check for a component.
var ErrMissingCheck = errors.New("probe result has no check for component")

// Build assembles the plan from its inputs.
func Build(in Inputs) (*Plan, error) {
	if in.Probe == nil {
		return nil, errors.New("plan: no probe result")
	}
	facts := in.Probe.Facts
	user := FinalUserFor(facts, in.Target)
	if err := CheckFinalUser(user); err != nil {
		return nil, err
	}
	misePlatform, err := mise.Platform(in.Platform.Value)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		SchemaVersion:   version.PlanSchemaVersion,
		SandcastVersion: version.Version,
		Target:          in.Target,
		Platform:        in.Platform,
		Engine:          in.Engine,
		BaseImage:       in.BaseImage,
		ImageFacts:      factsOf(facts),
		FinalUser:       user,
		Image:           ImageSetup{Env: []EnvVar{}, Dirs: []Dir{}},
		ProbeDelta:      []DeltaEntry{},
		Components:      []Component{},
		Mise:            Mise{Platform: misePlatform},
		Profiles:        []Profile{},
		Shell:           shellOf(in.Manifest),
		Harnesses:       in.Harnesses,
		Unresolved:      []Unresolved{},
		Conflicts:       []component.Conflict{},
		Notices:         append([]string{}, in.Notices...),
	}
	if p.BaseImage.Notes == nil {
		p.BaseImage.Notes = []string{}
	}

	overmount := OvermountPrefix(in.Target)
	var (
		packages   []string
		packageIdx []int
		miseTools  []mise.Tool
		miseIdx    []int
		defs       []*component.Component
		installed  []*component.Component
		// fragmentDirs are the /opt/<key> trees of fragment installs.
		fragmentDirs []string
		// reprovisioned collects the notices for base tools found only under
		// the overmount home (FR-009).
		reprovisioned []RelocationNotice
	)
	for _, w := range in.Wanted {
		stored, anyStored := in.StoredFragments[w.Key]
		hasStored := anyStored && !stored.Superseded
		if w.Def == nil {
			if hasStored {
				p.Components = append(p.Components, fragmentComponent(w.Key, stored))
				fragmentDirs = append(fragmentDirs, OptDir(w.Key))
				continue
			}
			p.Unresolved = append(p.Unresolved, Unresolved{
				Key:      w.Key,
				Reason:   "no component definition in any tier and no stored fragment",
				Optional: w.Ref.Optional,
			})
			continue
		}
		defs = append(defs, w.Def)
		check, ok := in.Probe.Checks[w.Key]
		if !ok {
			return nil, fmt.Errorf("%w %s", ErrMissingCheck, w.Key)
		}
		// A base-image tool whose only copy lives under the overmount home is
		// hidden once the workspace volume is mounted, so it is treated as not
		// present and re-provisioned to a persistent location (FR-009, R7).
		// The probe cache keeps the raw result; this is a pure function of the
		// cached result plus the known overmount prefix, applied at plan time.
		if overmount != "" && check.Status == probe.StatusPresent && UnderOvermount(overmount, check.Path) {
			to := reprovisionTarget(w.Def)
			reprovisioned = append(reprovisioned, RelocationNotice{
				Kind: NoticeReprovisioned, Subject: w.Key, From: check.Path, To: to,
			})
			check.Status = probe.StatusMissing
			check.Path = ""
		}
		entry, reason := DeltaFor(w.Def, w.Selector(), check, facts.PackageManager)
		p.ProbeDelta = append(p.ProbeDelta, entry)
		if entry.Action == ActionUnresolvable {
			if hasStored {
				c := fragmentComponent(w.Key, stored)
				describe(&c, w.Def, w.Selector())
				p.Components = append(p.Components, c)
				installed = append(installed, w.Def)
				fragmentDirs = append(fragmentDirs, OptDir(w.Key))
				continue
			}
			p.Unresolved = append(p.Unresolved, Unresolved{Key: w.Key, Reason: reason, Optional: w.Ref.Optional})
			continue
		}
		if anyStored {
			state := "stale"
			if stored.Superseded {
				state = "stale (superseded)"
			}
			p.Notices = append(p.Notices, fmt.Sprintf(
				"stored fragment %s is %s: %s now resolves through %s; the fragment is not used",
				stored.Source, state, w.Key, w.Def.SourceFile))
		}
		c := Component{
			Key:           w.Key,
			Tier:          string(w.Def.Tier),
			SourceFile:    w.Def.SourceFile,
			InstallerKind: w.Def.Kind(),
			Action:        entry.Action,
			Implicit:      w.Implicit,
		}
		describe(&c, w.Def, w.Selector())
		installed = append(installed, w.Def)
		if entry.Action != ActionSkip {
			switch w.Def.Kind() {
			case component.KindPackage:
				packages = append(packages, w.Def.Installer.Package.Packages[facts.PackageManager]...)
				packageIdx = append(packageIdx, len(p.Components))
				c.FragmentName = SectionPackages
			case component.KindMise:
				t, _ := miseTool(w)
				miseTools = append(miseTools, t)
				miseIdx = append(miseIdx, len(p.Components))
				c.FragmentName = SectionMise
			case component.KindScript:
				text, err := ScriptFragment(w.Def, user)
				if err != nil {
					return nil, err
				}
				c.FragmentName = SectionRecipes + "/" + w.Key
				c.Fragment = text
			}
		}
		p.Components = append(p.Components, c)
	}

	if conflicts := component.Conflicts(defs); len(conflicts) > 0 {
		p.Conflicts = conflicts
	}

	if len(packages) > 0 {
		text, err := PackageFragment(facts.PackageManager, packages)
		if err != nil {
			return nil, err
		}
		for _, i := range packageIdx {
			p.Components[i].Fragment = text
		}
	}
	if len(miseTools) > 0 {
		if err := checkLock(in.Lock, miseTools, misePlatform); err != nil {
			return nil, err
		}
		toml, err := mise.RenderConfig(miseTools, []string{misePlatform})
		if err != nil {
			return nil, err
		}
		text, err := MiseFragment(user, version.MisePin, misePlatform)
		if err != nil {
			return nil, err
		}
		for _, i := range miseIdx {
			p.Components[i].Fragment = text
		}
		p.Mise.TOML = string(toml)
		p.Mise.LockPath = LockPathRel
	}

	p.Image = imageSetupOf(installed)
	mergeHarnessNeeds(&p.Image, in.Harnesses)

	// Resolve the non-shell dotfiles: redact, pick a relocation target outside
	// the home, dedup, and record one notice per dotfile (US3).
	dotfiles, relocated, dedup, err := resolveDotfiles(in.Dotfiles, overmount)
	if err != nil {
		return nil, err
	}
	p.Dotfiles = dotfiles

	// Relocation variables are set image-wide so non-shell processes read the
	// baked configuration too (research R3). They win over component and
	// harness env, and each replaced value is reported.
	envNotices := mergeRelocationEnv(&p.Image, RelocationEnv(p))

	// Every overmount decision is reported in the plan output and the build
	// log, sorted by subject so rebuilds are byte-identical (FR-010, SC-005).
	p.Relocations = append(p.Relocations, reprovisioned...)
	p.Relocations = append(p.Relocations, relocated...)
	sort.Slice(p.Relocations, func(i, j int) bool {
		if p.Relocations[i].Subject != p.Relocations[j].Subject {
			return p.Relocations[i].Subject < p.Relocations[j].Subject
		}
		return p.Relocations[i].Kind < p.Relocations[j].Kind
	})
	for _, n := range p.Relocations {
		p.Notices = append(p.Notices, n.String())
	}
	p.Notices = append(p.Notices, dedup...)
	p.Notices = append(p.Notices, envNotices...)

	// The minimal safe default: a login shell that is neither zsh nor bash
	// gets /bin/sh with baked env, and capture (and the plan) report that the
	// shell was not curated (FR-011, research R2).
	if ShellKind(p) == "sh" {
		p.Notices = append(p.Notices, fmt.Sprintf(
			"the login shell is neither zsh nor bash; the image uses /bin/sh with a minimal baked default (%s) and the curated shell is not applied",
			ProfileScriptPath))
	}

	// Defense in depth: nothing sandcast bakes may land under the overmount
	// home (SC-004).
	if err := checkOvermountGuard(p, overmount); err != nil {
		return nil, err
	}

	profiles, err := profilesOf(installed, in.Profiles)
	if err != nil {
		return nil, err
	}
	p.Profiles = profiles

	if in.Target == manifest.TargetOpenShell {
		if in.PolicyDefaults == nil {
			return nil, errors.New("plan: no policy defaults for the openshell target")
		}
		pol, err := renderPolicy(in.PolicyDefaults, installed, in.Harnesses, fragmentDirs, user, len(miseTools) > 0)
		if err != nil {
			return nil, err
		}
		p.Policy = pol
		if user.GID == 0 {
			p.Notices = append(p.Notices, fmt.Sprintf(
				"the base image user %s has primary group 0 (root), which OpenShell %s rejects as a process group; "+
					"the image policy runs it with group %d (its uid) instead",
				user.Name, policy.Pin, user.UID))
		}
	}

	sort.Slice(p.ProbeDelta, func(i, j int) bool { return p.ProbeDelta[i].ComponentKey < p.ProbeDelta[j].ComponentKey })
	sort.Slice(p.Components, func(i, j int) bool { return p.Components[i].Key < p.Components[j].Key })
	sort.Slice(p.Unresolved, func(i, j int) bool { return p.Unresolved[i].Key < p.Unresolved[j].Key })
	return p, nil
}

func fragmentComponent(key string, f StoredFragment) Component {
	return Component{
		Key:           key,
		Tier:          string(component.TierProject),
		SourceFile:    f.Source,
		InstallerKind: KindFragment,
		Action:        ActionInstall,
		FragmentName:  SectionFragments + "/" + key,
		Fragment:      StoredFragmentText(key, f.Source, f.Body),
		Bins:          []string{key},
		Check:         Check{Command: DefaultCheck(key)},
	}
}

// DefaultCheck is the check used for components without a definition.
func DefaultCheck(bin string) string { return "command -v " + bin }

// describe copies what build and verify need from the definition.
func describe(c *Component, def *component.Component, selector string) {
	c.Profiles = slices.Clone(def.Profiles)
	c.Bins = slices.Clone(def.Bins())
	if def.Image != nil {
		c.RuntimeGlobs = slices.Clone(def.Image.RuntimeGlobs)
	}
	if selector == "latest" {
		selector = ""
	}
	c.Check = Check{
		Command:        def.Check.Command,
		ExpectExit:     def.Check.Expect.Exit,
		Version:        selector,
		StdoutContains: def.Check.Expect.StdoutContains,
	}
}

// imageSetupOf merges environment variables and directories of the
// installed components, sorted; on a duplicate the first key wins.
func imageSetupOf(installed []*component.Component) ImageSetup {
	setup := ImageSetup{Env: []EnvVar{}, Dirs: []Dir{}}
	env := map[string]string{}
	dirs := map[string]string{}
	for _, c := range installed {
		if c.Image == nil {
			continue
		}
		for k, v := range c.Image.Env {
			if _, ok := env[k]; !ok {
				env[k] = v
			}
		}
		for _, d := range c.Image.Dirs {
			if _, ok := dirs[d.Path]; !ok {
				dirs[d.Path] = d.OwnerOrDefault()
			}
		}
	}
	for _, k := range sortedKeys(env) {
		setup.Env = append(setup.Env, EnvVar{Name: k, Value: env[k]})
	}
	for _, k := range sortedKeys(dirs) {
		setup.Dirs = append(setup.Dirs, Dir{Path: k, Owner: dirs[k]})
	}
	return setup
}

// mergeHarnessNeeds adds harness environment variables and directories to
// the image setup; component values win on a name clash.
func mergeHarnessNeeds(setup *ImageSetup, harnesses []Harness) {
	env := map[string]string{}
	for _, e := range setup.Env {
		env[e.Name] = e.Value
	}
	dirs := map[string]string{}
	for _, d := range setup.Dirs {
		dirs[d.Path] = d.Owner
	}
	for _, h := range harnesses {
		for _, e := range h.Env {
			if _, ok := env[e.Name]; !ok {
				env[e.Name] = e.Value
			}
		}
		for _, d := range h.Dirs {
			if _, ok := dirs[d.Path]; !ok {
				owner := d.Owner
				if owner == "" {
					owner = "user"
				}
				dirs[d.Path] = owner
			}
		}
	}
	setup.Env = []EnvVar{}
	for _, k := range sortedKeys(env) {
		setup.Env = append(setup.Env, EnvVar{Name: k, Value: env[k]})
	}
	setup.Dirs = []Dir{}
	for _, k := range sortedKeys(dirs) {
		setup.Dirs = append(setup.Dirs, Dir{Path: k, Owner: dirs[k]})
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func checkLock(lock *mise.Lockfile, tools []mise.Tool, platform string) error {
	if lock == nil {
		errs := make([]mise.ToolError, 0, len(tools))
		for _, t := range tools {
			errs = append(errs, mise.ToolError{Key: t.Key, Tool: t.Tool, Platform: platform,
				Reason: fmt.Sprintf("no %s; run \"sandcast lock\"", LockPathRel)})
		}
		return &LockError{Platform: platform, Errors: errs}
	}
	if errs := lock.Check(tools, platform); len(errs) > 0 {
		return &LockError{Platform: platform, Errors: errs}
	}
	return nil
}

func profilesOf(installed []*component.Component, set profile.Set) ([]Profile, error) {
	if problems := ProfileProblems(installed, set); len(problems) > 0 {
		return nil, &ReferenceError{Problems: problems}
	}
	ids := map[string]bool{}
	for _, c := range installed {
		for _, id := range c.Profiles {
			ids[id] = true
		}
	}
	out := make([]Profile, 0, len(ids))
	for id := range ids {
		e := set[id]
		out = append(out, Profile{ID: id, Tier: string(e.Tier), SourceFile: e.SourceFile, AdaptedID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// reprovisionTarget names the persistent install location sandcast reports
// for a base tool that was found only under the overmount home (FR-009). It
// is the installer's natural destination so the notice points the user at a
// real path.
func reprovisionTarget(def *component.Component) string {
	switch def.Kind() {
	case component.KindMise:
		return MiseDataDir
	case component.KindPackage:
		return "/usr"
	default:
		return OptDir(def.Key)
	}
}

// OptDir is where the build skill tells fragments to unpack trees and
// configuration for key. The image policy grants only declared /opt paths,
// so fragment installs and promoted recipes declare it read-only.
func OptDir(key string) string { return "/opt/" + key }

// renderPolicy renders the OpenShell image policy. Besides the component,
// harness and fragment paths it always grants read access to RelocationPrefix:
// the 60-shell section bakes the shell configuration and the launchers there
// for every shell kind (and dotfiles may land there), and the upstream default
// policy covers nothing under /opt, so without the grant landlock would deny
// the sandbox user its own rc files and login shell (spec 003 FR-001, FR-003).
// A harness that needs a writable subtree (such as /opt/sandcast/claude-code)
// declares it read-write; landlock grants the union of both rules there.
func renderPolicy(defaults *policy.Defaults, installed []*component.Component, harnesses []Harness, fragmentDirs []string, user FinalUser, usesMise bool) (string, error) {
	ro := append(slices.Clone(fragmentDirs), RelocationPrefix)
	var rw []string
	for _, c := range installed {
		if c.Image == nil {
			continue
		}
		ro = append(ro, c.Image.ReadOnly...)
		rw = append(rw, c.Image.ReadWrite...)
	}
	for _, h := range harnesses {
		ro = append(ro, h.ReadOnly...)
		rw = append(rw, h.ReadWrite...)
	}
	if usesMise && !slices.Contains(rw, MiseDataDir) {
		ro = append(ro, MiseDataDir)
	}
	data, err := policy.Render(defaults, policy.Input{
		ReadOnly:   ro,
		ReadWrite:  rw,
		RunAsUser:  policyIdentity(user.Name, user.UID),
		RunAsGroup: policyGroup(user),
	})
	if err != nil {
		return "", fmt.Errorf("render image policy: %w", err)
	}
	return string(data), nil
}

// policyIdentity returns the process identity OpenShell accepts: the literal
// "sandbox" or a numeric id (names such as "node" are rejected upstream).
func policyIdentity(name string, id int) string {
	if name == policy.SandboxIdentity {
		return name
	}
	return strconv.Itoa(id)
}

// policyGroup is the run_as_group of the final user. OpenShell rejects GID 0
// for the policy, the OCI USER fallback and the Docker and Podman drivers
// alike, so a kept user whose primary group is root (the OpenShift style
// USER 1001 with GID 0) runs with its uid as the group, the private group
// convention that the created sandbox user follows too. Files the image
// gives that user stay accessible through ownership.
func policyGroup(user FinalUser) string {
	if user.GID == 0 {
		return strconv.Itoa(user.UID)
	}
	return policyIdentity(user.Group, user.GID)
}

func factsOf(f probe.Facts) ImageFacts {
	shells := slices.Clone(f.Shells)
	if shells == nil {
		shells = []string{}
	}
	return ImageFacts{
		OS:             OS{ID: f.OS.ID, IDLike: f.OS.IDLike, Name: f.OS.Name, Version: f.OS.Version},
		PackageManager: f.PackageManager,
		User: User{
			Name: f.User.Name, UID: f.User.UID, GID: f.User.GID, Group: f.User.Group,
			Home: f.User.Home, Shell: f.User.Shell,
		},
		Shells: shells,
	}
}

func shellOf(m *manifest.Manifest) Shell {
	if m == nil || m.Shell == nil {
		return Shell{Program: "none"}
	}
	s := Shell{Program: m.Shell.Program}
	if strings.TrimSpace(m.Shell.RC) != "" {
		s.RC = strings.TrimRight(m.Shell.RC, "\n") + "\n"
		if m.Shell.Program == "zsh" {
			s.RCPath = ZshRCPath
		} else {
			s.RCPath = BashRCPath
		}
	}
	return s
}
