// Package pipeline wires the deterministic core: it gathers the plan inputs
// (registry, tiers, probe, lockfile, stored fragments, policy defaults),
// builds the plan, and runs the build and verify steps. Commands in
// internal/cli call it; it holds no command-line concerns.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/mise"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/policy"
	"github.com/rhuss/sandcast/internal/probe"
	"github.com/rhuss/sandcast/internal/profile"
	"github.com/rhuss/sandcast/internal/redact"
	"github.com/rhuss/sandcast/internal/registry"
	"github.com/rhuss/sandcast/internal/version"
)

// UsageError marks input problems that map to exit code 2.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usage(format string, args ...any) error {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// Env is the per-invocation environment of the pipeline.
type Env struct {
	Layout       fsutil.Layout
	Manifest     *manifest.Manifest
	Engine       engine.Engine
	EngineSel    engine.Selection
	HostPlatform string
	CacheDir     string
	// Home is the host home directory; harness adapters resolve "~/"
	// sources against it.
	Home string
	// Logf prints progress and notices to stderr.
	Logf func(format string, args ...any)
}

func (e Env) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Registry loads the shipped base image registry merged with the user file.
func Registry() (*registry.Registry, error) {
	shipped, err := registry.LoadShipped(sandcast.BaseImages)
	if err != nil {
		return nil, err
	}
	userPath, err := registry.UserPath()
	if err != nil {
		return shipped, nil //nolint:nilerr // no home directory: shipped only
	}
	user, err := registry.LoadUser(userPath)
	if err != nil {
		return nil, err
	}
	return registry.Merge(shipped, user)
}

// Components resolves the three component tiers for a project: the shipped
// set, the cached remote registry at its active tag, and .sandcast/components
// plus .sandcast/recipes. It returns notices for rejected registry files.
func Components(l fsutil.Layout, cacheDir string) (*component.Set, []string, error) {
	tag := component.ActiveTag(cacheDir, version.ComponentRegistryPin)
	regFS, rejected, err := component.LoadCached(cacheDir, tag)
	if err != nil {
		return nil, nil, err
	}
	var notices []string
	for _, r := range rejected {
		notices = append(notices, fmt.Sprintf("component registry %s: %s was rejected (%s); the previous copy is used if present", tag, r.Path, r.Reason))
	}
	set, err := component.Resolve(component.Sources{
		Shipped:        sandcast.Components,
		ShippedPrefix:  sandcast.ComponentsPrefix,
		Registry:       regFS,
		RegistryPrefix: component.CachePrefix(tag),
		Project:        dirFS(l.ComponentsDir()),
		ProjectPrefix:  l.RelToRoot(l.ComponentsDir()),
		Recipes:        dirFS(l.RecipesDir()),
		RecipesPrefix:  l.RelToRoot(l.RecipesDir()),
	})
	if err != nil {
		return nil, nil, err
	}
	return set, notices, nil
}

// Profiles resolves the three profile tiers for a project. The project tier is
// read through a rooted FS so a committed symlink in .sandcast/profiles cannot
// make the resolver read a file outside that directory.
func Profiles(l fsutil.Layout) (profile.Set, []profile.Override, error) {
	projectFS, closeFS, err := rootedDirFS(l.ProfilesDir())
	if err != nil {
		return nil, nil, err
	}
	defer closeFS()
	return profile.Resolve(sandcast.UpstreamProfiles, sandcast.OverlayProfiles, projectFS)
}

// rootedDirFS returns an fs.FS rooted at dir that refuses to follow a symlink
// out of dir, or a nil FS when dir is not a directory. A dir that is itself a
// committed symlink is refused: os.OpenRoot would follow the symlinked root
// and os.Root only blocks escapes from within the tree, not the root link, so
// the leaf is checked with Lstat first. The returned close releases the
// handle; it must be called only after reads finish, so callers defer it
// around a synchronous use such as profile.Resolve.
func rootedDirFS(dir string) (fs.FS, func(), error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, func() {}, nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, func() {}, fmt.Errorf("%s is a symlink; sandcast reads the project profile tier in place and never follows a link there, remove it", dir)
	}
	if !fi.IsDir() {
		return nil, func() {}, nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, func() {}, err
	}
	return root.FS(), func() { _ = root.Close() }, nil
}

func dirFS(dir string) fs.FS {
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return os.DirFS(dir)
	}
	return nil
}

