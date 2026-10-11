package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/skills"
)

func TestSkillsInstallAndCheck(t *testing.T) {
	ta := newTestApp(t)
	home := t.TempDir()
	ta.Getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	b, err := skills.Embedded()
	require.NoError(t, err)
	if len(b.Skills) == 0 {
		t.Skip("skill bundle not written yet")
	}

	assert.Equal(t, ExitTool, ta.run("skills", "check", "--harness", "test-harness"))
	assert.Contains(t, ta.stderr.String(), "sandcast skills install --harness test-harness")

	require.Equal(t, 0, ta.run("skills", "install", "--harness", "test-harness"), ta.stderr.String())
	assert.Contains(t, ta.stdout.String(), "installed sandcast-")
	for _, s := range b.Skills {
		_, err := os.Stat(filepath.Join(home, ".test-harness", "skills", "sandcast-"+s.Dir, "SKILL.md"))
		assert.NoError(t, err, s.Dir)
	}

	require.Equal(t, 0, ta.run("skills", "check", "--harness", "test-harness", "--json"))
	var rep skills.Report
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.True(t, rep.OK)

	// An older installed skill is reported with the update hint.
	path := filepath.Join(home, ".test-harness", "skills", "sandcast-"+b.Skills[0].Dir, "SKILL.md")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "sandcast_version: "+b.Version, "sandcast_version: 0.0.1", 1)), 0o644))
	assert.Equal(t, ExitTool, ta.run("skills", "check", "--harness", "test-harness"))
	assert.Contains(t, ta.stderr.String(), "older than the sandcast binary")

	assert.Equal(t, ExitUsage, ta.run("skills", "install", "--harness", "test-harness", "--scope", "global"))
	assert.Equal(t, ExitUsage, ta.run("skills", "install", "--harness", "vscode"))
}
