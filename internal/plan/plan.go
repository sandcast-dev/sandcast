package plan

import (
	"errors"
	"fmt"
	"path"
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
	"github.com/rhuss/sandcast/internal/registry"
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
	// them fail.
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
	// DefaultOvermountHome is the path OpenShell mounts the workspace volume
	// over at runtime on the sandbox base images, and the image WORKDIR.
	// Anything baked under it is hidden once the volume is mounted (spec US4,
	// FR-008/FR-009). It is a property of the base image: a registry entry
	// sets another path with overmount_home, and the plan records the
	// effective value in base_image.overmount_home (see OvermountPrefix).
	DefaultOvermountHome = "/sandbox"
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

// OvermountPrefix returns the overmount home of the plan, or "" when its
// target applies no sandbox mount. The container target keeps the base image
// home and is never overmounted (FR-040), so only the OpenShell target has a
// prefix: the base image's overmount home, or DefaultOvermountHome when the
// plan does not record one (a plan.yaml written before the field existed).
// The build reads it for the WORKDIR and the workspace directory, so the
// WORKDIR and the overmount guard can never disagree (SC-004).
func OvermountPrefix(p *Plan) string {
	if p.Target != manifest.TargetOpenShell {
		return ""
	}
	if p.BaseImage.OvermountHome != "" {
		return p.BaseImage.OvermountHome
	}
	return DefaultOvermountHome
}

// overmountHomeOf returns the overmount home the plan records for a target:
// the base image's value or the default for the OpenShell target, nothing for
// the container target. The value is checked by the registry rule
// (registry.CheckOvermountHome), the one place that rule lives, because the
// build writes it into WORKDIR and a RUN instruction and a hand-built input
// need not come from a validated registry entry.
func overmountHomeOf(target, home string) (string, error) {
	if target != manifest.TargetOpenShell {
		return "", nil
	}
	if home == "" {
		return DefaultOvermountHome, nil
	}
	if err := registry.CheckOvermountHome(home); err != nil {
		return "", fmt.Errorf("base image %w", err)
	}
	return home, nil
}

// overmountHomeNotice is the notice for an overmount home other than the
// default. The value is the WORKDIR sandcast writes; the OpenShell podman
// driver mounts the workspace there, while the Kubernetes driver of v0.1.3
// always mounts at DefaultOvermountHome, so with that driver the guard would
// protect the wrong path.
func overmountHomeNotice(home string) string {
	return fmt.Sprintf("base image overmount home %s is not %s: it is the image WORKDIR, which the OpenShell podman driver mounts the workspace over; the Kubernetes driver of OpenShell v0.1.3 always mounts at %s, so use this base image with the podman driver only",
		home, DefaultOvermountHome, DefaultOvermountHome)
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

// CuratedRCLoadedVar is set (not exported) by the first lines of the curated
// rc, so verify can tell that a session read it.
const CuratedRCLoadedVar = "SANDCAST_RC_LOADED"

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
	// KindedNotices are notices with a kind prefix (R-15) gathered outside
	// the plan, such as reference warnings; they are sorted together with
	// the plan's own kinded notices.
	KindedNotices []string
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
	if p.BaseImage.OvermountHome, err = overmountHomeOf(in.Target, in.BaseImage.OvermountHome); err != nil {
		return nil, err
	}
	if h := p.BaseImage.OvermountHome; h != "" && h != DefaultOvermountHome {
		p.Notices = append(p.Notices, overmountHomeNotice(h))
	}

	overmount := OvermountPrefix(p)
	var binsProblems []string
	for _, w := range in.Wanted {
		if p := BinsProblem(w); p != "" {
			binsProblems = append(binsProblems, p)
		}
	}
	if len(binsProblems) > 0 {
		return nil, &BinsError{Problems: binsProblems}
	}
	// A mise tool whose lock entry lacks the target platform fails the plan
	// before any comparison (001 edge case): the lockfile cannot decide its
	// version. Tools without any lock entry keep today's rules and fail
	// below only when they need installing.
	if err := checkPlatformEntries(in.Lock, in.Wanted, misePlatform); err != nil {
		return nil, err
	}
	deltaNotices := slices.Clone(in.KindedNotices)
	// A base image PATH directory under the overmount home never reaches the
	// image PATH (setImagePath) or a session's PATH (sessionPathDirs): the
	// workspace mounted there may come from an untrusted project. The plan
	// says so once per directory.
	_, hiddenDirs := sessionPathDirs(p.ImageFacts.Path, overmount)
	for _, d := range hiddenDirs {
		deltaNotices = append(deltaNotices, Notice(NoticeWorkspacePath, NoticeSubjectImage, fmt.Sprintf(
			"base image PATH directory %s lies under the overmount home %s and is left out of the image PATH and the session PATH; a tool there is hidden once the workspace is mounted, and a virtual environment the project creates there is active only after it is activated",
			d, overmount)))
	}

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
				p.Components = append(p.Components, fragmentComponent(w, stored))
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
		// The workspace rule of DeltaFor is keyed on the overmount home of
		// the base image (spec 003 FR-009, spec 002 FR-003): a tool whose
		// every PATH hit lies under it is hidden once the workspace volume
		// is mounted, counts as missing, and is provisioned to a persistent
		// location, which the plan reports. The probe cache keeps the raw
		// result; this is a pure function of the cached result plus the
		// known overmount prefix, applied at plan time.
		opts := DeltaOptions{PackageManager: facts.PackageManager, Platform: in.Platform.Value, OvermountHome: overmount}
		if t, ok := miseTool(w); ok {
			opts.LockedVersion, _ = in.Lock.Locked(t, misePlatform)
		}
		entry, reason := DeltaFor(w.Def, w.Selector(), check, opts)
		deltaNotices = append(deltaNotices, entry.Notices...)
		p.ProbeDelta = append(p.ProbeDelta, entry)
		// reportHidden records what happens to a tool found only under the
		// overmount home once its outcome is known: re-provisioned to the
		// persistent location of its installer or the /opt tree of a stored
		// fragment, or hidden and unresolved when nothing can provision it.
		reportHidden := func(kind, to string) {
			if entry.Hidden() {
				reprovisioned = append(reprovisioned, RelocationNotice{
					Kind: kind, Subject: w.Key, From: strings.Join(entry.IgnoredPaths, ", "), To: to,
				})
			}
		}
		if entry.Action == ActionUnresolvable {
			if hasStored {
				c := fragmentComponent(w, stored)
				describe(&c, w.Def, w.Selector())
				p.Components = append(p.Components, c)
				installed = append(installed, w.Def)
				fragmentDirs = append(fragmentDirs, OptDir(w.Key))
				reportHidden(NoticeReprovisioned, OptDir(w.Key))
				continue
			}
			p.Unresolved = append(p.Unresolved, Unresolved{Key: w.Key, Reason: reason, Optional: w.Ref.Optional})
			reportHidden(NoticeHidden, "")
			continue
		}
		reportHidden(NoticeReprovisioned, reprovisionTarget(w.Def))
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

	if conflicts := component.Conflicts(append(defs, SyntheticDefinitions(in.Wanted)...)); len(conflicts) > 0 {
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
	// the home, dedup or merge, and record one notice per source (US3).
	dotfiles, relocated, dedup := resolveDotfiles(in.Dotfiles)
	p.Dotfiles = dotfiles

	// Relocation variables are set image-wide so non-shell processes read the
	// baked configuration too (research R3). They win over component and
	// harness env, and each replaced value is reported.
	envNotices := mergeRelocationEnv(&p.Image, RelocationEnv(p))
	envNotices = append(envNotices, setImagePath(&p.Image, p.ImageFacts.Path, overmount)...)

	// Every overmount decision is reported in plan.yaml and in the output of
	// sandcast plan and sandcast build, sorted by subject so rebuilds are
	// byte-identical (FR-010, SC-005).
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

	// Nothing the plan bakes or declares may land under the overmount home,
	// and no startup-file variable may point there (SC-004).
	policyRO, policyRW := policyPaths(installed, in.Harnesses, fragmentDirs, len(miseTools) > 0)
	if err := checkOvermountGuard(p, overmount, append(slices.Clone(policyRO), policyRW...)); err != nil {
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
		pol, err := renderPolicy(in.PolicyDefaults, policyRO, policyRW, user)
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

	// Kinded notices (R-15) follow the other notices, sorted by kind and
	// subject so the plan stays deterministic.
	// The probed rc files belong to the base image user's home; a created
	// final user has another home, so they do not apply.
	if !user.Created {
		deltaNotices = append(deltaNotices, homeRCNotices(facts, in.Target)...)
	}
	SortNotices(deltaNotices)
	p.Notices = append(p.Notices, deltaNotices...)

	sort.Slice(p.ProbeDelta, func(i, j int) bool { return p.ProbeDelta[i].ComponentKey < p.ProbeDelta[j].ComponentKey })
	sort.Slice(p.Components, func(i, j int) bool { return p.Components[i].Key < p.Components[j].Key })
	sort.Slice(p.Unresolved, func(i, j int) bool { return p.Unresolved[i].Key < p.Unresolved[j].Key })
	return p, nil
}

// setImagePath sets the image PATH when the base image's PATH names a
// directory under the overmount home: the OpenShell default agent inherits
// the image ENV, and the community base image's PATH starts with
// /sandbox/.venv/bin, which would put the mounted workspace ahead of
// /usr/bin for every command the agent runs and for Ubuntu's system bashrc,
// which calls groups by name (spec 003 FR-002, SC-002). The value is the
// base image's PATH without those directories, in its order, or the
// OpenShell default PATH when nothing is left; the 30-mise layer prepends
// the shims to it. A component or harness PATH is replaced, with a notice,
// as a relocation value would be. Without an overmount home, or when the
// base PATH is clean, the image keeps the base image's PATH.
func setImagePath(setup *ImageSetup, basePath []string, overmount string) []string {
	if overmount == "" {
		return nil
	}
	var kept []string
	hidden := false
	for _, d := range basePath {
		d = path.Clean(d)
		switch {
		case UnderOvermount(overmount, d):
			hidden = true
		case !slices.Contains(kept, d):
			kept = append(kept, d)
		}
	}
	if !hidden {
		return nil
	}
	if len(kept) == 0 {
		kept = slices.Clone(OpenShellDefaultPath)
	}
	value := strings.Join(kept, ":")
	env := envMap(setup.Env)
	var notices []string
	if prev, ok := env["PATH"]; ok && prev != value {
		notices = append(notices, fmt.Sprintf(
			"component or harness env PATH=%s is replaced by the image PATH %s, which leaves out the directories under the overmount home %s",
			prev, value, overmount))
	}
	env["PATH"] = value
	setup.Env = sortedEnv(env)
	return notices
}

// homeRCNotices reports the home-directory rc files of the base image that
// reset PATH (spec 002 FR-007), for the container target only: there a bash
// started directly still reads them and loses the directories set before.
// In OpenShell images no shell ever reads a startup file from the home
// (spec 003 FR-002), so the files have no effect and nothing is reported;
// the probe fact stays in the image facts of the plan.
func homeRCNotices(facts probe.Facts, target string) []string {
	if target == manifest.TargetOpenShell {
		return nil
	}
	var out []string
	for _, f := range facts.HomeRCResetsPath {
		file := path.Join(facts.User.Home, f)
		out = append(out, Notice(NoticeHomeRCPath, NoticeSubjectImage, file+" resets PATH; bash sessions that read it lose the directories set before it"))
	}
	return out
}

// fragmentComponent is the planned component of a stored fragment. Its
// binaries are the manifest entry's declared bins, else the key (FR-018).
func fragmentComponent(w Wanted, f StoredFragment) Component {
	key := w.Key
	bins := DeclaredBins(w.Ref)
	return Component{
		Key:           key,
		Tier:          string(component.TierProject),
		SourceFile:    f.Source,
		InstallerKind: KindFragment,
		Action:        ActionInstall,
		FragmentName:  SectionFragments + "/" + key,
		Fragment:      StoredFragmentText(key, f.Source, f.Body),
		Bins:          bins,
		Check:         Check{Command: DefaultCheck(bins...)},
	}
}

// DeclaredBins returns the binaries a manifest entry declares, else its
// key.
func DeclaredBins(ref manifest.ComponentRef) []string {
	if len(ref.Bins) > 0 {
		return slices.Clone(ref.Bins)
	}
	return []string{ref.Key}
}

// DefaultCheck is the check used for components without a definition: every
// binary must resolve, "command -v a && command -v b".
func DefaultCheck(bins ...string) string {
	parts := make([]string, len(bins))
	for i, b := range bins {
		parts[i] = "command -v " + b
	}
	return strings.Join(parts, " && ")
}

// SyntheticDefinitions turns the manifest entries no tier defines but that
// declare bins into definitions for the conflict check, so declared
// binaries conflict with other components like defined ones (FR-018).
func SyntheticDefinitions(wanted []Wanted) []*component.Component {
	var out []*component.Component
	for _, w := range wanted {
		if w.Def == nil && len(w.Ref.Bins) > 0 {
			out = append(out, &component.Component{Key: w.Key, Provides: slices.Clone(w.Ref.Bins)})
		}
	}
	return out
}

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
	setup := ImageSetup{Dirs: []Dir{}}
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
	setup.Env = sortedEnv(env)
	for _, k := range sortedKeys(dirs) {
		setup.Dirs = append(setup.Dirs, Dir{Path: k, Owner: dirs[k]})
	}
	return setup
}

// mergeHarnessNeeds adds harness environment variables and directories to
// the image setup; component values win on a name clash.
func mergeHarnessNeeds(setup *ImageSetup, harnesses []Harness) {
	env := envMap(setup.Env)
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
	setup.Env = sortedEnv(env)
	setup.Dirs = []Dir{}
	for _, k := range sortedKeys(dirs) {
		setup.Dirs = append(setup.Dirs, Dir{Path: k, Owner: dirs[k]})
	}
}

// envMap indexes environment variables by name.
func envMap(env []EnvVar) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		m[e.Name] = e.Value
	}
	return m
}

