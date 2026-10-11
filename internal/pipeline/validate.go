package pipeline

import (
	"fmt"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/harness"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
)

// ValidateReferences reports the reference problems plan refuses, for every
// declared target and without probing an image: a harness without an
// adapter, a component no tier defines and no active stored fragment
// covers, an invalid stored fragment, a binary conflict, and a profile id
// that no profile tier defines, referenced by any component the target
// selects, implicit ones included. It selects components with plan.Select
// and checks them with the rules plan applies, so manifest validation and
// plan agree. Diagnostics carry a manifest path but no line; the error
// reports I/O failures only.
func ValidateReferences(l fsutil.Layout, cacheDir string, m *manifest.Manifest) ([]manifest.Diagnostic, error) {
	v := diagnostics{seen: map[string]bool{}}
	for i, h := range m.Harnesses {
		if _, err := harness.Get(h.Name); err != nil {
			v.add(fmt.Sprintf("$.harnesses[%d].name", i), fmt.Sprintf("no adapter for harness %s in this release (adapters: %s)", h.Name, strings.Join(harness.Names(), ", ")))
		}
	}
	set, _, err := Components(l, cacheDir)
	if err != nil {
		v.add("$.components", "component tiers: "+err.Error())
		return v.list, nil
	}
	profiles, _, err := Profiles(l)
	if err != nil {
		v.add("$.components", "profile tiers: "+err.Error())
		return v.list, nil
	}
	stored, problems, err := readStoredFragments(l)
	if err != nil {
		return nil, err
	}
	for _, p := range problems {
		v.add("$.components", p)
	}
	harnesses := HarnessComponents(m)
	for _, target := range m.TargetNames() {
		wanted, _ := plan.Select(m, set, target, harnesses)
		var defs []*component.Component
		for _, w := range wanted {
			path := wantedPath(m, w, harnesses)
			if w.Def == nil {
				f, ok := stored[w.Key]
				if !w.Ref.Optional && (!ok || f.Superseded) {
					v.add(path, fmt.Sprintf("component %q is not defined in any tier (shipped, registry, .sandcast/components, .sandcast/recipes) and has no stored fragment", w.Key))
				}
				continue
			}
			defs = append(defs, w.Def)
			for _, msg := range plan.ProfileProblems([]*component.Component{w.Def}, profiles) {
				if w.Implicit {
					msg += " (added implicitly: " + w.Reason + ")"
				}
				v.add(path, msg)
			}
		}
		for _, c := range component.Conflicts(defs) {
			v.add("$.components", plan.ConflictProblem(c))
		}
	}
	return v.list, nil
}

// diagnostics collects diagnostics once each; a problem found for both
// targets is reported once.
type diagnostics struct {
	list []manifest.Diagnostic
	seen map[string]bool
}

func (d *diagnostics) add(path, msg string) {
	key := path + "\x00" + msg
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	d.list = append(d.list, manifest.Diagnostic{Path: path, Message: msg})
}

// wantedPath is the manifest path that brought a component into the plan:
// its components entry, the harness whose agent it is, or the shell
// configuration; other implicit additions point at the components list.
func wantedPath(m *manifest.Manifest, w plan.Wanted, harnesses map[string]string) string {
	if !w.Implicit {
		for i, ref := range m.Components {
			if ref.Key == w.Key {
				return fmt.Sprintf("$.components[%d].key", i)
			}
		}
	}
	for i, h := range m.Harnesses {
		if key, ok := harnesses[h.Name]; ok && key == w.Key {
			return fmt.Sprintf("$.harnesses[%d].name", i)
		}
	}
	if m.Shell != nil {
		if m.Shell.Program == w.Key {
			return "$.shell.program"
		}
		for i, k := range m.Shell.Components {
			if k == w.Key {
				return fmt.Sprintf("$.shell.components[%d]", i)
			}
		}
	}
	return "$.components"
}
