package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/capture"
	"github.com/rhuss/sandcast/internal/harness"
)

// cliTestAdapter is a harness adapter that installs skills under the test
// home, registered for the CLI tests.
type cliTestAdapter struct{}

func (cliTestAdapter) Name() string                         { return "test-harness" }
func (cliTestAdapter) ComponentKey() string                 { return "claude-code" }
func (cliTestAdapter) Installed(harness.Env) (bool, string) { return true, "/usr/bin/test-harness" }
func (cliTestAdapter) Discover(context.Context, harness.Env) (*harness.Discovery, error) {
	return &harness.Discovery{Harness: "test-harness", Notes: []string{"nothing configured"}}, nil
}
func (cliTestAdapter) Scaffold(harness.ScaffoldInput) (*harness.Scaffold, error) {
	return &harness.Scaffold{}, nil
}
func (cliTestAdapter) SkillFileName() string { return "SKILL.md" }
func (cliTestAdapter) SkillInstallDir(s harness.Scope, env harness.Env) (string, error) {
	if s == harness.ScopeProject {
		return filepath.Join(env.Project, ".test-harness", "skills"), nil
	}
	return filepath.Join(env.Home, ".test-harness", "skills"), nil
}

func init() { harness.Register(cliTestAdapter{}) }

func TestComponentsMatch(t *testing.T) {
	ta := newTestApp(t)
	ta.LookPath = func(name string) (string, error) {
		if name == "podman" || name == "fzf" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	// The host binary is never executed; the fake answers its version.
	var queried []string
	ta.HostVersion = func(_ context.Context, bin string) string {
		queried = append(queried, bin)
		return "0.56.3 (brew)"
	}
	fixture, err := filepath.Abs("../../tests/fixtures/reference-project")
	require.NoError(t, err)
	// The host home is the test home (HOME through Getenv), never the
	// developer's: its .gitconfig is the one dotfile proposed.
	require.NoError(t, os.WriteFile(filepath.Join(ta.home, ".gitconfig"), []byte("[user]\n\temail = a@b.c\n"), 0o644))
	require.Equal(t, 0, ta.run("components", "match", fixture, "--json", "--harness", "test-harness"), ta.stderr.String())
	var res capture.Result
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &res))
	assert.Equal(t, []capture.DotfileProposal{{Path: "~/.gitconfig"}}, res.Dotfiles)
	assert.Empty(t, res.DotfileNotices)
	keys := map[string]string{}
	versions := map[string]string{}
	for _, p := range res.Proposals {
		keys[p.Key] = p.Source
		versions[p.Key] = p.DetectedVersion
	}
	assert.Equal(t, "project", keys["go"])
	assert.Equal(t, "host", keys["fzf"])
	assert.Equal(t, "0.56.3", versions["fzf"], "the version comes from the injected host version function")
	assert.Equal(t, []string{"/usr/bin/fzf"}, queried)
	assert.NotContains(t, keys, "starship", "not on this fake host")

	queried = nil
	require.Equal(t, 0, ta.run("components", "match", fixture), ta.stderr.String())
	assert.Contains(t, ta.stdout.String(), "dotfiles: ~/.gitconfig\n")

	require.Equal(t, 0, ta.run("components", "match", fixture, "--no-host"))
	assert.Contains(t, ta.stdout.String(), "go.mod")
	assert.NotContains(t, ta.stdout.String(), "/usr/bin/fzf")
	assert.NotContains(t, ta.stdout.String(), "dotfiles:", "no host scan without host detection")

	assert.Equal(t, ExitUsage, ta.run("components", "match", fixture, "--harness", "vscode"))
	assert.Equal(t, ExitUsage, ta.run("components", "match", filepath.Join(fixture, "go.mod")))
}

// TestComponentsMatchUnknownHome checks an unknown host home (HOME unset in
// a CI container or a service) is reported instead of silently proposing no
// dotfiles.
func TestComponentsMatchUnknownHome(t *testing.T) {
	ta := newTestApp(t)
	ta.Getenv = func(string) string { return "" }
	t.Setenv("HOME", "")
	fixture, err := filepath.Abs("../../tests/fixtures/reference-project")
	require.NoError(t, err)
	require.Equal(t, 0, ta.run("components", "match", fixture, "--json"), ta.stderr.String())
	var res capture.Result
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &res))
	assert.Empty(t, res.Dotfiles)
	require.Len(t, res.DotfileNotices, 1)
	assert.Contains(t, res.DotfileNotices[0], "host home directory is unknown")
	assert.Contains(t, res.DotfileNotices[0], "dotfiles were not scanned")

	require.Equal(t, 0, ta.run("components", "match", fixture))
	assert.Contains(t, ta.stdout.String(), "dotfile skipped: the host home directory is unknown")
}

func TestHarnessListDiscover(t *testing.T) {
	ta := newTestApp(t)
	require.Equal(t, 0, ta.run("harness", "list", "--json"))
	var rows []harnessRow
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rows))
	found := false
	for _, r := range rows {
		if r.Name == "test-harness" {
			found = true
			assert.True(t, r.Installed)
		}
	}
	assert.True(t, found)
	require.Equal(t, 0, ta.run("harness", "discover", "test-harness"))
	assert.Contains(t, ta.stdout.String(), "nothing configured")
	assert.Equal(t, ExitUsage, ta.run("harness", "discover", "vscode"))
}