// sortedEnv returns the variables of m sorted by name, the order the plan
// and the Containerfile use so rebuilds are byte-identical (FR-004). Every
// env merge (components, harnesses, relocation) ends with it.
func sortedEnv(m map[string]string) []EnvVar {
	out := make([]EnvVar, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, EnvVar{Name: k, Value: m[k]})
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checkPlatformEntries fails for mise components whose lock entry exists
// but has no artifact for platform.
func checkPlatformEntries(lock *mise.Lockfile, wanted []Wanted, platform string) error {
	if lock == nil {
		return nil
	}
	var gaps []mise.Tool
	for _, w := range wanted {
		t, ok := miseTool(w)
		if !ok || !lock.HasEntry(t) {
			continue
		}
		if _, ok := lock.Locked(t, platform); !ok {
			gaps = append(gaps, t)
		}
	}
	if len(gaps) == 0 {
		return nil
	}
	return &LockError{Platform: platform, Errors: lock.Check(gaps, platform)}
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

// policyPaths returns the read-only and read-write paths the image policy
// adds to the pinned defaults: the component, harness and fragment paths, and
// always RelocationPrefix read-only. The 60-shell section bakes the shell
// configuration and the launchers there for every shell kind (and dotfiles
// may land there), and the upstream default policy covers nothing under
// /opt, so without the grant landlock would deny the sandbox user its own rc
// files and login shell (spec 003 FR-001, FR-003). A harness that needs a
// writable subtree (such as /opt/sandcast/claude-code) declares it
// read-write; landlock grants the union of both rules there. MiseDataDir is
// read-only when mise tools are installed, unless a component declares it
// read-write. The overmount guard checks the same paths.
func policyPaths(installed []*component.Component, harnesses []Harness, fragmentDirs []string, usesMise bool) (ro, rw []string) {
	ro = append(slices.Clone(fragmentDirs), RelocationPrefix)
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
	return ro, rw
}

// renderPolicy renders the OpenShell image policy from the pinned defaults,
// the policyPaths and the final user.
func renderPolicy(defaults *policy.Defaults, ro, rw []string, user FinalUser) (string, error) {
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
		Shells:           shells,
		HomeRCResetsPath: slices.Clone(f.HomeRCResetsPath),
		Path:             slices.Clone(f.Path),
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
