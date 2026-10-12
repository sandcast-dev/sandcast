package profile

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	upstreamDir = "../../profiles/upstream/v0.1.3"
	overlayDir  = "../../profiles/overlay"
	lintCorpus  = "testdata/lint"
)

func loadShipped(t *testing.T) (upstream, overlay []Entry) {
	t.Helper()
	upstream, err := LoadDir(os.DirFS(upstreamDir), TierUpstream)
	require.NoError(t, err)
	overlay, err = LoadDir(os.DirFS(overlayDir), TierOverlay)
	require.NoError(t, err)
	return upstream, overlay
}

func TestShippedProfilesLintClean(t *testing.T) {
	upstream, overlay := loadShipped(t)
	require.Len(t, upstream, 17, "vendored upstream profiles")
	require.Len(t, overlay, 4, "overlay profiles")

	for _, e := range append(append([]Entry{}, upstream...), overlay...) {
		t.Run(e.SourceFile, func(t *testing.T) {
			assert.Empty(t, Lint(e.Profile, e.SourceFile))
		})
	}
	// Overlay ids must not collide with upstream ids, so the set lints too.
	assert.Empty(t, LintSet(append(upstream, overlay...)))
}

func TestOverlayProfilesFollowUpstreamHeader(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(overlayDir, "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		text := string(data)
		name := filepath.Base(f)
		assert.True(t, strings.HasPrefix(text, "# SPDX-License-Identifier: Apache-2.0\n\n# Example provider profile."), name)
		assert.Contains(t, text, "openshell provider profile lint   -f providers/"+name, name)
		assert.Contains(t, text, "openshell provider profile import -f providers/"+name+" --global", name)
		for _, key := range []string{"# Client binaries:", "# Reference layout:", "# Credential scope:", "# Endpoint access:", "# Smoke test:"} {
			assert.Contains(t, text, key, name)
		}
		p, err := Parse(data, name)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimSuffix(name, ".yaml"), p.ID, "file name matches id")
		assert.NotEmpty(t, p.Binaries, name)
	}
}