// SelectPlatform applies the precedence flag, manifest, host (FR-026a).
func SelectPlatform(flag, manifestValue, host string) (plan.Selected, error) {
	sel := plan.Selected{Value: host, Source: "host"}
	switch {
	case flag != "":
		sel = plan.Selected{Value: flag, Source: "flag"}
	case manifestValue != "":
		sel = plan.Selected{Value: manifestValue, Source: "manifest"}
	}
	if _, err := mise.Platform(sel.Value); err != nil {
		return sel, usage("platform %q (%s) is not supported: use linux/amd64 or linux/arm64", sel.Value, sel.Source)
	}
	return sel, nil
}

// ResolveBase resolves the manifest's base image for target. A name that is
// a registry entry of another target is used as is with a notice, so one
// manifest builds both targets from the same base image.
func ResolveBase(reg *registry.Registry, input, target string) (registry.Entry, bool, string, error) {
	entry, inRegistry, notice, err := reg.Resolve(input, target)
	if err != nil {
		return entry, false, "", usage("base image: %v", err)
	}
	if inRegistry {
		return entry, true, notice, nil
	}
	for _, other := range registry.KnownTargets {
		if other == target {
			continue
		}
		for _, e := range reg.List(other) {
			if e.Name == input {
				return e, true, fmt.Sprintf("base image %s is a registry entry of the %s target; the %s target uses it as well", input, other, target), nil
			}
		}
	}
	return entry, false, notice, nil
}

// TargetOf validates the requested target against the manifest; empty means
// the manifest's default target.
func TargetOf(m *manifest.Manifest, requested string) (string, error) {
	t := requested
	if t == "" {
		t = m.DefaultTarget()
	}
	if t != manifest.TargetOpenShell && t != manifest.TargetContainer {
		return "", usage("unknown target %q: use openshell or container", t)
	}
	if m.Target(t) == nil {
		return "", usage("the manifest declares no %s target; add targets.%s.image", t, t)
	}
	return t, nil
}

// LockTools returns the mise tools of every declared target, the list
// sandcast lock resolves: each tool once, in first-seen order over the
// targets, which plan.MiseTools sorts by key.
func LockTools(m *manifest.Manifest, set *component.Set) []mise.Tool {
	var tools []mise.Tool
	seen := map[string]bool{}
	harnesses := HarnessComponents(m)
	for _, target := range m.TargetNames() {
		wanted, _ := plan.Select(m, set, target, harnesses)
		for _, t := range plan.MiseTools(wanted) {
			if !seen[t.Key] {
				seen[t.Key] = true
				tools = append(tools, t)
			}
		}
	}
	return tools
}

// PlanOptions select what to plan.
type PlanOptions struct {
	Target       string
	PlatformFlag string
	NoCache      bool
}

// Planned is a plan with what the build step needs next to it.
type Planned struct {
	Plan     *plan.Plan
	Dir      string
	LockData []byte
	Profiles profile.Set
	Probe    *probe.Result
}

// Plan gathers the inputs and builds the plan. It does not write anything
// except probe cache entries.
func Plan(ctx context.Context, env Env, opts PlanOptions) (*Planned, error) {
	m := env.Manifest
	if err := manifest.CheckSecrets(m, env.Layout.RelToRoot(env.Layout.ManifestPath())); err != nil {
		return nil, &UsageError{Err: err}
	}
	probed, err := ProbeBase(ctx, env, ProbeOptions{Target: opts.Target, PlatformFlag: opts.PlatformFlag, NoCache: opts.NoCache})
	if err != nil {
		return nil, err
	}
	target, platform, base, wanted, result := probed.Target, probed.Platform, probed.Base, probed.Wanted, probed.Result
	notices := probed.Notices

	stored, err := storedFragments(env.Layout)
	if err != nil {
		return nil, err
	}
	var lock *mise.Lockfile
	var lockData []byte
	if len(plan.MiseTools(wanted)) > 0 {
		lockData, err = os.ReadFile(env.Layout.LockPath())
		switch {
		case err == nil:
			if lock, err = mise.ParseLock(lockData); err != nil {
				return nil, usage("%s: %v", env.Layout.RelToRoot(env.Layout.LockPath()), err)
			}
		case errors.Is(err, os.ErrNotExist):
			lockData = nil
		default:
			return nil, err
		}
	}
	defaults, err := policy.LoadDefaults(sandcast.Policy)
	if err != nil {
		return nil, err
	}
	profiles, profOverrides, err := Profiles(env.Layout)
	if err != nil {
		return nil, err
	}
	// The scaffold renders the final user before plan.Build checks it.
	user := plan.FinalUserFor(result.Facts, target)
	if err := plan.CheckFinalUser(user); err != nil {
		return nil, err
	}
	harnesses, err := harnessSections(env, m, user)
	if err != nil {
		return nil, err
	}

	p, err := plan.Build(plan.Inputs{
		Manifest:        m,
		Target:          target,
		Platform:        platform,
		Engine:          plan.EngineInfo{Name: env.EngineSel.Name, Source: env.EngineSel.Source},
		BaseImage:       base,
		Probe:           result,
		Wanted:          wanted,
		Profiles:        profiles,
		Lock:            lock,
		PolicyDefaults:  defaults,
		StoredFragments: stored,
		Harnesses:       harnesses,
		Notices:         notices,
	})
	if err != nil {
		var le *plan.LockError
		var re *plan.ReferenceError
		if errors.As(err, &le) || errors.As(err, &re) {
			return nil, &UsageError{Err: err}
		}
		return nil, err
	}
	referenced := map[string]bool{}
	for _, pr := range p.Profiles {
		referenced[pr.ID] = true
	}
	for _, ov := range profOverrides {
		if referenced[ov.ID] {
			p.Notices = append(p.Notices, fmt.Sprintf("profile %s: %s overrides %s", ov.ID, ov.Winner, ov.Loser))
		}
	}
	return &Planned{
		Plan:     p,
		Dir:      env.Layout.BuildDir(target),
		LockData: lockData,
		Profiles: profiles,
		Probe:    result,
	}, nil
}

