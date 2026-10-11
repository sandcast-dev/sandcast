package mise

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// LockFormatVersion is the mise lockfile format sandcast reads
// (lockfile_version in mise.lock).
const LockFormatVersion = 3

// platformKeyPrefix starts the per-platform table keys of a locked tool. mise
// writes them as one quoted key, [tools.go."platforms.linux-x64"].
const platformKeyPrefix = "platforms."

// Lockfile is a parsed mise lockfile.
type Lockfile struct {
	// FormatVersion is lockfile_version.
	FormatVersion int
	// Tools maps the tool name as written in mise.toml [tools] to its locked
	// entries ([[tools.<name>]]), one per resolved version.
	Tools map[string][]LockedTool
}

// LockedTool is one [[tools.<name>]] entry.
type LockedTool struct {
	// Version is the exact resolved version.
	Version string
	// Backend is the resolved mise backend, for example "aqua:jqlang/jq".
	Backend string
	// Specifiers are the version selectors from mise.toml this entry
	// satisfies, for example ["latest"] or ["1.25"].
	Specifiers []string
	// Platforms maps a mise platform name to its locked artifact.
	Platforms map[string]PlatformAsset
}

// PlatformAsset is the locked artifact of a tool version on one platform.
type PlatformAsset struct {
	// Checksum is "<algorithm>:<hex>", for example "sha256:...".
	Checksum string
	// URL is the download URL.
	URL string
	// URLAPI is the GitHub API URL of the release asset, when known.
	URLAPI string
	// Provenance names the verified provenance, for example
	// "github-attestations", when known.
	Provenance string
}

// ToolError reports a component whose tool cannot be installed in locked
// mode on a platform.
type ToolError struct {
	// Key is the component key.
	Key string
	// Tool is the mise tool name.
	Tool string
	// Platform is the mise platform the error applies to.
	Platform string
	// Reason explains the failure and names the remedy.
	Reason string
}

// Error formats the failure as "component <key> (mise tool <tool>): <reason>".
func (e ToolError) Error() string {
	return fmt.Sprintf("component %s (mise tool %s): %s", e.Key, e.Tool, e.Reason)
}

// rawLock is the TOML shape of mise.lock. Tool entries stay untyped because
// their per-platform keys are dynamic.
type rawLock struct {
	LockfileVersion int                         `toml:"lockfile_version"`
	Tools           map[string][]map[string]any `toml:"tools"`
}

// ReadLock reads and parses the lockfile at path. A missing file yields an
// error wrapping os.ErrNotExist.
func ReadLock(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mise lockfile: %w", err)
	}
	l, err := ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// ParseLock parses mise lockfile content. Only format version
// LockFormatVersion is accepted; unknown fields are ignored.
func ParseLock(data []byte) (*Lockfile, error) {
	// Check the format version first: older formats have a different
	// [tools] shape and would fail decoding with a less useful message.
	var head struct {
		LockfileVersion int `toml:"lockfile_version"`
	}
	if _, err := toml.Decode(string(data), &head); err != nil {
		return nil, fmt.Errorf("parse mise lockfile: %w", err)
	}
	if head.LockfileVersion != LockFormatVersion {
		return nil, fmt.Errorf("unsupported mise lockfile format version %d: sandcast reads version %d",
			head.LockfileVersion, LockFormatVersion)
	}
	var raw rawLock
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("parse mise lockfile: %w", err)
	}
	l := &Lockfile{FormatVersion: raw.LockfileVersion, Tools: map[string][]LockedTool{}}
	for name, entries := range raw.Tools {
		for i, e := range entries {
			lt, err := parseLockedTool(e)
			if err != nil {
				return nil, fmt.Errorf("parse mise lockfile: tools.%s[%d]: %w", name, i, err)
			}
			l.Tools[name] = append(l.Tools[name], lt)
		}
	}
	return l, nil
}

func parseLockedTool(e map[string]any) (LockedTool, error) {
	var lt LockedTool
	var err error
	if lt.Version, err = stringField(e, "version"); err != nil {
		return lt, err
	}
	if lt.Backend, err = stringField(e, "backend"); err != nil {
		return lt, err
	}
	if v, ok := e["specifiers"]; ok {
		list, ok := v.([]any)
		if !ok {
			return lt, fmt.Errorf("specifiers: want an array of strings")
		}
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return lt, fmt.Errorf("specifiers: want an array of strings")
			}
			lt.Specifiers = append(lt.Specifiers, s)
		}
	}
	lt.Platforms = map[string]PlatformAsset{}
	for k, v := range e {
		switch {
		case strings.HasPrefix(k, platformKeyPrefix):
			if err := addPlatform(lt.Platforms, strings.TrimPrefix(k, platformKeyPrefix), v); err != nil {
				return lt, err
			}
		case k == "platforms":
			// Nested form [tools.<name>.platforms.<platform>], accepted for
			// robustness although mise writes the quoted-key form.
			nested, ok := v.(map[string]any)
			if !ok {
				return lt, fmt.Errorf("platforms: want a table")
			}
			for p, pv := range nested {
				if err := addPlatform(lt.Platforms, p, pv); err != nil {
					return lt, err
				}
			}
		}
	}
	return lt, nil
}

