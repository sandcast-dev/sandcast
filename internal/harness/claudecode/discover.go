package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/harness"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/redact"
)

// Host files Discover reads, relative to the home or the project directory.
const (
	userConfigFile        = ".claude.json"
	projectMCPFile        = ".mcp.json"
	installedPluginsFile  = ".claude/plugins/installed_plugins.json"
	knownMarketplacesFile = ".claude/plugins/known_marketplaces.json"
	userSettingsFile      = ".claude/settings.json"
)

// OfficialMarketplace is the plugin marketplace Anthropic publishes. A fresh
// Claude Code installation has no marketplace configured (brainstorm 072,
// failure 7), so the scaffold adds it before any plugin install.
const (
	OfficialMarketplace       = "claude-plugins-official"
	OfficialMarketplaceSource = "anthropics/claude-plugins-official"
)

// sandcastSkillPrefix marks sandcast's own skills, which are never baked.
const sandcastSkillPrefix = "sandcast-"

// MCP server scopes in Claude Code precedence order, lowest first: local
// (per project in ~/.claude.json) overrides project (.mcp.json), which
// overrides user (top-level in ~/.claude.json).
const (
	scopeUser = iota
	scopeProject
	scopeLocal
)

var envNameCleanRe = regexp.MustCompile(`[^A-Z0-9]+`)

// serverJSON is one mcpServers entry of a Claude Code configuration file.
type serverJSON struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// configJSON is the part of ~/.claude.json and .mcp.json discovery reads.
type configJSON struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Projects   map[string]struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	} `json:"projects"`
}

// rawServer is a server as configured on the host, before redaction.
type rawServer struct {
	name   string
	origin string
	scope  int
	raw    json.RawMessage
}

// pluginInstall is one installation record in installed_plugins.json.
type pluginInstall struct {
	Scope       string `json:"scope"`
	ProjectPath string `json:"projectPath"`
}

// marketplaceJSON is one entry of known_marketplaces.json.
type marketplaceJSON struct {
	Source struct {
		Source string `json:"source"`
		Repo   string `json:"repo"`
		URL    string `json:"url"`
		Path   string `json:"path"`
	} `json:"source"`
}

// Discover lists the MCP servers, plugins and skills configured for Claude
// Code on the host (FR-038):
//
//   - MCP servers from ~/.claude.json (top level and the entry for the
//     project) and <project>/.mcp.json, keeping Claude Code's precedence
//     when a name repeats. stdio servers become a command; http and sse
//     servers a url, with a bearer Authorization header turned into an env
//     credential (see headerEnv).
//   - Plugins installed for the user or for the project, from
//     ~/.claude/plugins/installed_plugins.json, as name@marketplace with the
//     marketplace source from known_marketplaces.json.
//   - Skills in ~/.claude/skills and <project>/.claude/skills, except
//     sandcast's own.
//
// Every credential is replaced by a ${SANDCAST_SECRET_N} placeholder before
// the server leaves this function and is reported in Redacted. Redacted
// EnvVar names the variable the image configuration reads at runtime. All
// lists are sorted.
func (Adapter) Discover(ctx context.Context, env harness.Env) (*harness.Discovery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if env.Home == "" {
		return nil, errors.New("claude-code discovery: home directory unknown")
	}
	d := &harness.Discovery{
		Harness:    Name,
		MCPServers: []harness.MCPServer{},
		Plugins:    []harness.Extension{},
		Skills:     []harness.Extension{},
		Redacted:   []harness.Redaction{},
		Notes:      []string{},
	}

	raws, err := readServers(env)
	if err != nil {
		return nil, err
	}
	redactor := harness.NewRedactor()
	for _, r := range resolveServers(raws, &d.Notes) {
		s, ok := convertServer(r, &d.Notes)
		if !ok {
			continue
		}
		for _, red := range redactor.Server(&s) {
			if red.EnvVar == "" {
				// Claude Code expands ${VAR} in args and urls from its
				// environment, so the placeholder itself names the variable.
				red.EnvVar = harness.PlaceholderVar(red.Placeholder)
			}
			d.Redacted = append(d.Redacted, red)
		}
		d.MCPServers = append(d.MCPServers, s)
	}

	if d.Plugins, err = discoverPlugins(ctx, env, &d.Notes); err != nil {
		return nil, err
	}
	if d.Skills, err = discoverSkills(env, &d.Notes); err != nil {
		return nil, err
	}
	for _, list := range [][]harness.Extension{d.Plugins, d.Skills} {
		for i := range list {
			list[i].Portable, list[i].Reason = portability(list[i].Source)
		}
	}
	slices.Sort(d.Notes)
	return d, nil
}

