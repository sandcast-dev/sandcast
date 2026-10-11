//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sandcastCmd(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), isolatedEnv(t)...)
	return cmd
}

// TestPromoteFlow covers US3 scenario 4 and SC-005: a stored fragment is
// reused by the non-interactive build, promoted into a recipe through the
// CLI, and the next build uses the recipe while the plan reports the
// fragment as stale.
func TestPromoteFlow(t *testing.T) {
	engine := requireEngine(t)
	dir := project(t, `schema_version: 1
name: sc-promote
base_image: nvidia-community-base
components:
  - key: faketool
targets:
  openshell:
    image: localhost/sandcast-it-promote
`)
	removeImages(t, engine, "localhost/sandcast-it-promote:latest")
	writeFragment(t, dir, "faketool", `USER root
RUN printf '#!/bin/sh\necho faketool 1.0.0\n' > /usr/local/bin/faketool \
 && chmod 0755 /usr/local/bin/faketool
USER sandbox
`)

	require.Zero(t, sandcast(t, dir, nil, "build", "--verify").code, "fragment reused without an agent")

	r := sandcast(t, dir, nil, "promote", "faketool", "--yes")
	require.Zero(t, r.code)
	recipe, err := os.ReadFile(filepath.Join(dir, ".sandcast", "recipes", "faketool.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(recipe), "as_root: true")

	r = sandcast(t, dir, nil, "plan", "--json")
	require.Zero(t, r.code)
	var p struct {
		Components []struct {
			Key           string `json:"key"`
			InstallerKind string `json:"installer_kind"`
			SourceFile    string `json:"source_file"`
		} `json:"components"`
		Notices []string `json:"notices"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &p))
	found := false
	for _, c := range p.Components {
		if c.Key == "faketool" {
			found = true
			assert.Equal(t, "script", c.InstallerKind)
			assert.Equal(t, ".sandcast/recipes/faketool.yaml", c.SourceFile)
		}
	}
	assert.True(t, found)
	assert.Contains(t, strings.Join(p.Notices, "\n"), "fragments/faketool.containerfile is stale")

	require.Zero(t, sandcast(t, dir, nil, "build", "--verify").code, "the promoted recipe builds without an agent (SC-005)")
}
