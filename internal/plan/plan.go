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
	"github.com/rhuss/sandcast/internal/version"
)

// Shell configuration paths inside the image (R-10).
const (
	ZshRCPath  = "/etc/zsh/zshrc.d/sandcast.zsh"
	BashRCPath = "/etc/profile.d/sandcast.sh"
)

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
	workspace := WorkspacePath(facts, in.Target)
	deltaNotices := slices.Clone(in.KindedNotices)

	var (
		packages   []string
		packageIdx []int
		miseTools  []mise.Tool
		miseIdx    []int
		defs       []*component.Component
		installed  []*component.Component
		// fragmentDirs are the /opt/<key> trees of fragment installs.
		fragmentDirs []string
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
		opts := DeltaOptions{PackageManager: facts.PackageManager, Platform: in.Platform.Value, WorkspacePath: workspace}
		if t, ok := miseTool(w); ok {
			opts.LockedVersion, _ = in.Lock.Locked(t, misePlatform)
		}
		entry, reason := DeltaFor(w.Def, w.Selector(), check, opts)
		deltaNotices = append(deltaNotices, entry.Notices...)
		p.ProbeDelta = append(p.ProbeDelta, entry)
		if entry.Action == ActionUnresolvable {
			if hasStored {
				c := fragmentComponent(w, stored)
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

	// Kinded notices (R-15) follow the other notices, sorted by kind and
	// subject so the plan stays deterministic.
	// The probed rc files belong to the base image user's home; a created
	// final user has another home, so they do not apply.
	if !user.Created {
		deltaNotices = append(deltaNotices, homeRCNotices(facts, in.Target, len(miseTools) > 0)...)
	}
	SortNotices(deltaNotices)
	p.Notices = append(p.Notices, deltaNotices...)

	sort.Slice(p.ProbeDelta, func(i, j int) bool { return p.ProbeDelta[i].ComponentKey < p.ProbeDelta[j].ComponentKey })
	sort.Slice(p.Components, func(i, j int) bool { return p.Components[i].Key < p.Components[j].Key })
	sort.Slice(p.Unresolved, func(i, j int) bool { return p.Unresolved[i].Key < p.Unresolved[j].Key })
	return p, nil
}

// homeRCNotices reports the home-directory rc files of the base image that
// reset PATH (FR-007). For an OpenShell image with mise tools the session
// environment re-applies the mise shims after them (R-05).
func homeRCNotices(facts probe.Facts, target string, usesMise bool) []string {
	var out []string
	for _, f := range facts.HomeRCResetsPath {
		file := path.Join(facts.User.Home, f)
		msg := file + " resets PATH; sandcast re-applies the mise shims after it"
		if target != manifest.TargetOpenShell || !usesMise {
			msg = file + " resets PATH; bash sessions that read it lose the directories set before it"
		}
		out = append(out, Notice(NoticeHomeRCPath, NoticeSubjectImage, msg))
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

// OptDir is where the build skill tells fragments to unpack trees and
// configuration for key. The image policy grants only declared /opt paths,
// so fragment installs and promoted recipes declare it read-only.
func OptDir(key string) string { return "/opt/" + key }

func renderPolicy(defaults *policy.Defaults, installed []*component.Component, harnesses []Harness, fragmentDirs []string, user FinalUser, usesMise bool) (string, error) {
	ro := slices.Clone(fragmentDirs)
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
