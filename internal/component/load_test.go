package component

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goYAML = `key: go
name: Go
match:
  files: [go.mod]
installer:
  mise:
    tool: go
    version: "1.25"
    bins: [go, gofmt]
check:
  command: go version
  expect:
    version: "1.25"
image:
  read_write: [/opt/go]
  env:
    GOPATH: /opt/go
  runtime_globs: [/opt/mise/installs/go/*/bin/*]
profiles: [go-proxy]
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(goYAML), "components/go.yaml", TierShipped)
	require.NoError(t, err)
	assert.Equal(t, "go", c.Key)
	assert.Equal(t, KindMise, c.Kind())
	assert.Equal(t, []string{"go", "gofmt"}, c.Bins())
	assert.Equal(t, TierShipped, c.Tier)
	assert.Equal(t, "components/go.yaml", c.SourceFile)
	assert.Equal(t, "1.25", c.VersionSelector(""))
	assert.Equal(t, "1.24", c.VersionSelector("1.24"))
	assert.True(t, c.AppliesTo("openshell"))
	assert.Len(t, c.CheckHash("1.25"), 64)
	assert.NotEqual(t, c.CheckHash("1.25"), c.CheckHash("1.24"))
}

// TestCheckHashCoversCheck makes sure every part of the probe check feeds
// the per-component cache key (FR-012), not only the version selector.
func TestCheckHashCoversCheck(t *testing.T) {
	base, err := Parse([]byte(goYAML), "components/go.yaml", TierShipped)
	require.NoError(t, err)
	baseHash := base.CheckHash("1.25")
	tests := []struct {
		name   string
		mutate func(c *Component)
	}{
		{"check command", func(c *Component) { c.Check.Command = "go env GOVERSION" }},
		{"expected exit", func(c *Component) { c.Check.Expect.Exit = 1 }},
		{"expected stdout", func(c *Component) { c.Check.Expect.StdoutContains = "go version" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base.Clone()
			tt.mutate(c)
			assert.NotEqual(t, baseHash, c.CheckHash("1.25"))
			assert.Equal(t, baseHash, base.CheckHash("1.25"), "the clone does not change the base")
		})
	}
	// Fields are separated, so moving text from the selector into the
	// adjacent expected output changes the key.
	a, b := base.Clone(), base.Clone()
	a.Check.Expect.StdoutContains = "5"
	assert.NotEqual(t, a.CheckHash("1.2"), b.CheckHash("1.25"))
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name, doc, want string
	}{
		{"endpoints", "key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\nendpoints: [{host: a.b.c}]\n", `"endpoints" is not allowed`},
		{"unknown field", "key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\ncolour: red\n", "colour"},
		{"two installers", "key: x\nname: X\ninstaller: {mise: {tool: x}, script: {run: x}}\ncheck: {command: x}\n", "exactly one"},
		{"no installer", "key: x\nname: X\ninstaller: {}\ncheck: {command: x}\n", "exactly one"},
		{"bad key", "key: X_Y\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\n", "kebab-case"},
		{"no check", "key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: \"\"}\n", "check.command"},
		{"bad pm", "key: x\nname: X\ninstaller: {package: {packages: {brew: [x]}}}\ncheck: {command: x}\n", "unknown package manager"},
		{"relative path", "key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\nimage: {read_write: [opt/x]}\n", "must be absolute"},
		{"bad target", "key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\ntargets: [k8s]\n", "unknown target"},
		{"host tool path", "key: x\nname: X\nmatch: {host_tools: [./.sandcast/x.sh]}\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\n", "bare command name"},
		{"host tool absolute", "key: x\nname: X\nmatch: {host_tools: [/tmp/x]}\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\n", "bare command name"},
		{"bad owner", "key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\nimage: {dirs: [{path: /x, owner: nobody}]}\n", "root or user"},
		{"emulation flag not a bool", "key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\nemulation_unsafe: sometimes\n", "EmulationUnsafe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc), "x.yaml", TierProject)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// TestParseEmulationUnsafe checks the strict decoder accepts the field and
// the shipped rust component sets it (FR-016).
func TestParseEmulationUnsafe(t *testing.T) {
	c, err := Parse([]byte("key: x\nname: X\ninstaller: {mise: {tool: x}}\ncheck: {command: x}\nemulation_unsafe: true\n"), "x.yaml", TierProject)
	require.NoError(t, err)
	assert.True(t, c.EmulationUnsafe)
	c, err = Parse([]byte(goYAML), "go.yaml", TierShipped)
	require.NoError(t, err)
	assert.False(t, c.EmulationUnsafe)
}

func TestLoadDir(t *testing.T) {
	fsys := fstest.MapFS{
		"go.yaml":    {Data: []byte(goYAML)},
		"README.md":  {Data: []byte("# not a component")},
		"index.yaml": {Data: []byte("schema_version: 1\nfiles: []\n")},
		"jq.yml":     {Data: []byte("key: jq\nname: jq\ninstaller: {mise: {tool: jq}}\ncheck: {command: jq --version}\n")},
	}
	got, err := LoadDir(fsys, TierRegistry, "cache/v1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "go", got[0].Key)
	assert.Equal(t, "jq", got[1].Key)
	assert.Equal(t, "cache/v1/jq.yml", got[1].SourceFile)
	assert.Equal(t, TierRegistry, got[1].Tier)

	none, err := LoadDir(nil, TierProject, "")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestLoadDirDuplicateKey(t *testing.T) {
	fsys := fstest.MapFS{
		"a.yaml": {Data: []byte(goYAML)},
		"b.yaml": {Data: []byte(goYAML)},
	}
	_, err := LoadDir(fsys, TierShipped, "components")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "defined twice")
}

func TestBinsFallback(t *testing.T) {
	c := &Component{Key: "zsh", Installer: Installer{Package: &PackageInstaller{Packages: map[string][]string{"apt-get": {"zsh"}}}}}
	assert.Equal(t, []string{"zsh"}, c.Bins())
	c.Provides = []string{"zsh", "zsh-5"}
	assert.Equal(t, []string{"zsh", "zsh-5"}, c.Bins())
	assert.Equal(t, KindPackage, c.Kind())
}

func TestCloneIsDeep(t *testing.T) {
	c, err := Parse([]byte(goYAML), "go.yaml", TierShipped)
	require.NoError(t, err)
	d := c.Clone()
	d.Installer.Mise.Bins[0] = "changed"
	d.Image.Env["GOPATH"] = "/x"
	assert.Equal(t, "go", c.Installer.Mise.Bins[0])
	assert.Equal(t, "/opt/go", c.Image.Env["GOPATH"])
}

func TestMarshalRoundTrip(t *testing.T) {
	c, err := Parse([]byte(goYAML), "go.yaml", TierShipped)
	require.NoError(t, err)
	data, err := Marshal(c)
	require.NoError(t, err)
	back, err := Parse(data, "go.yaml", TierShipped)
	require.NoError(t, err)
	assert.Equal(t, c, back)
}
