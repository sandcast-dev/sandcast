package plan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
)

func TestSelectUsesHarnessComponentKeys(t *testing.T) {
	comps, err := component.LoadDir(testComponents, component.TierShipped, "components")
	require.NoError(t, err)
	set := component.NewSet(comps)
	m := testManifest()
	m.Components = []manifest.ComponentRef{{Key: "go"}}
	m.Shell = nil
	m.Harnesses = []manifest.HarnessRef{{Name: "agent-x"}, {Name: "ghost"}}

	// The adapter of agent-x installs its agent through the starship
	// component; the key differs from the harness name.
	wanted, notices := Select(m, set, manifest.TargetOpenShell, map[string]string{"agent-x": "starship"})
	byKey := map[string]Wanted{}
	for _, w := range wanted {
		byKey[w.Key] = w
	}
	require.Contains(t, byKey, "starship", "the adapter's component key is used, not the harness name")
	assert.True(t, byKey["starship"].Implicit)
	assert.Equal(t, "harness agent-x", byKey["starship"].Reason)
	assert.NotContains(t, byKey, "agent-x")
	assert.NotContains(t, byKey, "ghost", "a harness without an adapter adds no component")
	assert.Contains(t, strings.Join(notices, "\n"), "harness ghost has no adapter")
}
