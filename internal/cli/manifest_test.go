package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestValidate(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	require.Equal(t, 0, ta.run("manifest", "validate"), ta.stdout.String())
	assert.Contains(t, ta.stdout.String(), "valid")

	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: not-a-tool\n  - key: maybe-tool\n    optional: true\n", 1))
	assert.Equal(t, ExitUsage, ta.run("manifest", "validate", "--json"))
	var rep validateReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.False(t, rep.Valid)
	require.Len(t, rep.Diagnostics, 1, "optional components may be undefined")
	assert.Equal(t, 8, rep.Diagnostics[0].Line)
	assert.Contains(t, rep.Diagnostics[0].Message, "not-a-tool")

	// A stored fragment makes an undefined component valid.
	ta.write(t, ".sandcast/fragments/not-a-tool.containerfile", "RUN true\n")
	require.Equal(t, 0, ta.run("manifest", "validate"), ta.stdout.String())
}

// TestManifestValidateAgreesWithPlan covers references that only implicit
// components carry: validate must refuse what plan refuses.
func TestManifestValidateAgreesWithPlan(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  program: zsh\n", "  program: zsh\n  components: [starship]\n", 1))
	// A package installer keeps the lockfile out of the way: plan reaches
	// the profile check.
	ta.write(t, ".sandcast/components/starship.yaml", "key: starship\nname: Starship\ninstaller: {package: {packages: {apt-get: [starship]}, bins: [starship]}}\ncheck: {command: starship --version}\nprofiles: [no-such-profile]\n")

	assert.Equal(t, ExitUsage, ta.run("plan"), ta.stderr.String())
	assert.Contains(t, ta.stderr.String(), `references profile "no-such-profile"`)

	assert.Equal(t, ExitUsage, ta.run("manifest", "validate", "--json"), ta.stdout.String())
	var rep validateReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	require.Len(t, rep.Diagnostics, 1, "%v", rep.Diagnostics)
	assert.Equal(t, "$.shell.components[0]", rep.Diagnostics[0].Path)
	assert.Equal(t, 10, rep.Diagnostics[0].Line)
	assert.Contains(t, rep.Diagnostics[0].Message, `references profile "no-such-profile"`)

	// A harness name the schema accepts but no adapter handles.
	ta.write(t, ".sandcast/manifest.yaml", testManifest+"harnesses:\n  - name: codex\n")
	assert.Equal(t, ExitUsage, ta.run("plan"))
	assert.Equal(t, ExitUsage, ta.run("manifest", "validate"))
	assert.Contains(t, ta.stdout.String(), "no adapter for harness codex in this release")
}

func TestManifestValidateSchemaAndSecrets(t *testing.T) {
	ta := newTestApp(t)
	ta.write(t, ".sandcast/manifest.yaml", "schema_version: 2\nname: demo\n")
	assert.Equal(t, ExitUsage, ta.run("manifest", "validate"))
	assert.Contains(t, ta.stdout.String(), "schema version 2")

	secret := "ghp_" + strings.Repeat("Z", 36)
	ta.write(t, ".sandcast/manifest.yaml", testManifest+"harnesses:\n  - name: claude-code\n    mcp_servers:\n      - name: gh\n        url: https://mcp.example.com\n        env:\n          GITHUB_TOKEN: "+secret+"\n")
	assert.Equal(t, ExitUsage, ta.run("manifest", "validate"))
	assert.Contains(t, ta.stdout.String(), "looks like a credential")
	assert.NotContains(t, ta.stdout.String(), secret)
}

func TestManifestSetShell(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	rc := filepath.Join(t.TempDir(), "rc")
	require.NoError(t, os.WriteFile(rc, []byte("alias ls=lsd\n"), 0o644))
	require.Equal(t, 0, ta.run("manifest", "set-shell", "--program", "bash", "--file", rc), ta.stderr.String())
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "program: bash")
	assert.Contains(t, string(data), "alias ls=lsd")

	ta.Stdin = strings.NewReader("export TOKEN=glpat-" + strings.Repeat("q", 20) + "\n")
	assert.Equal(t, ExitUsage, ta.run("manifest", "set-shell"))
	assert.Equal(t, ExitUsage, ta.run("manifest", "set-shell", "--program", "fish"))
}
