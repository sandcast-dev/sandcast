package profile

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
)

// The rules below mirror validate_profile_set in
// crates/openshell-providers/src/profiles.rs at OpenShell v0.1.3, including
// the checks upstream performs while deserializing (category, refresh
// strategy and token grant type enums, MCP version lists). Messages keep the
// upstream wording. Policy rules that upstream applies only at sandbox start
// are added in policyRules and hostRules (hostrules.go).

const (
	pathTemplatePlaceholder = "{credential}"
	mcpVersionRemediation   = "omit mcp.versions to use the pinned default revision; use an exact supported revision; or omit protocol and mcp for deliberate uninspected L4 passthrough only when that weaker boundary is acceptable"
)

// supportedMCPVersions are the MCP protocol revisions OpenShell v0.1.3 knows
// (McpProtocolVersion::ALL). The pinned default revision stays 2025-11-25;
// 2026-07-28 is accepted when a profile names it explicitly.
var supportedMCPVersions = map[string]bool{
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
	"2026-07-28": true,
}

// Diagnostic is one lint finding. Every diagnostic is an error.
type Diagnostic struct {
	ProfileID string `json:"profile_id"`
	Field     string `json:"field"`
	Message   string `json:"message"`
	Source    string `json:"source"`
}

// String formats the diagnostic as "source: profile id: field: message",
// leaving out empty parts.
func (d Diagnostic) String() string {
	var parts []string
	for _, p := range []string{d.Source, d.ProfileID, d.Field} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	parts = append(parts, d.Message)
	return strings.Join(parts, ": ")
}

// LintError carries the diagnostics that made an operation fail.
type LintError struct {
	Diagnostics []Diagnostic
}

// Error lists every diagnostic on its own line.
func (e *LintError) Error() string {
	lines := make([]string, 0, len(e.Diagnostics)+1)
	lines = append(lines, fmt.Sprintf("provider profile lint failed with %d error(s):", len(e.Diagnostics)))
	for _, d := range e.Diagnostics {
		lines = append(lines, "  "+d.String())
	}
	return strings.Join(lines, "\n")
}

// Lint checks one profile against every per-profile rule. source names the
// profile in the diagnostics.
func Lint(p *Profile, source string) []Diagnostic {
	return lintProfile(p, source, nil)
}

// LintSet checks every entry and additionally reports duplicate ids, as
// upstream does for a profile set.
func LintSet(entries []Entry) []Diagnostic {
	ids := map[string]bool{}
	var diags []Diagnostic
	for _, e := range entries {
		diags = append(diags, lintProfile(e.Profile, e.SourceFile, ids)...)
	}
	return diags
}

// linter accumulates the diagnostics of one profile.
type linter struct {
	source    string
	profileID string
	diags     []Diagnostic
}

func (l *linter) add(field, message string) {
	l.diags = append(l.diags, Diagnostic{ProfileID: l.profileID, Field: field, Message: message, Source: l.source})
}

// lintProfile runs every rule. ids collects valid ids for duplicate
// detection; nil disables it.
func lintProfile(p *Profile, source string, ids map[string]bool) []Diagnostic {
	l := &linter{source: source, profileID: strings.TrimSpace(p.ID)}

	switch {
	case l.profileID == "":
		l.add("id", "provider profile id is required")
	case normalizeProfileID(p.ID) != p.ID:
		l.add("id", "provider profile id must be lowercase kebab-case using only a-z, 0-9, and '-'")
	case ids != nil && ids[l.profileID]:
		l.add("id", "duplicate provider profile id: "+l.profileID)
	case ids != nil:
		ids[l.profileID] = true
	}

	if !validCategory(p.Category) {
		l.add("category", "unsupported provider profile category: "+p.Category)
	}

	credentialNames := map[string]bool{}
	for _, c := range p.Credentials {
		if strings.ContainsFunc(c.Name, unicode.IsControl) {
			l.add("credentials.name", "credential name must not contain control characters")
			continue
		}
		name := strings.TrimSpace(c.Name)
		switch {
		case name == "":
			l.add("credentials.name", "credential name is required")
		case credentialNames[name]:
			l.add("credentials.name", "duplicate credential name: "+name)
		default:
			credentialNames[name] = true
		}
	}

	discovered := map[string]bool{}
	for i, raw := range p.Discovery.Credentials {
		field := fmt.Sprintf("discovery.credentials[%d]", i)
		name := strings.TrimSpace(raw)
		switch {
		case name == "":
			l.add(field, "discovery credential name must not be empty")
		case discovered[name]:
			l.add(field, "duplicate discovery credential: "+name)
		case !credentialNames[name]:
			discovered[name] = true
			l.add(field, "unknown discovery credential: "+name)
		default:
			discovered[name] = true
		}
	}

	envVars := map[string]bool{}
	for i := range p.Credentials {
		l.credential(&p.Credentials[i], envVars)
	}

	credentialed := hasCredentialedEndpoints(p)
	tokenGrant := profileHasTokenGrant(p)
	for i := range p.Endpoints {
		l.endpoint(i, &p.Endpoints[i], credentialed, tokenGrant)
	}

	for i, b := range p.Binaries {
		if strings.TrimSpace(b) == "" {
			l.add(fmt.Sprintf("binaries[%d]", i), "binary path must not be empty")
		}
	}
	return l.diags
}