// lintCases maps each file of the negative corpus to a substring of the
// expected parse error or lint diagnostic.
var lintCases = map[string]string{
	"access-unknown.yaml":                             "unknown access value 'read' (expected read-only, read-write, or full)",
	"allowed-ips-blocked.yaml":                        "always-blocked allowed_ips entry '169.254.0.0/16'",
	"allowed-ips-invalid.yaml":                        "allowed_ips entry 'not-an-ip' is not a valid IP address or CIDR",
	"auth-bearer-no-header.yaml":                      "header_name is required for bearer auth",
	"auth-path-no-template.yaml":                      "path_template is required for path auth",
	"auth-path-template-count.yaml":                   "path_template should contain {credential} exactly once, /v1/{key}/resources contains {credential} 0 times",
	"auth-query-no-param.yaml":                        "query_param is required for query auth",
	"auth-style-unknown.yaml":                         "unsupported auth_style: digest",
	"binary-empty.yaml":                               "binary path must not be empty",
	"binary-extra-field.yaml":                         "unsupported provider profile binary fields: harness",
	"category-unknown.yaml":                           "unsupported provider profile category: storage",
	"credential-name-control-char.yaml":               "credential name must not contain control characters",
	"credential-name-duplicate.yaml":                  "duplicate credential name: api_key",
	"credential-name-empty.yaml":                      "credential name is required",
	"credential-name-missing.yaml":                    `credentials[0]: missing field "name"`,
	"credentialed-l4.yaml":                            "credentialed endpoint 'api.example.com:443' uses L4-only; configure L7 inspection or explicitly set allow_uninspected_credentials: true",
	"credentialed-tls-skip.yaml":                      "credentialed endpoint 'api.example.com:443' uses tls: skip",
	"deny-rules-empty.yaml":                           "deny_rules list cannot be empty (would have no effect)",
	"deny-rules-no-base.yaml":                         "deny_rules require rules or access to define the base allow set",
	"deny-rules-no-protocol.yaml":                     "deny_rules require protocol (L7 inspection must be enabled)",
	"discovery-duplicate.yaml":                        "duplicate discovery credential: api_key",
	"discovery-unknown.yaml":                          "unknown discovery credential: missing_key",
	"display-name-missing.yaml":                       `missing field "display_name"`,
	"endpoint-host-empty.yaml":                        "invalid endpoint ':443'",
	"endpoint-host-missing.yaml":                      `endpoints[0]: missing field "host"`,
	"endpoint-port-zero.yaml":                         "invalid endpoint 'api.example.com:0'",
	"endpoint-ports-range.yaml":                       "invalid endpoint 'api.example.com:0'",
	"enforcement-unknown.yaml":                        "unknown enforcement value 'block' (expected enforce or audit)",
	"env-var-duplicate.yaml":                          "duplicate credential env var 'EXAMPLE_TOKEN'",
	"env-var-empty.yaml":                              "credential env var must not be empty",
	"env-var-reserved.yaml":                           "credential env var 'v10_GITHUB_TOKEN' uses reserved OpenShell placeholder revision namespace",
	"graphql-operation-type.yaml":                     "rules[0].allow.operation_type must be query, mutation, or subscription",
	"host-ipv6-loopback.yaml":                         "endpoint host '::1' is an always-blocked address",
	"host-ipv6-zone.yaml":                             `endpoint host "2001:db8::1%eth0" is not a valid DNS name, wildcard pattern or IP address`,
	"host-newline.yaml":                               `endpoint host "evil.com\n[core]\n\tfsmonitor = touch /tmp/pwned" is not a valid DNS name`,
	"host-space.yaml":                                 `endpoint host "api.example.com other.example.com" is not a valid DNS name`,
	"host-link-local.yaml":                            "endpoint host '169.254.169.254' is an always-blocked address",
	"host-localhost.yaml":                             "endpoint host 'localhost' is always blocked",
	"host-loopback.yaml":                              "endpoint host '127.0.0.1' is an always-blocked address",
	"host-metadata.yaml":                              "endpoint host 'metadata.google.internal' is a known cloud metadata hostname",
	"id-empty.yaml":                                   "provider profile id is required",
	"id-missing.yaml":                                 `missing field "id"`,
	"id-underscore.yaml":                              "provider profile id must be lowercase kebab-case",
	"id-uppercase.yaml":                               "provider profile id must be lowercase kebab-case",
	"jsonrpc-access.yaml":                             "protocol json-rpc does not support access presets",
	"jsonrpc-no-rules.yaml":                           "protocol json-rpc requires explicit rules with allow.method",
	"mcp-broad-method-conflict.yaml":                  "method matcher allows every tool call and conflicts with MCP tool allow rules",
	"mcp-deny-broad-method-conflict.yaml":             "method matcher denies every tool call and conflicts with MCP tool allow rules",
	"mcp-matcher-empty.yaml":                          "matcher must specify glob or any",
	"mcp-matcher-glob-and-any.yaml":                   "matcher cannot specify both glob and any",
	"mcp-method-glob.yaml":                            "MCP method globs are only valid for the tools/ method family",
	"mcp-method-not-tools-call.yaml":                  "method must be tools/call when an MCP rule uses tool or params.name",
	"mcp-method-required.yaml":                        "method is required when mcp.allow_all_known_mcp_methods is false",
	"mcp-no-rules.yaml":                               "protocol mcp requires rules when mcp.allow_all_known_mcp_methods is false",
	"mcp-on-non-mcp.yaml":                             "mcp options are only valid for protocol mcp",
	"mcp-params-argument.yaml":                        "MCP tool argument matching is not supported yet",
	"mcp-path-rule.yaml":                              "mcp L7 rules must use method/tool, not path/query",
	"mcp-tool-and-params.yaml":                        "MCP rules must use either tool or params.name, not both",
	"mcp-unknown-option.yaml":                         `unknown field "future_option"`,
	"mcp-version-unsupported.yaml":                    "unsupported MCP protocol version '2024-01-01'",
	"mcp-versions-empty.yaml":                         "mcp.versions must contain at least one supported protocol version",
	"mcp-wildcard-not-strict.yaml":                    "wildcard tool-name matchers require mcp.strict_tool_names to remain enabled",
	"null-discovery.yaml":                             "discovery: null is not an object",
	"null-mcp.yaml":                                   "endpoints[0].mcp: null is not an object",
	"params-on-rest.yaml":                             "params matching is only valid for protocol mcp",
	"path-relative.yaml":                              "path must start with '/' or be '**'",
	"persisted-queries-unknown.yaml":                  "persisted_queries must be 'deny' or 'allow_registered', got 'maybe'",
	"port-control-plane.yaml":                         "port 6443 (Kubernetes API server) is a blocked control-plane port",
	"ports-control-plane.yaml":                        "port 10250 (kubelet API) is a blocked control-plane port",
	"protocol-no-rules.yaml":                          "protocol requires rules or access to define allowed traffic",
	"protocol-unknown.yaml":                           "unknown protocol 'http' (expected tcp, rest, websocket, graphql, sql, json-rpc, or mcp)",
	"refresh-strategy-missing.yaml":                   "refresh strategy is required",
	"refresh-strategy-unknown.yaml":                   "unsupported provider refresh strategy: magic",
	"rules-and-access.yaml":                           "rules and access are mutually exclusive",
	"rules-deny-all.yaml":                             "rules would deny all traffic (no allow clause found)",
	"rules-empty.yaml":                                "rules list cannot be empty (would deny all traffic)",
	"signing-body-rewrite.yaml":                       "has both credential_signing and request_body_credential_rewrite set",
	"signing-no-service.yaml":                         "has credential_signing set but signing_service is empty",
	"signing-unknown.yaml":                            "unrecognized credential_signing value 'sigv5'",
	"sql-enforce.yaml":                                "SQL enforcement requires full SQL parsing; use enforcement: audit",
	"tcp-access.yaml":                                 "protocol tcp does not support access, rules, or deny_rules",
	"tcp-empty-label.yaml":                            "invalid DNS host selector 'db..example.com': empty DNS labels",
	"tcp-ip-literal.yaml":                             "protocol tcp endpoint '203.0.113.10' must use a DNS hostname, not an IP literal",
	"tcp-l7-fields.yaml":                              "protocol tcp does not support L7-only fields: enforcement; remove those fields",
	"tls-unknown.yaml":                                "unknown tls value 'terminate'",
	"token-grant-audience-override-control-char.yaml": "token_grant audience override selectors must not contain control characters",
	"token-grant-endpoint-control-char.yaml":          "token_grant endpoint selectors must not contain control characters",
	"token-grant-sql.yaml":                            "token_grant credentials require HTTP header injection, which protocol sql does not support",
	"token-grant-tls-skip.yaml":                       "token_grant credentials require HTTP header injection, which tls: skip bypasses",
	"token-grant-type-unknown.yaml":                   "unsupported provider token grant type: password",
	"tool-on-rest.yaml":                               "MCP tool matching is only valid for protocol mcp",
	"unknown-endpoint-field.yaml":                     `unknown field "future_option"`,
	"unknown-field.yaml":                              `unknown field "homepage"`,
	"wildcard-bare.yaml":                              "invalid host wildcard '*'",
	"wildcard-partial-middle.yaml":                    "invalid host wildcard 'api.*foo.example.com'",
	"wildcard-recursive-middle.yaml":                  "invalid host wildcard 'api.**.example.com'",
	"wildcard-recursive-tld.yaml":                     "TLD wildcard '**.io' is not allowed",
	"wildcard-tld.yaml":                               "TLD wildcard '*.com' is not allowed; use subdomain wildcards like '*.example.com' instead",
}

