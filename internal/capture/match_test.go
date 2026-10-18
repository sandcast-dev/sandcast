package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/component"
)

func shipped(t *testing.T) []*component.Component {
	t.Helper()
	comps, err := component.LoadDir(sandcast.Components, component.TierShipped, sandcast.ComponentsPrefix)
	require.NoError(t, err)
	return comps
}

func fakeHost(tools map[string]string) Options {
	return Options{
		Host: true,
		LookPath: func(name string) (string, error) {
			if _, ok := tools[name]; ok {
				return "/opt/homebrew/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Version: func(_ context.Context, bin string) string { return tools[filepath.Base(bin)] },
	}
}

func byKey(r *Result) map[string]Proposal {
	out := map[string]Proposal{}
	for _, p := range r.Proposals {
		out[p.Key] = p
	}
	return out
}

func TestMatchReferenceProject(t *testing.T) {
	o := fakeHost(map[string]string{"starship": "starship 1.20.1", "fzf": "0.56.3 (devel)", "lsd": "lsd 1.1.5"})
	o.Harnesses = []string{"claude-code"}
	res, err := Match(context.Background(), "../../tests/fixtures/reference-project", shipped(t), o)
	require.NoError(t, err)
	got := byKey(res)

	for key, src := range map[string]string{
		"go": SourceProject, "python": SourceProject, "uv": SourceProject,
		"starship": SourceHost, "fzf": SourceHost, "lsd": SourceHost,
		"claude-code": SourceHarness, "git": SourceAlways,
	} {
		p, ok := got[key]
		if assert.True(t, ok, "proposal %s", key) {
			assert.Equal(t, src, p.Source, key)
			assert.NotEmpty(t, p.Evidence, key)
		}
	}
	assert.Contains(t, got["go"].Evidence, "go.mod")
	assert.Contains(t, got["uv"].Evidence, "uv.lock")
	assert.Equal(t, "1.20.1", got["starship"].DetectedVersion)
	_, hasCodex := got["codex"]
	assert.False(t, hasCodex, "unselected harness leaves no trace")
	keys := []string{}
	for _, a := range res.Available {
		keys = append(keys, a.Key)
	}
	assert.Contains(t, keys, "codex")
	assert.Contains(t, keys, "node")
}

func TestMatchNoHost(t *testing.T) {
	o := fakeHost(map[string]string{"starship": "starship 1.20.1"})
	o.Host = false
	res, err := Match(context.Background(), "../../tests/fixtures/reference-project", shipped(t), o)
	require.NoError(t, err)
	_, ok := byKey(res)["starship"]
	assert.False(t, ok)
}

func TestMatchEmptyProjectStillOffersTiers(t *testing.T) {
	res, err := Match(context.Background(), t.TempDir(), shipped(t), fakeHost(nil))
	require.NoError(t, err)
	for _, p := range res.Proposals {
		assert.Equal(t, SourceAlways, p.Source, "only always-components are proposed for an empty project: %s", p.Key)
	}
	assert.NotEmpty(t, res.Available)
}

func TestMatchRespectsGitignoreAndSkips(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}
	write(".gitignore")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("generated/\n*.tmp\n"), 0o644))
	write("generated/go.mod")
	write("node_modules/pkg/package.json")
	write("services/api/go.mod")
	write("a/b/c/d/e/Cargo.toml")
	files, err := projectFiles(dir)
	require.NoError(t, err)
	assert.Contains(t, files, "services/api/go.mod")
	assert.NotContains(t, files, "generated/go.mod")
	assert.NotContains(t, files, "node_modules/pkg/package.json")
	assert.NotContains(t, files, "a/b/c/d/e/Cargo.toml", "depth limit")
}

func TestGlobMatch(t *testing.T) {
	assert.True(t, globMatch("go.mod", "svc/go.mod"))
	assert.True(t, globMatch("requirements*.txt", "requirements-dev.txt"))
	assert.False(t, globMatch("docs/*.md", "README.md"))
	assert.True(t, globMatch("docs/*.md", "docs/a.md"))
}

