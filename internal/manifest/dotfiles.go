package manifest

import (
	"path"
	"slices"
	"strings"
)

// The dotfile source bounds live with the manifest field they bound, so
// capture (which proposes entries) and the pipeline (which reads them for
// the plan) apply the same rules without the deterministic core depending on
// the capture package (spec 003 R4, R6).

// DotfileAllowlist is the bounded set of host home dotfiles capture proposes
// to bake (spec 003 R4). Paths are relative to the host home. The curated
// shell configuration is handled separately and is not listed here.
var DotfileAllowlist = []string{
	".gitconfig",
	".config/git/config",
	".config/starship.toml",
	".tmux.conf",
	".inputrc",
}

// ConfigDotfileAllowlist lists what a manifest may name under ~/.config
// beyond DotfileAllowlist, relative to the home: a tool's configuration
// directory (with a trailing slash; every file below it) or one file. These
// are the editor, terminal, multiplexer, prompt and pager configurations a
// developer carries along, none of which stores credentials. Everything
// else under ~/.config is refused: the directory also holds credential
// stores (gh, gcloud, sops, op, containers, rclone) whose shapes no
// redactor knows, and a committed manifest must not be able to pull them
// into an image (spec 003 R6, FR-007).
var ConfigDotfileAllowlist = []string{
	".config/git/",
	".config/starship.toml",
	".config/nvim/",
	".config/helix/",
	".config/tmux/",
	".config/alacritty/",
	".config/kitty/",
	".config/wezterm/",
	".config/ghostty/",
	".config/bat/",
	".config/ripgrep/",
}

// MaxDotfileSize caps a dotfile read from the host. Dotfiles are small
// configuration files; the cap keeps a huge file or a device out of
// plan.yaml.
const MaxDotfileSize = 256 << 10

// IsAllowlisted reports whether a home-relative path (as written in the
// manifest, with an optional "~/" prefix) is in the bounded dotfile allowlist.
// The pipeline uses it when it reads manifest dotfiles, to mark a dotfile's
// source as allowlist vs explicit.
func IsAllowlisted(p string) bool {
	return slices.Contains(DotfileAllowlist, path.Clean(strings.TrimPrefix(p, "~/")))
}

// HomeDotfileAllowed reports whether a home-relative dotfile path (slash
// separated, without the "~/" prefix) may be baked: an allowlist entry or a
// ConfigDotfileAllowlist entry. Anything else in the host home (credential
// stores, shell history, documents, the rest of ~/.config) is never read,
// whatever a committed manifest lists (spec 003 R6, FR-007). The path is
// cleaned first, so ".." cannot leave an allowed directory. The comparison
// is exact: on a case-insensitive host file system a differently spelled
// path is refused rather than resolved.
func HomeDotfileAllowed(rel string) bool {
	rel = path.Clean(rel)
	if IsAllowlisted(rel) {
		return true
	}
	for _, d := range ConfigDotfileAllowlist {
		if strings.HasSuffix(d, "/") {
			if strings.HasPrefix(rel+"/", d) {
				return true
			}
		} else if rel == d {
			return true
		}
	}
	return false
}

// HomeDotfileBound describes, for notices, what HomeDotfileAllowed accepts.
func HomeDotfileBound() string {
	names := make([]string, 0, len(ConfigDotfileAllowlist))
	for _, d := range ConfigDotfileAllowlist {
		names = append(names, strings.TrimPrefix(d, ".config/"))
	}
	return "the dotfile allowlist (~/" + strings.Join(DotfileAllowlist, ", ~/") + ") and these ~/.config entries: " + strings.Join(names, ", ")
}

// deniedDirs are home or project directories that hold credentials; no file
// below them is ever baked. They are spelled in lower case and matched
// without regard to case. The ~/.config stores are outside
// ConfigDotfileAllowlist anyway; listing them is defense in depth.
var deniedDirs = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".docker",
	".config/gh", ".config/sops", ".config/op", ".config/gcloud", ".config/containers",
	".config/rclone", ".config/hcloud", ".config/doctl", ".config/wrangler", ".config/helm/registry",
}

// DotfileDenied reports why a dotfile path (slash separated, relative to the
// home or the project) must never be baked, or "" when it may: a credential
// store directory (deniedDirs), .netrc, a shell or tool history file
// (*history), or a credential-looking name in any path segment. It is
// applied to the path as written and to the path it resolves to, and it
// compares without regard to case: the host file system may be
// case-insensitive (macOS), where ~/.config/GH/hosts.yml reads
// ~/.config/gh/hosts.yml.
func DotfileDenied(rel string) string {
	rel = strings.ToLower(path.Clean(rel))
	for _, d := range deniedDirs {
		if rel == d || strings.HasPrefix(rel, d+"/") || strings.Contains(rel, "/"+d+"/") {
			return "it is in the credential store " + d
		}
	}
	base := path.Base(rel)
	switch {
	case base == ".netrc" || base == "_netrc":
		return "it is a credential file (.netrc)"
	case strings.HasSuffix(base, "history"):
		return "it is a history file"
	case IsCredentialName(rel):
		return "its name looks like a credential file"
	}
	return ""
}

// IsCredentialName reports whether any segment of a path looks like a
// credential file or store (R6): a dotenv file, a key or certificate, or a
// name that mentions a token, secret, credential or password. Every segment
// counts, not only the base name, so gcloud/legacy_credentials/x/adc.json is
// refused by its directory. The comparison ignores case.
func IsCredentialName(rel string) bool {
	for _, seg := range strings.Split(strings.ToLower(path.Clean(rel)), "/") {
		if credentialSegment(seg) {
			return true
		}
	}
	return false
}

func credentialSegment(seg string) bool {
	if seg == ".env" || strings.HasPrefix(seg, ".env.") {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".crt"} {
		if strings.HasSuffix(seg, ext) {
			return true
		}
	}
	for _, word := range []string{"token", "secret", "credential", "password"} {
		if strings.Contains(seg, word) {
			return true
		}
	}
	return false
}
