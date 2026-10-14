package capture

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

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

// DotfileProposal is one host dotfile capture found and offers to bake.
type DotfileProposal struct {
	// Path is the "~/"-prefixed source path written into the manifest.
	Path string `json:"path"`
}

// IsAllowlisted reports whether a home-relative path (as written in the
// manifest, with an optional "~/" prefix) is in the bounded dotfile allowlist.
// The plan uses it to mark a dotfile's source as allowlist vs explicit.
func IsAllowlisted(p string) bool {
	rel := strings.TrimPrefix(p, "~/")
	rel = path.Clean(rel)
	for _, a := range DotfileAllowlist {
		if rel == a {
			return true
		}
	}
	return false
}

// ProposeDotfiles scans the host home for the allowlist entries that exist as
// a regular file and are safe to copy, returning one manifest path per found
// entry and a notice for each entry skipped. It never fails: a missing entry,
// a symlink that leaves the home, or a credential-named file is skipped with a
// notice rather than aborting capture (spec 003 Edge Cases, R6).
func ProposeDotfiles(home string) (proposals []DotfileProposal, skipped []string) {
	if home == "" {
		return nil, nil
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		realHome = home
	}
	for _, rel := range DotfileAllowlist {
		src := filepath.Join(home, filepath.FromSlash(rel))
		fi, err := os.Lstat(src)
		if err != nil {
			continue // not present: skip silently, it is the common case
		}
		if isCredentialName(rel) {
			skipped = append(skipped, fmt.Sprintf("~/%s looks like a credential file; not proposed", rel))
			continue
		}
		real := src
		if fi.Mode()&os.ModeSymlink != 0 {
			real, err = filepath.EvalSymlinks(src)
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("~/%s is a broken symlink; skipped", rel))
				continue
			}
			if !within(realHome, real) {
				skipped = append(skipped, fmt.Sprintf("~/%s is a symlink to %s outside the home; skipped", rel, real))
				continue
			}
		}
		rfi, err := os.Stat(real)
		if err != nil || !rfi.Mode().IsRegular() {
			skipped = append(skipped, fmt.Sprintf("~/%s is not a regular file; skipped", rel))
			continue
		}
		proposals = append(proposals, DotfileProposal{Path: "~/" + rel})
	}
	return proposals, skipped
}

// isCredentialName reports whether the base name of a path looks like a
// credential file (R6): a dotenv file, a key or certificate, or a name that
// mentions a token, secret or credential.
func isCredentialName(rel string) bool {
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

// within reports whether p is root itself or below it.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
