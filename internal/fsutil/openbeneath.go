package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrSymlinkInPath reports that OpenBeneath met a symlink, or a non-directory
// where a directory was expected, while it walked the path.
var ErrSymlinkInPath = errors.New("a path component is a symlink or not a directory")

// OpenBeneath opens rel below root for reading without following any symlink
// on the way: root and every component of rel are opened with O_NOFOLLOW, one
// directory descriptor at a time, so a component swapped for a symlink after
// the caller resolved the path fails with ErrSymlinkInPath instead of being
// followed. root must already be free of symlinks (filepath.EvalSymlinks).
// The leaf is opened with O_NONBLOCK, so a FIFO cannot block the caller;
// callers check the type with Stat on the returned file. rel must be
// relative and must not leave root.
func OpenBeneath(root, rel string) (*os.File, error) {
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s is not a path below %s", rel, root)
	}
	dirfd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, openErr(root, err, true)
	}
	if rel == "." {
		return os.NewFile(uintptr(dirfd), root), nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		leaf := i == len(parts)-1
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if leaf {
			flags |= unix.O_NONBLOCK
		} else {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(dirfd, part, flags, 0)
		_ = unix.Close(dirfd)
		if err != nil {
			return nil, openErr(filepath.Join(root, filepath.Join(parts[:i+1]...)), err, !leaf)
		}
		if leaf {
			return os.NewFile(uintptr(fd), filepath.Join(root, rel)), nil
		}
		dirfd = fd
	}
	panic("unreachable")
}

func openErr(name string, err error, dir bool) error {
	if errors.Is(err, unix.ELOOP) || (dir && errors.Is(err, unix.ENOTDIR)) {
		return fmt.Errorf("%s: %w", name, ErrSymlinkInPath)
	}
	return &os.PathError{Op: "open", Path: name, Err: err}
}
