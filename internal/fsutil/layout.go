package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fixed names of the project-local layout (FR-028a, R-12).
const (
	DirName        = ".sandcast"
	ManifestFile   = "manifest.yaml"
	LockFile       = "mise.lock"
	LearningsFile  = "learnings.yaml"
	FragmentsDir   = "fragments"
	RecipesDir     = "recipes"
	ComponentsDir  = "components"
	ProfilesDir    = "profiles"
	BuildDirName   = "build"
	GitignoreEntry = "build/"
)

// buildEntries are the names sandcast writes directly inside a build
// directory: the lock, the engine log, the plan outputs, the build context
// and the verify report. internal/pipeline tests that the list matches the
// constants of internal/build, internal/plan and internal/verify.
var buildEntries = []string{
	".lock", "build.log", "plan.yaml", "policy.yaml", "verify.yaml",
	"Containerfile", "Containerfile.final", "profiles", "mise", "shell", "harness", "dotfiles",
}

// BuildEntries returns a copy of the names sandcast writes directly inside
// a build directory. No user-chosen file in that directory may take one of
// them.
func BuildEntries() []string { return append([]string(nil), buildEntries...) }

// IsBuildEntry reports whether name is one of BuildEntries. Case is ignored:
// the default macOS file systems treat "Build.log" and "build.log" as one
// file.
func IsBuildEntry(name string) bool {
	for _, e := range buildEntries {
		if strings.EqualFold(name, e) {
			return true
		}
	}
	return false
}

// Layout is the resolved project layout.
type Layout struct {
	// Root is the project root, the directory that contains .sandcast/.
	Root string
}

// FindRoot returns the nearest ancestor of start (inclusive) that contains a
// .sandcast directory, or start itself when none does.
func FindRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for dir := abs; ; {
		if fi, err := os.Stat(filepath.Join(dir, DirName)); err == nil && fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs, nil
		}
		dir = parent
	}
}

// NewLayout resolves the layout for a project. An empty project means the
// current directory with ancestor discovery; an explicit project is used as is.
func NewLayout(project string) (Layout, error) {
	if project != "" {
		abs, err := filepath.Abs(project)
		if err != nil {
			return Layout{}, err
		}
		return Layout{Root: abs}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Layout{}, err
	}
	root, err := FindRoot(cwd)
	if err != nil {
		return Layout{}, err
	}
	return Layout{Root: root}, nil
}

// SandcastDir is <root>/.sandcast.
func (l Layout) SandcastDir() string { return filepath.Join(l.Root, DirName) }

// ManifestPath is <root>/.sandcast/manifest.yaml.
func (l Layout) ManifestPath() string { return filepath.Join(l.SandcastDir(), ManifestFile) }

// LockPath is <root>/.sandcast/mise.lock.
func (l Layout) LockPath() string { return filepath.Join(l.SandcastDir(), LockFile) }

// LearningsPath is <root>/.sandcast/learnings.yaml.
func (l Layout) LearningsPath() string { return filepath.Join(l.SandcastDir(), LearningsFile) }

// FragmentsDir is <root>/.sandcast/fragments.
func (l Layout) FragmentsDir() string { return filepath.Join(l.SandcastDir(), FragmentsDir) }

// RecipesDir is <root>/.sandcast/recipes.
func (l Layout) RecipesDir() string { return filepath.Join(l.SandcastDir(), RecipesDir) }

// ComponentsDir is <root>/.sandcast/components.
func (l Layout) ComponentsDir() string { return filepath.Join(l.SandcastDir(), ComponentsDir) }

// ProfilesDir is <root>/.sandcast/profiles.
func (l Layout) ProfilesDir() string { return filepath.Join(l.SandcastDir(), ProfilesDir) }

// BuildRoot is <root>/.sandcast/build.
func (l Layout) BuildRoot() string { return filepath.Join(l.SandcastDir(), BuildDirName) }

// BuildDir is <root>/.sandcast/build/<target>.
func (l Layout) BuildDir(target string) string { return filepath.Join(l.BuildRoot(), target) }

// RelToRoot returns path relative to the project root with forward slashes,
// or path unchanged when it is outside the root.
func (l Layout) RelToRoot(path string) string {
	rel, ok := relWithin(l.Root, path)
	if !ok {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// CacheDir returns the per-user sandcast cache directory
// (os.UserCacheDir()/sandcast). SANDCAST_CACHE_DIR overrides it.
func CacheDir() (string, error) {
	if dir := os.Getenv("SANDCAST_CACHE_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache directory: %w", err)
	}
	return filepath.Join(base, "sandcast"), nil
}

// ConfigDir returns the per-user sandcast configuration directory
// (~/.config/sandcast). SANDCAST_CONFIG_DIR overrides it.
func ConfigDir() (string, error) {
	if dir := os.Getenv("SANDCAST_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "sandcast"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "sandcast"), nil
}

// AtomicWriteFile writes data to a temporary file in the target directory and
// renames it over path, creating parent directories as needed. A symlink at
// path is replaced, never followed: the temporary file is created
// exclusively (O_EXCL) under a random name and chmod-ed through its open
// descriptor, and the rename swaps the directory entry itself.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// EnsureGitignore makes sure <root>/.sandcast/.gitignore contains "build/".
// A symlink there is refused: following it would copy the content of the
// link target into the project.
func (l Layout) EnsureGitignore() error {
	path := filepath.Join(l.SandcastDir(), ".gitignore")
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; sandcast only reads and writes a regular file there, remove the link", l.RelToRoot(path))
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == GitignoreEntry || strings.TrimSpace(line) == "/"+GitignoreEntry {
			return nil
		}
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += GitignoreEntry + "\n"
	return AtomicWriteFile(path, []byte(content), 0o644)
}

// CheckNoSymlinks fails when any existing directory between the project
// root (exclusive) and path (inclusive) is a symlink. A committed symlink
// such as .sandcast/build pointing elsewhere would otherwise make sandcast
// delete and write files outside the project and send them as build
// context.
func (l Layout) CheckNoSymlinks(path string) error {
	rel, ok := relWithin(l.Root, path)
	if !ok {
		return fmt.Errorf("%s is outside the project %s", path, l.Root)
	}
	cur := l.Root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; sandcast only writes into real directories below the project", l.RelToRoot(cur))
		}
	}
	return nil
}

// CheckNoSymlinkEntries fails when any of names exists directly inside dir
// as a symlink, dangling or not. sandcast opens some of these files in place
// (the lock, build.log), so a committed link would make it create or
// truncate its target outside the build directory. A missing dir or entry
// is fine.
func (l Layout) CheckNoSymlinkEntries(dir string, names ...string) error {
	for _, name := range names {
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; sandcast writes this entry of the build directory itself and never follows a link there, remove it", l.RelToRoot(path))
		}
	}
	return nil
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
