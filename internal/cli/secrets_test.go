package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRefusesManifestWithCredential(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	token := "sk-ant-api03-" + strings.Repeat("x", 90)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "    autoload -Uz compinit && compinit -C\n", "    autoload -Uz compinit && compinit -C\n    export ANTHROPIC_API_KEY="+token+"\n", 1))
	assert.Equal(t, ExitUsage, ta.run("build"))
	assert.Zero(t, ta.eng.CountOp("build"), "no image is built from a manifest with a credential")
	assert.Contains(t, ta.stderr.String(), "credential")
	assert.NotContains(t, ta.stderr.String(), token)
}

func TestPlanRefusesInvalidStoredFragment(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: handmade\n", 1))
	ta.write(t, ".sandcast/fragments/handmade.containerfile", "ADD https://example.com/x /usr/local/bin/x\n")
	assert.Equal(t, ExitUsage, ta.run("plan"))
	assert.Contains(t, ta.stderr.String(), ".sandcast/fragments/handmade.containerfile")
}

func TestBuildRefusesSymlinkedBuildDir(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	elsewhere := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ta.dir, ".sandcast", "build"), 0o755))
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(ta.dir, ".sandcast", "build", "openshell")))
	assert.Equal(t, ExitUsage, ta.run("build"))
	assert.Contains(t, ta.stderr.String(), "symlink")
	assert.Zero(t, ta.eng.CountOp("build"), "nothing is built through a symlinked build directory")
	entries, err := os.ReadDir(elsewhere)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing is written outside the project")
}

func TestPlanRefusesStoredFragmentWithCredential(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	token := "ghp_" + strings.Repeat("k", 36)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: handmade\n", 1))
	ta.write(t, ".sandcast/fragments/handmade.containerfile", "USER root\nRUN echo "+token+"\nUSER sandbox\n")
	assert.Equal(t, ExitUsage, ta.run("plan"))
	assert.Contains(t, ta.stderr.String(), ".sandcast/fragments/handmade.containerfile")
	assert.Contains(t, ta.stderr.String(), "credential")
	assert.NotContains(t, ta.stderr.String(), token)
	_, err := os.Stat(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "plan.yaml"))
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing is written")
}

func TestPlanRefusesLiteralMCPCredentialWithExit2(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", testManifest+"harnesses:\n  - name: claude-code\n    mcp_servers:\n      - name: cli\n        command: [srv, --token, plain1]\n")
	assert.Equal(t, ExitUsage, ta.run("plan"), ta.stderr.String())
	assert.Contains(t, ta.stderr.String(), "literal credential")
	assert.NotContains(t, ta.stderr.String(), "plain1")
}

func TestRefreshInvalidRefExit2(t *testing.T) {
	ta := newTestApp(t)
	assert.Equal(t, ExitUsage, ta.run("components", "refresh", "--ref", "../x"))
	assert.Contains(t, ta.stderr.String(), "not a valid registry tag")
	assert.Equal(t, ExitUsage, ta.run("components", "refresh", "--ref", "current"), "the active-tag file name is reserved")
}

func TestPlanRefusesBadHarnessSourcesWithExit2(t *testing.T) {
	for name, tc := range map[string]struct{ harness, want string }{
		"skill outside the allowed roots": {
			harness: "harnesses:\n  - name: claude-code\n    skills:\n      - name: keys\n        source: ~/.ssh\n",
			want:    "outside the project, ~/.claude/skills and ~/.claude/plugins",
		},
		"marketplace with a token as user": {
			harness: "harnesses:\n  - name: claude-code\n    plugins:\n      - name: x@y\n        source: https://0123456789abcdef0123456789abcdef01234567@gitea.example.com/o/r.git\n",
			want:    "carries a credential",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ta := newTestApp(t)
			ta.project(t)
			// ~/.ssh exists in the test home, so the refusal comes from the
			// allowed-roots check and not from a missing directory.
			require.NoError(t, os.MkdirAll(filepath.Join(ta.home, ".ssh"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(ta.home, ".ssh", "id_ed25519"), []byte("key\n"), 0o600))
			ta.write(t, ".sandcast/manifest.yaml", testManifest+tc.harness)
			assert.Equal(t, ExitUsage, ta.run("plan"), ta.stderr.String())
			assert.Contains(t, ta.stderr.String(), "scaffold harness claude-code", "refused by the scaffold, not by schema validation")
			assert.Contains(t, ta.stderr.String(), tc.want)
		})
	}
}