// credential checks env vars, auth_style and the enums of the pass-through
// refresh and token_grant blocks.
func (l *linter) credential(c *Credential, envVars map[string]bool) {
	for _, env := range c.EnvVars {
		trimmed := strings.TrimSpace(env)
		switch {
		case trimmed == "":
			l.add("credentials.env_vars", "credential env var must not be empty")
		case usesReservedRevisionNamespace(trimmed):
			l.add("credentials.env_vars", fmt.Sprintf(
				"credential env var '%s' uses reserved OpenShell placeholder revision namespace", env))
		case envVars[trimmed]:
			l.add("credentials.env_vars", fmt.Sprintf("duplicate credential env var '%s'", env))
		default:
			envVars[trimmed] = true
		}
	}

	authStyle := strings.ToLower(strings.TrimSpace(c.AuthStyle))
	switch authStyle {
	case "", "basic":
	case "bearer", "header":
		if strings.TrimSpace(c.HeaderName) == "" {
			l.add("credentials.header_name", fmt.Sprintf("header_name is required for %s auth", authStyle))
		}
	case "path":
		tmpl := strings.TrimSpace(c.PathTemplate)
		if tmpl == "" {
			l.add("credentials.path_template", "path_template is required for path auth")
		} else if n := strings.Count(tmpl, pathTemplatePlaceholder); n != 1 {
			l.add("credentials.path_template", fmt.Sprintf(
				"path_template should contain {credential} exactly once, %s contains {credential} %d times", tmpl, n))
		}
	case "query":
		if strings.TrimSpace(c.QueryParam) == "" {
			l.add("credentials.query_param", "query_param is required for query auth")
		}
	default:
		l.add("credentials.auth_style", "unsupported auth_style: "+c.AuthStyle)
	}

	if c.Refresh != nil {
		strategy, ok := mapString(c.Refresh, "strategy")
		switch normalized := normalizeEnum(strategy); {
		case !ok:
			l.add("credentials.refresh.strategy", "refresh strategy must be a string")
		case normalized == "":
			l.add("credentials.refresh.strategy", "refresh strategy is required")
		case !refreshStrategies[normalized]:
			l.add("credentials.refresh.strategy", "unsupported provider refresh strategy: "+strategy)
		}
	}
	if c.TokenGrant != nil {
		grantType, ok := mapString(c.TokenGrant, "grant_type")
		switch normalized := normalizeEnum(grantType); {
		case !ok:
			l.add("credentials.token_grant.grant_type", "token grant type must be a string")
		case normalized != "" && normalized != "client_credentials" && normalized != "token_exchange":
			l.add("credentials.token_grant.grant_type", "unsupported provider token grant type: "+grantType)
		}
		l.tokenGrantAudienceOverrides(c.TokenGrant)
	}
}

// tokenGrantAudienceOverrides reports control characters in the host and path
// selectors of a credential's token_grant.audience_overrides entries. token_grant
// is a pass-through block in sandcast (R-07), so only this self-contained check
// is mirrored; the override-to-endpoint binding checks upstream added in v0.1.3
// are left to the gateway, which re-validates on import.
func (l *linter) tokenGrantAudienceOverrides(tg yaml.MapSlice) {
	overrides, ok := lookupField(tg, "audience_overrides")
	if !ok {
		return
	}
	list, ok := overrides.([]any)
	if !ok {
		return
	}
	for i, item := range list {
		for _, sel := range []string{"host", "path"} {
			raw, ok := lookupField(item, sel)
			if !ok {
				continue
			}
			if s, ok := raw.(string); ok && strings.ContainsFunc(s, unicode.IsControl) {
				l.add(fmt.Sprintf("credentials.token_grant.audience_overrides[%d].%s", i, sel),
					"token_grant audience override selectors must not contain control characters")
			}
		}
	}
}

