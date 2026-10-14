package fsutil

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// HostPathKind says what a manifest host path is relative to.
type HostPathKind int

// The kinds of manifest host paths.
const (
	// HostPathHome is "~" or a "~/"-prefixed path, resolved against the host
	// home directory.
	HostPathHome HostPathKind = iota + 1
	// HostPathProject is a relative path, resolved against the project root.
	HostPathProject
	// HostPathAbsolute is an absolute path, used as is.
	HostPathAbsolute
)

func (k HostPathKind) String() string {
	switch k {
	case HostPathHome:
		return "home"
	case HostPathProject:
		return "project"
	case HostPathAbsolute:
		return "absolute"
	}
	return "unknown"
}

// ResolveHostPath classifies a manifest host path and joins it to the
// directory it is relative to, without following symlinks: "~" and "~/..."
// against home, an absolute path as is (cleaned), anything else against
// project. Every caller that reads host files named in the manifest (harness
// sources, dotfiles) uses it, so the same string is never resolved one way and
// checked another. It fails for an empty path, for "~user" forms (only "~/"
// names the home), and when the directory a path needs is unknown.
func ResolveHostPath(p, home, project string) (HostPathKind, string, error) {
	switch {
	case p == "":
		return 0, "", errors.New("the path is empty")
	case p == "~" || strings.HasPrefix(p, "~/"):
		if home == "" {
			return HostPathHome, "", fmt.Errorf("%s needs the home directory, which is unknown", p)
		}
		return HostPathHome, filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	case strings.HasPrefix(p, "~"):
		return 0, "", fmt.Errorf("%s: only ~/ names the home directory; ~user paths are not supported", p)
	case filepath.IsAbs(p):
		return HostPathAbsolute, filepath.Clean(p), nil
	default:
		if project == "" {
			return HostPathProject, "", fmt.Errorf("%s needs the project directory, which is unknown", p)
		}
		return HostPathProject, filepath.Join(project, p), nil
	}
}
