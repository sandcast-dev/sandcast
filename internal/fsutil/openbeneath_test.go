package fsutil

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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
}

// TestOpenBeneathSearchOnlyDirs checks that the root and the directories on
// the way need execute permission only, as for a plain open of the path.
func TestOpenBeneathSearchOnlyDirs(t *testing.T) {
	root := realTempDir(t)
	writeTestFile(t, filepath.Join(root, "x", "f"), "hi")
	for _, d := range []string{filepath.Join(root, "x"), root} {
		require.NoError(t, os.Chmod(d, 0o311))
		t.Cleanup(func() { _ = os.Chmod(d, 0o755) })
	}
	f, err := OpenBeneath(root, filepath.Join("x", "f"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "hi", string(data))
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

func TestOpenBeneathMissingAndUnreadable(t *testing.T) {
	root := realTempDir(t)
	for _, rel := range []string{"missing", "nodir/f"} {
		_, err := OpenBeneath(root, filepath.FromSlash(rel))
		assert.ErrorIs(t, err, os.ErrNotExist, rel)
		assert.NotErrorIs(t, err, ErrSymlinkInPath, rel)
	}

	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	writeTestFile(t, filepath.Join(root, "locked", "f"), "x")
	require.NoError(t, os.Chmod(filepath.Join(root, "locked"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o755) })
	_, err := OpenBeneath(root, filepath.Join("locked", "f"))
	assert.ErrorIs(t, err, os.ErrPermission)
	assert.NotErrorIs(t, err, ErrSymlinkInPath)
}

// TestOpenBeneathNotRegular checks that a FIFO, a directory and the root
// itself are refused by type, before an open that could block.
func TestOpenBeneathNotRegular(t *testing.T) {
	root := realTempDir(t)
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "fifo"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0o755))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, rel := range []string{"fifo", "dir", "."} {
			f, err := OpenBeneath(root, rel)
			if f != nil {
				_ = f.Close()
			}
			assert.ErrorIs(t, err, ErrNotRegular, rel)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