// refreshStrategies are the refresh strategies upstream accepts, normalized.
var refreshStrategies = map[string]bool{
	"static":                     true,
	"external":                   true,
	"oauth2_refresh_token":       true,
	"oauth2_client_credentials":  true,
	"google_service_account_jwt": true,
	"aws_sts_assume_role":        true,
}

// endpoint checks one endpoint. tokenGrant reports whether any credential of
// the profile carries a token_grant block, which tightens a few endpoint rules.
func (l *linter) endpoint(index int, ep *Endpoint, credentialed, tokenGrant bool) {
	field := fmt.Sprintf("endpoints[%d]", index)

	if !endpointIsValid(ep) {
		l.add(field, fmt.Sprintf("invalid endpoint '%s:%d'", ep.Host, ep.Port))
	}
	// sandcast rule, not upstream: hosts are written into generated files
	// such as allowed-domains.txt, so only host name characters pass.
	if strings.TrimSpace(ep.Host) != "" && !ValidHost(ep.Host) {
		l.add(field+".host", fmt.Sprintf(
			"endpoint host %q is not a valid DNS name, wildcard pattern or IP address (allowed: letters, digits, '-', '_', '.', '*')", ep.Host))
	}
	l.mcpOptions(index, ep)

	if ep.Rules != nil && len(ep.Rules) == 0 {
		l.add(field, "rules list cannot be empty (would deny all traffic). Use `access: full` or remove rules.")
	}
	if ep.DenyRules != nil && len(ep.DenyRules) == 0 {
		l.add(field, "deny_rules list cannot be empty (would have no effect). Remove it if no denials are needed.")
	}

	allowAll := ep.MCP != nil && ep.MCP.AllowAllKnownMCPMethods != nil && *ep.MCP.AllowAllKnownMCPMethods
	for _, msg := range l7EndpointSemantics(ep, allowAll) {
		l.add(field, msg)
	}
	for _, msg := range endpointModes(ep.TLS, ep.Enforcement, ep.Access) {
		l.add(field, msg)
	}
	if isExplicitTCP(ep.Protocol) {
		if present := l7OnlyFields(ep); len(present) > 0 {
			l.add(field, fmt.Sprintf("protocol tcp does not support L7-only fields: %s; remove those fields",
				strings.Join(present, ", ")))
		}
	}

	if isMCPProtocol(ep.Protocol) {
		l.mcpRules(index, ep, allowAll)
	} else {
		for j, r := range ep.Rules {
			if r.Allow == nil {
				continue
			}
			loc := fmt.Sprintf("%s.rules[%d].allow", field, j)
			if r.Allow.Tool != nil {
				l.add(loc+".tool", "MCP tool matching is only valid for protocol mcp")
			}
			if len(r.Allow.Params) > 0 {
				l.add(loc+".params", "params matching is only valid for protocol mcp")
			}
		}
		for j, d := range ep.DenyRules {
			loc := fmt.Sprintf("%s.deny_rules[%d]", field, j)
			if d.Tool != nil {
				l.add(loc+".tool", "MCP tool matching is only valid for protocol mcp")
			}
			if len(d.Params) > 0 {
				l.add(loc+".params", "params matching is only valid for protocol mcp")
			}
		}
	}

	if tokenGrant {
		l.tokenGrantEndpoint(index, ep)
	}

	if credentialed && !ep.AllowUninspectedCredentials {
		mode := ""
		switch {
		case strings.TrimSpace(ep.Protocol) == "":
			mode = "L4-only"
		case strings.EqualFold(strings.TrimSpace(ep.TLS), "skip"):
			mode = "tls: skip"
		}
		if mode != "" {
			l.add(field+".allow_uninspected_credentials", fmt.Sprintf(
				"credentialed endpoint '%s:%d' uses %s; configure L7 inspection or explicitly set allow_uninspected_credentials: true",
				ep.Host, ep.Port, mode))
		}
	}

	l.policyRules(index, ep)
	l.hostRules(index, ep)
}