func TestLintNegativeCorpus(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(lintCorpus, "*.yaml"))
	require.NoError(t, err)
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	var want []string
	for name := range lintCases {
		want = append(want, name)
	}
	sort.Strings(want)
	require.Equal(t, want, names, "every corpus file needs a table entry and every entry a file")
	require.GreaterOrEqual(t, len(names), 20)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			p, err := LoadFile(filepath.Join(lintCorpus, name))
			var messages []string
			if err != nil {
				messages = append(messages, err.Error())
			} else {
				for _, d := range Lint(p, name) {
					messages = append(messages, d.String())
				}
			}
			require.NotEmpty(t, messages, "expected a failure")
			assert.Contains(t, strings.Join(messages, "\n"), lintCases[name])
		})
	}
}

func TestMCPVersion2026Accepted(t *testing.T) {
	p, err := Parse([]byte(`id: example
display_name: Example
endpoints:
  - host: api.example.com
    port: 443
    protocol: mcp
    mcp:
      versions: ["2026-07-28"]
      allow_all_known_mcp_methods: true
`), "mcp-2026.yaml")
	require.NoError(t, err)
	assert.Empty(t, Lint(p, "mcp-2026.yaml"), "2026-07-28 is a supported MCP revision in v0.1.3")
}

func TestValidHost(t *testing.T) {
	for _, h := range []string{
		"github.com", "*.s3.amazonaws.com", "**.example.com", "*-aiplatform.googleapis.com",
		"s3.dualstack.*.amazonaws.com", "_acme.example.com", "example.com.", "203.0.113.10", "2001:db8::1",
	} {
		assert.True(t, ValidHost(h), h)
	}
	for _, h := range []string{
		"", " github.com", "github.com\n", "a\nb", "a b", "a'b", `a"b`, "a;b", "a/b", "[2001:db8::1]",
		"2001:db8::1%eth0", "fe80::1%a\nb", "a$(id)", "\x00",
	} {
		assert.False(t, ValidHost(h), "%q", h)
	}
}

