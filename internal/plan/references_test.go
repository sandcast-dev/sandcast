package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/profile"
)

func TestProfileProblems(t *testing.T) {
	set := profile.Set{"github": {}}
	comps := []*component.Component{
		{Key: "starship", SourceFile: ".sandcast/components/starship.yaml", Profiles: []string{"zeta", "github", "alpha"}},
		{Key: "git", SourceFile: "components/git.yaml", Profiles: []string{"github"}},
	}
	assert.Equal(t, []string{
		`component starship (.sandcast/components/starship.yaml) references profile "alpha", which no profile tier defines`,
		`component starship (.sandcast/components/starship.yaml) references profile "zeta", which no profile tier defines`,
	}, ProfileProblems(comps, set))
	assert.Empty(t, ProfileProblems(comps[1:], set))
}

func TestPlanProblems(t *testing.T) {
	p := &Plan{
		Conflicts: []component.Conflict{{Binary: "rg", Components: []string{"a", "b"}}},
		Unresolved: []Unresolved{
			{Key: "mystery", Reason: "no definition"},
			{Key: "maybe", Reason: "no definition", Optional: true},
		},
	}
	assert.Equal(t, []string{
		"conflict: binary rg is provided by [a b]; remove one of them from the manifest",
		`unresolved: mystery: no definition; store a fragment with "sandcast fragments write mystery" or run the build skill`,
	}, p.Problems(), "optional unresolved components do not block")
	assert.Empty(t, (&Plan{}).Problems())
}