// mcpOptions checks the mcp block: the version list rules upstream applies
// while deserializing, and that mcp appears only with protocol mcp.
func (l *linter) mcpOptions(index int, ep *Endpoint) {
	if ep.MCP == nil {
		return
	}
	field := fmt.Sprintf("endpoints[%d].mcp", index)
	if ep.MCP.Versions != nil && len(ep.MCP.Versions) == 0 {
		l.add(field+".versions", "mcp.versions must contain at least one supported protocol version")
	}
	seen := map[string]bool{}
	for _, v := range ep.MCP.Versions {
		if seen[v] {
			l.add(field+".versions", fmt.Sprintf("duplicate MCP protocol version '%s'", v))
		}
		seen[v] = true
		if !supportedMCPVersions[v] {
			l.add(field+".versions", fmt.Sprintf("unsupported MCP protocol version '%s'; %s", v, mcpVersionRemediation))
		}
	}
	if !isMCPProtocol(ep.Protocol) {
		l.add(field, "mcp options are only valid for protocol mcp")
	}
}

// mcpRules checks the allow and deny rules of an endpoint with protocol mcp.
func (l *linter) mcpRules(index int, ep *Endpoint, allowAll bool) {
	strict := ep.MCP == nil || ep.MCP.StrictToolNames == nil || *ep.MCP.StrictToolNames
	for j, r := range ep.Rules {
		if r.Allow == nil {
			continue
		}
		a := r.Allow
		l.mcpSelector(fmt.Sprintf("endpoints[%d].rules[%d].allow", index, j),
			a.Method, a.Path, a.Query, a.Params, a.Tool, allowAll, strict)
	}
	for j, d := range ep.DenyRules {
		l.mcpSelector(fmt.Sprintf("endpoints[%d].deny_rules[%d]", index, j),
			d.Method, d.Path, d.Query, d.Params, d.Tool, allowAll, strict)
	}

	hasToolAllow := false
	for _, r := range ep.Rules {
		if r.Allow != nil && hasToolSelector(r.Allow.Params, r.Allow.Tool) {
			hasToolAllow = true
		}
	}
	if !hasToolAllow {
		return
	}
	const allowConflict = "method matcher allows every tool call and conflicts with MCP tool allow rules; add tool or params.name to narrow tools/call, or remove the tool allow rules"
	for j, r := range ep.Rules {
		loc := fmt.Sprintf("endpoints[%d].rules[%d].allow", index, j)
		switch {
		case r.Allow == nil:
			if allowAll {
				l.add(loc, allowConflict)
			}
		case !hasToolSelector(r.Allow.Params, r.Allow.Tool):
			if methodMatchesToolsCall(r.Allow.Method) || (r.Allow.Method == "" && allowAll) {
				l.add(loc, allowConflict)
			}
		}
	}
	for j, d := range ep.DenyRules {
		if !hasToolSelector(d.Params, d.Tool) && methodMatchesToolsCall(d.Method) {
			l.add(fmt.Sprintf("endpoints[%d].deny_rules[%d]", index, j),
				"method matcher denies every tool call and conflicts with MCP tool allow rules; add tool or params.name to deny specific tools, or remove the tool allow rules")
		}
	}
}

// mcpSelector checks one MCP allow or deny selector.
func (l *linter) mcpSelector(loc, method, path string, query, params map[string]QueryMatcher,
	tool *QueryMatcher, allowAll, strict bool) {
	if _, hasName := params["name"]; tool != nil && hasName {
		l.add(loc, "MCP rules must use either tool or params.name, not both")
	}
	if hasToolSelector(params, tool) && method != "" && method != "tools/call" {
		l.add(loc, "method must be tools/call when an MCP rule uses tool or params.name")
	}
	if method == "" && !allowAll {
		l.add(loc, "method is required when mcp.allow_all_known_mcp_methods is false")
	}
	if globUsesWildcard(method) && !strings.HasPrefix(method, "tools/") {
		l.add(loc+".method", "MCP method globs are only valid for the tools/ method family; omit method to use the endpoint method profile")
	}
	if path != "" || len(query) > 0 {
		l.add(loc, "mcp L7 rules must use method/tool, not path/query")
	}
	for _, key := range sortedKeys(params) {
		if key != "name" {
			l.add(loc+".params."+key, "MCP tool argument matching is not supported yet")
		}
	}
	if tool != nil {
		l.mcpMatcher(loc+".tool", tool, strict)
	}
	if name, ok := params["name"]; ok {
		l.mcpMatcher(loc+".params.name", &name, strict)
	}
}

