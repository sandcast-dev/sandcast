package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/rhuss/sandcast/internal/harness"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/profile"
	"github.com/rhuss/sandcast/internal/redact"
	"github.com/rhuss/sandcast/internal/shell"
)

// Image paths of the Claude Code configuration. The final user's home can
// be /sandbox, which the OpenShell Kubernetes driver masks with the
// workspace volume, so the configuration lives under /opt and
// CLAUDE_CONFIG_DIR points Claude Code at it. CLAUDE_CONFIG_DIR relocates
// settings.json, .claude.json, plugins/ and skills/ (verified with Claude
// Code 2.1.294).
const (
	ConfigDirEnv    = "CLAUDE_CONFIG_DIR"
	ConfigDir       = "/opt/sandcast/claude-code"
	MarketplacesDir = "/opt/sandcast/claude-code-marketplaces"
)

// Build context paths of the files the scaffold copies into the image.
const (
	contextSkillsDir       = "harness/claude-code/skills"
	contextMarketplacesDir = "harness/claude-code/marketplaces"
)

// pluginsSubdir holds Claude Code's plugin state and marketplace clones
// below the home directory.
const pluginsSubdir = ".claude/plugins"

// settingsEnv is written into settings.json "env" so that every session
// runs without update and telemetry traffic: the binary is installed by
// mise into a read-only location, and the sandbox policy does not grant the
// telemetry endpoints by default.
var settingsEnv = map[string]string{
	"DISABLE_AUTOUPDATER":     "1",
	"DISABLE_ERROR_REPORTING": "1",
	"DISABLE_TELEMETRY":       "1",
}

// mergeJQ merges sandcast's JSON into an existing configuration file. The
// first part is the verified array-preserving expression of brainstorm 072
// (change 9); the tail drops the null hooks key it adds when the existing
// file has no hooks.
const mergeJQ = `.[0] as $orig | $orig * .[1] | .hooks = $orig.hooks | if .hooks == null then del(.hooks) else . end`

var (
	pluginNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(@[A-Za-z0-9][A-Za-z0-9._-]*)?$`)
	dirNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	userNameRe   = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_.-]*\$?|[0-9]+)$`)
)

var harnessTemplate = template.Must(template.New(plan.SectionHarness).Parse(`# {{.Section}}: Claude Code with its configuration in {{.ConfigDir}}
# ({{.ConfigDirEnv}}), outside the home directory, which the OpenShell
# Kubernetes driver masks with the workspace volume.
USER root
ENV {{.ConfigDirEnv}}={{.ConfigDir}}
RUN install -d -o {{.User}} -g {{.Group}} {{.ConfigDir}} {{.ConfigDir}}/skills
{{- if .Skills}}
COPY {{.ContextSkills}}/ {{.ConfigDir}}/skills/
RUN chown -R {{.User}}:{{.Group}} {{.ConfigDir}}/skills
{{- end}}
{{- if .LocalMarketplaces}}
COPY {{.ContextMarketplaces}}/ {{.MarketplacesDir}}/
RUN chmod -R a+rX {{.MarketplacesDir}}
{{- end}}
USER {{.User}}
RUN <<'SANDCAST_EOF'
set -eu
# Write a configuration file, or merge into an existing one with the
# array-preserving jq expression of brainstorm 072.
merge_json() {
  if [ -s "$1" ]; then
    command -v jq >/dev/null 2>&1 || { echo "sandcast: jq is needed to merge $1" >&2; exit 1; }
    jq -s '{{.MergeJQ}}' "$1" "$2" > "$1.sandcast-tmp"
    mv "$1.sandcast-tmp" "$1"
  else
    cp "$2" "$1"
  fi
  rm -f "$2"
}
cat > /tmp/sandcast-settings.json <<'SANDCAST_JSON'
{{.Settings}}
SANDCAST_JSON
merge_json {{.ConfigDir}}/settings.json /tmp/sandcast-settings.json
cat > /tmp/sandcast-claude.json <<'SANDCAST_JSON'
{{.ClaudeJSON}}
SANDCAST_JSON
merge_json {{.ConfigDir}}/.claude.json /tmp/sandcast-claude.json
SANDCAST_EOF
{{- if .Plugins}}
RUN claude plugin marketplace add {{.OfficialMarketplace}}
{{- range .Marketplaces}}
RUN claude plugin marketplace add {{.}}
{{- end}}
{{- range .Plugins}}
RUN claude plugin install {{.}} --scope user
{{- end}}
{{- end}}
USER root
`))

