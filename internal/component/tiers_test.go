package component

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func comp(key, tool string) string {
	return "key: " + key + "\nname: " + key + "\ninstaller: {mise: {tool: " + tool + "}}\ncheck: {command: " + key + " --version}\n"
}

func TestResolveTiers(t *testing.T) {
	src := Sources{
		Shipped:        fstest.MapFS{"go.yaml": {Data: []byte(comp("go", "go"))}, "jq.yaml": {Data: []byte(comp("jq", "jq"))}},
		ShippedPrefix:  "components",
		Registry:       fstest.MapFS{"jq.yaml": {Data: []byte(comp("jq", "aqua:jqlang/jq"))}},
		RegistryPrefix: "registry/v0.1.0",
		Project:        fstest.MapFS{"go.yaml": {Data: []byte(comp("go", "go@local"))}, "mytool.yaml": {Data: []byte(comp("mytool", "ubi:me/mytool"))}},
		ProjectPrefix:  ".sandcast/components",
		Recipes:        fstest.MapFS{"mytool.yaml": {Data: []byte("key: mytool\nname: mytool\ninstaller: {script: {run: echo}}\ncheck: {command: mytool}\npromoted_from: fragments/mytool.containerfile\n")}},
		RecipesPrefix:  ".sandcast/recipes",
	}
	set, err := Resolve(src)
	require.NoError(t, err)
	assert.Equal(t, []string{"go", "jq", "mytool"}, set.Keys())

	g, _ := set.Get("go")
	assert.Equal(t, TierProject, g.Tier)
	assert.Equal(t, "go@local", g.Installer.Mise.Tool)
	j, _ := set.Get("jq")
	assert.Equal(t, TierRegistry, j.Tier)
	assert.Equal(t, "registry/v0.1.0/jq.yaml", j.SourceFile)
	m, _ := set.Get("mytool")
	assert.Equal(t, KindScript, m.Kind())
	assert.Equal(t, ".sandcast/recipes/mytool.yaml", m.SourceFile)

	ov := set.Overrides()
	require.Len(t, ov, 3)
	assert.Equal(t, Override{Key: "go", Winner: ".sandcast/components/go.yaml", WinnerTier: TierProject, Loser: "components/go.yaml", LoserTier: TierShipped}, ov[0])
	assert.Equal(t, "jq", ov[1].Key)
	assert.Equal(t, "mytool", ov[2].Key)
	assert.Len(t, set.Filter(TierShipped), 0)
	assert.Equal(t, 3, set.Len())
}

func TestResolveErrorNamesTier(t *testing.T) {
	_, err := Resolve(Sources{Registry: fstest.MapFS{"x.yaml": {Data: []byte("key: x\n")}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry tier")
}

func TestConflicts(t *testing.T) {
	a := &Component{Key: "ripgrep", Provides: []string{"rg"}}
	b := &Component{Key: "rg-fork", Provides: []string{"rg"}}
	c := &Component{Key: "jq"}
	got := Conflicts([]*Component{c, b, a})
	assert.Equal(t, []Conflict{{Binary: "rg", Components: []string{"rg-fork", "ripgrep"}}}, got)
	assert.Empty(t, Conflicts([]*Component{a, c}))
}