// mcpMatcher mirrors validate_mcp_matcher.
func (l *linter) mcpMatcher(field string, m *QueryMatcher, strict bool) {
	if m.Glob != "" && len(m.Any) > 0 {
		l.add(field, "matcher cannot specify both glob and any")
	}
	if m.Glob == "" && len(m.Any) == 0 {
		l.add(field, "matcher must specify glob or any")
	}
	if !strict && matcherUsesWildcard(m) {
		l.add(field, "wildcard tool-name matchers require mcp.strict_tool_names to remain enabled")
	}
}

// policyRules are the endpoint field rules validate_sandbox_policy applies
// when the composed policy is loaded at sandbox start.
func (l *linter) policyRules(index int, ep *Endpoint) {
	field := fmt.Sprintf("endpoints[%d]", index)
	if s := ep.CredentialSigning; s != "" && s != "sigv4" && s != "sigv4:body" && s != "sigv4:no_body" {
		l.add(field+".credential_signing", fmt.Sprintf(
			"endpoint '%s' has unrecognized credential_signing value '%s' (expected sigv4, sigv4:body, or sigv4:no_body)", ep.Host, s))
	}
	if ep.CredentialSigning != "" && ep.SigningService == "" {
		l.add(field+".signing_service", fmt.Sprintf(
			"endpoint '%s' has credential_signing set but signing_service is empty", ep.Host))
	}
	if ep.CredentialSigning != "" && ep.RequestBodyCredentialRewrite {
		l.add(field, fmt.Sprintf(
			"endpoint '%s' has both credential_signing and request_body_credential_rewrite set; these options are mutually exclusive", ep.Host))
	}
	if ep.Path != "" && !strings.HasPrefix(ep.Path, "/") && ep.Path != "**" {
		l.add(field+".path", "path must start with '/' or be '**'")
	}
	if q := ep.PersistedQueries; q != "" && q != "deny" && q != "allow_registered" {
		l.add(field+".persisted_queries", fmt.Sprintf(
			"persisted_queries must be 'deny' or 'allow_registered', got '%s'", q))
	}
	if ep.Protocol == "sql" && ep.Enforcement == "enforce" {
		l.add(field, "SQL enforcement requires full SQL parsing; use enforcement: audit")
	}
	if ep.Protocol == "graphql" {
		for j, r := range ep.Rules {
			opType := ""
			if r.Allow != nil {
				opType = r.Allow.OperationType
			}
			if !isGraphqlOperationType(opType) {
				l.add(field, fmt.Sprintf("rules[%d].allow.operation_type must be query, mutation, or subscription", j))
			}
		}
		for j, d := range ep.DenyRules {
			if !isGraphqlOperationType(d.OperationType) {
				l.add(field, fmt.Sprintf("deny_rules[%d].operation_type must be query, mutation, or subscription", j))
			}
		}
		for _, key := range sortedKeys(ep.GraphqlPersistedQueries) {
			if !isGraphqlOperationType(ep.GraphqlPersistedQueries[key].OperationType) {
				l.add(field, fmt.Sprintf("graphql_persisted_queries[%s].operation_type must be query, mutation, or subscription", key))
			}
		}
	}
}

