package harness

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/redact"
)

// Obviously fake credentials, assembled at run time: a token in the GitHub
// classic shape, a base64 key as hosted MCP servers put into the url path,
// and a UUID access key.
var (
	fakeGitHub   = "ghp_" + strings.Repeat("x", 36)
	fakePathKey  = "Zm9vYmFyLTEyMzQtNTY3OC05" + "YWJjZGVm"
	fakePathUUID = "0f1e2d3c-4b5a-4968-8776-" + "655443322110"
	// fakeMixedKey is a random-looking mixed-case key, fakeEncodedKey a
	// base64 key with percent-encoded padding, and fakeCommitSHA a 40-hex
	// git object name.
	fakeMixedKey   = "k8Fj2Lm9Qp4R" + "s7Tv1Wx3Yz5"
	fakeEncodedKey = "dGhpcyBpcyBhIHNlY3Jl" + "dCB0b2tlbg%3D%3D"
	fakeCommitSHA  = "4b825dc642cb6eb9a060" + "e54bf8d69288fbee4904"
)

func TestForceable(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"abc", true},
		{"s3cr3t", true},
		{"", false},
		{"  ", false},
		{"true", false},
		{"Off", false},
		{"0", false},
		{"1", false},
		{"null", false},
		{"None", false},
		{"$HOME/.config/x.json", false},
		{"${SANDCAST_SECRET_1}", false},
		{"${GITHUB_TOKEN}", false},
		{"$GITHUB_TOKEN", false},
		{"prefix-${X}", false},
		{"/run/secrets/token", false},
		{"~/.config/token", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			assert.Equal(t, tt.want, Forceable(tt.value))
		})
	}
}