func TestMatchNeverRunsPathHostTools(t *testing.T) {
	ran := []string{}
	comps := []*component.Component{{Key: "evil", Name: "evil", Match: &component.Match{HostTools: []string{"./x.sh", "rel"}}}}
	o := Options{
		Host:     true,
		LookPath: func(name string) (string, error) { return name, nil },
		Version:  func(_ context.Context, bin string) string { ran = append(ran, bin); return "1.0.0" },
	}
	res, err := Match(context.Background(), t.TempDir(), comps, o)
	require.NoError(t, err)
	assert.Empty(t, ran, "neither a path nor a relative LookPath result is executed")
	assert.Empty(t, res.Proposals)
}

// TestMatchPinnedProject covers acceptance scenario 6 of User Story 2 on
// tests/fixtures/pinned-project.
func TestMatchPinnedProject(t *testing.T) {
	res, err := Match(context.Background(), "../../tests/fixtures/pinned-project", shipped(t), Options{})
	require.NoError(t, err)
	assert.Empty(t, res.Notes)
	got := byKey(res)

	first := func(key string) PinnedVersion {
		p, ok := got[key]
		require.True(t, ok, "%s proposed", key)
		assert.Equal(t, SourceProject, p.Source, key)
		require.NotEmpty(t, p.PinnedVersions, key)
		return p.PinnedVersions[0]
	}
	assert.Equal(t, PinnedVersion{Value: "1.95.0", File: "rust-toolchain.toml", Kind: PinExact}, first("rust"))
	assert.Equal(t, PinnedVersion{Value: "3.14.5", File: ".python-version", Kind: PinExact}, first("python"))
	assert.Equal(t, PinnedVersion{Value: "1.26.7", File: "go.mod", Kind: PinExact}, first("go"))
	assert.Equal(t, "1.26.7", got["go"].PinnedSummary())
	assert.Equal(t, []PinnedVersion{
		{Value: "1.26.7", File: "go.mod", Kind: PinExact},
		{Value: "1.26.7", File: "go.work", Kind: PinExact},
		{Value: "1.25.3", File: "tools/go.mod", Kind: PinExact},
	}, got["go"].PinnedVersions, "each source is reported, highest first")
	assert.Equal(t, []PinnedVersion{
		{Value: "20.11.1", File: ".tool-versions", Kind: PinExact},
		{Value: "^20", File: "package.json", Kind: PinRange},
	}, got["node"].PinnedVersions)
	assert.Contains(t, got["node"].Evidence, ".tool-versions", ".tool-versions counts as evidence")
}

// TestMatchToolVersionsCreatesProposal checks that a .tool-versions entry
// alone proposes the component whose mise tool it names.
func TestMatchToolVersionsCreatesProposal(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".tool-versions"), []byte("golang 1.26.7\n"), 0o644))
	res, err := Match(context.Background(), root, shipped(t), Options{})
	require.NoError(t, err)
	goProp, ok := byKey(res)["go"]
	require.True(t, ok)
	assert.Equal(t, SourceProject, goProp.Source)
	assert.Equal(t, []string{".tool-versions"}, goProp.Evidence)
	assert.Equal(t, "1.26.7", goProp.PinnedSummary())
}

func TestPinnedSummary(t *testing.T) {
	assert.Equal(t, "", Proposal{}.PinnedSummary())
	assert.Equal(t, "^20 (range)", Proposal{PinnedVersions: []PinnedVersion{{Value: "^20", Kind: PinRange}}}.PinnedSummary())
	assert.Equal(t, "20.1.0", Proposal{PinnedVersions: []PinnedVersion{{Value: "lts/*", Kind: PinChannel}, {Value: "20.1.0", Kind: PinExact}}}.PinnedSummary())
	// go: the highest directive is the proposal even when a lower one is exact.
	goPins := []PinnedVersion{{Value: "1.27", File: "b/go.mod", Kind: PinRange}, {Value: "1.26.7", File: "a/go.mod", Kind: PinExact}}
	assert.Equal(t, "1.27 (range)", Proposal{Key: "go", PinnedVersions: goPins}.PinnedSummary())
	assert.Equal(t, "1.26.7", Proposal{Key: "go", PinnedVersions: goPins[1:]}.PinnedSummary())
}