// l7EndpointSemantics mirrors validate_l7_endpoint_semantics in
// crates/openshell-policy/src/l7_validate.rs.
func l7EndpointSemantics(ep *Endpoint, allowAll bool) []string {
	var errs []string
	protocol := ep.Protocol
	l7, known := parseL7Protocol(protocol)
	explicitTCP := isExplicitTCP(protocol)
	isMCP := known && l7 == "mcp"
	isJSONRPC := known && l7 == "json-rpc"
	hasRules := len(ep.Rules) > 0
	hasDenyRules := len(ep.DenyRules) > 0

	if protocol != "" && !known && !explicitTCP {
		errs = append(errs, fmt.Sprintf(
			"unknown protocol '%s' (expected tcp, rest, websocket, graphql, sql, json-rpc, or mcp)", protocol))
	}
	if explicitTCP && (ep.Access != "" || hasRules || hasDenyRules) {
		errs = append(errs, "protocol tcp does not support access, rules, or deny_rules; remove those L7 fields")
	}
	if hasRules && ep.Access != "" {
		errs = append(errs, "rules and access are mutually exclusive")
	}
	if (isMCP || isJSONRPC) && ep.Access != "" {
		if isMCP {
			errs = append(errs, fmt.Sprintf(
				"protocol %s does not support access presets; use rules/deny_rules or set mcp.allow_all_known_mcp_methods: true for an allow-all MCP policy", protocol))
		} else {
			errs = append(errs, fmt.Sprintf(
				"protocol %s does not support access presets; use explicit rules with allow.method such as \"*\"", protocol))
		}
	}
	if isJSONRPC && !hasRules && ep.Access == "" {
		errs = append(errs, fmt.Sprintf("protocol %s requires explicit rules with allow.method", protocol))
	}
	if known && !isMCP && !isJSONRPC && !hasRules && ep.Access == "" {
		errs = append(errs, "protocol requires rules or access to define allowed traffic")
	}
	if isMCP && !hasRules && ep.Access == "" && !allowAll {
		errs = append(errs, "protocol mcp requires rules when mcp.allow_all_known_mcp_methods is false")
	}
	// MCP with allow_all_known_mcp_methods is exempt: normalization supplies
	// method "*".
	if rulesWouldDenyAll(ep.Rules) && (!isMCP || !allowAll) {
		errs = append(errs, "rules would deny all traffic (no allow clause found). Use `access: full` or add allow clauses to rules.")
	}
	if hasDenyRules && !known {
		errs = append(errs, "deny_rules require protocol (L7 inspection must be enabled)")
	}
	if hasDenyRules && !isMCP && !hasRules && ep.Access == "" {
		errs = append(errs, "deny_rules require rules or access to define the base allow set")
	}
	return errs
}

// endpointModes mirrors validate_endpoint_modes.
func endpointModes(tls, enforcement, access string) []string {
	var errs []string
	if tls != "" && tls != "skip" {
		errs = append(errs, fmt.Sprintf(
			"unknown tls value '%s'; omit the field to keep automatic TLS termination", tls))
	}
	switch enforcement {
	case "", "enforce", "audit":
	default:
		errs = append(errs, fmt.Sprintf("unknown enforcement value '%s' (expected enforce or audit)", enforcement))
	}
	switch access {
	case "", "read-only", "read-write", "full":
	default:
		errs = append(errs, fmt.Sprintf("unknown access value '%s' (expected read-only, read-write, or full)", access))
	}
	return errs
}

// l7OnlyFields mirrors additional_l7_profile_fields: the fields with a
// non-default value that explicit TCP rejects.
func l7OnlyFields(ep *Endpoint) []string {
	var present []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"enforcement", ep.Enforcement != ""},
		{"path", ep.Path != ""},
		{"allow_encoded_slash", ep.AllowEncodedSlash},
		{"websocket_credential_rewrite", ep.WebsocketCredentialRewrite},
		{"request_body_credential_rewrite", ep.RequestBodyCredentialRewrite},
		{"persisted_queries", ep.PersistedQueries != ""},
		{"graphql_persisted_queries", len(ep.GraphqlPersistedQueries) > 0},
		{"graphql_max_body_bytes", ep.GraphqlMaxBodyBytes > 0},
		{"json_rpc_max_body_bytes", ep.JSONRPCMaxBodyBytes > 0},
		{"mcp", ep.MCP != nil},
		{"credential_signing", ep.CredentialSigning != ""},
		{"signing_service", ep.SigningService != ""},
		{"signing_region", ep.SigningRegion != ""},
	} {
		if f.set {
			present = append(present, f.name)
		}
	}
	return present
}

// endpointIsValid mirrors endpoint_is_valid: a host and every port in range.
func endpointIsValid(ep *Endpoint) bool {
	if strings.TrimSpace(ep.Host) == "" {
		return false
	}
	if len(ep.Ports) > 0 {
		for _, p := range ep.Ports {
			if p < 1 || p > 65535 {
				return false
			}
		}
		return true
	}
	return ep.Port >= 1 && ep.Port <= 65535
}

