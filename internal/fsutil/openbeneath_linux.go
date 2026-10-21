package fsutil

import "golang.org/x/sys/unix"

// searchDir opens a directory for lookups only. O_PATH needs no read
// permission on the directory, and fstatat and openat accept the descriptor.
const searchDir = unix.O_PATH | unix.O_DIRECTORY