// readServers collects the raw server entries of every configuration file.
func readServers(env harness.Env) ([]rawServer, error) {
	var out []rawServer
	add := func(servers map[string]json.RawMessage, origin string, scope int) {
		for name, raw := range servers {
			out = append(out, rawServer{name: name, origin: origin, scope: scope, raw: raw})
		}
	}

	var user configJSON
	if _, err := readJSON(filepath.Join(env.Home, userConfigFile), &user); err != nil {
		return nil, err
	}
	add(user.MCPServers, "~/"+userConfigFile, scopeUser)
	if env.Project != "" {
		if key, ok := projectKey(user.Projects, env.Project); ok {
			add(user.Projects[key].MCPServers, fmt.Sprintf("~/%s projects[%s]", userConfigFile, key), scopeLocal)
		}
		var project configJSON
		if _, err := readJSON(filepath.Join(env.Project, projectMCPFile), &project); err != nil {
			return nil, err
		}
		add(project.MCPServers, projectMCPFile, scopeProject)
	}
	return out, nil
}

// projectKey finds the projects entry of ~/.claude.json for the project
// directory, which Claude Code keys by absolute path.
func projectKey[V any](projects map[string]V, project string) (string, bool) {
	candidates := []string{filepath.Clean(project)}
	if real, err := filepath.EvalSymlinks(project); err == nil {
		candidates = append(candidates, real)
	}
	for _, c := range candidates {
		if _, ok := projects[c]; ok {
			return c, true
		}
	}
	return "", false
}

// resolveServers keeps one server per name, the one with the highest
// precedence, notes the shadowed ones, and sorts by name.
func resolveServers(raws []rawServer, notes *[]string) []rawServer {
	slices.SortFunc(raws, func(a, b rawServer) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		return b.scope - a.scope
	})
	var out []rawServer
	for _, r := range raws {
		if n := len(out); n > 0 && out[n-1].name == r.name {
			*notes = append(*notes, fmt.Sprintf("MCP server %s in %s is shadowed by the one in %s", r.name, r.origin, out[n-1].origin))
			continue
		}
		out = append(out, r)
	}
	return out
}

// convertServer turns a host server entry into manifest shape. Secrets are
// still in it; the caller redacts before anything else sees the server.
func convertServer(r rawServer, notes *[]string) (harness.MCPServer, bool) {
	var cfg serverJSON
	if err := json.Unmarshal(r.raw, &cfg); err != nil {
		*notes = append(*notes, fmt.Sprintf("MCP server %s in %s skipped: unsupported entry (%v)", r.name, r.origin, err))
		return harness.MCPServer{}, false
	}
	s := harness.MCPServer{MCPServer: manifest.MCPServer{Name: r.name}, Origin: r.origin}
	switch cfg.Type {
	case "", "stdio", "http", "sse":
	default:
		*notes = append(*notes, fmt.Sprintf("MCP server %s in %s skipped: transport %q cannot be baked", r.name, r.origin, cfg.Type))
		return harness.MCPServer{}, false
	}
	switch {
	case cfg.URL != "":
		s.URL = cfg.URL
		s.Env = headerEnv(r.name, cfg.Headers, notes)
	case cfg.Command != "":
		s.Command = append([]string{cfg.Command}, cfg.Args...)
		if len(cfg.Env) > 0 {
			s.Env = maps.Clone(cfg.Env)
		}
	default:
		*notes = append(*notes, fmt.Sprintf("MCP server %s in %s skipped: neither a command nor a url", r.name, r.origin))
		return harness.MCPServer{}, false
	}
	return s, true
}