// mcpEntry is one mcpServers entry of the image's .claude.json.
type mcpEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Scaffold renders the 70-harness section for Claude Code (FR-038, US4
// scenario 2). As root it creates the configuration directory owned by the
// final user and copies the skills; as the final user it writes
// settings.json (non-interactive env) and .claude.json (onboarding done,
// MCP servers), adds the official marketplace and every other marketplace
// the plugins come from, and installs one plugin per manifest entry. It
// ends as root, so the next section starts as root.
//
// MCP servers are written with references, never values: an env value that
// is a ${SANDCAST_SECRET_N} placeholder becomes ${KEY}, so Claude Code reads
// the credential from the runtime variable that the generated profile
// declares (profile.MCPCredentialVar). A url server's env credential is
// sent as a bearer Authorization header. Placeholders in command arguments
// and urls stay as they are and are read from SANDCAST_SECRET_N at runtime.
func (Adapter) Scaffold(in harness.ScaffoldInput) (*harness.Scaffold, error) {
	user := in.User
	if user.Group == "" {
		user.Group = user.Name
	}
	if !userNameRe.MatchString(user.Name) || !userNameRe.MatchString(user.Group) {
		return nil, fmt.Errorf("claude-code scaffold: invalid final user %q, group %q", user.Name, user.Group)
	}
	files := map[string]string{}

	// Skill, plugin and MCP server problems come from the manifest.
	skills, err := skillFiles(in, files)
	if err != nil {
		return nil, harness.Invalid(err)
	}
	plugins, marketplaces, local, err := pluginCommands(in, files)
	if err != nil {
		return nil, harness.Invalid(err)
	}
	settings, err := marshalJSON(map[string]any{"env": settingsEnv})
	if err != nil {
		return nil, err
	}
	claudeJSON, err := claudeConfig(in.Harness.MCPServers)
	if err != nil {
		return nil, harness.Invalid(err)
	}

	var buf bytes.Buffer
	err = harnessTemplate.Execute(&buf, map[string]any{
		"Section":             plan.SectionHarness,
		"ConfigDirEnv":        ConfigDirEnv,
		"ConfigDir":           ConfigDir,
		"MarketplacesDir":     MarketplacesDir,
		"ContextSkills":       contextSkillsDir,
		"ContextMarketplaces": contextMarketplacesDir,
		"User":                user.Name,
		"Group":               user.Group,
		"Skills":              skills,
		"LocalMarketplaces":   local,
		"MergeJQ":             mergeJQ,
		"Settings":            settings,
		"ClaudeJSON":          claudeJSON,
		"OfficialMarketplace": OfficialMarketplaceSource,
		"Marketplaces":        marketplaces,
		"Plugins":             plugins,
	})
	if err != nil {
		return nil, fmt.Errorf("render %s fragment: %w", plan.SectionHarness, err)
	}

	out := &harness.Scaffold{
		Fragment:  buf.String(),
		Env:       []plan.EnvVar{{Name: ConfigDirEnv, Value: ConfigDir}},
		Dirs:      []plan.Dir{{Path: ConfigDir, Owner: "user"}},
		ReadWrite: []string{ConfigDir},
	}
	if len(files) > 0 {
		out.Files = files
	}
	if local {
		out.ReadOnly = []string{MarketplacesDir}
	}
	return out, nil
}

// skillFiles maps every manifest skill to its build context directory and
// reports whether there are any.
func skillFiles(in harness.ScaffoldInput, files map[string]string) (bool, error) {
	for _, s := range in.Harness.Skills {
		if !dirNameRe.MatchString(s.Name) {
			return false, fmt.Errorf("claude-code skill %q: name must be a single directory name", s.Name)
		}
		key := path.Join(contextSkillsDir, s.Name)
		if _, dup := files[key]; dup {
			return false, fmt.Errorf("claude-code skill %q is listed twice", s.Name)
		}
		src, err := resolveSource(s.Source, in.Home, in.Project)
		if errors.Is(err, errHostOnlySource) {
			proj := path.Join(skillsSubdir, s.Name)
			return false, fmt.Errorf("claude-code skill %s: %w; copy the skill directory into the project as %s, commit it, and set the skill source in the manifest to %s",
				s.Name, err, proj, proj)
		}
		if err != nil {
			return false, fmt.Errorf("claude-code skill %s: %w", s.Name, err)
		}
		files[key] = src
	}
	return len(in.Harness.Skills) > 0, nil
}

// pluginCommands validates the plugins and returns the install arguments,
// the marketplace sources to add besides the official one (in first-use
// order), and whether a local marketplace is copied into the image.
func pluginCommands(in harness.ScaffoldInput, files map[string]string) (plugins, marketplaces []string, local bool, err error) {
	for _, p := range in.Harness.Plugins {
		if !pluginNameRe.MatchString(p.Name) {
			return nil, nil, false, fmt.Errorf("claude-code plugin %q: want name or name@marketplace", p.Name)
		}
		plugins = append(plugins, p.Name)
		source := strings.TrimSpace(p.Source)
		// The official marketplace is added before every install; a manifest
		// may name it by its source or by its marketplace name.
		if source == "" || source == OfficialMarketplaceSource || source == OfficialMarketplace {
			continue
		}
		if strings.ContainsFunc(source, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return nil, nil, false, fmt.Errorf("claude-code plugin %s: marketplace source contains control characters", p.Name)
		}
		if strings.HasPrefix(source, "-") {
			// Quoting does not help: "claude plugin marketplace add" would
			// read it as an option.
			return nil, nil, false, fmt.Errorf("claude-code plugin %s: marketplace source must not start with -", p.Name)
		}
		if err := checkSourceCredentials(p.Name, source); err != nil {
			return nil, nil, false, err
		}
		if isPathSource(source) {
			name, err := localMarketplaceName(p.Name, source)
			if err != nil {
				return nil, nil, false, err
			}
			resolved, err := resolveSource(source, in.Home, in.Project)
			if errors.Is(err, errHostOnlySource) {
				return nil, nil, false, fmt.Errorf("claude-code plugin %s: %w; copy the marketplace directory into the project, commit it, and set the plugin source to its ./ path, "+
					"or publish the marketplace as a git repository and use owner/repo or its URL", p.Name, err)
			}
			if err != nil {
				return nil, nil, false, fmt.Errorf("claude-code plugin %s: %w", p.Name, err)
			}
			key := path.Join(contextMarketplacesDir, name)
			if prev, ok := files[key]; ok && prev != resolved {
				return nil, nil, false, fmt.Errorf("claude-code plugin %s: marketplace %s comes from two directories", p.Name, name)
			}
			files[key] = resolved
			source = path.Join(MarketplacesDir, name)
			local = true
		}
		if quoted := shell.QuoteIfNeeded(source); !slices.Contains(marketplaces, quoted) {
			marketplaces = append(marketplaces, quoted)
		}
	}
	return plugins, marketplaces, local, nil
}

// checkSourceCredentials refuses a marketplace source that would put a
// credential into the Containerfile. It applies the rule discovery redacts
// with (harness.Redactor.URL: credential shapes, URL user information other
// than the git user, secret-looking query values) and also refuses a
// placeholder, which "claude plugin marketplace add" cannot expand.
func checkSourceCredentials(plugin, source string) error {
	if sourceCarriesCredential(source) {
		return fmt.Errorf("claude-code plugin %s: the marketplace source carries a credential or placeholder; use a URL without credentials", plugin)
	}
	return nil
}

// sourceCarriesCredential is the one rule for marketplace sources, used by
// discovery and the scaffold: anything discovery's redactor would replace
// (credential shapes, URL user information other than the git user, secret
// query values) or a placeholder, which "claude plugin marketplace add"
// cannot expand.
func sourceCarriesCredential(source string) bool {
	_, reds := harness.NewRedactor().URL("source", source)
	return len(reds) > 0 || strings.Contains(source, "${")
}

// isPathSource reports a marketplace source that is a host path rather than
// owner/repo or a URL.
func isPathSource(source string) bool {
	for _, p := range []string{"/", "~/", "./", "../"} {
		if strings.HasPrefix(source, p) {
			return true
		}
	}
	return false
}

// localMarketplaceName names the image directory of a local marketplace:
// the marketplace part of name@marketplace, else the source's base name.
func localMarketplaceName(plugin, source string) (string, error) {
	_, name, ok := strings.Cut(plugin, "@")
	if !ok {
		name = path.Base(filepath.ToSlash(source))
	}
	if strings.HasSuffix(source, ".json") {
		return "", fmt.Errorf("claude-code plugin %s: local marketplace %s is a file; use its directory", plugin, source)
	}
	if !dirNameRe.MatchString(name) {
		return "", fmt.Errorf("claude-code plugin %s: no usable marketplace directory name in %q", plugin, source)
	}
	return name, nil
}

// errHostOnlySource marks a source outside the project tree that does not
// exist on this host.
var errHostOnlySource = errors.New("a source outside the project exists only on the machine that captured the manifest, " +
	"so plan and build fail in CI and on every other machine")

// resolveSource resolves a manifest source on the host, follows symlinks,
// and accepts the result only strictly inside the project tree or inside
// the harness's own ~/.claude/skills and ~/.claude/plugins directories. The
// scaffold copies sources into the image, so anything else, such as ~/.ssh
// or a symlink out of the project, is an error. The returned path is the
// symlink-free one. A missing source outside the project, such as a user
// skill on a CI runner, is an errHostOnlySource error.
func resolveSource(src, home, project string) (string, error) {
	p, err := hostPath(src, home, project)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) && (project == "" || !strictlyInside(project, p)) {
		return "", fmt.Errorf("source %s does not exist on this host: %w", src, errHostOnlySource)
	}
	if err != nil {
		return "", fmt.Errorf("source %s: %w", src, err)
	}
	for _, root := range sourceRoots(home, project) {
		if strictlyInside(root, real) {
			return real, nil
		}
	}
	return "", fmt.Errorf("source %s resolves to %s, outside the project, ~/%s and ~/%s; only those are copied into the image",
		src, real, skillsSubdir, pluginsSubdir)
}

