package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/fsutil"
	_ "github.com/rhuss/sandcast/internal/harness/claudecode" // registers the claude-code adapter
	"github.com/rhuss/sandcast/internal/manifest"
)

const validateManifest = `schema_version: 1
name: demo
base_image: nvidia-community-base
components:
  - key: go
  - key: git
targets:
  openshell:
    image: localhost/demo
  container:
    image: localhost/demo-plain
`

// validateProject writes the manifest and extra files (relative to .sandcast)
// into a fresh project and runs ValidateReferences on it.
func validateProject(t *testing.T, manifestText string, files map[string]string) []manifest.Diagnostic {
	t.Helper()
	dir := t.TempDir()
	files["manifest.yaml"] = manifestText
	for rel, content := range files {
		path := filepath.Join(dir, fsutil.DirName, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	l, err := fsutil.NewLayout(dir)
	require.NoError(t, err)
	m, err := manifest.Load(l.ManifestPath())
	require.NoError(t, err)
	diags, err := ValidateReferences(l, t.TempDir(), m)
	require.NoError(t, err)
	return diags
}

const badStarship = `key: starship
name: Starship
installer: {mise: {tool: starship, version: latest, bins: [starship]}}
check: {command: starship --version}
profiles: [no-such-profile]
`

func TestValidateReferences(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		assert.Empty(t, validateProject(t, validateManifest, map[string]string{}))
	})

	t.Run("implicit shell component with an undefined profile", func(t *testing.T) {
		// plan.Build checks the profiles of every installed component,
		// implicit ones included; validate must catch the same.
		diags := validateProject(t, validateManifest+"shell:\n  program: zsh\n  components: [starship]\n",
			map[string]string{"components/starship.yaml": badStarship})
		require.Len(t, diags, 1, "reported once although both targets install it: %v", diags)
		assert.Equal(t, "$.shell.components[0]", diags[0].Path)
		assert.Contains(t, diags[0].Message, `component starship (.sandcast/components/starship.yaml) references profile "no-such-profile"`)
		assert.Contains(t, diags[0].Message, "referenced by the shell configuration")
	})

	t.Run("harness agent component from the adapter", func(t *testing.T) {
		diags := validateProject(t, validateManifest+"harnesses:\n  - name: claude-code\n",
			map[string]string{"components/claude-code.yaml": strings.Replace(badStarship, "key: starship", "key: claude-code", 1)})
		require.Len(t, diags, 1, "%v", diags)
		assert.Equal(t, "$.harnesses[0].name", diags[0].Path)
		assert.Contains(t, diags[0].Message, "component claude-code")
	})

	t.Run("harness without an adapter", func(t *testing.T) {
		diags := validateProject(t, validateManifest+"harnesses:\n  - name: codex\n", map[string]string{})
		require.Len(t, diags, 1, "%v", diags)
		assert.Equal(t, "$.harnesses[0].name", diags[0].Path)
		assert.Contains(t, diags[0].Message, "no adapter for harness codex in this release")
	})

	t.Run("undefined component", func(t *testing.T) {
		text := strings.Replace(validateManifest, "  - key: git\n", "  - key: git\n  - key: mystery\n  - key: maybe\n    optional: true\n", 1)
		diags := validateProject(t, text, map[string]string{})
		require.Len(t, diags, 1, "%v", diags)
		assert.Equal(t, "$.components[2].key", diags[0].Path)
		assert.Contains(t, diags[0].Message, `component "mystery" is not defined`)

		assert.Empty(t, validateProject(t, text, map[string]string{"fragments/mystery.containerfile": "RUN true\n"}),
			"an active stored fragment covers it")
	})

	t.Run("invalid stored fragment", func(t *testing.T) {
		text := strings.Replace(validateManifest, "  - key: git\n", "  - key: git\n  - key: mystery\n", 1)
		diags := validateProject(t, text, map[string]string{
			"fragments/mystery.containerfile": "ADD https://example.com/x /usr/local/bin/x\n",
			"fragments/leaky.containerfile":   "RUN echo ghp_" + strings.Repeat("k", 36) + "\n",
		})
		var msgs []string
		for _, d := range diags {
			msgs = append(msgs, d.Message)
		}
		joined := strings.Join(msgs, "\n")
		assert.Contains(t, joined, ".sandcast/fragments/mystery.containerfile", "the checks plan applies")
		assert.Contains(t, joined, ".sandcast/fragments/leaky.containerfile")
		assert.Contains(t, joined, "credential")
		assert.NotContains(t, joined, `component "mystery" is not defined`, "the fragment still counts as stored")
	})

	t.Run("binary conflict", func(t *testing.T) {
		text := strings.Replace(validateManifest, "  - key: git\n", "  - key: git\n  - key: othergo\n", 1)
		diags := validateProject(t, text, map[string]string{
			"components/othergo.yaml": "key: othergo\nname: Other Go\ninstaller: {mise: {tool: go, version: latest, bins: [go]}}\ncheck: {command: go version}\n",
		})
		require.Len(t, diags, 1, "%v", diags)
		assert.Equal(t, "$.components", diags[0].Path)
		assert.Contains(t, diags[0].Message, "conflict: binary go is provided by [go othergo]")
	})
}

// TestValidateReferencesBins covers FR-018: bins on a shipped key are
// rejected with the contract's message, bins on an undefined optional key
// are fine.
func TestValidateReferencesBins(t *testing.T) {
	diags := validateProject(t, `schema_version: 1
name: demo
base_image: nvidia-community-base
components:
  - key: go
    bins: [go]
  - key: helix
    optional: true
    bins: [hx]
targets:
  openshell:
    image: localhost/demo
`, map[string]string{})
	require.Len(t, diags, 1, "%v", diags)
	assert.Equal(t, "$.components[0].bins", diags[0].Path)
	assert.Equal(t, "go is defined by the shipped tier; its binaries come from the definition, remove bins", diags[0].Message)
}

func TestValidateReferencesDeclaredBinsConflict(t *testing.T) {
	diags := validateProject(t, `schema_version: 1
name: demo
base_image: nvidia-community-base
components:
  - key: git
  - key: my-git
    optional: true
    bins: [git]
targets:
  openshell:
    image: localhost/demo
`, map[string]string{})
	require.Len(t, diags, 1, "%v", diags)
	assert.Equal(t, "$.components[1].bins[0]", diags[0].Path, "the conflict points at the declared binary")
	assert.Contains(t, diags[0].Message, "git")
	assert.Contains(t, diags[0].Message, "my-git")
}