func TestRedactorServer(t *testing.T) {
	tests := []struct {
		name      string
		server    manifest.MCPServer
		want      manifest.MCPServer
		redacted  []Redaction
		redactedN int
	}{
		{
			name: "env shapes and short secrets under secret keys",
			server: manifest.MCPServer{Name: "docs", Command: []string{"npx", "-y", "docs-mcp"}, Env: map[string]string{
				"GITHUB_TOKEN": fakeGitHub,
				"API_KEY":      "short",
				"LOG_LEVEL":    "debug",
				"REF_TOKEN":    "${HOST_TOKEN}",
				"KEEP":         "${SANDCAST_SECRET_9}",
			}},
			want: manifest.MCPServer{Name: "docs", Command: []string{"npx", "-y", "docs-mcp"}, Env: map[string]string{
				"API_KEY":      "${SANDCAST_SECRET_1}",
				"GITHUB_TOKEN": "${SANDCAST_SECRET_2}",
				"LOG_LEVEL":    "debug",
				"REF_TOKEN":    "${HOST_TOKEN}",
				"KEEP":         "${SANDCAST_SECRET_9}",
			}},
			redacted: []Redaction{
				{Path: "mcp_servers[docs].env.API_KEY", Kind: redact.KindKeyValue, Placeholder: "${SANDCAST_SECRET_1}", EnvVar: "API_KEY"},
				{Path: "mcp_servers[docs].env.GITHUB_TOKEN", Kind: redact.KindGitHub, Placeholder: "${SANDCAST_SECRET_2}", EnvVar: "GITHUB_TOKEN"},
			},
		},
		{
			name: "command arguments",
			server: manifest.MCPServer{Name: "cli", Command: []string{
				"server", "--token", "abc", "--api-key=def", "--verbose", "--header", fakeGitHub,
			}},
			want: manifest.MCPServer{Name: "cli", Command: []string{
				"server", "--token", "${SANDCAST_SECRET_1}", "--api-key=${SANDCAST_SECRET_2}", "--verbose", "--header", "${SANDCAST_SECRET_3}",
			}},
			redactedN: 3,
		},
		{
			name:      "url password and query",
			server:    manifest.MCPServer{Name: "web", URL: "https://user:hunter2@mcp.example.com/mcp?mode=ro&access_token=abc#frag"},
			want:      manifest.MCPServer{Name: "web", URL: "https://user:${SANDCAST_SECRET_1}@mcp.example.com/mcp?mode=ro&access_token=${SANDCAST_SECRET_2}#frag"},
			redactedN: 2,
		},
		{
			name: "lower-case credential flags, env keys and headers",
			server: manifest.MCPServer{Name: "x", Command: []string{
				"srv", "--access-key", "AbCdEf123456", "--private-key=Zz9Yy8Xx7Ww6", "--key", "0f8e-uuid",
				"--sort-key", "name", "--header", "Authorization: Token opaque123", "-H", "X-Api-Key: k1",
			}, Env: map[string]string{"access_key": "short1", "privateKey": "short2", "cache_key": "keep"}},
			want: manifest.MCPServer{Name: "x", Command: []string{
				"srv", "--access-key", "${SANDCAST_SECRET_1}", "--private-key=${SANDCAST_SECRET_2}", "--key", "${SANDCAST_SECRET_3}",
				"--sort-key", "name", "--header", "Authorization: Token ${SANDCAST_SECRET_4}", "-H", "X-Api-Key: ${SANDCAST_SECRET_5}",
			}, Env: map[string]string{"access_key": "${SANDCAST_SECRET_6}", "privateKey": "${SANDCAST_SECRET_7}", "cache_key": "keep"}},
			redactedN: 7,
		},
		{
			name:      "basic credentials in a header argument",
			server:    manifest.MCPServer{Name: "r", Command: []string{"mcp-remote", "https://x/sse", "--header", "Authorization: Basic dXNlcjpwYXNzd29yZDEyMw=="}},
			want:      manifest.MCPServer{Name: "r", Command: []string{"mcp-remote", "https://x/sse", "--header", "Authorization: Basic ${SANDCAST_SECRET_1}"}},
			redactedN: 1,
		},
		{
			name: "assignments with a colon in the value and multi-word key flags",
			server: manifest.MCPServer{Name: "a", Command: []string{
				"srv", "DB_PASSWORD=adm:hunter2", "--client-secret=ab:cd", "--account-key", "Zm9vYmFy", "--ado-pat=abc1",
			}},
			want: manifest.MCPServer{Name: "a", Command: []string{
				"srv", "DB_PASSWORD=${SANDCAST_SECRET_1}", "--client-secret=${SANDCAST_SECRET_2}", "--account-key", "${SANDCAST_SECRET_3}", "--ado-pat=${SANDCAST_SECRET_4}",
			}},
			redactedN: 4,
		},
		{
			name:      "subscription key query parameter",
			server:    manifest.MCPServer{Name: "z", URL: "https://x.azure-api.net/mcp?subscription-key=0123456789abcdef"},
			want:      manifest.MCPServer{Name: "z", URL: "https://x.azure-api.net/mcp?subscription-key=${SANDCAST_SECRET_1}"},
			redactedN: 1,
		},
		{
			name:      "a cookie header is secret as a whole",
			server:    manifest.MCPServer{Name: "c", Command: []string{"srv", "--header", "Cookie: session=abcdef123456; theme=dark"}},
			want:      manifest.MCPServer{Name: "c", Command: []string{"srv", "--header", "Cookie: ${SANDCAST_SECRET_1}; theme=dark"}},
			redactedN: 1,
		},
		{
			name:      "a short cookie without a digit is secret as a whole",
			server:    manifest.MCPServer{Name: "d", Command: []string{"srv", "--header", "Cookie: sid=abc; theme=dark"}},
			want:      manifest.MCPServer{Name: "d", Command: []string{"srv", "--header", "Cookie: ${SANDCAST_SECRET_1}"}},
			redactedN: 1,
		},
		{
			name: "two-argument header form and nested env assignment",
			server: manifest.MCPServer{Name: "p", Command: []string{
				"mcp-proxy", "--headers", "X-Api-Key", "abcdefSECRET", "-H", "Authorization", "Token opaqueSECRET", "--env=GITHUB_TOKEN=SECRET4x", "https://y/sse",
			}},
			want: manifest.MCPServer{Name: "p", Command: []string{
				"mcp-proxy", "--headers", "X-Api-Key", "${SANDCAST_SECRET_1}", "-H", "Authorization", "Token ${SANDCAST_SECRET_2}", "--env=GITHUB_TOKEN=${SANDCAST_SECRET_3}", "https://y/sse",
			}},
			redactedN: 3,
		},
		{
			name: "parameter-like header names, flags and paths that are not credentials",
			server: manifest.MCPServer{Name: "h", Command: []string{
				"mcp-remote", "https://apim.example.net/mcp", "--header", "Ocp-Apim-Subscription-Key: 0f1e2d3c", "--header", "X-Auth: abc1", "--auth", "none",
			}, Env: map[string]string{"SKIP_AUTH": "1", "GOOGLE_APPLICATION_CREDENTIALS": "$HOME/.config/gcloud/adc.json"}},
			want: manifest.MCPServer{Name: "h", Command: []string{
				"mcp-remote", "https://apim.example.net/mcp", "--header", "Ocp-Apim-Subscription-Key: ${SANDCAST_SECRET_1}", "--header", "X-Auth: ${SANDCAST_SECRET_2}", "--auth", "none",
			}, Env: map[string]string{"SKIP_AUTH": "1", "GOOGLE_APPLICATION_CREDENTIALS": "$HOME/.config/gcloud/adc.json"}},
			redactedN: 2,
		},
		{
			name:      "colons that are not headers stay unchanged",
			server:    manifest.MCPServer{Name: "n", Command: []string{"srv", "--note", "use the token: abc", "--token:abc", "-H", "my token", "abc"}},
			want:      manifest.MCPServer{Name: "n", Command: []string{"srv", "--note", "use the token: abc", "--token:abc", "-H", "my token", "abc"}},
			redactedN: 0,
		},
		{
			name:      "joined header flag and cookie",
			server:    manifest.MCPServer{Name: "j", Command: []string{"srv", "--header=Authorization: Bearer short1", "-H", "Cookie: session=abc"}},
			want:      manifest.MCPServer{Name: "j", Command: []string{"srv", "--header=Authorization: Bearer ${SANDCAST_SECRET_1}", "-H", "Cookie: ${SANDCAST_SECRET_2}"}},
			redactedN: 2,
		},
		{
			name:      "secret query parameters in any case",
			server:    manifest.MCPServer{Name: "q", URL: "https://mcp.example.com/mcp?access_key=a1&auth=b2&mode=ro"},
			want:      manifest.MCPServer{Name: "q", URL: "https://mcp.example.com/mcp?access_key=${SANDCAST_SECRET_1}&auth=${SANDCAST_SECRET_2}&mode=ro"},
			redactedN: 2,
		},
		{
			name:   "nothing to redact",
			server: manifest.MCPServer{Name: "plain", URL: "https://mcp.example.com/mcp?mode=ro"},
			want:   manifest.MCPServer{Name: "plain", URL: "https://mcp.example.com/mcp?mode=ro"},
		},
		{
			name:   "credential in a url path segment",
			server: manifest.MCPServer{Name: "zap", URL: "https://mcp.zapier.com/api/mcp/s/" + fakePathKey + "/mcp"},
			want:   manifest.MCPServer{Name: "zap", URL: "https://mcp.zapier.com/api/mcp/s/${SANDCAST_SECRET_1}/mcp"},
			redacted: []Redaction{
				{Path: "mcp_servers[zap].url", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_1}"},
			},
		},
		{
			name:      "UUID access key in a url path segment",
			server:    manifest.MCPServer{Name: "pd", URL: "https://mcp.pipedream.net/" + fakePathUUID + "/github"},
			want:      manifest.MCPServer{Name: "pd", URL: "https://mcp.pipedream.net/${SANDCAST_SECRET_1}/github"},
			redactedN: 1,
		},
		{
			name: "ordinary path segments, versions and dates stay",
			server: manifest.MCPServer{Name: "o", URL: "https://mcp.example.com/v1/google-drive-integration-v2/2025-03-26/" +
				"k8s-cluster-production-us-east-1/GetUserProfileInformationV2/modelcontextprotocol/server-filesystem.v2/sse"},
			want: manifest.MCPServer{Name: "o", URL: "https://mcp.example.com/v1/google-drive-integration-v2/2025-03-26/" +
				"k8s-cluster-production-us-east-1/GetUserProfileInformationV2/modelcontextprotocol/server-filesystem.v2/sse"},
		},
		{
			name: "slugs with short letter-digit words and percent-encoded names stay",
			server: manifest.MCPServer{Name: "s", URL: "https://mcp.example.com/k8s-cluster-production-v2/ipv6-to-ipv4-gateway-v2/" +
				"e2e-tests-2024-01-15-run42/my-group%2Fmy-project-2024/mcp"},
			want: manifest.MCPServer{Name: "s", URL: "https://mcp.example.com/k8s-cluster-production-v2/ipv6-to-ipv4-gateway-v2/" +
				"e2e-tests-2024-01-15-run42/my-group%2Fmy-project-2024/mcp"},
		},
		{
			name:   "percent-encoded key in a url path segment",
			server: manifest.MCPServer{Name: "enc", URL: "https://mcp.example.com/" + fakeEncodedKey + "/mcp"},
			want:   manifest.MCPServer{Name: "enc", URL: "https://mcp.example.com/${SANDCAST_SECRET_1}/mcp"},
			redacted: []Redaction{
				{Path: "mcp_servers[enc].url", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_1}"},
			},
		},
		{
			name: "url embedded in a shell command argument",
			server: manifest.MCPServer{Name: "sh", Command: []string{
				"sh", "-c", "npx -y mcp-remote https://mcp.pipedream.net/0f3c9a2e-7b1d-4c8e-" + "9f2a-1b3c5d7e9f11/github",
			}},
			want: manifest.MCPServer{Name: "sh", Command: []string{
				"sh", "-c", "npx -y mcp-remote https://mcp.pipedream.net/${SANDCAST_SECRET_1}/github",
			}},
			redacted: []Redaction{
				{Path: "mcp_servers[sh].command[2]", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_1}"},
			},
		},
		{
			name: "urls embedded in an env value, with trailing punctuation and user information",
			server: manifest.MCPServer{Name: "emb", Command: []string{"sh", "-c", "exec mcp-remote https://mcp.example.com/" + fakePathKey + "; wait"},
				Env: map[string]string{
					"MCP_ARGS": "--url https://mcp.example.com/" + fakeMixedKey + "/sse",
					"MIRROR":   "fetch from (https://deploy:hunter2@git.example.com/o/r.git) first",
				}},
			want: manifest.MCPServer{Name: "emb", Command: []string{"sh", "-c", "exec mcp-remote https://mcp.example.com/${SANDCAST_SECRET_1}; wait"},
				Env: map[string]string{
					"MCP_ARGS": "--url https://mcp.example.com/${SANDCAST_SECRET_2}/sse",
					"MIRROR":   "fetch from (https://deploy:${SANDCAST_SECRET_3}@git.example.com/o/r.git) first",
				}},
			redacted: []Redaction{
				{Path: "mcp_servers[emb].command[2]", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_1}"},
				{Path: "mcp_servers[emb].env.MCP_ARGS", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_2}", EnvVar: "SANDCAST_SECRET_2"},
				{Path: "mcp_servers[emb].env.MIRROR", Kind: redact.KindURLSecret, Placeholder: "${SANDCAST_SECRET_3}", EnvVar: "SANDCAST_SECRET_3"},
			},
		},
		{
			name: "url path credentials in a command argument and an env value",
			server: manifest.MCPServer{Name: "remote",
				Command: []string{"npx", "-y", "mcp-remote", "https://mcp.zapier.com/api/mcp/s/" + fakePathKey + "/mcp", "--url=https://h.example.com/" + fakePathUUID},
				Env:     map[string]string{"WEBHOOK": "https://hooks.example.com/services/" + fakePathKey, "DB": "postgres://admin@db.example.com/app"}},
			want: manifest.MCPServer{Name: "remote",
				Command: []string{"npx", "-y", "mcp-remote", "https://mcp.zapier.com/api/mcp/s/${SANDCAST_SECRET_1}/mcp", "--url=https://h.example.com/${SANDCAST_SECRET_2}"},
				Env:     map[string]string{"WEBHOOK": "https://hooks.example.com/services/${SANDCAST_SECRET_1}", "DB": "postgres://admin@db.example.com/app"}},
			redacted: []Redaction{
				{Path: "mcp_servers[remote].command[3]", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_1}"},
				{Path: "mcp_servers[remote].command[4]", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_2}"},
				{Path: "mcp_servers[remote].env.WEBHOOK", Kind: redact.KindURLPath, Placeholder: "${SANDCAST_SECRET_1}", EnvVar: "SANDCAST_SECRET_1"},
			},
		},
		{
			name: "an env value that is partly a placeholder names the placeholder variable",
			server: manifest.MCPServer{Name: "env", Command: []string{"srv"}, Env: map[string]string{
				"API_KEY":      fakeGitHub,
				"AUTH":         "Bearer " + fakeGitHub,
				"DATABASE_URL": "postgres://u:pw@h/db",
			}},
			want: manifest.MCPServer{Name: "env", Command: []string{"srv"}, Env: map[string]string{
				"API_KEY":      "${SANDCAST_SECRET_1}",
				"AUTH":         "Bearer ${SANDCAST_SECRET_2}",
				"DATABASE_URL": "postgres://u:${SANDCAST_SECRET_3}@h/db",
			}},
			redacted: []Redaction{
				{Path: "mcp_servers[env].env.API_KEY", Kind: redact.KindGitHub, Placeholder: "${SANDCAST_SECRET_1}", EnvVar: "API_KEY"},
				{Path: "mcp_servers[env].env.AUTH", Kind: redact.KindGitHub, Placeholder: "${SANDCAST_SECRET_2}", EnvVar: "SANDCAST_SECRET_2"},
				{Path: "mcp_servers[env].env.DATABASE_URL", Kind: redact.KindURLSecret, Placeholder: "${SANDCAST_SECRET_3}", EnvVar: "SANDCAST_SECRET_3"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &MCPServer{MCPServer: tt.server}
			got := NewRedactor().Server(s)
			assert.Equal(t, tt.want, s.MCPServer)
			if tt.redacted != nil {
				assert.Equal(t, tt.redacted, got)
			} else {
				assert.Len(t, got, tt.redactedN)
			}
			for _, r := range got {
				assert.True(t, redact.IsPlaceholder(r.Placeholder), r.Placeholder)
			}
		})
	}
}

func TestCredentialSegment(t *testing.T) {
	tests := []struct {
		seg  string
		want bool
	}{
		// Names: letter-digit changes inside short words (k8s, v2, e2e,
		// ipv6, run42) do not count.
		{"k8s-cluster-production-v2", false},
		{"ipv6-to-ipv4-gateway-v2", false},
		{"e2e-tests-2024-01-15-run42", false},
		{"sha256sum-linux-x86_64-v1", false},
		{"k8s-cluster-production-us-east-1", false},
		{"GetUserProfileInformationV2", false},
		{"google-drive-integration-v2", false},
		{"2025-03-26", false},
		{"my-group%2Fmy-project-2024", false},
		{"server-filesystem.v2", false},
		// Keys: UUIDs, random mixed-case and base64 keys, also percent-encoded.
		{fakePathUUID, true},
		{fakePathKey, true},
		{fakeMixedKey, true},
		{fakeEncodedKey, true},
		{"0f3c9a2e-7b1d-4c8e-" + "9f2a-1b3c5d7e9f11", true},
		// A 40-hex git object name is a key: it has the shape of a classic
		// 40-hex GitHub token and of many hex API keys, and discovery cannot
		// tell them apart. The placeholder names the variable that supplies
		// it at runtime.
		{fakeCommitSHA, true},
	}
	for _, tt := range tests {
		t.Run(tt.seg, func(t *testing.T) {
			assert.Equal(t, tt.want, credentialSegment(tt.seg))
		})
	}
}

func TestRedactorNumbersAcrossServers(t *testing.T) {
	d := NewRedactor()
	a := &MCPServer{MCPServer: manifest.MCPServer{Name: "a", Env: map[string]string{"TOKEN": fakeGitHub}}}
	b := &MCPServer{MCPServer: manifest.MCPServer{Name: "b", Env: map[string]string{"TOKEN": fakeGitHub, "PASSWORD": "pw"}}}
	d.Server(a)
	d.Server(b)
	assert.Equal(t, "${SANDCAST_SECRET_1}", a.Env["TOKEN"])
	assert.Equal(t, "${SANDCAST_SECRET_1}", b.Env["TOKEN"], "the same secret keeps its number")
	assert.Equal(t, "${SANDCAST_SECRET_2}", b.Env["PASSWORD"])
	assert.Equal(t, "${SANDCAST_SECRET_2}", d.Secret("pw"))
	assert.Equal(t, "${SANDCAST_SECRET_3}", d.Secret("other"))
}

func TestRedactorText(t *testing.T) {
	d := NewRedactor()
	out, hits := d.Text("Bearer " + fakeGitHub)
	require.Len(t, hits, 1)
	assert.Equal(t, "Bearer ${SANDCAST_SECRET_1}", out)
	out2, hits2 := d.Text("Bearer " + fakeGitHub)
	assert.Equal(t, out, out2)
	assert.Equal(t, hits, hits2)
}

func TestRedactorURL(t *testing.T) {
	d := NewRedactor()
	out, reds := d.URL("plugins[x].source", "https://deploy:hunter2@git.example.com/repo.git?private_token=abc#main")
	assert.Equal(t, "https://deploy:${SANDCAST_SECRET_1}@git.example.com/repo.git?private_token=${SANDCAST_SECRET_2}#main", out)
	assert.Equal(t, []Redaction{
		{Path: "plugins[x].source", Kind: redact.KindURLSecret, Placeholder: "${SANDCAST_SECRET_1}"},
		{Path: "plugins[x].source", Kind: redact.KindKeyValue, Placeholder: "${SANDCAST_SECRET_2}"},
	}, reds)

	// A user name alone is an opaque token unless it is the git SSH user.
	out, reds = NewRedactor().URL("p", "https://0123456789abcdef@gitea.example.com/o/r.git")
	assert.Equal(t, "https://${SANDCAST_SECRET_1}@gitea.example.com/o/r.git", out)
	require.Len(t, reds, 1)
	assert.Equal(t, redact.KindURLSecret, reds[0].Kind)
	out, reds = NewRedactor().URL("p", "ssh://git@github.com/o/r.git")
	assert.Equal(t, "ssh://git@github.com/o/r.git", out)
	assert.Empty(t, reds)
}

func TestSplitUserinfo(t *testing.T) {
	tests := []struct {
		in, rest, userinfo string
		found              bool
	}{
		{"https://u:p@host/repo.git", "https://host/repo.git", "u:p", true},
		{"https://" + fakeGitHub + "@github.com/o/r", "https://github.com/o/r", fakeGitHub, true},
		{"ssh://git@host:22/o/r", "ssh://host:22/o/r", "git", true},
		{"https://host/path@v1", "https://host/path@v1", "", false},
		{"https://host?q=a@b", "https://host?q=a@b", "", false},
		{"owner/repo", "owner/repo", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			rest, userinfo, found := SplitUserinfo(tt.in)
			assert.Equal(t, tt.rest, rest)
			assert.Equal(t, tt.userinfo, userinfo)
			assert.Equal(t, tt.found, found)
		})
	}
}

func TestPlaceholderVar(t *testing.T) {
	assert.Equal(t, "SANDCAST_SECRET_3", PlaceholderVar("${SANDCAST_SECRET_3}"))
}
