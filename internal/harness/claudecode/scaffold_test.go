package claudecode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/harness"
	"github.com/rhuss/sandcast/internal/harness/claudecode"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/profile"
	"github.com/rhuss/sandcast/internal/redact"
	"github.com/rhuss/sandcast/internal/testutil"
)

var sandboxUser = plan.FinalUser{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/sandbox"}

// host is a temporary home and project with the sources goldenHarness
// names. real is the symlink-free temporary root.
type host struct {
	home, project, real string
}

func newHost(t *testing.T) host {
	t.Helper()
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	h := host{home: filepath.Join(root, "home"), project: filepath.Join(root, "project"), real: real}
	for _, dir := range []string{
		filepath.Join(h.project, ".claude", "skills", "review"),
		filepath.Join(h.home, ".claude", "skills", "writing"),
		filepath.Join(h.project, "tools", "dev-marketplace", ".claude-plugin"),
		filepath.Join(h.home, ".ssh"),
		filepath.Join(root, "outside", "secrets"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	return h
}

func (h host) input(ref manifest.HarnessRef) harness.ScaffoldInput {
	return harness.ScaffoldInput{Harness: ref, User: sandboxUser, Project: h.project, Home: h.home}
}

// goldenHarness exercises every scaffold feature.
func goldenHarness() manifest.HarnessRef {
	return manifest.HarnessRef{
		Name: "claude-code",
		MCPServers: []manifest.MCPServer{
			{Name: "db", Command: []string{"db-mcp", "--api-key", "${SANDCAST_SECRET_3}"}, Env: map[string]string{
				"DB_PASSWORD": "${SANDCAST_SECRET_5}",
				"LOG_LEVEL":   "info",
			}},
			{Name: "docs", URL: "https://docs.example.com/sse", Env: map[string]string{"DOCS_TOKEN": "${DOCS_TOKEN}"}},
			{Name: "github", URL: "https://api.githubcopilot.com/mcp/", Env: map[string]string{"MCP_GITHUB_TOKEN": "${SANDCAST_SECRET_6}"}},
			{Name: "search", URL: "https://search.example.com/mcp?apikey=${SANDCAST_SECRET_7}&mode=ro"},
		},
		Plugins: []manifest.Extension{
			{Name: "github@claude-plugins-official", Source: "anthropics/claude-plugins-official"},
			{Name: "review-kit@team-tools", Source: "example-org/team-tools"},
			{Name: "legacy@team-tools", Source: "example-org/team-tools"},
			{Name: "local-tool@dev-marketplace", Source: "./tools/dev-marketplace"},
			{Name: "pinned@ref-market", Source: "example-org/ref-market#v1"},
		},
		Skills: []manifest.Extension{
			{Name: "review", Source: ".claude/skills/review"},
			{Name: "writing", Source: "~/.claude/skills/writing"},
		},
	}
}

func TestScaffoldGolden(t *testing.T) {
	h := newHost(t)
	sc, err := claudecode.Adapter{}.Scaffold(h.input(goldenHarness()))
	require.NoError(t, err)

	testutil.Golden(t, filepath.Join("testdata", "70-harness.golden"), []byte(sc.Fragment))

	assert.Equal(t, map[string]string{
		"harness/claude-code/skills/review":                filepath.Join(h.real, "project", ".claude", "skills", "review"),
		"harness/claude-code/skills/writing":               filepath.Join(h.real, "home", ".claude", "skills", "writing"),
		"harness/claude-code/marketplaces/dev-marketplace": filepath.Join(h.real, "project", "tools", "dev-marketplace"),
	}, sc.Files, "sources are resolved to their symlink-free paths")
	assert.Equal(t, []plan.EnvVar{{Name: "CLAUDE_CONFIG_DIR", Value: "/opt/sandcast/claude-code"}}, sc.Env)
	assert.Equal(t, []plan.Dir{{Path: "/opt/sandcast/claude-code", Owner: "user"}}, sc.Dirs)
	assert.Equal(t, []string{"/opt/sandcast/claude-code"}, sc.ReadWrite)
	assert.Equal(t, []string{"/opt/sandcast/claude-code-marketplaces"}, sc.ReadOnly)

	// The section ends as root by design, so the next section starts as
	// root; that is the only finding the fragment validator may report.
	err = build.ValidateFragment(sc.Fragment)
	require.Error(t, err)
	problems := strings.Split(err.Error(), "\n")[1:]
	require.Len(t, problems, 1, err.Error())
	assert.Contains(t, problems[0], "ends as USER root")
	assert.NotRegexp(t, regexp.MustCompile(`(?m)^FROM\b`), sc.Fragment)
}

func TestScaffoldOrder(t *testing.T) {
	sc, err := claudecode.Adapter{}.Scaffold(newHost(t).input(goldenHarness()))
	require.NoError(t, err)
	f := sc.Fragment
	pos := func(s string) int {
		t.Helper()
		i := strings.Index(f, s)
		require.GreaterOrEqual(t, i, 0, "missing %q", s)
		return i
	}
	assert.Less(t, pos("RUN install -d -o sandbox -g sandbox /opt/sandcast/claude-code"), pos("USER sandbox"),
		"the configuration directory belongs to the user before anything runs as the user")
	assert.Less(t, pos("RUN chown -R sandbox:sandbox /opt/sandcast/claude-code/skills"), pos("USER sandbox"))
	assert.Less(t, pos("claude plugin marketplace add anthropics/claude-plugins-official"), pos("claude plugin install"))
	assert.Less(t, pos("claude plugin marketplace add example-org/team-tools"), pos("claude plugin install"))
	assert.Equal(t, 1, strings.Count(f, "marketplace add example-org/team-tools"), "a marketplace is added once")
	assert.Equal(t, 5, strings.Count(f, "claude plugin install "))
	assert.True(t, strings.HasSuffix(f, "USER root\n"))
}

func TestScaffoldMCPConfig(t *testing.T) {
	sc, err := claudecode.Adapter{}.Scaffold(newHost(t).input(goldenHarness()))
	require.NoError(t, err)

	var cfg struct {
		HasCompletedOnboarding bool `json:"hasCompletedOnboarding"`
		MCPServers             map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(heredoc(t, sc.Fragment, "/tmp/sandcast-claude.json")), &cfg))
	assert.True(t, cfg.HasCompletedOnboarding)

	db := cfg.MCPServers["db"]
	assert.Equal(t, "stdio", db.Type)
	assert.Equal(t, "db-mcp", db.Command)
	assert.Equal(t, []string{"--api-key", "${SANDCAST_SECRET_3}"}, db.Args)
	assert.Equal(t, map[string]string{"DB_PASSWORD": "${DB_PASSWORD}", "LOG_LEVEL": "info"}, db.Env)

	assert.Equal(t, "sse", cfg.MCPServers["docs"].Type)
	assert.Equal(t, map[string]string{"Authorization": "Bearer ${DOCS_TOKEN}"}, cfg.MCPServers["docs"].Headers)

	gh := cfg.MCPServers["github"]
	assert.Equal(t, "http", gh.Type)
	assert.Equal(t, map[string]string{"Authorization": "Bearer ${MCP_GITHUB_TOKEN}"}, gh.Headers)

	assert.Equal(t, "https://search.example.com/mcp?apikey=${SANDCAST_SECRET_7}&mode=ro", cfg.MCPServers["search"].URL)
	assert.Empty(t, cfg.MCPServers["search"].Headers)

	// The bearer variable the configuration reads is the one the generated
	// profile declares.
	profiles, _, err := profile.MCPProfiles(goldenHarness().MCPServers, profile.Set{}, []string{"/usr/local/bin/claude"})
	require.NoError(t, err)
	for _, p := range profiles {
		if p.ID == "mcp-github" {
			assert.Equal(t, []string{"MCP_GITHUB_TOKEN"}, p.Credentials[0].EnvVars)
		}
	}

	var settings struct {
		Env map[string]string `json:"env"`
	}
	require.NoError(t, json.Unmarshal([]byte(heredoc(t, sc.Fragment, "/tmp/sandcast-settings.json")), &settings))
	assert.Equal(t, map[string]string{
		"DISABLE_AUTOUPDATER":     "1",
		"DISABLE_ERROR_REPORTING": "1",
		"DISABLE_TELEMETRY":       "1",
	}, settings.Env)

	assert.Empty(t, redact.Scan(sc.Fragment), "the fragment carries references, never values")
}

// heredoc returns the body of the "cat > target <<'SANDCAST_JSON'" heredoc.
func heredoc(t *testing.T, fragment, target string) string {
	t.Helper()
	_, rest, ok := strings.Cut(fragment, "cat > "+target+" <<'SANDCAST_JSON'\n")
	require.True(t, ok, "no heredoc for %s", target)
	body, _, ok := strings.Cut(rest, "\nSANDCAST_JSON\n")
	require.True(t, ok)
	return body
}

func TestScaffoldMinimal(t *testing.T) {
	sc, err := claudecode.Adapter{}.Scaffold(harness.ScaffoldInput{
		Harness: manifest.HarnessRef{Name: "claude-code"},
		User:    plan.FinalUser{Name: "dev", Group: "dev", Home: "/home/dev"},
	})
	require.NoError(t, err)
	assert.NotContains(t, sc.Fragment, "COPY")
	assert.NotContains(t, sc.Fragment, "claude plugin")
	assert.Nil(t, sc.Files)
	assert.Nil(t, sc.ReadOnly)
	assert.Equal(t, "{\n  \"hasCompletedOnboarding\": true\n}", heredoc(t, sc.Fragment, "/tmp/sandcast-claude.json"))
	assert.Contains(t, sc.Fragment, "RUN install -d -o dev -g dev /opt/sandcast/claude-code")
}

func TestScaffoldNumericUser(t *testing.T) {
	// A container-target base image with USER 1001 and no passwd entry.
	sc, err := claudecode.Adapter{}.Scaffold(harness.ScaffoldInput{
		Harness: manifest.HarnessRef{Name: "claude-code"},
		User:    plan.FinalUser{Name: "1001", UID: 1001, Group: "0", Home: "/home/app"},
	})
	require.NoError(t, err)
	assert.Contains(t, sc.Fragment, "RUN install -d -o 1001 -g 0 /opt/sandcast/claude-code")

	_, err = claudecode.Adapter{}.Scaffold(harness.ScaffoldInput{
		Harness: manifest.HarnessRef{Name: "claude-code"},
		User:    plan.FinalUser{Name: "1001x;rm", Group: "0", Home: "/home/app"},
	})
	assert.ErrorContains(t, err, "invalid final user")
	_, err = claudecode.Adapter{}.Scaffold(harness.ScaffoldInput{
		Harness: manifest.HarnessRef{Name: "claude-code"},
		User:    plan.FinalUser{Name: "1001", Group: "0;rm", Home: "/home/app"},
	})
	assert.ErrorContains(t, err, "invalid final user", "the group is checked too")
}

func TestScaffoldDeterministic(t *testing.T) {
	in := newHost(t).input(goldenHarness())
	first, err := claudecode.Adapter{}.Scaffold(in)
	require.NoError(t, err)
	for range 5 {
		again, err := claudecode.Adapter{}.Scaffold(in)
		require.NoError(t, err)
		assert.Equal(t, first, again)
	}
}

// TestScaffoldSymlinkInsideAllowedRoots accepts a skill that links to
// another allowed directory and copies the link target.
func TestScaffoldSymlinkInsideAllowedRoots(t *testing.T) {
	h := newHost(t)
	link := filepath.Join(h.home, ".claude", "skills", "linked")
	require.NoError(t, os.Symlink(filepath.Join(h.project, ".claude", "skills", "review"), link))
	sc, err := claudecode.Adapter{}.Scaffold(h.input(manifest.HarnessRef{
		Name:   "claude-code",
		Skills: []manifest.Extension{{Name: "linked", Source: "~/.claude/skills/linked"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(h.real, "project", ".claude", "skills", "review"), sc.Files["harness/claude-code/skills/linked"])
}

func TestScaffoldErrors(t *testing.T) {
	fakeGitHub := "ghp_" + strings.Repeat("x", 36)
	skill := func(source string) []manifest.Extension {
		return []manifest.Extension{{Name: "s", Source: source}}
	}
	tests := []struct {
		name   string
		mutate func(t *testing.T, in *harness.ScaffoldInput, h host)
		want   string
	}{
		{
			name:   "no user",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) { in.User = plan.FinalUser{} },
			want:   "invalid final user",
		},
		{
			name: "plugin name with shell characters",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x; rm -rf /", Source: "a/b"}}
			},
			want: "want name or name@marketplace",
		},
		{
			name: "marketplace source with a newline",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "a/b\nRUN evil"}}
			},
			want: "control characters",
		},
		{
			name: "marketplace source that reads as an option",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "--scope=project"}}
			},
			want: "marketplace source must not start with -",
		},
		{
			name: "marketplace URL with a password",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "https://deploy:pw@git.example.com/o/r.git"}}
			},
			want: "carries a credential or placeholder",
		},
		{
			name: "marketplace URL with a token as user",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "https://" + fakeGitHub + "@github.com/o/r"}}
			},
			want: "carries a credential or placeholder",
		},
		{
			name: "marketplace URL with a placeholder",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "https://git.example.com/m.git?private_token=${SANDCAST_SECRET_1}"}}
			},
			want: "carries a credential or placeholder",
		},
		{
			name: "local marketplace file",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "/opt/m/marketplace.json"}}
			},
			want: "is a file",
		},
		{
			name: "local marketplace linked out of the project",
			mutate: func(t *testing.T, in *harness.ScaffoldInput, h host) {
				require.NoError(t, os.Symlink(filepath.Join(filepath.Dir(h.project), "outside", "secrets"), filepath.Join(h.project, "tools", "link")))
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "./tools/link"}}
			},
			want: "outside the project",
		},
		{
			name: "skill name with a slash",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Skills = []manifest.Extension{{Name: "../escape", Source: "/tmp/x"}}
			},
			want: "single directory name",
		},
		{
			name: "duplicate skill",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Skills = []manifest.Extension{
					{Name: "a", Source: ".claude/skills/review"},
					{Name: "a", Source: ".claude/skills/review"},
				}
			},
			want: "listed twice",
		},
		{
			name: "skill linked out of the project",
			mutate: func(t *testing.T, in *harness.ScaffoldInput, h host) {
				require.NoError(t, os.Symlink(filepath.Join(filepath.Dir(h.project), "outside", "secrets"),
					filepath.Join(h.project, ".claude", "skills", "evil")))
				in.Harness.Skills = skill(".claude/skills/evil")
			},
			want: "resolves to",
		},
		{
			name:   "skill from ~/.ssh",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) { in.Harness.Skills = skill("~/.ssh") },
			want:   "outside the project, ~/.claude/skills and ~/.claude/plugins",
		},
		{
			name:   "skill from ~/.claude itself",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) { in.Harness.Skills = skill("~/.claude") },
			want:   "outside the project",
		},
		{
			name: "absolute path outside",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, h host) {
				in.Harness.Skills = skill(filepath.Join(filepath.Dir(h.project), "outside", "secrets"))
			},
			want: "outside the project",
		},
		{
			name: "relative path escaping the project",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Skills = skill("../outside/secrets")
			},
			want: "outside the project",
		},
		{
			name: "project at the home directory",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Project = in.Home
				in.Harness.Skills = skill("./.ssh")
			},
			want: "outside the project",
		},
		{
			name: "missing skill",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Skills = skill(".claude/skills/missing")
			},
			want: "no such file",
		},
		{
			name: "relative skill without project",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Project = ""
				in.Harness.Skills = skill(".claude/skills/a")
			},
			want: "needs the project directory",
		},
		{
			name: "home skill without home",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Home = ""
				in.Harness.Skills = skill("~/.claude/skills/a")
			},
			want: "needs the home directory",
		},
		{
			name: "literal credential in env",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "db", Command: []string{"db-mcp"},
					Env: map[string]string{"LOG_LEVEL": "info", "API_KEY": "plain"}}}
			},
			want: "env API_KEY holds a literal credential; replace it with a ${SANDCAST_SECRET_N} placeholder",
		},
		{
			name: "literal bearer credential on a url server",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "web", URL: "https://x.example.com",
					Env: map[string]string{"MCP_WEB_TOKEN": "plain"}}}
			},
			want: "env MCP_WEB_TOKEN holds a literal credential",
		},
		{
			name: "credential inside a longer env value",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "cli", Command: []string{"srv"},
					Env: map[string]string{"AUTH": "Bearer " + fakeGitHub}}}
			},
			want: "env AUTH holds a literal credential",
		},
		{
			name: "url path credential in an env value",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "cli", Command: []string{"srv"},
					Env: map[string]string{"WEBHOOK": "https://hooks.example.com/services/Zm9vYmFyLTEy" + "MzQtNTY3OC05YWJj"}}}
			},
			want: "env WEBHOOK holds a literal credential",
		},
		{
			name: "credential shape in an argument",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "cli", Command: []string{"srv", "--header", fakeGitHub}}}
			},
			want: "command[2] holds a literal credential (github-token",
		},
		{
			name: "short literal after a secret flag",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "cli", Command: []string{"srv", "--api-key", "plain"}}}
			},
			want: "command[2] holds a literal credential",
		},
		{
			name: "secret query value on a url server",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "web", URL: "https://x.example.com/mcp?access_token=plain"}}
			},
			want: "url holds a literal credential",
		},
		{
			name: "credential in a url path segment",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "zap", URL: "https://mcp.zapier.com/api/mcp/s/Zm9vYmFyLTEy" + "MzQtNTY3OC05YWJj/mcp"}}
			},
			want: "url holds a literal credential (url-path-secret)",
		},
		{
			name: "opaque token as the url user of a marketplace",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "https://0123456789abcdef0123456789abcdef01234567@gitea.example.com/org/market.git"}}
			},
			want: "carries a credential or placeholder",
		},
		{
			name: "secret query value on a marketplace",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.Plugins = []manifest.Extension{{Name: "x@y", Source: "https://git.example.com/m.git?access_token=plain"}}
			},
			want: "carries a credential or placeholder",
		},
		{
			name: "literal value under a plain env key on a url server",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "web", URL: "https://x.example.com",
					Env: map[string]string{"MCP_WEB": "plain"}}}
			},
			want: "env MCP_WEB holds a literal credential",
		},
		{
			name: "credential shape that only the final scan sees",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: fakeGitHub, Command: []string{"x"}}}
			},
			want: "MCP configuration carries a credential",
		},
		{
			name: "url server with two credentials",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "web", URL: "https://x.example.com",
					Env: map[string]string{"A": "${SANDCAST_SECRET_1}", "B": "${SANDCAST_SECRET_2}"}}}
			},
			want: "one bearer credential",
		},
		{
			name: "duplicate MCP server",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "a", Command: []string{"a"}}, {Name: "a", Command: []string{"b"}}}
			},
			want: "listed twice",
		},
		{
			name: "MCP server without command or url",
			mutate: func(_ *testing.T, in *harness.ScaffoldInput, _ host) {
				in.Harness.MCPServers = []manifest.MCPServer{{Name: "a"}}
			},
			want: "needs a command or a url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHost(t)
			in := h.input(manifest.HarnessRef{Name: "claude-code"})
			tt.mutate(t, &in, h)
			_, err := claudecode.Adapter{}.Scaffold(in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), "plain", "an error never repeats a credential value")
		})
	}
}