func TestLintSetDuplicateIDs(t *testing.T) {
	a := &Profile{ID: "example", DisplayName: "A"}
	b := &Profile{ID: "example", DisplayName: "B"}
	bad := &Profile{ID: "Bad", DisplayName: "Bad"}
	diags := LintSet([]Entry{
		{Profile: a, SourceFile: "a.yaml"},
		{Profile: bad, SourceFile: "bad.yaml"},
		{Profile: b, SourceFile: "b.yaml"},
		{Profile: bad, SourceFile: "bad2.yaml"},
	})
	var dups []Diagnostic
	for _, d := range diags {
		if strings.HasPrefix(d.Message, "duplicate provider profile id") {
			dups = append(dups, d)
		}
	}
	require.Len(t, dups, 1, "invalid ids are not counted as duplicates")
	assert.Equal(t, Diagnostic{ProfileID: "example", Field: "id", Message: "duplicate provider profile id: example", Source: "b.yaml"}, dups[0])

	// Lint alone never reports duplicates.
	assert.Empty(t, Lint(a, "a.yaml"))
}

func TestLintReportsEveryDiagnostic(t *testing.T) {
	// Mirrors upstream validate_profile_set_returns_all_discoverable_diagnostics.
	p, err := Parse([]byte(`
id: broken
display_name: Broken
credentials:
  - name: api_key
    env_vars: [BROKEN_TOKEN]
    auth_style: query
  - name: api_key
    env_vars: [BROKEN_TOKEN, "", v10_GITHUB_TOKEN]
    auth_style: unknown
  - name: path_key
    env_vars: [PATH_TOKEN]
    auth_style: path
  - name: path_key_bad
    env_vars: [PATH_TOKEN_BAD]
    auth_style: path
    path_template: /v1/{key}/resources
discovery:
  credentials: [api_key, missing_key]
endpoints:
  - host: ""
    port: 0
binaries: ["", /usr/bin/broken]
`), "broken.yaml")
	require.NoError(t, err)
	var messages []string
	for _, d := range Lint(p, "broken.yaml") {
		assert.Equal(t, "broken.yaml", d.Source)
		assert.Equal(t, "broken", d.ProfileID)
		messages = append(messages, d.Message)
	}
	for _, want := range []string{
		"duplicate credential name: api_key",
		"duplicate credential env var 'BROKEN_TOKEN'",
		"credential env var must not be empty",
		"credential env var 'v10_GITHUB_TOKEN' uses reserved OpenShell placeholder revision namespace",
		"query_param is required for query auth",
		"path_template is required for path auth",
		"path_template should contain {credential} exactly once, /v1/{key}/resources contains {credential} 0 times",
		"unsupported auth_style: unknown",
		"unknown discovery credential: missing_key",
		"invalid endpoint ':0'",
		"binary path must not be empty",
	} {
		assert.Contains(t, messages, want)
	}
}

