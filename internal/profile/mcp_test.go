package profile

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/manifest"
)

func referencedSet(profiles ...*Profile) Set {
	set := Set{}
	for _, p := range profiles {
		set[p.ID] = Entry{Profile: p, Tier: TierUpstream, SourceFile: p.ID + ".yaml"}
	}
	return set
}

func TestMCPCredentialVar(t *testing.T) {
	tests := []struct {
		key, value string
		want       string
		ok         bool
	}{
		{"DOCS_TOKEN", "${SANDCAST_SECRET_2}", "DOCS_TOKEN", true},
		{"GITHUB_TOKEN", "${GH_PAT}", "GH_PAT", true},
		{"GITHUB_TOKEN", "$GH_PAT", "GH_PAT", true},
		{"GITHUB_TOKEN", "${GH_PAT:-}", "GH_PAT", true},
		{"LOG_LEVEL", "debug", "", false},
		{"X", "Bearer ${SANDCAST_SECRET_1}", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			got, ok := MCPCredentialVar(tt.key, tt.value)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMCPProfileID(t *testing.T) {
	assert.Equal(t, "mcp-docs", MCPProfileID("docs"))
	assert.Equal(t, "mcp-my-docs-server", MCPProfileID("My_Docs..Server"))
	assert.Equal(t, "mcp-", MCPProfileID("__"))
}

func TestMCPProfiles(t *testing.T) {
	agent := []string{"/opt/mise/installs/claude/2.1.0/bin/claude"}
	github := &Profile{ID: "github", DisplayName: "GitHub", Endpoints: []Endpoint{
		{Host: "api.github.com", Port: 443},
		{Host: "*.githubusercontent.com", Port: 443},
	}}
	wide := &Profile{ID: "wide", DisplayName: "Wide", Endpoints: []Endpoint{
		{Host: "**.corp.example", Ports: []uint32{443, 8443}},
	}}
	referenced := referencedSet(github, wide)

	servers := []manifest.MCPServer{
		{Name: "zeta-docs", URL: "https://mcp.docs.example.com/mcp", Env: map[string]string{
			"MCP_ZETA_DOCS_TOKEN": "${SANDCAST_SECRET_1}",
			"LOG_LEVEL":           "debug",
		}},
		{Name: "alpha", URL: "http://alpha.example.org:8080/sse"},
		{Name: "gh-ref", URL: "https://mcp.ref.example.net/", Env: map[string]string{"GITHUB_TOKEN": "${GH_PAT}"}},
		{Name: "local-cmd", Command: []string{"npx", "-y", "server"}},
		{Name: "loop", URL: "http://localhost:3000/mcp"},
		{Name: "loop6", URL: "http://[::1]:3000/mcp"},
		{Name: "covered", URL: "https://api.github.com/mcp"},
		{Name: "covered-wild", URL: "https://raw.githubusercontent.com/mcp"},
		{Name: "covered-deep", URL: "https://a.b.corp.example:8443/mcp"},
		{Name: "port-miss", URL: "https://a.b.corp.example:9443/mcp"},
		{Name: "userinfo", URL: "https://user:${SANDCAST_SECRET_3}@secure.example.com/mcp"},
	}

	profiles, notices, err := MCPProfiles(servers, referenced, agent)
	require.NoError(t, err)

	ids := make([]string, 0, len(profiles))
	for _, p := range profiles {
		ids = append(ids, p.ID)
		assert.Empty(t, Lint(p, "test"), p.ID)
		assert.Equal(t, "other", p.Category)
		assert.Equal(t, Binaries(agent), p.Binaries)
	}
	assert.Equal(t, []string{"mcp-alpha", "mcp-gh-ref", "mcp-port-miss", "mcp-userinfo", "mcp-zeta-docs"}, ids)

	byID := map[string]*Profile{}
	for _, p := range profiles {
		byID[p.ID] = p
	}

	zeta := byID["mcp-zeta-docs"]
	assert.Equal(t, "MCP server zeta-docs", zeta.DisplayName)
	assert.Equal(t, []Endpoint{{Host: "mcp.docs.example.com", Port: 443, Protocol: "rest", Access: "full", Enforcement: "enforce"}}, zeta.Endpoints)
	require.Len(t, zeta.Credentials, 1)
	assert.Equal(t, Credential{
		Name:        MCPCredentialName,
		Description: "Credential of MCP server zeta-docs",
		EnvVars:     []string{"MCP_ZETA_DOCS_TOKEN"},
		Required:    true,
		AuthStyle:   "bearer",
		HeaderName:  "authorization",
	}, zeta.Credentials[0])
	assert.Equal(t, []string{MCPCredentialName}, zeta.Discovery.Credentials)

	alpha := byID["mcp-alpha"]
	assert.Equal(t, []Endpoint{{Host: "alpha.example.org", Port: 8080}}, alpha.Endpoints, "no credential: plain L4 endpoint")
	assert.Empty(t, alpha.Credentials)

	assert.Equal(t, []string{"GH_PAT"}, byID["mcp-gh-ref"].Credentials[0].EnvVars)
	assert.Equal(t, uint32(9443), byID["mcp-port-miss"].Endpoints[0].Port)
	assert.Equal(t, "secure.example.com", byID["mcp-userinfo"].Endpoints[0].Host)
	assert.Empty(t, byID["mcp-userinfo"].Credentials, "a url placeholder has no env key to declare")

	joined := strings.Join(notices, "\n")
	for _, want := range []string{
		"MCP server covered: api.github.com:443 is covered by profile github",
		"MCP server covered-deep: a.b.corp.example:8443 is covered by profile wide",
		"MCP server covered-wild: raw.githubusercontent.com:443 is covered by profile github",
		"MCP server local-cmd runs as a local command",
		"MCP server loop is served on loopback",
		"MCP server loop6 is served on loopback",
	} {
		assert.Contains(t, joined, want)
	}
	assert.Len(t, notices, 6)

	for _, p := range profiles {
		data, err := Marshal(p)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "SANDCAST_SECRET", "a generated profile never carries a value or placeholder")
	}
}

// TestMCPProfilesCoveredByAgentProfile uses the vendored claude-code
// profile: a server on one of its hosts needs no profile of its own.
func TestMCPProfilesCoveredByAgentProfile(t *testing.T) {
	claude, err := LoadFile(filepath.Join(upstreamDir, "claude-code.yaml"))
	require.NoError(t, err)
	servers := []manifest.MCPServer{
		{Name: "anthropic-tools", URL: "https://api.anthropic.com/v1/mcp", Env: map[string]string{"MCP_ANTHROPIC_TOOLS_TOKEN": "${SANDCAST_SECRET_1}"}},
		{Name: "elsewhere", URL: "https://mcp.example.com/mcp"},
	}
	profiles, notices, err := MCPProfiles(servers, referencedSet(claude), []string{"/usr/local/bin/claude"})
	require.NoError(t, err)

	require.Len(t, profiles, 1)
	assert.Equal(t, "mcp-elsewhere", profiles[0].ID, "only the uncovered server gets a profile")
	assert.Equal(t, []string{
		"MCP server anthropic-tools: api.anthropic.com:443 is covered by profile claude-code; no profile generated",
	}, notices)
}

func TestMCPProfilesDeterministic(t *testing.T) {
	servers := []manifest.MCPServer{
		{Name: "b", URL: "https://b.example.com", Env: map[string]string{"B_TOKEN": "${SANDCAST_SECRET_1}", "A_TOKEN": "${SANDCAST_SECRET_2}"}},
		{Name: "a", URL: "https://a.example.com"},
	}
	first, n1, err := MCPProfiles(servers, Set{}, nil)
	require.NoError(t, err)
	servers[0], servers[1] = servers[1], servers[0]
	second, n2, err := MCPProfiles(servers, Set{}, nil)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, n1, n2)
	assert.Equal(t, []string{"A_TOKEN", "B_TOKEN"}, first[1].Credentials[0].EnvVars)
	assert.Empty(t, first[0].Binaries)
}

func TestMCPProfilesErrors(t *testing.T) {
	tests := []struct {
		name       string
		servers    []manifest.MCPServer
		referenced Set
		want       string
	}{
		{
			name:    "unsupported scheme",
			servers: []manifest.MCPServer{{Name: "ws", URL: "wss://ws.example.com"}},
			want:    `MCP server ws: url scheme "wss" is not http or https`,
		},
		{
			name:    "no scheme",
			servers: []manifest.MCPServer{{Name: "bare", URL: "mcp.example.com"}},
			want:    "MCP server bare: url has no scheme",
		},
		{
			name:    "bad port",
			servers: []manifest.MCPServer{{Name: "port", URL: "https://mcp.example.com:0/"}},
			want:    `url port "0" is not between 1 and 65535`,
		},
		{
			name:    "name without id characters",
			servers: []manifest.MCPServer{{Name: "__", URL: "https://mcp.example.com"}},
			want:    "name gives no valid profile id",
		},
		{
			name: "two servers map to one id",
			servers: []manifest.MCPServer{
				{Name: "my_docs", URL: "https://one.example.com"},
				{Name: "my-docs", URL: "https://two.example.com"},
			},
			want: "both map to profile id mcp-my-docs",
		},
		{
			name:       "referenced id does not cover",
			servers:    []manifest.MCPServer{{Name: "docs", URL: "https://docs.example.com"}},
			referenced: referencedSet(&Profile{ID: "mcp-docs", DisplayName: "x", Endpoints: []Endpoint{{Host: "other.example.com", Port: 443}}}),
			want:       "profile mcp-docs is already referenced but does not cover docs.example.com:443",
		},
		{
			name: "lint failure",
			servers: []manifest.MCPServer{{Name: "meta", URL: "http://metadata.google.internal/mcp",
				Env: map[string]string{"TOKEN": "${SANDCAST_SECRET_1}"}}},
			want: "provider profile lint failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := MCPProfiles(tt.servers, tt.referenced, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestHostCovers(t *testing.T) {
	tests := []struct {
		pattern, host string
		want          bool
	}{
		{"api.example.com", "api.example.com", true},
		{"API.Example.com.", "api.example.com", true},
		{"api.example.com", "x.api.example.com", false},
		{"*.example.com", "api.example.com", true},
		{"*.example.com", "a.b.example.com", false},
		{"*.example.com", "example.com", false},
		{"**.example.com", "a.b.example.com", true},
		{"**.example.com", "example.com", false},
		{"api-*.example.com", "api-eu.example.com", true},
		{"api.*.example.com", "api.eu.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" "+tt.host, func(t *testing.T) {
			assert.Equal(t, tt.want, hostCovers(tt.pattern, tt.host))
		})
	}
}
