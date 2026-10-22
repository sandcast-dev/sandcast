package fsutil

import (
	"path/filepath"
	"strings"
)

// Within reports whether p is root itself or a path below it. The check is
// lexical (filepath.Rel on the cleaned paths), so callers that must not be
// fooled by a symlink resolve both paths first. A relative path that starts
// with a ".." segment leaves root; a name that merely starts with two dots
// ("..x") does not.
func Within(root, p string) bool {
	_, ok := RelWithin(root, p)
	return ok
}

// StrictlyWithin reports whether p is below root and not root itself.
func StrictlyWithin(root, p string) bool {
	rel, ok := RelWithin(root, p)
	return ok && rel != "."
}

// RelWithin returns p relative to root and whether p is root or below it,
// with the same lexical check as Within. Every containment check in sandcast
// goes through it or escapesRoot, so a fix here (for example to separator
// handling) reaches all of them.
func RelWithin(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || escapesRoot(rel) {
		return "", false
	}
	return rel, true
}

// escapesRoot reports whether the cleaned relative path rel leaves the
// directory it is relative to: it is ".." or starts with a ".." segment.
func escapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