func TestLintAcceptsValidVariants(t *testing.T) {
	tests := map[string]string{
		"category normalized": `
id: example
display_name: Example
category: Source-Control
`,
		"auth style case-insensitive": `
id: example
display_name: Example
credentials:
  - name: api_key
    env_vars: [EXAMPLE_TOKEN]
    auth_style: Bearer
    header_name: authorization
endpoints:
  - host: api.example.com
    port: 443
    protocol: rest
    access: read-only
`,
		"uninspected credentials allowed explicitly": `
id: example
display_name: Example
credentials:
  - name: api_key
    env_vars: [EXAMPLE_TOKEN]
endpoints:
  - host: telemetry.example.com
    port: 443
    allow_uninspected_credentials: true
`,
		"explicit tcp to a hostname": `
id: example
display_name: Example
endpoints:
  - host: db.example.com
    port: 5432
    protocol: tcp
`,
		"mcp with allow all": `
id: example
display_name: Example
endpoints:
  - host: mcp.example.com
    port: 443
    protocol: MCP
    mcp:
      versions: ["2025-06-18", "2025-11-25"]
      allow_all_known_mcp_methods: true
`,
		"mcp tool rules": `
id: example
display_name: Example
endpoints:
  - host: mcp.example.com
    port: 443
    protocol: mcp
    rules:
      - allow: { method: tools/call, tool: "search_*" }
      - allow: { method: tools/list }
    deny_rules:
      - method: tools/call
        tool: { any: [delete_repo, drop_table] }
`,
		"wildcards": `
id: example
display_name: Example
endpoints:
  - host: "*.example.com"
    port: 443
  - host: "**.example.com"
    port: 443
  - host: "*-api.example.com"
    port: 443
  - host: "*.s3.*.amazonaws.com"
    port: 443
`,
		"allowed ips": `
id: example
display_name: Example
endpoints:
  - host: db.internal.example.com
    port: 5432
    allowed_ips: [10.0.0.0/8, 192.168.1.10]
`,
		"graphql rules": `
id: example
display_name: Example
endpoints:
  - host: api.example.com
    port: 443
    protocol: graphql
    persisted_queries: allow_registered
    rules:
      - allow: { operation_type: query }
`,
		"refresh and token grant": `
id: example
display_name: Example
credentials:
  - name: token
    env_vars: [EXAMPLE_TOKEN]
    auth_style: bearer
    header_name: authorization
    refresh:
      strategy: oauth2-client-credentials
      token_url: https://auth.example.com/token
  - name: exchanged
    env_vars: [EXAMPLE_EXCHANGED]
    token_grant:
      grant_type: token_exchange
      token_endpoint: https://auth.example.com/token
endpoints:
  - host: api.example.com
    port: 443
    protocol: rest
    access: read-only
`,
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := Parse([]byte(doc), name)
			require.NoError(t, err)
			assert.Empty(t, Lint(p, name))
		})
	}
}

func TestDiagnosticString(t *testing.T) {
	d := Diagnostic{Source: "a.yaml", ProfileID: "x", Field: "id", Message: "bad"}
	assert.Equal(t, "a.yaml: x: id: bad", d.String())
	assert.Equal(t, "bad", Diagnostic{Message: "bad"}.String())

	err := &LintError{Diagnostics: []Diagnostic{d, {Message: "worse"}}}
	assert.Equal(t, "provider profile lint failed with 2 error(s):\n  a.yaml: x: id: bad\n  worse", err.Error())
}