// sourceRoots returns the symlink-free directories sources may come from.
// The project counts only when it is not the home directory or one of its
// ancestors, so that a project at ~ does not open the whole home.
func sourceRoots(home, project string) []string {
	var roots []string
	realHome := ""
	if home != "" {
		realHome, _ = filepath.EvalSymlinks(home)
		for _, sub := range []string{skillsSubdir, pluginsSubdir} {
			if r, err := filepath.EvalSymlinks(filepath.Join(home, sub)); err == nil {
				roots = append(roots, r)
			}
		}
	}
	if project != "" {
		if r, err := filepath.EvalSymlinks(project); err == nil && (realHome == "" || (r != realHome && !strictlyInside(r, realHome))) {
			roots = append(roots, r)
		}
	}
	return roots
}

// strictlyInside reports whether path is below root, not root itself.
func strictlyInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hostPath resolves a manifest source without following symlinks: "~/"
// against the home directory, a relative path against the project, an
// absolute path as is.
func hostPath(src, home, project string) (string, error) {
	switch {
	case src == "":
		return "", errors.New("source is empty")
	case src == "~" || strings.HasPrefix(src, "~/"):
		if home == "" {
			return "", fmt.Errorf("source %s needs the home directory", src)
		}
		return filepath.Join(home, strings.TrimPrefix(src, "~")), nil
	case filepath.IsAbs(src):
		return filepath.Clean(src), nil
	default:
		if project == "" {
			return "", fmt.Errorf("source %s needs the project directory", src)
		}
		return filepath.Join(project, src), nil
	}
}

