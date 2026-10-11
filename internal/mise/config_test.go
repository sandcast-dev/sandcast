package mise

import (
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/testutil"
)

var sampleTools = []Tool{
	{Key: "jq", Tool: "jq"},
	{Key: "go", Tool: "go", Version: "1.25"},
	{Key: "claude-code", Tool: "aqua:anthropics/claude-code", Version: "latest"},
}

func TestRenderConfigGolden(t *testing.T) {
	got, err := RenderConfig(sampleTools, []string{"linux-x64", "linux-arm64"})
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join("testdata", "mise.toml.golden"), got)
}

func TestRenderConfigIsValidTOML(t *testing.T) {
	got, err := RenderConfig(sampleTools, []string{"linux-arm64"})
	require.NoError(t, err)

	var parsed struct {
		Settings struct {
			Lockfile           bool     `toml:"lockfile"`
			Locked             bool     `toml:"locked"`
			LockfilePlatforms  []string `toml:"lockfile_platforms"`
			UseVersionsHost    *bool    `toml:"use_versions_host"`
			TrustedConfigPaths []string `toml:"trusted_config_paths"`
		} `toml:"settings"`
		Tools map[string]string `toml:"tools"`
	}
	_, err = toml.Decode(string(got), &parsed)
	require.NoError(t, err)
	assert.True(t, parsed.Settings.Lockfile)
	assert.True(t, parsed.Settings.Locked)
	assert.Equal(t, []string{"linux-arm64"}, parsed.Settings.LockfilePlatforms)
	require.NotNil(t, parsed.Settings.UseVersionsHost)
	assert.False(t, *parsed.Settings.UseVersionsHost)
	assert.Equal(t, []string{"/etc/mise"}, parsed.Settings.TrustedConfigPaths)
	assert.Equal(t, map[string]string{
		"aqua:anthropics/claude-code": "latest",
		"go":                          "1.25",
		"jq":                          "latest",
	}, parsed.Tools)
}

func TestRenderConfigDeterministic(t *testing.T) {
	reversed := []Tool{sampleTools[2], sampleTools[1], sampleTools[0]}
	a, err := RenderConfig(sampleTools, []string{"linux-x64", "linux-arm64"})
	require.NoError(t, err)
	b, err := RenderConfig(reversed, []string{"linux-arm64", "linux-x64", "linux-arm64"})
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b))
}

func TestRenderLockConfig(t *testing.T) {
	got, err := renderLockConfig(sampleTools, []string{"linux-x64"})
	require.NoError(t, err)
	s := string(got)
	assert.Contains(t, s, "lockfile = true\n")
	assert.Contains(t, s, `lockfile_platforms = ["linux-x64"]`)
	assert.NotContains(t, s, "locked")
	assert.NotContains(t, s, "use_versions_host")
	assert.NotContains(t, s, "trusted_config_paths")
	assert.Contains(t, s, "go = \"1.25\"\n")
}

func TestRenderConfigErrors(t *testing.T) {
	tests := []struct {
		name      string
		tools     []Tool
		platforms []string
		wantErr   string
	}{
		{
			name: "duplicate tool names both keys",
			tools: []Tool{
				{Key: "golang", Tool: "go"},
				{Key: "go", Tool: "go", Version: "1.24"},
			},
			platforms: []string{"linux-x64"},
			wantErr:   `components "go" and "golang" both install mise tool "go"`,
		},
		{
			name:      "empty tool name",
			tools:     []Tool{{Key: "broken"}},
			platforms: []string{"linux-x64"},
			wantErr:   `component "broken": empty mise tool name`,
		},
		{
			name:      "no platform",
			tools:     sampleTools,
			platforms: nil,
			wantErr:   "no lockfile platform given",
		},
		{
			name:      "unknown platform",
			tools:     sampleTools,
			platforms: []string{"macos-arm64"},
			wantErr:   `unsupported mise platform "macos-arm64"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RenderConfig(tt.tools, tt.platforms)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestTOMLQuoting(t *testing.T) {
	tests := []struct {
		in      string
		wantKey string
	}{
		{in: "go", wantKey: "go"},
		{in: "node_20-lts", wantKey: "node_20-lts"},
		{in: "aqua:jqlang/jq", wantKey: `"aqua:jqlang/jq"`},
		{in: `odd"name\x`, wantKey: `"odd\"name\\x"`},
		{in: "ctl\x01\n", wantKey: `"ctl\u0001\n"`},
		{in: "", wantKey: `""`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.wantKey, tomlKey(tt.in))

			var parsed map[string]string
			_, err := toml.Decode(tomlKey(tt.in)+" = "+tomlString(tt.in)+"\n", &parsed)
			require.NoError(t, err)
			assert.Equal(t, tt.in, parsed[tt.in])
		})
	}
}
