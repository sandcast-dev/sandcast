package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseImagesList(t *testing.T) {
	ta := newTestApp(t)
	require.Equal(t, 0, ta.run("base-images", "list", "--json"))
	var rows []baseImageRow
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rows))
	require.NotEmpty(t, rows)
	defaults := map[string]string{}
	for _, r := range rows {
		if r.Default {
			defaults[r.Target] = r.Name
		}
	}
	assert.Equal(t, map[string]string{"openshell": "nvidia-community-base", "container": "fedora-42"}, defaults)

	require.Equal(t, 0, ta.run("base-images", "list", "--target", "container"))
	assert.Contains(t, ta.stdout.String(), "fedora-42")
	assert.NotContains(t, ta.stdout.String(), "nvidia")
}

func TestComponentsListShow(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/components/go.yaml", "key: go\nname: Go (project)\ninstaller: {mise: {tool: go, version: \"1.24\"}}\ncheck: {command: go version}\n")

	require.Equal(t, 0, ta.run("components", "list", "--json"))
	var rows []componentRow
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rows))
	byKey := map[string]componentRow{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	assert.Equal(t, "project", byKey["go"].Tier)
	assert.Equal(t, ".sandcast/components/go.yaml", byKey["go"].SourceFile)
	assert.Equal(t, "shipped", byKey["starship"].Tier)

	require.Equal(t, 0, ta.run("components", "list", "--tier", "project"))
	assert.Contains(t, ta.stdout.String(), "go")
	assert.NotContains(t, ta.stdout.String(), "starship")
	assert.Equal(t, ExitUsage, ta.run("components", "list", "--tier", "remote"))

	require.Equal(t, 0, ta.run("components", "show", "starship"))
	assert.Contains(t, ta.stdout.String(), "key: starship")
	assert.Equal(t, ExitUsage, ta.run("components", "show", "nope"))
}