func TestIsValidProfileID(t *testing.T) {
	for id, want := range map[string]bool{
		"github":     true,
		"go-proxy":   true,
		"a1-b2":      true,
		"":           false,
		"-a":         false,
		"a-":         false,
		"a--b":       false,
		"A":          false,
		"a_b":        false,
		"a.b":        false,
		"crates-io9": true,
	} {
		assert.Equal(t, want, isValidProfileID(id), id)
	}
	assert.False(t, isValidProfileID("\tgithub\n"), "surrounding whitespace")
	assert.Equal(t, "alex-api", normalizeProfileID(" Alex-API "))
	assert.Equal(t, "", normalizeProfileID("alex_api"))
}

func TestUsesReservedRevisionNamespace(t *testing.T) {
	handle := strings.Repeat("ab", 32)
	for key, want := range map[string]bool{
		"v1_TOKEN":               true,
		"v10_GITHUB_TOKEN":       true,
		"v_TOKEN":                false,
		"vx_TOKEN":               false,
		"v1_":                    false,
		"v1_BAD-KEY":             false,
		"s" + handle + "_K":      true,
		"s" + handle[:62] + "_K": false,
		"S" + handle + "_K":      false,
		"GITHUB_TOKEN":           false,
		"value_TOKEN":            false,
	} {
		assert.Equal(t, want, usesReservedRevisionNamespace(key), key)
	}
}

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"tools/call", "tools/call", true},
		{"tools/*", "tools/call", true},
		{"*", "tools/call", true},
		{"t*s/c?ll", "tools/call", true},
		{"tools/[a-c]all", "tools/call", true},
		{"tools/[!c]all", "tools/call", false},
		{"tools/**", "tools/call", true},
		{"**", "tools/call", true},
		{"tools**", "tools/call", false},
		{"tools/[", "tools/call", false},
		{"resources/*", "tools/call", false},
		{"tools/list", "tools/call", false},
	} {
		assert.Equal(t, tc.want, globMatch(tc.pattern, tc.name), tc.pattern)
	}
	assert.True(t, methodMatchesToolsCall("*"))
	assert.True(t, methodMatchesToolsCall("tools/call"))
	assert.False(t, methodMatchesToolsCall("tools/list"))
}

func TestHostRulesHelpers(t *testing.T) {
	for host, want := range map[string]bool{
		"*.com":            true,
		"**.com":           true,
		"*.example.com":    false,
		"example.com":      false,
		"*-api.com":        false,
		"**.example.co.uk": false,
	} {
		assert.Equal(t, want, isTLDWildcard(host), host)
	}
	for host, want := range map[string]bool{
		"*":                           true,
		"**":                          true,
		"a**.example.com":             true,
		"api.*foo.example.com":        true,
		"api.**.example.com":          true,
		"*.example.com":               false,
		"**.example.com":              false,
		"*-aiplatform.googleapis.com": false,
		"*.s3.*.amazonaws.com":        false,
		"api.example.com":             false,
	} {
		assert.Equal(t, want, hostWildcardShapeInvalid(host), host)
	}
	for entry, want := range map[string]bool{
		"127.0.0.1":        true,
		"0.0.0.0/0":        true,
		"169.254.1.0/24":   true,
		"::1":              true,
		"fe80::/64":        true,
		"::ffff:127.0.0.1": true,
		"10.0.0.0/8":       false,
		"203.0.113.0/24":   false,
		"2001:db8::/32":    false,
	} {
		prefix, ok := parseAllowedIP(entry)
		require.True(t, ok, entry)
		assert.Equal(t, want, isAlwaysBlockedPrefix(prefix), entry)
	}
	assert.True(t, isKnownMetadataHostname("Metadata.Google.Internal."))
	assert.False(t, isKnownMetadataHostname("metadata.example.com"))
}