// claudeConfig renders the image's .claude.json: onboarding marked complete
// and the manifest MCP servers.
func claudeConfig(servers []manifest.MCPServer) (string, error) {
	cfg := map[string]any{"hasCompletedOnboarding": true}
	entries := map[string]mcpEntry{}
	for _, s := range servers {
		if s.Name == "" {
			return "", errors.New("claude-code MCP server without a name")
		}
		if _, dup := entries[s.Name]; dup {
			return "", fmt.Errorf("claude-code MCP server %s is listed twice", s.Name)
		}
		e, err := mcpServerEntry(s)
		if err != nil {
			return "", err
		}
		entries[s.Name] = e
	}
	if len(entries) > 0 {
		cfg["mcpServers"] = entries
	}
	out, err := marshalJSON(cfg)
	if err != nil {
		return "", err
	}
	// Last line of defence: a credential shape anywhere, such as a token in
	// a command argument, never reaches the image.
	if hits := redact.Scan(out); len(hits) > 0 {
		return "", fmt.Errorf("claude-code MCP configuration carries a credential (%s, %s); replace it with a ${SANDCAST_SECRET_N} placeholder",
			hits[0].Kind, hits[0].Excerpt)
	}
	return out, nil
}

// mcpServerEntry renders one server. See Scaffold for how credentials are
// referenced. A server that discovery would have redacted (a credential in
// the url, a command argument or an env value) is refused, so a hand-edited
// manifest meets the same rule as a discovered one.
func mcpServerEntry(s manifest.MCPServer) (mcpEntry, error) {
	if reds := literalCredentials(s); len(reds) > 0 {
		r := reds[0]
		field := strings.TrimPrefix(r.Path, "mcp_servers["+s.Name+"].")
		// Name the env key the value is under: Redaction.EnvVar is the
		// runtime variable, which for a partly redacted value is the
		// placeholder's SANDCAST_SECRET_N, not a key of the manifest.
		if key, ok := strings.CutPrefix(field, "env."); ok {
			return mcpEntry{}, literalCredentialError(s.Name, key)
		}
		return mcpEntry{}, fmt.Errorf("claude-code MCP server %s: %s holds a literal credential (%s); "+
			"replace it with a ${SANDCAST_SECRET_N} placeholder or a ${VAR} reference and supply the value at runtime",
			s.Name, field, r.Kind)
	}
	switch {
	case s.URL != "" && len(s.Command) > 0:
		return mcpEntry{}, fmt.Errorf("claude-code MCP server %s has both a command and a url", s.Name)
	case len(s.Command) > 0:
		e := mcpEntry{Type: "stdio", Command: s.Command[0], Args: s.Command[1:]}
		if len(s.Env) > 0 {
			e.Env = map[string]string{}
			for _, k := range manifest.SortedKeys(s.Env) {
				e.Env[k] = envReference(k, s.Env[k])
			}
		}
		return e, nil
	case s.URL != "":
		e := mcpEntry{Type: urlTransport(s.URL), URL: s.URL}
		switch len(s.Env) {
		case 0:
		case 1:
			for k, v := range s.Env {
				if _, ok := profile.MCPCredentialVar(k, v); !ok {
					return mcpEntry{}, literalCredentialError(s.Name, k)
				}
				e.Headers = map[string]string{"Authorization": "Bearer " + envReference(k, v)}
			}
		default:
			return mcpEntry{}, fmt.Errorf("claude-code MCP server %s: a url server carries one bearer credential in env, found %d entries",
				s.Name, len(s.Env))
		}
		return e, nil
	default:
		return mcpEntry{}, fmt.Errorf("claude-code MCP server %s needs a command or a url", s.Name)
	}
}

