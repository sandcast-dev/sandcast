package manifest

import (
	"path"
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

// MaxDotfileSize caps a dotfile read from the host. Dotfiles are small
// configuration files; the cap keeps a huge file or a device out of
// plan.yaml.
const MaxDotfileSize = 256 << 10

// IsAllowlisted reports whether a home-relative path (as written in the
// manifest, with an optional "~/" prefix) is in the bounded dotfile allowlist.
// The plan uses it to mark a dotfile's source as allowlist vs explicit.
func IsAllowlisted(p string) bool {
	rel := path.Clean(strings.TrimPrefix(p, "~/"))
	for _, a := range DotfileAllowlist {
		if rel == a {
			return true
		}
	}
	return false
}

// HomeDotfileAllowed reports whether a home-relative dotfile path (slash
// separated, without the "~/" prefix) may be baked: an allowlist entry or a
// file under ~/.config. Anything else in the host home (credential stores,
// shell history, documents) is never read, whatever a committed manifest
// lists (spec 003 R6, FR-007). The path is cleaned first, so ".." cannot
// leave ~/.config.
func HomeDotfileAllowed(rel string) bool {
	rel = path.Clean(rel)
	if IsAllowlisted(rel) {
		return true
	}
	return strings.HasPrefix(rel, ".config/")
}

// deniedDirs are home or project directories that hold credentials; no file
// below them is ever baked.
var deniedDirs = []string{".ssh", ".aws", ".gnupg", ".kube", ".docker", ".config/gh"}

// DotfileDenied reports why a dotfile path (slash separated, relative to the
// home or the project) must never be baked, or "" when it may: a credential
// store directory (.ssh, .aws, .gnupg, .kube, .docker, .config/gh), .netrc,
// a shell or tool history file (*_history), or a credential-looking name.
// It is applied to the path as written and to the path it resolves to.
func DotfileDenied(rel string) string {
	rel = path.Clean(rel)
	for _, d := range deniedDirs {
		if rel == d || strings.HasPrefix(rel, d+"/") || strings.Contains(rel, "/"+d+"/") {
			return "it is in the credential store " + d
		}
	}
	base := strings.ToLower(path.Base(rel))
	switch {
	case base == ".netrc" || base == "_netrc":
		return "it is a credential file (.netrc)"
	case strings.HasSuffix(base, "_history") || strings.HasSuffix(base, "history"):
		return "it is a history file"
	case IsCredentialName(rel):
		return "its name looks like a credential file"
	}
	return ""
}

// IsCredentialName reports whether the base name of a path looks like a
// credential file (R6): a dotenv file, a key or certificate, or a name that
// mentions a token, secret or credential.
func IsCredentialName(rel string) bool {
	base := strings.ToLower(path.Base(rel))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".crt"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	for _, word := range []string{"token", "secret", "credential", "password"} {
		if strings.Contains(base, word) {
			return true
		}
	}
	return false
}
