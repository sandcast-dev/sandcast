package claudecode

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/rhuss/sandcast/internal/harness"
)

// SkillFile is the skill entry file inside a skill directory.
const SkillFile = "SKILL.md"

// skillsSubdir is where Claude Code discovers personal skills below the home
// directory and project skills below the project root.
const skillsSubdir = ".claude/skills"

// SkillInstallDir returns ~/.claude/skills for the user scope and
// <project>/.claude/skills for the project scope (R-16).
func (Adapter) SkillInstallDir(scope harness.Scope, env harness.Env) (string, error) {
	switch scope {
	case harness.ScopeUser:
		if env.Home == "" {
			return "", errors.New("claude-code user skills: home directory unknown")
		}
		return filepath.Join(env.Home, skillsSubdir), nil
	case harness.ScopeProject:
		if env.Project == "" {
			return "", errors.New("claude-code project skills: project directory unknown")
		}
		return filepath.Join(env.Project, skillsSubdir), nil
	default:
		return "", fmt.Errorf("claude-code: unknown skill scope %q (want user or project)", scope)
	}
}

// SkillFileName returns SKILL.md.
func (Adapter) SkillFileName() string { return SkillFile }
