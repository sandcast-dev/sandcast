package fsutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, DirName), 0o755))
	deep := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	got, err := FindRoot(deep)
	require.NoError(t, err)
	assert.Equal(t, evalPath(t, root), evalPath(t, got))

	plain := t.TempDir()
	got, err = FindRoot(plain)
	require.NoError(t, err)
	assert.Equal(t, evalPath(t, plain), evalPath(t, got))
}

func TestLayoutPaths(t *testing.T) {
	l := Layout{Root: "/p"}
	assert.Equal(t, filepath.FromSlash("/p/.sandcast"), l.SandcastDir())
	assert.Equal(t, filepath.FromSlash("/p/.sandcast/manifest.yaml"), l.ManifestPath())
	assert.Equal(t, filepath.FromSlash("/p/.sandcast/mise.lock"), l.LockPath())
	assert.Equal(t, filepath.FromSlash("/p/.sandcast/build/openshell"), l.BuildDir("openshell"))
	assert.Equal(t, filepath.FromSlash("/p/.sandcast/fragments"), l.FragmentsDir())
	assert.Equal(t, filepath.FromSlash("/p/.sandcast/recipes"), l.RecipesDir())
	assert.Equal(t, ".sandcast/manifest.yaml", l.RelToRoot(l.ManifestPath()))
	assert.Equal(t, "/elsewhere/x", l.RelToRoot("/elsewhere/x"))
}

func TestNewLayoutExplicitProject(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLayout(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, l.Root)
}

func TestCacheDirOverride(t *testing.T) {
	t.Setenv("SANDCAST_CACHE_DIR", "/tmp/x")
	got, err := CacheDir()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/x", got)
}

func TestConfigDirOverride(t *testing.T) {
	t.Setenv("SANDCAST_CONFIG_DIR", "/cfg")
	got, err := ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, "/cfg", got)
}

func TestAtomicWriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x", "y.txt")
	require.NoError(t, AtomicWriteFile(path, []byte("one"), 0o644))
	require.NoError(t, AtomicWriteFile(path, []byte("two"), 0o644))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "two", string(got))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp files left behind")
}

func TestEnsureGitignore(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	require.NoError(t, l.EnsureGitignore())
	require.NoError(t, l.EnsureGitignore())
	got, err := os.ReadFile(filepath.Join(l.SandcastDir(), ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "build/\n", string(got))

	require.NoError(t, os.WriteFile(filepath.Join(l.SandcastDir(), ".gitignore"), []byte("other"), 0o644))
	require.NoError(t, l.EnsureGitignore())
	got, err = os.ReadFile(filepath.Join(l.SandcastDir(), ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "other\nbuild/\n", string(got))
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	assert.True(t, Exists(dir))
	assert.False(t, Exists(filepath.Join(dir, "nope")))
}

func evalPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

func TestCheckNoSymlinks(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	require.NoError(t, os.MkdirAll(filepath.Join(l.Root, ".sandcast", "build"), 0o755))
	require.NoError(t, l.CheckNoSymlinks(l.BuildDir("openshell")), "a missing leaf is fine")

	elsewhere := t.TempDir()
	require.NoError(t, os.Symlink(elsewhere, l.BuildDir("openshell")))
	err := l.CheckNoSymlinks(l.BuildDir("openshell"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".sandcast/build/openshell is a symlink")
	assert.Error(t, l.CheckNoSymlinks("/etc"))

	// A symlinked directory higher up is refused too.
	l2 := Layout{Root: t.TempDir()}
	require.NoError(t, os.MkdirAll(filepath.Join(l2.Root, ".sandcast"), 0o755))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(l2.Root, ".sandcast", "build")))
	err = l2.CheckNoSymlinks(l2.BuildDir("openshell"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".sandcast/build is a symlink")
}

func TestCheckNoSymlinkEntries(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	dir := l.BuildDir("openshell")
	require.NoError(t, l.CheckNoSymlinkEntries(dir, BuildEntries()...), "a missing directory is fine")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "profiles"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build.log"), []byte("old\n"), 0o644))
	require.NoError(t, l.CheckNoSymlinkEntries(dir, BuildEntries()...), "regular files and directories are fine")

	outside := filepath.Join(t.TempDir(), "victim")
	for _, name := range []string{"build.log", ".lock", "profiles"} {
		link := filepath.Join(dir, name)
		require.NoError(t, os.RemoveAll(link))
		require.NoError(t, os.Symlink(outside, link), "a dangling link is refused too")
		err := l.CheckNoSymlinkEntries(dir, BuildEntries()...)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), ".sandcast/build/openshell/"+name+" is a symlink", name)
		require.NoError(t, os.Remove(link))
	}
	_, err := os.Lstat(outside)
	assert.ErrorIs(t, err, os.ErrNotExist, "the check never creates the link target")
}

func TestIsBuildEntry(t *testing.T) {
	for _, name := range BuildEntries() {
		assert.True(t, IsBuildEntry(name), name)
	}
	// macOS file systems ignore case by default, so these name the same file.
	for _, name := range []string{"BUILD.LOG", "containerfile", "Plan.yaml", "Profiles"} {
		assert.True(t, IsBuildEntry(name), name)
	}
	for _, name := range []string{"allowed-domains.txt", "build.log.txt", "lock", "Containerfile.bak"} {
		assert.False(t, IsBuildEntry(name), name)
	}
	entries := BuildEntries()
	entries[0] = "changed"
	assert.NotEqual(t, "changed", BuildEntries()[0], "callers get a copy")
}

func TestEnsureGitignoreRefusesSymlink(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	require.NoError(t, os.MkdirAll(l.SandcastDir(), 0o755))
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(secret, []byte("PRIVATE KEY\n"), 0o600))
	link := filepath.Join(l.SandcastDir(), ".gitignore")
	require.NoError(t, os.Symlink(secret, link))

	err := l.EnsureGitignore()
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".sandcast/.gitignore is a symlink")
	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the link is left alone, so its target is never copied into the project")
}

func TestRelToRootKeepsDotDotPrefixedNames(t *testing.T) {
	l := Layout{Root: "/p"}
	assert.Equal(t, "..config/x", l.RelToRoot("/p/..config/x"))
	assert.Equal(t, "/x", l.RelToRoot("/x"))
}

func TestAtomicWriteFileReplacesSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(victim, []byte("keep\n"), 0o600))
	link := filepath.Join(dir, "plan.yaml")
	require.NoError(t, os.Symlink(victim, link))
	dangling := filepath.Join(dir, "verify.yaml")
	missing := filepath.Join(t.TempDir(), "created")
	require.NoError(t, os.Symlink(missing, dangling))

	require.NoError(t, AtomicWriteFile(link, []byte("new\n"), 0o644))
	require.NoError(t, AtomicWriteFile(dangling, []byte("new\n"), 0o644))

	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data), "the link target is untouched")
	_, err = os.Lstat(missing)
	assert.ErrorIs(t, err, os.ErrNotExist, "a dangling link target is never created")
	for _, p := range []string{link, dangling} {
		fi, err := os.Lstat(p)
		require.NoError(t, err)
		assert.True(t, fi.Mode().IsRegular(), "%s is now a regular file", p)
		assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	}
}
