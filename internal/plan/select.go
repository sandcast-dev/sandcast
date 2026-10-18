package plan

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/mise"
)

// Wanted is one component the plan has to account for: a manifest entry or
// an implicit addition.
type Wanted struct {
	// Key is the component key.
	Key string
	// Ref is the manifest entry (version override, optional flag).
	Ref manifest.ComponentRef
	// Def is the winning definition, nil when no tier defines the key.
	Def *component.Component
	// Implicit is true for components added by sandcast rather than the
	// manifest.
	Implicit bool
	// Reason explains an implicit addition.
	Reason string
}

// Selector returns the effective version selector.
func (w Wanted) Selector() string {
	if w.Def == nil {
		return w.Ref.Version
	}
	return w.Def.VersionSelector(w.Ref.Version)
}

// Implicit components (R-05, R-10).
var (
	openshellImplicit = []string{"iproute2", "nftables"}
	miseImplicit      = []string{"curl", "ca-certificates"}
)

// Select resolves the manifest's components for target and adds the
// implicit ones: iproute2 and nftables for the OpenShell target, the shell
// program, components the curated shell configuration references, each
// harness's agent component, and curl plus ca-certificates when any mise
// tool is used. harnessComponents maps a harness name to the component key
// its adapter installs the agent with (harness.Adapter.ComponentKey); a
// harness without an entry has no adapter and adds no component. The result
// is sorted by key; notices explain additions and skips.
func Select(m *manifest.Manifest, set *component.Set, target string, harnessComponents map[string]string) ([]Wanted, []string) {
	var notices []string
	byKey := map[string]*Wanted{}
	var order []string
	add := func(w Wanted) {
		if _, ok := byKey[w.Key]; ok {
			return
		}
		cp := w
		byKey[w.Key] = &cp
		order = append(order, w.Key)
	}
	for _, ref := range m.Components {
		def, _ := set.Get(ref.Key)
		if def != nil && !def.AppliesTo(target) {
			notices = append(notices, fmt.Sprintf("component %s is not used for the %s target (targets: %v)", ref.Key, target, def.Targets))
			continue
		}
		add(Wanted{Key: ref.Key, Ref: ref, Def: def})
	}
	implicit := func(key, reason string) {
		if _, ok := byKey[key]; ok {
			return
		}
		def, ok := set.Get(key)
		if !ok {
			notices = append(notices, fmt.Sprintf("implicit component %s (%s) has no definition in any tier; skipped", key, reason))
			return
		}
		if !def.AppliesTo(target) {
			return
		}
		add(Wanted{Key: key, Ref: manifest.ComponentRef{Key: key}, Def: def, Implicit: true, Reason: reason})
		notices = append(notices, fmt.Sprintf("added %s implicitly: %s", key, reason))
	}
	for _, h := range m.Harnesses {
		if key, ok := harnessComponents[h.Name]; ok {
			implicit(key, "harness "+h.Name)
		} else {
			notices = append(notices, fmt.Sprintf("harness %s has no adapter in this release; no agent component is added for it", h.Name))
		}
		if len(h.Plugins) > 0 {
			implicit("git", "harness "+h.Name+" clones plugin marketplaces")
		}
	}
	if m.Shell != nil {
		implicit(m.Shell.Program, "shell program")
		comps := slices.Clone(m.Shell.Components)
		slices.Sort(comps)
		for _, k := range comps {
			implicit(k, "referenced by the shell configuration")
		}
	}
	if target == manifest.TargetOpenShell {
		for _, k := range openshellImplicit {
			implicit(k, "required by OpenShell sandbox images")
		}
	}
	usesMise := false
	for _, k := range order {
		if d := byKey[k].Def; d != nil && d.Kind() == component.KindMise {
			usesMise = true
		}
	}
	if usesMise {
		for _, k := range miseImplicit {
			implicit(k, "needed by the mise installer")
		}
	}
	out := make([]Wanted, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, notices
}

// BinsProblem explains why a manifest entry must not declare bins: a
// component tier defines the key, so its binaries come from the definition
// (FR-018). It returns "" when the entry may declare them.
func BinsProblem(w Wanted) string {
	if w.Def == nil || len(w.Ref.Bins) == 0 {
		return ""
	}
	return fmt.Sprintf("%s is defined by the %s tier; its binaries come from the definition, remove bins", w.Key, w.Def.Tier)
}

// BinsError reports manifest entries that declare bins for a tier-defined
// component; plan exits 2.
type BinsError struct {
	Problems []string
}

func (e *BinsError) Error() string {
	return "manifest components declare bins for defined components:\n  " + strings.Join(e.Problems, "\n  ")
}

// MiseTools returns the mise tools of the wanted components with
// definitions, sorted by key. sandcast lock locks this whole list, including
// tools the base image already satisfies, so the lockfile does not depend on
// the probed base image; plan.Build renders the subset it installs through
// the same miseTool rule, so every rendered tool has a lock entry.
func MiseTools(wanted []Wanted) []mise.Tool {
	var out []mise.Tool
	for _, w := range wanted {
		if t, ok := miseTool(w); ok {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// miseTool is the one rule that turns a wanted component into a mise tool.
// Its version is the effective selector the probe, the delta and the checks
// use: the manifest version, else the installer's, else the check's expected
// version.
func miseTool(w Wanted) (mise.Tool, bool) {
	if w.Def == nil || w.Def.Installer.Mise == nil {
		return mise.Tool{}, false
	}
	return mise.Tool{Key: w.Key, Tool: w.Def.Installer.Mise.Tool, Version: w.Selector()}, true
}
