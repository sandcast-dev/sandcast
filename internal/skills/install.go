package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/harness"
)

// Install actions.
const (
	ActionInstalled = "installed"
	ActionUpdated   = "updated"
	ActionUnchanged = "unchanged"
	ActionSkipped   = "skipped"
)

// InstallResult is the outcome for one skill.
type InstallResult struct {
	Skill   string `json:"skill"`
	Path    string `json:"path"`
	Action  string `json:"action"`
	Version string `json:"version"`
	Reason  string `json:"reason,omitempty"`
}

// Install copies every bundle skill <dir>/ to <install dir>/sandcast-<dir>/
// for the harness and scope (FR-003). An installed skill with the same
// version is left alone and one with a newer version is skipped, unless
// force is set; an older one is replaced.
func Install(b *Bundle, a harness.Adapter, scope harness.Scope, env harness.Env, force bool) ([]InstallResult, error) {
	dir, err := a.SkillInstallDir(scope, env)
	if err != nil {
		return nil, err
	}
	var out []InstallResult
	for _, s := range b.Skills {
		target := filepath.Join(dir, InstalledName(s.Dir))
		res := InstallResult{Skill: InstalledName(s.Dir), Path: target, Version: b.Version, Action: ActionInstalled}
		if current, ok := installedVersion(target, a.SkillFileName()); ok {
			switch c := component.Compare(current, b.Version); {
			case c == 0 && !force:
				res.Action = ActionUnchanged
				out = append(out, res)
				continue
			case c > 0 && !force:
				res.Action = ActionSkipped
				res.Reason = fmt.Sprintf("installed version %s is newer than this binary's bundle %s; update sandcast or use --force", current, b.Version)
				out = append(out, res)
				continue
			}
			res.Action = ActionUpdated
		}
		if err := os.RemoveAll(target); err != nil {
			return out, err
		}
		if err := copySkill(b.FS, s.Dir, target, a.SkillFileName()); err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

// copySkill copies the bundle directory src into target, renaming SKILL.md
// to the harness's entry file name.
func copySkill(fsys fs.FS, src, target, entry string) error {
	return fs.WalkDir(fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(target, rel), 0o755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if rel == EntryFile {
			rel = entry
		}
		return fsutil.AtomicWriteFile(filepath.Join(target, rel), data, 0o644)
	})
}

func installedVersion(dir, entry string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, entry))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		return "", false
	}
	fm, err := ParseFrontmatter(data)
	if err != nil {
		return "unknown", true
	}
	return fm.SandcastVersion, true
}