// TestScaffoldManifestValuesStayData renders manifest values with shell
// and heredoc metacharacters: a marketplace source is one quoted word, and
// MCP server values stay inside the JSON heredoc, which escapes newlines,
// so no value ends the heredoc or starts a Containerfile instruction.
func TestScaffoldManifestValuesStayData(t *testing.T) {
	evilArg := "x\nSANDCAST_JSON\nSANDCAST_EOF\nRUN touch /pwned"
	sc, err := claudecode.Adapter{}.Scaffold(newHost(t).input(manifest.HarnessRef{
		Name: "claude-code",
		MCPServers: []manifest.MCPServer{{Name: "q'uote\"s", Command: []string{"srv", evilArg, "'; touch /pwned; '"},
			Env: map[string]string{"MODE": "$(touch /pwned)"}}},
		Plugins: []manifest.Extension{{Name: "kit@m", Source: "example-org/m#v1; touch /pwned"}},
	}))
	require.NoError(t, err)
	for _, line := range strings.Split(sc.Fragment, "\n") {
		assert.NotEqual(t, "RUN touch /pwned", line)
	}
	assert.Contains(t, sc.Fragment, "RUN claude plugin marketplace add 'example-org/m#v1; touch /pwned'\n")

	var cfg struct {
		MCPServers map[string]struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(heredoc(t, sc.Fragment, "/tmp/sandcast-claude.json")), &cfg))
	assert.Equal(t, []string{evilArg, "'; touch /pwned; '"}, cfg.MCPServers["q'uote\"s"].Args)
	assert.Equal(t, "$(touch /pwned)", cfg.MCPServers["q'uote\"s"].Env["MODE"])
	assert.Contains(t, sc.Fragment, "RUN <<'SANDCAST_EOF'\n", "the outer heredoc is quoted, so the shell expands nothing in it")
}

