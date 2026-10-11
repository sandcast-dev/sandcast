package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/fsutil"
	_ "github.com/rhuss/sandcast/internal/harness/claudecode" // registers the claude-code adapter
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/mise"
)

// lockTools runs LockTools on validateManifest plus extra against the
// shipped component tier.
func lockTools(t *testing.T, extra string) []mise.Tool {
	t.Helper()
	m, err := manifest.Parse([]byte(validateManifest+extra), "manifest.yaml")
	require.NoError(t, err)
	l, err := fsutil.NewLayout(t.TempDir())
	require.NoError(t, err)
	set, _, err := Components(l, t.TempDir())
	require.NoError(t, err)
	return LockTools(m, set)
}

func TestLockToolsIncludesHarnessAgents(t *testing.T) {
	goTool := mise.Tool{Key: "go", Tool: "go", Version: "latest"}
	claude := mise.Tool{Key: "claude-code", Tool: "claude", Version: "latest"}

	assert.ElementsMatch(t, []mise.Tool{goTool}, lockTools(t, ""), "no harness, no agent tool")

	// sandcast lock resolves the agent the build installs for claude-code,
	// so a locked build finds it in mise.lock.
	assert.ElementsMatch(t, []mise.Tool{goTool, claude}, lockTools(t, "harnesses:\n  - name: claude-code\n"))

	// codex has a shipped component but no adapter in this release: the
	// build adds no agent for it, so lock adds no tool either.
	assert.ElementsMatch(t, []mise.Tool{goTool}, lockTools(t, "harnesses:\n  - name: codex\n"))
	assert.ElementsMatch(t, []mise.Tool{goTool, claude}, lockTools(t, "harnesses:\n  - name: claude-code\n  - name: codex\n"))
}
