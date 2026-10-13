package claudecode

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/harness"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/redact"
)

// fakeSecrets are the obviously fake credentials embedded in the fixtures.
var fakeSecrets = []string{
	"ghp_" + strings.Repeat("x", 36),
	"sk-ant-" + strings.Repeat("x", 24),
	"AKIA" + strings.Repeat("X", 16),
	"hunter2",
	"shortkey1",
	"abc123",
	"hunter3",
	"qwerty9",
	"dXNlcjpwYXNz",
}

// undot renames every dot-NAME entry below dir to .NAME. The fixtures are
// stored without leading dots because the repository ignores .claude/ and
// .mcp.json files, which must never be committed.
func undot(t *testing.T, dir string) {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), "dot-") {
			paths = append(paths, p)
		}
		return err
	}))
	// Deepest first, so parents are renamed after their children.
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		require.NoError(t, os.Rename(p, filepath.Join(filepath.Dir(p), "."+strings.TrimPrefix(filepath.Base(p), "dot-"))))
	}
}

// fixtureEnv copies testdata/home and testdata/project into a temporary
// directory, restores the leading dots of the fixture names, and fills in
// the __HOME__ and __PROJECT__ markers.
func fixtureEnv(t *testing.T) harness.Env {
	t.Helper()
	root := t.TempDir()
	env := harness.Env{Home: filepath.Join(root, "home"), Project: filepath.Join(root, "project")}
	require.NoError(t, os.CopyFS(env.Home, os.DirFS(filepath.Join("testdata", "home"))))
	require.NoError(t, os.CopyFS(env.Project, os.DirFS(filepath.Join("testdata", "project"))))
	undot(t, env.Home)
	undot(t, env.Project)
	for _, rel := range []string{
		".claude.json",
		".claude/plugins/installed_plugins.json",
		".claude/plugins/known_marketplaces.json",
	} {
		path := filepath.Join(env.Home, rel)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		data = []byte(strings.NewReplacer("__HOME__", env.Home, "__PROJECT__", env.Project).Replace(string(data)))
		require.NoError(t, os.WriteFile(path, data, 0o644))
	}
	return env
}

func server(name, origin string, command []string, url string, env map[string]string) harness.MCPServer {
	return harness.MCPServer{
		MCPServer: manifest.MCPServer{Name: name, Command: command, URL: url, Env: env},
		Origin:    origin,
	}
}

