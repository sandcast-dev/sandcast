package fsutil

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realTempDir is t.TempDir resolved, since OpenBeneath wants a root free of
// symlinks (the darwin temp dir lives under the /var symlink).
func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return d
}

func TestOpenBeneathReads(t *testing.T) {
	root := realTempDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "f"), []byte("hi"), 0o644))

	f, err := OpenBeneath(root, filepath.Join("a", "b", "f"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "hi", string(data))
	assert.Equal(t, filepath.Join(root, "a", "b", "f"), f.Name())

	d, err := OpenBeneath(root, ".")
	require.NoError(t, err)
	fi, err := d.Stat()
	require.NoError(t, err)
	assert.True(t, fi.IsDir())
	_ = d.Close()
}

func TestOpenBeneathRefusesSymlinks(t *testing.T) {
	root := realTempDir(t)
	outside := realTempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dir", "f"), []byte("x"), 0o644))

	// A symlinked leaf, inside or outside the root.
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "leaf")))
	require.NoError(t, os.Symlink(filepath.Join(root, "dir", "f"), filepath.Join(root, "inside")))
	// A symlinked parent directory.
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "parent")))
	require.NoError(t, os.Symlink(filepath.Join(root, "dir"), filepath.Join(root, "parentin")))
	// A regular file where a directory is expected.
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o644))

	for _, rel := range []string{"leaf", "inside", "parent/secret", "parentin/f", "file/x"} {
		f, err := OpenBeneath(root, filepath.FromSlash(rel))
		if f != nil {
			_ = f.Close()
		}
		assert.ErrorIs(t, err, ErrSymlinkInPath, rel)
	}

	// A symlinked root is refused too.
	link := filepath.Join(realTempDir(t), "root")
	require.NoError(t, os.Symlink(root, link))
	_, err := OpenBeneath(link, filepath.Join("dir", "f"))
	assert.ErrorIs(t, err, ErrSymlinkInPath)
}

func TestOpenBeneathRefusesEscapes(t *testing.T) {
	root := realTempDir(t)
	for _, rel := range []string{"..", "../x", "a/../../x", "/etc/passwd"} {
		_, err := OpenBeneath(root, filepath.FromSlash(rel))
		assert.ErrorContains(t, err, "is not a path below", rel)
	}
}

func TestOpenBeneathMissingAndFIFO(t *testing.T) {
	root := realTempDir(t)
	_, err := OpenBeneath(root, "missing")
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NotErrorIs(t, err, ErrSymlinkInPath)

	// A FIFO opens without blocking, and Stat tells it apart.
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "fifo"), 0o644))
	f, err := OpenBeneath(root, "fifo")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	require.NoError(t, err)
	assert.False(t, fi.Mode().IsRegular())
}