// hasCredentialedEndpoints mirrors has_credentialed_endpoints: any declared
// credential, or any endpoint with credential signing.
func hasCredentialedEndpoints(p *Profile) bool {
	if len(p.Credentials) > 0 {
		return true
	}
	for _, ep := range p.Endpoints {
		if strings.TrimSpace(ep.CredentialSigning) != "" {
			return true
		}
	}
	return false
}

// profileHasTokenGrant reports whether any credential carries a token_grant
// block (has_token_grant upstream).
func profileHasTokenGrant(p *Profile) bool {
	for i := range p.Credentials {
		if p.Credentials[i].TokenGrant != nil {
			return true
		}
	}
	return false
}

// tokenGrantEndpoint applies the endpoint rules upstream adds when any
// credential carries a token_grant block: selectors free of control
// characters, and a transport that HTTP header injection can use (not protocol
// sql, not tls: skip).
func (l *linter) tokenGrantEndpoint(index int, ep *Endpoint) {
	field := fmt.Sprintf("endpoints[%d]", index)
	for _, sel := range []struct{ name, value string }{{"host", ep.Host}, {"path", ep.Path}} {
		if strings.ContainsFunc(sel.value, unicode.IsControl) {
			l.add(field+"."+sel.name, "token_grant endpoint selectors must not contain control characters")
		}
	}
	if strings.EqualFold(strings.TrimSpace(ep.Protocol), "sql") {
		l.add(field+".protocol", "token_grant credentials require HTTP header injection, which protocol sql does not support")
	}
	if strings.EqualFold(strings.TrimSpace(ep.TLS), "skip") {
		l.add(field+".tls", "token_grant credentials require HTTP header injection, which tls: skip bypasses; remove tls: skip")
	}
}

// lookupField returns the value of key in a decoded YAML mapping, handling both
// the ordered (yaml.MapSlice) and plain (map[string]any) forms, and whether the
// key is present.
func lookupField(v any, key string) (any, bool) {
	switch m := v.(type) {
	case yaml.MapSlice:
		for _, item := range m {
			if fmt.Sprint(item.Key) == key {
				return item.Value, true
			}
		}
	case map[string]any:
		val, ok := m[key]
		return val, ok
	}
	return nil, false
}

// rulesWouldDenyAll reports a non-empty rule list in which no rule has an
// effective allow clause.
func rulesWouldDenyAll(rules []Rule) bool {
	if len(rules) == 0 {
		return false
	}
	for _, r := range rules {
		if r.Allow != nil && !allowIsEffectivelyEmpty(r.Allow) {
			return false
		}
	}
	return true
}

// allowIsEffectivelyEmpty mirrors L7AllowProfile::is_effectively_empty.
func allowIsEffectivelyEmpty(a *Allow) bool {
	return a.Method == "" && a.Path == "" && a.Command == "" && a.OperationType == "" &&
		a.OperationName == "" && !hasToolSelector(a.Params, a.Tool)
}

// hasToolSelector reports an MCP tool selector: params.name or tool.
func hasToolSelector(params map[string]QueryMatcher, tool *QueryMatcher) bool {
	_, ok := params["name"]
	return ok || tool != nil
}

// parseL7Protocol mirrors L7Protocol::parse (case-insensitive).
func parseL7Protocol(protocol string) (string, bool) {
	switch p := strings.ToLower(protocol); p {
	case "rest", "websocket", "graphql", "sql", "json-rpc", "mcp":
		return p, true
	default:
		return "", false
	}
}

// isMCPProtocol reports protocol mcp, case-insensitively.
func isMCPProtocol(protocol string) bool {
	p, ok := parseL7Protocol(protocol)
	return ok && p == "mcp"
}

// isExplicitTCP reports protocol tcp, case-insensitively.
func isExplicitTCP(protocol string) bool {
	return strings.EqualFold(protocol, "tcp")
}

// isGraphqlOperationType reports query, mutation or subscription.
func isGraphqlOperationType(s string) bool {
	return s == "query" || s == "mutation" || s == "subscription"
}

// normalizeProfileID mirrors normalize_profile_id: trimmed and lowercased,
// or "" when the result is not a valid id.
func normalizeProfileID(input string) string {
	id := strings.ToLower(strings.TrimSpace(input))
	if !isValidProfileID(id) {
		return ""
	}
	return id
}