func TestDiscover(t *testing.T) {
	env := fixtureEnv(t)
	d, err := Adapter{}.Discover(context.Background(), env)
	require.NoError(t, err)

	local := "~/.claude.json projects[" + env.Project + "]"
	assert.Equal(t, "claude-code", d.Harness)
	assert.Equal(t, []harness.MCPServer{
		server("anthropic-proxy", ".mcp.json", []string{"proxy"}, "", map[string]string{"ANTHROPIC_API_KEY": "${SANDCAST_SECRET_1}"}),
		server("basic", ".mcp.json", nil, "https://basic.example.com/mcp", map[string]string{"MCP_BASIC_TOKEN": "${SANDCAST_SECRET_2}"}),
		server("db", local, []string{"db-mcp", "--api-key", "${SANDCAST_SECRET_3}"}, "", map[string]string{
			"AWS_ACCESS_KEY_ID": "${SANDCAST_SECRET_4}",
			"DB_PASSWORD":       "${SANDCAST_SECRET_5}",
			"LOG_LEVEL":         "info",
		}),
		server("docs", ".mcp.json", nil, "https://docs.example.com/sse", map[string]string{"DOCS_TOKEN": "${DOCS_TOKEN}"}),
		server("filesystem", "~/.claude.json", []string{"npx", "-y", "@modelcontextprotocol/server-filesystem", "/workspace"}, "", nil),
		server("github", "~/.claude.json", nil, "https://api.githubcopilot.com/mcp/", map[string]string{"MCP_GITHUB_TOKEN": "${SANDCAST_SECRET_6}"}),
		server("search", "~/.claude.json", nil, "https://search.example.com/mcp?apikey=${SANDCAST_SECRET_7}", nil),
		server("shared", local, []string{"local-shared"}, "", nil),
	}, d.MCPServers)

	assert.Equal(t, []harness.Redaction{
		{Path: "mcp_servers[anthropic-proxy].env.ANTHROPIC_API_KEY", Kind: redact.KindAnthropic, Placeholder: "${SANDCAST_SECRET_1}", EnvVar: "ANTHROPIC_API_KEY"},
		{Path: "mcp_servers[basic].env.MCP_BASIC_TOKEN", Kind: redact.KindKeyValue, Placeholder: "${SANDCAST_SECRET_2}", EnvVar: "MCP_BASIC_TOKEN"},
		{Path: "mcp_servers[db].command[2]", Kind: redact.KindKeyValue, Placeholder: "${SANDCAST_SECRET_3}", EnvVar: "SANDCAST_SECRET_3"},
		{Path: "mcp_servers[db].env.AWS_ACCESS_KEY_ID", Kind: redact.KindAWS, Placeholder: "${SANDCAST_SECRET_4}", EnvVar: "AWS_ACCESS_KEY_ID"},
		{Path: "mcp_servers[db].env.DB_PASSWORD", Kind: redact.KindKeyValue, Placeholder: "${SANDCAST_SECRET_5}", EnvVar: "DB_PASSWORD"},
		{Path: "mcp_servers[github].env.MCP_GITHUB_TOKEN", Kind: redact.KindGitHub, Placeholder: "${SANDCAST_SECRET_6}", EnvVar: "MCP_GITHUB_TOKEN"},
		{Path: "mcp_servers[search].url", Kind: redact.KindKeyValue, Placeholder: "${SANDCAST_SECRET_7}", EnvVar: "SANDCAST_SECRET_7"},
	}, d.Redacted)

	official := "anthropics/claude-plugins-official"
	pluginOrigin := "~/.claude/plugins/installed_plugins.json"
	assert.Equal(t, []harness.Extension{
		{Name: "github@claude-plugins-official", Source: official, Origin: pluginOrigin, Portable: true},
		{Name: "gopls-lsp@claude-plugins-official", Source: official, Origin: pluginOrigin, Portable: true},
		{Name: "legacy@team-tools", Source: "example-org/team-tools", Origin: pluginOrigin, Portable: true},
		{Name: "proj-tool@proj-market", Source: "./tools/proj-market", Origin: pluginOrigin, Portable: true},
		{Name: "review-kit@team-tools", Source: "example-org/team-tools", Origin: pluginOrigin, Portable: true},
		{Name: "secret-kit@private-tools", Source: "https://git.example.com/org/private-tools.git", Origin: pluginOrigin, Portable: true},
	}, d.Plugins)

	assert.Equal(t, []harness.Extension{
		{Name: "deploy", Source: ".claude/skills/deploy", Origin: ".claude/skills", Portable: true},
		{Name: "review", Source: ".claude/skills/review", Origin: ".claude/skills", Portable: true},
		{Name: "writing", Source: "~/.claude/skills/writing", Origin: "~/.claude/skills",
			Reason: "source under ~/.claude/skills is read on the machine that runs the plan"},
	}, d.Skills)

	notes := strings.Join(d.Notes, "\n")
	for _, want := range []string{
		"MCP server basic: the Basic authorization scheme is sent as a bearer token in the image",
		"MCP server broken in .mcp.json skipped: unsupported entry",
		"MCP server search: header X-Client is not baked",
		"MCP server shared in .mcp.json is shadowed by the one in " + local,
		"MCP server shared in ~/.claude.json is shadowed by the one in " + local,
		`MCP server socket in ~/.claude.json skipped: transport "ws" cannot be baked`,
		"plugin disabled-one@claude-plugins-official is disabled",
		`plugin mystery@unknown-market skipped: the source of marketplace "unknown-market" is unknown`,
		`plugin local-tool@dev-marketplace skipped: local marketplace "dev-marketplace" cannot be copied into the image and has no git remote: source ~/dev/dev-marketplace resolves to`,
		`plugin secret-kit@private-tools: credentials removed from the URL of marketplace "private-tools"`,
		`plugin token-kit@token-query skipped: the URL of marketplace "token-query" carries a credential`,
		"skill review in ~/.claude/skills is shadowed by the project skill",
	} {
		assert.Contains(t, notes, want)
	}
	assert.NotContains(t, notes, "elsewhere")
	assert.IsIncreasing(t, d.Notes)
}

// TestDiscoverNoSecretSurvives checks SC-009 for discovery: no fixture
// credential appears anywhere in the marshalled result.
func TestDiscoverNoSecretSurvives(t *testing.T) {
	d, err := Adapter{}.Discover(context.Background(), fixtureEnv(t))
	require.NoError(t, err)

	asJSON, err := json.Marshal(d)
	require.NoError(t, err)
	asYAML, err := yaml.Marshal(d)
	require.NoError(t, err)
	for _, out := range []string{string(asJSON), string(asYAML)} {
		for _, secret := range fakeSecrets {
			assert.NotContains(t, out, secret)
		}
		assert.Empty(t, redact.Scan(out))
	}
}

