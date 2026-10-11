package plan

import (
	"fmt"
	"sort"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/profile"
)

// ProfileProblems returns one message per profile id that a component
// references and no profile tier defines, sorted. plan.Build and manifest
// validation apply this one rule.
func ProfileProblems(comps []*component.Component, set profile.Set) []string {
	var problems []string
	for _, c := range comps {
		for _, id := range c.Profiles {
			if _, ok := set[id]; !ok {
				problems = append(problems, fmt.Sprintf("component %s (%s) references profile %q, which no profile tier defines", c.Key, c.SourceFile, id))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// ConflictProblem describes a binary conflict and how to resolve it.
func ConflictProblem(c component.Conflict) string {
	return fmt.Sprintf("conflict: binary %s is provided by %v; remove one of them from the manifest", c.Binary, c.Components)
}

// UnresolvedProblem describes an unresolved component and how to resolve it.
func UnresolvedProblem(u Unresolved) string {
	return fmt.Sprintf("unresolved: %s: %s; store a fragment with \"sandcast fragments write %s\" or run the build skill", u.Key, u.Reason, u.Key)
}

// Problems lists what keeps the plan from being built, one sorted line
// each: binary conflicts and unresolved components that are not optional.
func (p *Plan) Problems() []string {
	var out []string
	for _, c := range p.Conflicts {
		out = append(out, ConflictProblem(c))
	}
	for _, u := range p.Blocking() {
		out = append(out, UnresolvedProblem(u))
	}
	sort.Strings(out)
	return out
}
