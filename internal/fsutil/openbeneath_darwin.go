package fsutil

import "golang.org/x/sys/unix"

// oExec is darwin's O_EXEC, which golang.org/x/sys/unix does not define
// (sys/fcntl.h: O_SEARCH is O_EXEC|O_DIRECTORY).
const oExec = 0x40000000

// searchDir opens a directory for lookups only (O_SEARCH), so it needs no
// read permission on the directory.
const searchDir = oExec | unix.O_DIRECTORY

// searchOnly reports that searchDir needs no read permission.
const searchOnly = true
