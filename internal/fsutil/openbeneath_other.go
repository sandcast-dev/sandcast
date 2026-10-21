//go:build unix && !linux && !darwin

package fsutil

import "golang.org/x/sys/unix"

// searchDir opens a directory for reading where no search-only open is
// known, so the directories on the way need read permission as well.
const searchDir = unix.O_RDONLY | unix.O_DIRECTORY

// searchOnly reports that searchDir needs no read permission.
const searchOnly = false
