package probe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/component"
)

// checkComponent is a mise component whose probe check the tests change.
func checkComponent(key string) *component.Component {
	return &component.Component{
		Key:       key,
		Installer: component.Installer{Mise: &component.MiseInstaller{Tool: key}},
		Check:     component.Check{Command: "command -v " + key + " && " + key + " --version"},
	}
}

// TestProbeCheckChangeReprobes covers FR-012: check results are cached by
// digest, component key and the component's probe check. Changing the check
// command, the expected exit code or the expected output re-runs that one
// check, while the image facts and the other checks come from the cache.
func TestProbeCheckChangeReprobes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *component.Component)
	}{
		{"check command", func(c *component.Component) { c.Check.Command = "go version" }},
		{"expected exit", func(c *component.Component) { c.Check.Expect.Exit = 1 }},
		{"expected stdout", func(c *component.Component) { c.Check.Expect.StdoutContains = "go version" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFake(baseImage())
			o := opts(t)
			goDef, nodeDef := checkComponent("go"), checkComponent("node")
			first, err := Probe(ctx, f, testRef, testPlatform, []Request{RequestFor(goDef, ""), RequestFor(nodeDef, "")}, o)
			require.NoError(t, err)
			require.Equal(t, StatusPresent, first.Checks["go"].Status)

			changed := goDef.Clone()
			tt.mutate(changed)
			reqs := []Request{RequestFor(changed, ""), RequestFor(nodeDef, "")}
			require.NotEqual(t, first.Checks["go"].CheckHash, reqs[0].Hash, "the cache key follows the check")

			runs := f.CountOp("run")
			res, err := Probe(ctx, f, testRef, testPlatform, reqs, o)
			require.NoError(t, err)
			assert.Equal(t, runs+1, f.CountOp("run"))
			assert.True(t, res.ContainerStarted)
			assert.True(t, res.FactsFromCache, "the facts come from the cache")
			assert.Equal(t, first.Facts, res.Facts)
			assert.Equal(t, []string{"go"}, res.Probed, "only the changed check runs again")
			script := lastScript(t, f)
			assert.Equal(t, []string{"go"}, scriptKeys(script))
			assert.Contains(t, script, changed.Check.Command)
			assert.NotContains(t, script, "os-release", "no facts sections in a partial probe")
			assert.Equal(t, reqs[0].Hash, res.Checks["go"].CheckHash)
			assert.Equal(t, first.Checks["node"], res.Checks["node"], "the unchanged check comes from the cache")

			// The changed check is cached under its own key in turn.
			runs = f.CountOp("run")
			again, err := Probe(ctx, f, testRef, testPlatform, reqs, o)
			require.NoError(t, err)
			assert.Equal(t, runs, f.CountOp("run"))
			assert.False(t, again.ContainerStarted)
			assert.Equal(t, res.Checks, again.Checks)
		})
	}
}