// isValidProfileID mirrors is_valid_profile_id: lowercase kebab-case.
func isValidProfileID(id string) bool {
	if id == "" || strings.HasPrefix(id, "-") || strings.HasSuffix(id, "-") {
		return false
	}
	for _, part := range strings.Split(id, "-") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			if c := part[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

// validCategory mirrors provider_profile_category_from_yaml.
func validCategory(raw string) bool {
	switch normalizeEnum(raw) {
	case "", "other", "inference", "agent", "source_control", "messaging", "data", "knowledge":
		return true
	default:
		return false
	}
}

// normalizeEnum applies the upstream enum normalization: trimmed,
// lowercased, '-' replaced by '_'.
func normalizeEnum(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "-", "_")
}

// usesReservedRevisionNamespace mirrors uses_reserved_revision_namespace in
// crates/openshell-core/src/secrets.rs: v<digits>_<KEY> and s<64 hex>_<KEY>.
func usesReservedRevisionNamespace(key string) bool {
	if rest, ok := strings.CutPrefix(key, "v"); ok {
		if rev, envKey, found := strings.Cut(rest, "_"); found && rev != "" && allBytes(rev, isDigit) &&
			envKey != "" && allBytes(envKey, isEnvKeyChar) {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(key, "s"); ok {
		if handle, envKey, found := strings.Cut(rest, "_"); found && len(handle) == 64 && allBytes(handle, isLowerHex) &&
			envKey != "" && allBytes(envKey, isEnvKeyChar) {
			return true
		}
	}
	return false
}

func allBytes(s string, pred func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !pred(s[i]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool    { return c >= '0' && c <= '9' }
func isLowerHex(c byte) bool { return isDigit(c) || (c >= 'a' && c <= 'f') }
func isEnvKeyChar(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// globUsesWildcard mirrors glob_uses_wildcard.
func globUsesWildcard(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// matcherUsesWildcard reports a wildcard in the glob or any entry.
func matcherUsesWildcard(m *QueryMatcher) bool {
	if globUsesWildcard(m.Glob) {
		return true
	}
	for _, s := range m.Any {
		if globUsesWildcard(s) {
			return true
		}
	}
	return false
}

// methodMatchesToolsCall mirrors method_matcher_matches_tools_call.
func methodMatchesToolsCall(method string) bool {
	return method == "tools/call" || method == "*" || globMatch(method, "tools/call")
}

// globMatch implements the subset of the Rust glob crate that MCP method
// matchers use: '*' matches any run of characters (separators included, as
// with the crate's default options), '?' one character, '[...]' a class
// with '!' negation. A '**' that is not a whole path component is an invalid
// pattern and matches nothing, as in the crate.
func globMatch(pattern, name string) bool {
	for _, comp := range strings.Split(pattern, "/") {
		if strings.Contains(comp, "**") && comp != "**" {
			return false
		}
	}
	return globMatchFrom(pattern, name)
}

func globMatchFrom(pattern, name string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			rest := strings.TrimLeft(pattern, "*")
			for i := 0; i <= len(name); i++ {
				if globMatchFrom(rest, name[i:]) {
					return true
				}
			}
			return false
		case '?':
			if name == "" {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		case '[':
			end := strings.IndexByte(pattern[1:], ']')
			if end < 0 || name == "" {
				return false
			}
			class := pattern[1 : end+1]
			negate := strings.HasPrefix(class, "!")
			if negate {
				class = class[1:]
			}
			if classMatches(class, name[0]) == negate {
				return false
			}
			pattern, name = pattern[end+2:], name[1:]
		default:
			if name == "" || pattern[0] != name[0] {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		}
	}
	return name == ""
}

// classMatches reports whether c is in a glob character class body.
func classMatches(class string, c byte) bool {
	for i := 0; i < len(class); i++ {
		if i+2 < len(class) && class[i+1] == '-' {
			if c >= class[i] && c <= class[i+2] {
				return true
			}
			i += 2
			continue
		}
		if class[i] == c {
			return true
		}
	}
	return false
}

// mapString returns the string value of key in a pass-through block. A
// missing key yields ("", true); a non-string value yields ("", false).
func mapString(m yaml.MapSlice, key string) (string, bool) {
	for _, item := range m {
		if fmt.Sprint(item.Key) != key {
			continue
		}
		if item.Value == nil {
			return "", true
		}
		s, ok := item.Value.(string)
		return s, ok
	}
	return "", true
}

// sortedKeys returns the keys of a string-keyed map in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