// TestScaffoldMissingHostSource checks that a skill or local marketplace
// outside the project that is missing on this host, as in CI or on another
// machine, fails with the remedy: copy it into the project (FR-028a).
func TestScaffoldMissingHostSource(t *testing.T) {
	tests := []struct {
		name string
		ref  manifest.HarnessRef
		want []string
	}{
		{
			name: "user-scope skill",
			ref:  manifest.HarnessRef{Name: "claude-code", Skills: []manifest.Extension{{Name: "gone", Source: "~/.claude/skills/gone"}}},
			want: []string{
				"claude-code skill gone: source ~/.claude/skills/gone does not exist on this host",
				"exists only on the machine that captured the manifest",
				"copy the skill directory into the project as .claude/skills/gone, commit it, and set the skill source in the manifest to .claude/skills/gone",
			},
		},
		{
			name: "user-scope local marketplace",
			ref: manifest.HarnessRef{Name: "claude-code", Plugins: []manifest.Extension{
				{Name: "tool@gone-market", Source: "~/.claude/plugins/marketplaces/gone-market"},
			}},
			want: []string{
				"claude-code plugin tool@gone-market: source ~/.claude/plugins/marketplaces/gone-market does not exist on this host",
				"exists only on the machine that captured the manifest",
				"copy the marketplace directory into the project, commit it, and set the plugin source to its ./ path",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHost(t)
			_, err := claudecode.Adapter{}.Scaffold(h.input(tt.ref))
			require.Error(t, err)
			require.ErrorIs(t, err, harness.ErrInvalidManifest)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}

	// A home without ~/.claude/skills at all, as on a fresh CI runner.
	h := newHost(t)
	require.NoError(t, os.RemoveAll(filepath.Join(h.home, ".claude")))
	_, err := claudecode.Adapter{}.Scaffold(h.input(manifest.HarnessRef{Name: "claude-code",
		Skills: []manifest.Extension{{Name: "writing", Source: "~/.claude/skills/writing"}}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "copy the skill directory into the project as .claude/skills/writing")
}

func TestScaffoldOfficialMarketplaceByName(t *testing.T) {
	sc, err := claudecode.Adapter{}.Scaffold(harness.ScaffoldInput{
		Harness: manifest.HarnessRef{Name: "claude-code", Plugins: []manifest.Extension{{Name: "clangd-lsp@claude-plugins-official", Source: claudecode.OfficialMarketplace}}},
		User:    plan.FinalUser{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/sandbox"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(sc.Fragment, "plugin marketplace add"), "the official marketplace is added once, by its source")
	assert.Contains(t, sc.Fragment, "marketplace add anthropics/claude-plugins-official")
}