// literalCredentials dry-runs discovery's redactor on a copy of s and returns
// what it would have replaced.
func literalCredentials(s manifest.MCPServer) []harness.Redaction {
	probe := harness.MCPServer{MCPServer: s}
	probe.Command = slices.Clone(s.Command)
	probe.Env = maps.Clone(s.Env)
	return harness.NewRedactor().Server(&probe)
}

// literalCredentialError refuses a literal value where a credential belongs,
// naming the key and never the value.
func literalCredentialError(server, key string) error {
	return fmt.Errorf("claude-code MCP server %s: env %s holds a literal credential; "+
		"replace it with a ${SANDCAST_SECRET_N} placeholder or a ${VAR} reference and supply the value at runtime", server, key)
}

// envReference renders an env value for the image configuration: a
// credential becomes a reference to the runtime variable that carries it,
// anything else stays as it is.
func envReference(key, value string) string {
	if name, ok := profile.MCPCredentialVar(key, value); ok {
		return "${" + name + "}"
	}
	return value
}

// urlTransport is sse for a url whose path ends in /sse, the convention of
// SSE servers, and http (streamable HTTP) otherwise.
func urlTransport(raw string) string {
	p, _, _ := strings.Cut(raw, "?")
	p, _, _ = strings.Cut(p, "#")
	if strings.HasSuffix(strings.TrimSuffix(p, "/"), "/sse") {
		return "sse"
	}
	return "http"
}

// marshalJSON encodes v indented with sorted keys and without HTML escaping.
func marshalJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("encode claude-code configuration: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