// WritePlan writes the plan outputs into the build directory under the
// build lock and makes sure .sandcast/.gitignore ignores build/.
func WritePlan(env Env, planned *Planned) error {
	unlock, err := lockBuildDir(env, planned)
	if err != nil {
		return err
	}
	defer unlock()
	return writePlan(env, planned)
}

// lockBuildDir refuses a project build directory reached through a symlink
// and any symlink among the entries sandcast writes inside it, then takes
// the build lock, so a plan never rewrites the directory while a build
// reads it. Both checks run before the lock file is created and before
// anything is written.
func lockBuildDir(env Env, planned *Planned) (func(), error) {
	// An explicit --out outside the project is the user's choice; the
	// project's own build directory must not be reached through a symlink.
	rel, err := filepath.Rel(env.Layout.Root, planned.Dir)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if err := env.Layout.CheckNoSymlinks(planned.Dir); err != nil {
			return nil, &UsageError{Err: err}
		}
	}
	// .lock and build.log are opened in place (the build refuses a link
	// there as well); the other entries are replaced by rename or removed
	// first, which never follows a link, but a link there is refused all
	// the same: sandcast never puts one in its build directory.
	if err := env.Layout.CheckNoSymlinkEntries(planned.Dir, fsutil.BuildEntries()...); err != nil {
		return nil, &UsageError{Err: err}
	}
	return build.LockTarget(planned.Dir, planned.Plan.Target)
}

func writePlan(env Env, planned *Planned) error {
	if err := env.Layout.EnsureGitignore(); err != nil {
		return err
	}
	return plan.WriteOutputs(planned.Dir, planned.Plan, planned.LockData)
}

// storedFragments reads .sandcast/fragments for plan; the first invalid
// active fragment is a usage error.
func storedFragments(l fsutil.Layout) (map[string]plan.StoredFragment, error) {
	out, problems, err := readStoredFragments(l)
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, usage("%s", problems[0])
	}
	return out, nil
}

// readStoredFragments lists the stored fragments and checks every active
// one the way sandcast fragments write does. problems has one message per
// invalid fragment; err reports I/O failures only.
func readStoredFragments(l fsutil.Layout) (map[string]plan.StoredFragment, []string, error) {
	list, err := build.ListFragments(l.FragmentsDir())
	if err != nil {
		return nil, nil, err
	}
	out := map[string]plan.StoredFragment{}
	var problems []string
	for _, f := range list {
		source := l.RelToRoot(filepath.Clean(f.Path))
		if f.Status == build.FragmentActive {
			// Committed or hand-edited fragments get the same checks as
			// sandcast fragments write.
			if err := build.ValidateFragment(f.Body); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", source, err))
			} else if hits := redact.Scan(f.Body); len(hits) > 0 {
				problems = append(problems, fmt.Sprintf("%s contains a credential-shaped value (%s at line %d); fragments are committed and must not carry credentials", source, hits[0].Kind, hits[0].Line))
			}
		}
		out[f.Key] = plan.StoredFragment{
			Key:        f.Key,
			Source:     source,
			Body:       f.Body,
			Superseded: f.Status == build.FragmentSuperseded,
		}
	}
	return out, problems, nil
}