func TestDiscoverDeterministic(t *testing.T) {
	env := fixtureEnv(t)
	first, err := Adapter{}.Discover(context.Background(), env)
	require.NoError(t, err)
	second, err := Adapter{}.Discover(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestDiscoverEmptyHome(t *testing.T) {
	d, err := Adapter{}.Discover(context.Background(), harness.Env{Home: t.TempDir(), Project: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, &harness.Discovery{
		Harness:    "claude-code",
		MCPServers: []harness.MCPServer{},
		Plugins:    []harness.Extension{},
		Skills:     []harness.Extension{},
		Redacted:   []harness.Redaction{},
		Notes:      []string{},
	}, d)
}

func TestDiscoverErrors(t *testing.T) {
	t.Run("no home", func(t *testing.T) {
		_, err := Adapter{}.Discover(context.Background(), harness.Env{})
		require.Error(t, err)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Adapter{}.Discover(ctx, harness.Env{Home: t.TempDir()})
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("corrupt config", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{"), 0o644))
		_, err := Adapter{}.Discover(context.Background(), harness.Env{Home: home})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse")
	})
}

func TestMarketplaceSourceMissingDirectory(t *testing.T) {
	home := t.TempDir()
	var m marketplaceJSON
	m.Source.Source = "directory"
	m.Source.Path = filepath.Join(home, "dev", "gone")
	var notes []string
	got, remote := marketplaceSource(context.Background(), "tool@gone", "gone", map[string]marketplaceJSON{"gone": m},
		harness.Env{Home: home, Project: t.TempDir()}, &notes)
	assert.Empty(t, got)
	assert.Nil(t, remote)
	assert.Equal(t, []string{`plugin tool@gone skipped: the directory ~/dev/gone of local marketplace "gone" does not exist`}, notes)
}

func TestTokenEnvVar(t *testing.T) {
	assert.Equal(t, "MCP_GITHUB_TOKEN", tokenEnvVar("github"))
	assert.Equal(t, "MCP_MY_DOCS_V2_TOKEN", tokenEnvVar("my-docs.v2"))
}

// gitRepo creates a git repository at dir with an initial commit of the
// marketplace file naming marketName, returning a runner for it.
func gitRepo(t *testing.T, dir, marketName string) func(args ...string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755))
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"), []byte(`{"name":"`+marketName+`","plugins":[]}`), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return run
}

// trackRemote records the current HEAD as <remote>/main and points
// <remote>/HEAD at it, as a clone would, without any network access.
func trackRemote(run func(args ...string) string, remote string) {
	run("update-ref", "refs/remotes/"+remote+"/main", "HEAD")
	run("symbolic-ref", "refs/remotes/"+remote+"/HEAD", "refs/remotes/"+remote+"/main")
}

// TestDiscoverGitMarketplaces covers FR-019 and SC-008 with real throwaway
// repositories: local marketplaces outside the copyable roots are proposed
// through their git remote with the remote's state; one without a remote is
// skipped with the reason; without git everything stays skipped.
func TestDiscoverGitMarketplaces(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	home := t.TempDir()
	dev := filepath.Join(home, "dev")
	markets := map[string]string{}
	plugins := map[string]string{}
	addMarket := func(name string) string {
		dir := filepath.Join(dev, name)
		markets[name] = `{"source":{"source":"directory","path":"` + dir + `"}}`
		plugins["tool@"+name] = `[{"scope":"user"}]`
		return dir
	}

	gh := gitRepo(t, addMarket("gh-market"), "gh-market")
	gh("remote", "add", "origin", "https://github.com/acme/gh-market.git")
	trackRemote(gh, "origin")
	require.NoError(t, os.WriteFile(filepath.Join(dev, "gh-market", "README.md"), []byte("x"), 0o644))
	gh("add", ".")
	gh("commit", "-q", "-m", "local change")

	other := gitRepo(t, addMarket("other-market"), "renamed-upstream")
	other("remote", "add", "upstream", "https://deploy:"+"s3cr3tvalue"+"@git.example.com/org/other-market.git")
	trackRemote(other, "upstream")

	dirty := gitRepo(t, addMarket("dirty-market"), "dirty-market")
	dirty("remote", "add", "origin", "git@github.com:acme/dirty-market.git")
	dirty("remote", "add", "backup", "https://git.example.com/backup.git")
	trackRemote(dirty, "origin")
	require.NoError(t, os.WriteFile(filepath.Join(dev, "dirty-market", "new.txt"), []byte("x"), 0o644))

	noref := gitRepo(t, addMarket("noref-market"), "noref-market")
	noref("remote", "add", "origin", "https://gitlab.example.com/team/noref-market")

	gitRepo(t, addMarket("noremote-market"), "noremote-market")

	plain := addMarket("plain-dir")
	require.NoError(t, os.MkdirAll(plain, 0o755))

	writeJSONFile := func(rel string, entries map[string]string) {
		var parts []string
		for k, v := range entries {
			parts = append(parts, `"`+k+`":`+v)
		}
		path := filepath.Join(home, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("{"+strings.Join(parts, ",")+"}"), 0o644))
	}
	writeJSONFile(knownMarketplacesFile, markets)
	require.NoError(t, os.WriteFile(filepath.Join(home, installedPluginsFile), []byte(`{"version":2,"plugins":{`+func() string {
		var parts []string
		for k, v := range plugins {
			parts = append(parts, `"`+k+`":`+v)
		}
		return strings.Join(parts, ",")
	}()+`}}`), 0o644))

	env := harness.Env{Home: home, Project: t.TempDir(), Git: harness.ExecGit{}}
	d, err := Adapter{}.Discover(context.Background(), env)
	require.NoError(t, err)
	byName := map[string]harness.Extension{}
	for _, p := range d.Plugins {
		byName[p.Name] = p
	}

	assert.Equal(t, "acme/gh-market", byName["tool@gh-market"].Source)
	assert.Equal(t, &harness.RemoteInfo{URL: "https://github.com/acme/gh-market.git", UnpushedCommits: 1, NameMatch: harness.NameMatch}, byName["tool@gh-market"].Remote)

	assert.Equal(t, "https://git.example.com/org/other-market.git", byName["tool@other-market"].Source, "credentials stripped")
	assert.Equal(t, &harness.RemoteInfo{URL: "https://git.example.com/org/other-market.git", NameMatch: harness.NameMismatch}, byName["tool@other-market"].Remote)

	assert.Equal(t, "acme/dirty-market", byName["tool@dirty-market"].Source, "origin wins over other remotes")
	assert.Equal(t, &harness.RemoteInfo{URL: "git@github.com:acme/dirty-market.git", UncommittedChanges: true, NameMatch: harness.NameMatch}, byName["tool@dirty-market"].Remote)

	assert.Equal(t, "https://gitlab.example.com/team/noref-market", byName["tool@noref-market"].Source)
	assert.Equal(t, &harness.RemoteInfo{URL: "https://gitlab.example.com/team/noref-market", NameMatch: harness.NameUnknown}, byName["tool@noref-market"].Remote)

	assert.NotContains(t, byName, "tool@noremote-market")
	assert.NotContains(t, byName, "tool@plain-dir")
	notes := strings.Join(d.Notes, "\n")
	assert.Contains(t, notes, `plugin tool@noremote-market skipped: local marketplace "noremote-market" cannot be copied into the image and has no git remote`)
	assert.Contains(t, notes, `plugin tool@plain-dir skipped: local marketplace "plain-dir" cannot be copied into the image and has no git remote`)
	assert.Contains(t, notes, `plugin tool@other-market: credentials removed from the git remote URL of local marketplace "other-market"`)
	for _, n := range d.Notes {
		assert.NotContains(t, n, "skipped: local marketplace \"gh-market\"")
	}
	asJSON, err := json.Marshal(d)
	require.NoError(t, err)
	assert.NotContains(t, string(asJSON), "s3cr3tvalue", "no credential leaves discovery")

	// Without git, every local marketplace outside the roots is skipped as
	// before.
	env.Git = nil
	d, err = Adapter{}.Discover(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, d.Plugins)
}

func TestGithubRepo(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/tools.git":     "acme/tools",
		"https://github.com/acme/tools":         "acme/tools",
		"git@github.com:acme/tools.git":         "acme/tools",
		"ssh://git@github.com/acme/tools.git":   "acme/tools",
		"https://gitlab.com/acme/tools.git":     "",
		"https://github.com/acme/tools/sub/dir": "",
	} {
		got, ok := githubRepo(url)
		assert.Equal(t, want, got, url)
		assert.Equal(t, want != "", ok, url)
	}
}

func TestPortability(t *testing.T) {
	for source, want := range map[string]string{
		"owner/repo":                          "",
		"https://git.example.com/org/x.git":   "",
		"git@github.com:acme/x.git":           "",
		"./tools/market":                      "",
		".claude/skills/deploy":               "",
		"~/.claude/skills/writing":            "source under ~/.claude/skills is read on the machine that runs the plan",
		"~/.claude/plugins/marketplaces/team": "source under ~/.claude/plugins is read on the machine that runs the plan",
		"~/dev/market":                        "source under the home directory is read on the machine that runs the plan",
		"/opt/shared/market":                  "source outside the project is read on the machine that runs the plan",
	} {
		portable, reason := portability(source)
		assert.Equal(t, want == "", portable, source)
		assert.Equal(t, want, reason, source)
	}
}
