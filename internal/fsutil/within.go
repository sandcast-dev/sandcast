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
	_, ok := relWithin(root, p)
	return ok
}

// StrictlyWithin reports whether p is below root and not root itself.
func StrictlyWithin(root, p string) bool {
	rel, ok := relWithin(root, p)
	return ok && rel != "."
}

// relWithin returns p relative to root and whether p is root or below it.
// Every containment check in sandcast goes through it, so a fix here (for
// example to separator handling) reaches all of them.
func relWithin(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