// headerEnv turns the headers of a url server into manifest env entries.
// The manifest has no header field: a url server carries one bearer
// credential in env, which the scaffold sends as the Authorization header.
// A header value that references a variable keeps that variable; a literal
// token goes under MCP_<NAME>_TOKEN, which the redactor then replaces with a
// placeholder. Other headers are not baked and get a note.
func headerEnv(server string, headers map[string]string, notes *[]string) map[string]string {
	env := map[string]string{}
	for _, h := range manifest.SortedKeys(headers) {
		if !strings.EqualFold(h, "Authorization") {
			*notes = append(*notes, fmt.Sprintf(
				"MCP server %s: header %s is not baked; the image sends only a bearer Authorization header", server, h))
			continue
		}
		cred := strings.TrimSpace(headers[h])
		if scheme, rest, ok := strings.Cut(cred, " "); ok {
			cred = strings.TrimSpace(rest)
			if !strings.EqualFold(scheme, "Bearer") {
				*notes = append(*notes, fmt.Sprintf(
					"MCP server %s: the %s authorization scheme is sent as a bearer token in the image", server, scheme))
			}
		}
		if name, ok := redact.EnvRef(cred); ok {
			env[name] = "${" + name + "}"
			continue
		}
		env[tokenEnvVar(server)] = cred
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// tokenEnvVar names the env credential of a url server: MCP_<NAME>_TOKEN.
func tokenEnvVar(server string) string {
	return "MCP_" + strings.Trim(envNameCleanRe.ReplaceAllString(strings.ToUpper(server), "_"), "_") + "_TOKEN"
}

// discoverPlugins lists the plugins installed for the user or the project.
func discoverPlugins(ctx context.Context, env harness.Env, notes *[]string) ([]harness.Extension, error) {
	var installed struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	found, err := readJSON(filepath.Join(env.Home, installedPluginsFile), &installed)
	if err != nil || !found {
		return []harness.Extension{}, err
	}
	var markets map[string]marketplaceJSON
	if _, err := readJSON(filepath.Join(env.Home, knownMarketplacesFile), &markets); err != nil {
		return nil, err
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if _, err := readJSON(filepath.Join(env.Home, userSettingsFile), &settings); err != nil {
		return nil, err
	}

	origin := "~/" + installedPluginsFile
	out := []harness.Extension{}
	for _, id := range slices.Sorted(maps.Keys(installed.Plugins)) {
		installs, err := parseInstalls(installed.Plugins[id])
		if err != nil {
			*notes = append(*notes, fmt.Sprintf("plugin %s skipped: unsupported entry in %s (%v)", id, origin, err))
			continue
		}
		if !slices.ContainsFunc(installs, func(in pluginInstall) bool { return installedFor(in, env.Project) }) {
			continue
		}
		if enabled, set := settings.EnabledPlugins[id]; set && !enabled {
			*notes = append(*notes, fmt.Sprintf("plugin %s is disabled in ~/%s; skipped", id, userSettingsFile))
			continue
		}
		_, market, _ := strings.Cut(id, "@")
		source, remote := marketplaceSource(ctx, id, market, markets, env, notes)
		if source == "" {
			continue
		}
		out = append(out, harness.Extension{Name: id, Source: source, Origin: origin, Remote: remote})
	}
	return out, nil
}

// parseInstalls accepts the list of installs (version 2) or a single one.
func parseInstalls(raw json.RawMessage) ([]pluginInstall, error) {
	var list []pluginInstall
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var one pluginInstall
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, err
	}
	return []pluginInstall{one}, nil
}

// installedFor reports a user install, or a project or local install for
// project.
func installedFor(in pluginInstall, project string) bool {
	switch in.Scope {
	case "user":
		return true
	case "project", "local":
		return project != "" && in.ProjectPath != "" && samePath(in.ProjectPath, project)
	}
	return false
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// marketplaceSource returns what "claude plugin marketplace add" accepts for
// the marketplace of plugin: owner/repo, a git or https URL without
// credentials, or a local directory the scaffold may copy, shown as ./ in
// the project or ~/ in the home directory. A local directory the scaffold
// cannot copy is proposed through its git remote instead, with the remote's
// state (FR-019). It is "" with a note when the plugin cannot be baked.
func marketplaceSource(ctx context.Context, plugin, name string, markets map[string]marketplaceJSON, env harness.Env, notes *[]string) (string, *harness.RemoteInfo) {
	skip := func(format string, args ...any) (string, *harness.RemoteInfo) {
		*notes = append(*notes, fmt.Sprintf("plugin %s skipped: ", plugin)+fmt.Sprintf(format, args...))
		return "", nil
	}
	m, ok := markets[name]
	if !ok {
		if name == OfficialMarketplace {
			return OfficialMarketplaceSource, nil
		}
		return skip("the source of marketplace %q is unknown", name)
	}
	switch m.Source.Source {
	case "github":
		return m.Source.Repo, nil
	case "git", "url":
		// User information in a clone URL is a credential (a password or a
		// token as the user name); only the plain SSH user "git" is kept.
		source := m.Source.URL
		if clean, userinfo, had := harness.SplitUserinfo(source); had && userinfo != "git" {
			source = clean
			*notes = append(*notes, fmt.Sprintf(
				"plugin %s: credentials removed from the URL of marketplace %q; the image clones it without them", plugin, name))
		}
		if sourceCarriesCredential(source) {
			return skip("the URL of marketplace %q carries a credential", name)
		}
		return source, nil
	case "directory", "file":
		source := displayPath(m.Source.Path, env.Home, env.Project)
		if _, err := localMarketplaceName(plugin, source); err != nil {
			return skip("%v", err)
		}
		_, err := resolveSource(source, env.Home, env.Project)
		if errors.Is(err, errHostOnlySource) {
			return skip("the directory %s of local marketplace %q does not exist", source, name)
		}
		if err != nil {
			dir, perr := hostPath(source, env.Home, env.Project)
			if perr == nil {
				remoteSource, remote, gerr := gitMarketplace(ctx, env.Git, dir, name, plugin, notes)
				if gerr == nil {
					return remoteSource, remote
				}
				switch {
				case errors.Is(gerr, errRemoteCredential):
					return skip("the git remote URL of local marketplace %q carries a credential that cannot be removed", name)
				case errors.Is(gerr, errNotRepoTop):
					return skip("local marketplace %q cannot be copied into the image, and %s is not the top level of a git repository, so no remote is proposed: %v", name, source, err)
				case errors.Is(gerr, errGitUnavailable):
					return skip("local marketplace %q cannot be copied into the image, and git is not available to propose its remote: %v", name, err)
				}
			}
			return skip("local marketplace %q cannot be copied into the image and has no git remote: %v", name, err)
		}
		return source, nil
	}
	return skip("marketplace %q has the unsupported source type %q", name, m.Source.Source)
}

var (
	githubHTTPSRe = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)
	githubSSHRe   = regexp.MustCompile(`^(?:ssh://)?git@github\.com[:/]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)
)

// githubRepo maps a GitHub remote URL to owner/repo.
func githubRepo(url string) (string, bool) {
	for _, re := range []*regexp.Regexp{githubHTTPSRe, githubSSHRe} {
		if m := re.FindStringSubmatch(url); m != nil {
			return m[1] + "/" + m[2], true
		}
	}
	return "", false
}

// The reasons gitMarketplace proposes no source.
var (
	errGitUnavailable   = errors.New("git is not available")
	errNotRepoTop       = errors.New("not the top level of a git repository")
	errNoGitRemote      = errors.New("no git remote")
	errRemoteCredential = errors.New("the git remote URL carries a credential")
)

// gitMarketplace proposes a local marketplace directory through its git
// remote (R-18): origin when present, else the first remote. The source is
// owner/repo for GitHub and the remote URL, without credentials, otherwise.
// It reads local git state only and never fetches: unpushed commits against
// <remote>/HEAD (else the upstream branch), uncommitted changes, and the
// marketplace name in the remote's .claude-plugin/marketplace.json compared
// with the locally registered name. Without a remote-tracking ref the
// unpushed count stays nil (unknown). It returns errGitUnavailable without
// git, errNotRepoTop when dir lies inside a repository without being its
// top level (that repository is not the marketplace), errNoGitRemote
// outside a repository or without a remote, and errRemoteCredential when
// the remote URL keeps a credential after the user information is removed.
func gitMarketplace(ctx context.Context, git harness.Git, dir, name, plugin string, notes *[]string) (string, *harness.RemoteInfo, error) {
	if git == nil {
		return "", nil, errGitUnavailable
	}
	top, err := git.Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", nil, errGitUnavailable
		}
		return "", nil, errNoGitRemote
	}
	if !sameDir(top, dir) {
		return "", nil, errNotRepoTop
	}
	out, err := git.Run(ctx, dir, "remote")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", nil, errNoGitRemote
	}
	remotes := strings.Fields(out)
	remote := remotes[0]
	if slices.Contains(remotes, "origin") {
		remote = "origin"
	}
	if strings.HasPrefix(remote, "-") {
		return "", nil, errNoGitRemote
	}
	url, err := git.Run(ctx, dir, "remote", "get-url", remote)
	if err != nil || url == "" {
		return "", nil, errNoGitRemote
	}
	if clean, userinfo, had := harness.SplitUserinfo(url); had && userinfo != "git" {
		url = clean
		*notes = append(*notes, fmt.Sprintf(
			"plugin %s: credentials removed from the git remote URL of local marketplace %q", plugin, name))
	}
	if sourceCarriesCredential(url) {
		return "", nil, errRemoteCredential
	}
	info := &harness.RemoteInfo{URL: url, NameMatch: harness.NameUnknown}
	tracking := ""
	// The full ref name keeps a remote name from being read as an option.
	for _, ref := range []string{"refs/remotes/" + remote + "/HEAD", "@{u}"} {
		if _, err := git.Run(ctx, dir, "rev-parse", "--verify", "--quiet", ref); err == nil {
			tracking = ref
			break
		}
	}
	if tracking != "" {
		if n, err := git.Run(ctx, dir, "rev-list", "--count", tracking+"..HEAD"); err == nil {
			var count int
			if _, err := fmt.Sscanf(n, "%d", &count); err == nil {
				info.UnpushedCommits = &count
			}
		}
		if data, err := git.Run(ctx, dir, "show", tracking+":.claude-plugin/marketplace.json"); err == nil {
			var mk struct {
				Name string `json:"name"`
			}
			if json.Unmarshal([]byte(data), &mk) == nil && mk.Name != "" {
				info.NameMatch = harness.NameMismatch
				if mk.Name == name {
					info.NameMatch = harness.NameMatch
				}
			}
		}
	}
	if status, err := git.Run(ctx, dir, "status", "--porcelain"); err == nil && status != "" {
		info.UncommittedChanges = true
	}
	source := url
	if repo, ok := githubRepo(url); ok {
		source = repo
	}
	return source, info, nil
}

// sameDir reports whether two host paths name the same directory, after
// resolving symbolic links (git prints the top level resolved).
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}

// portability decides whether a source resolves the same on every machine
// that runs plan and build (FR-020): owner/repo, a URL and a path inside the
// project are portable; a path under the home directory or elsewhere on the
// host is not, and the reason says why.
func portability(source string) (bool, string) {
	const why = " is read on the machine that runs the plan"
	switch {
	case strings.HasPrefix(source, "file:"):
		return false, "file: URL" + why
	case strings.HasPrefix(source, "./"), !isPathSource(source):
		return true, ""
	case strings.HasPrefix(source, "~/"+skillsSubdir+"/"):
		return false, "source under ~/" + skillsSubdir + why
	case strings.HasPrefix(source, "~/"+pluginsSubdir+"/"):
		return false, "source under ~/" + pluginsSubdir + why
	case strings.HasPrefix(source, "~/"):
		return false, "source under the home directory" + why
	default:
		return false, "source outside the project" + why
	}
}

// displayPath shows a host path inside the project as ./<rest>, below the
// home directory as ~/<rest>, and otherwise as is.
func displayPath(p, home, project string) string {
	if project != "" && fsutil.StrictlyWithin(project, p) {
		rel, _ := filepath.Rel(project, p)
		return "./" + filepath.ToSlash(rel)
	}
	return tildePath(p, home)
}

// discoverSkills lists the user and project skills; a project skill shadows
// a user skill of the same name.
func discoverSkills(env harness.Env, notes *[]string) ([]harness.Extension, error) {
	user, err := listSkills(filepath.Join(env.Home, skillsSubdir), "~/"+skillsSubdir)
	if err != nil {
		return nil, err
	}
	var project []harness.Extension
	if env.Project != "" {
		if project, err = listSkills(filepath.Join(env.Project, skillsSubdir), skillsSubdir); err != nil {
			return nil, err
		}
	}
	byName := map[string]harness.Extension{}
	for _, s := range user {
		byName[s.Name] = s
	}
	for _, s := range project {
		if _, shadowed := byName[s.Name]; shadowed {
			*notes = append(*notes, fmt.Sprintf("skill %s in ~/%s is shadowed by the project skill", s.Name, skillsSubdir))
		}
		byName[s.Name] = s
	}
	out := []harness.Extension{}
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		out = append(out, byName[name])
	}
	return out, nil
}

// listSkills returns the skill directories below dir that contain SKILL.md.
// prefix is how sources and origins show dir.
func listSkills(dir, prefix string) ([]harness.Extension, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	var out []harness.Extension
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, sandcastSkillPrefix) {
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, name, SkillFile))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, harness.Extension{Name: name, Source: prefix + "/" + name, Origin: prefix})
	}
	return out, nil
}

// tildePath shows a path below home as ~/<rest>.
func tildePath(path, home string) string {
	if filepath.IsAbs(path) && fsutil.StrictlyWithin(home, path) {
		rel, _ := filepath.Rel(home, path)
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

// readJSON decodes the JSON file at path into v. A missing file is not an
// error and reports false.
func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	return true, nil
}
