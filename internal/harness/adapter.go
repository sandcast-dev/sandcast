package harness

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
)

// Scope selects where skills are installed.
type Scope string

// Skill install scopes.
const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// Env is the host environment an adapter inspects.
type Env struct {
	// Home is the user's home directory.
	Home string
	// Project is the project root.
	Project string
	// LookPath finds executables on the host.
	LookPath func(string) (string, error)
}

// Adapter is one agent harness (FR-038, data-model "Harness adapter").
// Codex, OpenCode and pi adapters implement the same interface.
type Adapter interface {
	// Name is the harness name used in manifests, e.g. claude-code.
	Name() string
	// ComponentKey is the component that installs the agent; it resolves
	// through the component tiers like any other component.
	ComponentKey() string
	// Installed reports whether the harness is installed on the host and
	// where its binary is.
	Installed(env Env) (bool, string)
	// Discover lists the locally configured MCP servers, plugins and skills.
	// Secrets are already replaced by placeholders.
	Discover(ctx context.Context, env Env) (*Discovery, error)
	// Scaffold renders the 70-harness section for the manifest entry and the
	// files the build context needs for it.
	Scaffold(in ScaffoldInput) (*Scaffold, error)
	// SkillInstallDir is the directory the sandcast skills are installed into.
	SkillInstallDir(scope Scope, env Env) (string, error)
	// SkillFileName is the skill entry file name inside a skill directory.
	SkillFileName() string
}

// ScaffoldInput is what an adapter needs to scaffold a harness.
type ScaffoldInput struct {
	Harness manifest.HarnessRef
	User    plan.FinalUser
	// Project is the project root; relative skill sources resolve against it.
	Project string
	// Home is the host home directory; "~/" skill sources resolve against it.
	Home string
}

// Scaffold is a rendered 70-harness section.
type Scaffold struct {
	// Fragment is the Containerfile text of the section.
	Fragment string
	// Files are copied into the build context before the build: key is the
	// path relative to the build directory, value the host source path
	// (file or directory).
	Files map[string]string
	// Env, Dirs, ReadOnly and ReadWrite are image needs of the harness
	// configuration, merged into the plan's image setup and policy.
	Env       []plan.EnvVar
	Dirs      []plan.Dir
	ReadOnly  []string
	ReadWrite []string
}

// Discovery is the result of an adapter's local discovery.
type Discovery struct {
	Harness    string      `json:"harness" yaml:"harness"`
	MCPServers []MCPServer `json:"mcp_servers" yaml:"mcp_servers"`
	Plugins    []Extension `json:"plugins" yaml:"plugins"`
	Skills     []Extension `json:"skills" yaml:"skills"`
	Redacted   []Redaction `json:"redacted" yaml:"redacted"`
	Notes      []string    `json:"notes" yaml:"notes"`
}

// MCPServer is a discovered MCP server in manifest shape plus its origin.
type MCPServer struct {
	manifest.MCPServer `yaml:",inline"`
	// Origin is the configuration file it was found in.
	Origin string `json:"origin" yaml:"origin"`
}

// Extension is a discovered plugin or skill.
type Extension struct {
	Name   string `json:"name" yaml:"name"`
	Source string `json:"source" yaml:"source"`
	Origin string `json:"origin" yaml:"origin"`
}

// Redaction records a secret replaced by a placeholder.
type Redaction struct {
	// Path is where the value was, e.g. mcp_servers[docs].env.API_KEY.
	Path string `json:"path" yaml:"path"`
	// Kind is the credential shape that matched.
	Kind string `json:"kind" yaml:"kind"`
	// Placeholder is the ${SANDCAST_SECRET_N} that replaced it.
	Placeholder string `json:"placeholder" yaml:"placeholder"`
	// EnvVar is the environment variable the value was assigned to, if any;
	// supply it as a provider credential at runtime.
	EnvVar string `json:"env_var,omitempty" yaml:"env_var,omitempty"`
}

// registry is the static adapter map (data-model: "Registry of adapters is
// a static map").
var registry = map[string]Adapter{}

// Register adds an adapter; adapters call it from init.
func Register(a Adapter) {
	registry[a.Name()] = a
}

// Get returns the adapter for name.
func Get(name string) (Adapter, error) {
	a, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("no adapter for harness %q (known: %v)", name, Names())
	}
	return a, nil
}

// Names returns the registered harness names, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ErrInvalidManifest marks a scaffold error caused by the harness section of
// the manifest (a source outside the allowed roots, a literal credential, a
// malformed MCP server); commands report it as a validation error.
var ErrInvalidManifest = errors.New("invalid harness configuration")

// Invalid marks err as an ErrInvalidManifest without changing its message.
func Invalid(err error) error {
	if err == nil {
		return nil
	}
	return invalidError{err}
}

type invalidError struct{ error }

func (e invalidError) Unwrap() error        { return e.error }
func (e invalidError) Is(target error) bool { return target == ErrInvalidManifest }