func addPlatform(into map[string]PlatformAsset, platform string, v any) error {
	t, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("platform %s: want a table", platform)
	}
	var a PlatformAsset
	var err error
	if a.Checksum, err = stringField(t, "checksum"); err != nil {
		return fmt.Errorf("platform %s: %w", platform, err)
	}
	if a.URL, err = stringField(t, "url"); err != nil {
		return fmt.Errorf("platform %s: %w", platform, err)
	}
	if a.URLAPI, err = stringField(t, "url_api"); err != nil {
		return fmt.Errorf("platform %s: %w", platform, err)
	}
	if a.Provenance, err = stringField(t, "provenance"); err != nil {
		return fmt.Errorf("platform %s: %w", platform, err)
	}
	into[platform] = a
	return nil
}

// stringField returns m[key] as a string, "" when absent.
func stringField(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: want a string, got %T", key, v)
	}
	return s, nil
}

// Version returns the locked version of tool, the first entry when several
// versions are locked, or "" when the tool is not in the lockfile.
func (l *Lockfile) Version(tool string) string {
	entries := l.Tools[tool]
	if len(entries) == 0 {
		return ""
	}
	return entries[0].Version
}

// VersionOnlyBackends are mise backends whose lock entries carry an exact
// version but no per-platform artifacts because the backend's own installer
// verifies the download (core:rust installs through rustup, which checks the
// toolchain against its signed channel manifest). mise accepts such entries
// with --locked.
var VersionOnlyBackends = map[string]bool{"core:rust": true}

// Check reports, for each tool, why it cannot be installed in locked mode on
// platform (a mise platform name): the tool is missing from the lockfile, no
// entry matches its version selector, the entry has no table for platform, or
// that table lacks a checksum or url. Errors are sorted by component key.
func (l *Lockfile) Check(tools []Tool, platform string) []ToolError {
	sorted := append([]Tool(nil), tools...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	remedy := lockRemedy(platform)
	var errs []ToolError
	for _, t := range sorted {
		fail := func(format string, args ...any) {
			errs = append(errs, ToolError{
				Key: t.Key, Tool: t.Tool, Platform: platform,
				Reason: fmt.Sprintf(format, args...) + "; " + remedy,
			})
		}
		entry, reason := l.entry(t)
		if reason != "" {
			fail("%s", reason)
			continue
		}
		if VersionOnlyBackends[entry.Backend] && len(entry.Platforms) == 0 && entry.Version != "" {
			// rustup installs and verifies the pinned toolchain itself; mise
			// locks only the version and accepts that in locked mode.
			continue
		}
		asset, ok := entry.Platforms[platform]
		if !ok {
			fail("mise.lock has no %s entry for %s", platform, t.Tool)
			continue
		}
		var missing []string
		if asset.Checksum == "" {
			missing = append(missing, "checksum")
		}
		if asset.URL == "" {
			missing = append(missing, "url")
		}
		if len(missing) > 0 {
			fail("mise.lock entry for %s on %s has no %s", t.Tool, platform, strings.Join(missing, " or "))
		}
	}
	return errs
}

// entry returns the locked entry for t, matched by its version selector.
// A lockfile without specifiers with a single entry matches that entry.
// A non-empty reason explains why no entry matches.
func (l *Lockfile) entry(t Tool) (LockedTool, string) {
	entries := l.Tools[t.Tool]
	if len(entries) == 0 {
		return LockedTool{}, fmt.Sprintf("mise.lock has no entry for %s", t.Tool)
	}
	sel := t.selector()
	haveSpecifiers := false
	for _, e := range entries {
		for _, s := range e.Specifiers {
			haveSpecifiers = true
			if s == sel {
				return e, ""
			}
		}
	}
	if !haveSpecifiers && len(entries) == 1 {
		return entries[0], ""
	}
	versions := make([]string, len(entries))
	for i, e := range entries {
		versions[i] = e.Version
	}
	return LockedTool{}, fmt.Sprintf("mise.lock has no entry for %s matching %q (locked: %s)",
		t.Tool, sel, strings.Join(versions, ", "))
}

// lockRemedy returns the command that adds platform to the lockfile.
func lockRemedy(platform string) string {
	target, err := Target(platform)
	if err != nil {
		return `run "sandcast lock"`
	}
	return fmt.Sprintf(`run "sandcast lock --platform %s"`, target)
}
