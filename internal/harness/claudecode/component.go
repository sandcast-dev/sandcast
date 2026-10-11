// Package claudecode is the Claude Code harness adapter (FR-038).
package claudecode

import (
	"github.com/rhuss/sandcast/internal/harness"
)

// Name is the harness name in manifests.
const Name = "claude-code"

// ComponentKey is the shipped component that installs the claude binary
// (components/claude-code.yaml).
const ComponentKey = "claude-code"

// Binary is the agent executable.
const Binary = "claude"

// Adapter implements harness.Adapter for Claude Code.
type Adapter struct{}

func init() { harness.Register(Adapter{}) }

// Name returns claude-code.
func (Adapter) Name() string { return Name }

// ComponentKey returns the claude-code component key.
func (Adapter) ComponentKey() string { return ComponentKey }

// Installed reports whether claude is on the host PATH and where.
func (Adapter) Installed(env harness.Env) (bool, string) {
	if env.LookPath == nil {
		return false, ""
	}
	path, err := env.LookPath(Binary)
	if err != nil {
		return false, ""
	}
	return true, path
}
