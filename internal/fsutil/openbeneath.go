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

// ErrNotRegular reports that the leaf OpenBeneath was asked for is not a
// regular file. The type is checked before the open, so a device, socket or
// FIFO found there is not opened; one swapped in after the check is opened
// with O_NONBLOCK and O_NOCTTY, and callers check the type of the returned
// file with Stat.
var ErrNotRegular = errors.New("not a regular file")

// OpenBeneath opens the regular file rel below root for reading without
// following any symlink on the way: root and every directory of rel are
// opened with O_NOFOLLOW, one directory descriptor at a time, so a component
// swapped for a symlink after the caller resolved the path fails with
// ErrSymlinkInPath instead of being followed. root must already be free of
// symlinks (filepath.EvalSymlinks). On linux and darwin the directories are
// opened for search only (searchDir), so like a plain open they need execute
// permission, not read permission; elsewhere they need read permission too
// (searchOnly is false). The leaf is checked with fstatat before it is opened and
// is opened with O_NONBLOCK and O_NOCTTY, so a leaf swapped for a FIFO or a
// device between the two cannot block the caller or become its terminal;
// callers check the type again with Stat on the returned file. rel must be
// relative and must not leave root.
func OpenBeneath(root, rel string) (*os.File, error) {
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) || escapesRoot(rel) {
		return nil, fmt.Errorf("%s is not a path below %s", rel, root)
	}
	var dirfd int
	err := ignoringEINTR(func() (err error) {
		dirfd, err = unix.Open(root, searchDir|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		return err
	})
	if err != nil {
		return nil, openErr(unix.AT_FDCWD, root, root, err, true)
	}
	if rel == "." {
		_ = unix.Close(dirfd)
		return nil, fmt.Errorf("%s: %w", root, ErrNotRegular)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts[:len(parts)-1] {
		var fd int
		err := ignoringEINTR(func() (err error) {
			fd, err = unix.Openat(dirfd, part, searchDir|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			return err
		})
		if err != nil {
			err = openErr(dirfd, part, filepath.Join(root, filepath.Join(parts[:i+1]...)), err, true)
			_ = unix.Close(dirfd)
			return nil, err
		}
		_ = unix.Close(dirfd)
		dirfd = fd
	}
	defer func() { _ = unix.Close(dirfd) }()

	leaf, name := parts[len(parts)-1], filepath.Join(root, rel)
	var st unix.Stat_t
	if err := ignoringEINTR(func() error { return unix.Fstatat(dirfd, leaf, &st, unix.AT_SYMLINK_NOFOLLOW) }); err != nil {
		return nil, openErr(dirfd, leaf, name, err, false)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
	case unix.S_IFLNK:
		return nil, fmt.Errorf("%s: %w", name, ErrSymlinkInPath)
	default:
		return nil, fmt.Errorf("%s: %w", name, ErrNotRegular)
	}
	leafCheckedHook(name)
	var fd int
	err = ignoringEINTR(func() (err error) {
		fd, err = unix.Openat(dirfd, leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
		return err
	})
	if err != nil {
		return nil, openErr(dirfd, leaf, name, err, false)
	}
	return os.NewFile(uintptr(fd), name), nil
}

// leafCheckedHook runs between the type check and the open of the leaf in
// OpenBeneath, so tests can swap the leaf there.
var leafCheckedHook = func(string) {}

// ignoringEINTR retries fn while it fails with EINTR, as the os package does
// for its own opens: the Go runtime's preemption signal can interrupt an
// open on a network or FUSE file system.
func ignoringEINTR(fn func() error) error {
	for {
		if err := fn(); !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

// openErr maps a failed open or fstatat of part below dirfd, reported as
// name. A symlink is ErrSymlinkInPath whichever errno the system returns for
// O_NOFOLLOW (ELOOP, EMLINK on FreeBSD and DragonFly, EFTYPE on NetBSD): when
// the errno does not tell, the component itself is looked at.
func openErr(dirfd int, part, name string, err error, dir bool) error {
	if errors.Is(err, unix.ELOOP) || (dir && errors.Is(err, unix.ENOTDIR)) {
		return fmt.Errorf("%s: %w", name, ErrSymlinkInPath)
	}
	var st unix.Stat_t
	if unix.Fstatat(dirfd, part, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fmt.Errorf("%s: %w", name, ErrSymlinkInPath)
	}
	return &os.PathError{Op: "open", Path: name, Err: err}
}
