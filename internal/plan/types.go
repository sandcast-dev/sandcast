package plan

import (
	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
)

// Delta actions (data-model "Probe result").
const (
	ActionSkip         = "skip"
	ActionInstall      = "install"
	ActionShadow       = "shadow"
	ActionUnresolvable = "unresolvable"
)

// KindFragment marks a component installed by a stored fragment.
const KindFragment = "fragment"

// Fragment names of the aggregated and per-component sections (R-14).
const (
	SectionPackages  = "10-system-packages"
	SectionMise      = "30-mise"
	SectionRecipes   = "40-recipes"
	SectionFragments = "50-fragments"
	SectionHarness   = "70-harness"
)

// Plan is build/<target>/plan.yaml (contracts/plan.schema.yaml). Field
// order is the section order of the file.
type Plan struct {
	SchemaVersion   int                  `yaml:"schema_version" json:"schema_version"`
	SandcastVersion string               `yaml:"sandcast_version" json:"sandcast_version"`
	Target          string               `yaml:"target" json:"target"`
	Platform        Selected             `yaml:"platform" json:"platform"`
	Engine          EngineInfo           `yaml:"engine" json:"engine"`
	BaseImage       BaseImage            `yaml:"base_image" json:"base_image"`
	ImageFacts      ImageFacts           `yaml:"image_facts" json:"image_facts"`
	FinalUser       FinalUser            `yaml:"final_user" json:"final_user"`
	Image           ImageSetup           `yaml:"image" json:"image"`
	ProbeDelta      []DeltaEntry         `yaml:"probe_delta" json:"probe_delta"`
	Components      []Component          `yaml:"components" json:"components"`
	Mise            Mise                 `yaml:"mise" json:"mise"`
	Profiles        []Profile            `yaml:"profiles" json:"profiles"`
	Policy          string               `yaml:"policy,omitempty" json:"policy,omitempty"`
	Shell           Shell                `yaml:"shell" json:"shell"`
	Harnesses       []Harness            `yaml:"harnesses,omitempty" json:"harnesses,omitempty"`
	Unresolved      []Unresolved         `yaml:"unresolved" json:"unresolved"`
	Conflicts       []component.Conflict `yaml:"conflicts" json:"conflicts"`
	Notices         []string             `yaml:"notices" json:"notices"`
}

// Selected is a value with the source that selected it.
type Selected struct {
	Value  string `yaml:"value" json:"value"`
	Source string `yaml:"source" json:"source"`
}

// EngineInfo is the selected container engine.
type EngineInfo struct {
	Name   string `yaml:"name" json:"name"`
	Source string `yaml:"source" json:"source"`
}

// BaseImage is the resolved base image.
type BaseImage struct {
	Input         string   `yaml:"input" json:"input"`
	RegistryEntry *string  `yaml:"registry_entry" json:"registry_entry"`
	Reference     string   `yaml:"reference" json:"reference"`
	Digest        string   `yaml:"digest" json:"digest"`
	Notes         []string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// ImageFacts are the probed base image facts without the probe time.
type ImageFacts struct {
	OS             OS       `yaml:"os" json:"os"`
	PackageManager string   `yaml:"package_manager" json:"package_manager"`
	User           User     `yaml:"user" json:"user"`
	Shells         []string `yaml:"shells" json:"shells"`
}

// OS identifies the base image operating system.
type OS struct {
	ID      string `yaml:"id" json:"id"`
	IDLike  string `yaml:"id_like" json:"id_like"`
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
}

// User is the base image default user.
type User struct {
	Name  string `yaml:"name" json:"name"`
	UID   int    `yaml:"uid" json:"uid"`
	GID   int    `yaml:"gid" json:"gid"`
	Group string `yaml:"group" json:"group"`
	Home  string `yaml:"home" json:"home"`
	Shell string `yaml:"shell" json:"shell"`
}

// FinalUser is the user the image runs as.
type FinalUser struct {
	Name    string `yaml:"name" json:"name"`
	UID     int    `yaml:"uid" json:"uid"`
	GID     int    `yaml:"gid" json:"gid"`
	Group   string `yaml:"group" json:"group"`
	Home    string `yaml:"home" json:"home"`
	Created bool   `yaml:"created" json:"created"`
}

// DeltaEntry is one row of the probe delta.
type DeltaEntry struct {
	ComponentKey     string `yaml:"component_key" json:"component_key"`
	Status           string `yaml:"status" json:"status"`
	InstalledVersion string `yaml:"installed_version,omitempty" json:"installed_version,omitempty"`
	RequiredVersion  string `yaml:"required_version,omitempty" json:"required_version,omitempty"`
	Action           string `yaml:"action" json:"action"`
}

// Component is one resolved component with its rendered fragment.
type Component struct {
	Key           string   `yaml:"key" json:"key"`
	Tier          string   `yaml:"tier" json:"tier"`
	SourceFile    string   `yaml:"source_file" json:"source_file"`
	InstallerKind string   `yaml:"installer_kind" json:"installer_kind"`
	Action        string   `yaml:"action" json:"action"`
	Implicit      bool     `yaml:"implicit,omitempty" json:"implicit,omitempty"`
	FragmentName  string   `yaml:"fragment_name" json:"fragment_name"`
	Fragment      string   `yaml:"fragment" json:"fragment"`
	Profiles      []string `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Bins          []string `yaml:"bins,omitempty" json:"bins,omitempty"`
	RuntimeGlobs  []string `yaml:"runtime_globs,omitempty" json:"runtime_globs,omitempty"`
	Check         Check    `yaml:"check" json:"check"`
}

// Check is the component check verify runs in the built image.
type Check struct {
	Command        string `yaml:"command" json:"command"`
	ExpectExit     int    `yaml:"expect_exit,omitempty" json:"expect_exit,omitempty"`
	Version        string `yaml:"version,omitempty" json:"version,omitempty"`
	StdoutContains string `yaml:"stdout_contains,omitempty" json:"stdout_contains,omitempty"`
}

// ImageSetup collects the environment and directories the installed
// components need, rendered into the 20-user section.
type ImageSetup struct {
	Env  []EnvVar `yaml:"env" json:"env"`
	Dirs []Dir    `yaml:"dirs" json:"dirs"`
}

// EnvVar is one image environment variable.
type EnvVar struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value" json:"value"`
}

// Dir is a directory created with its owner (root or user).
type Dir struct {
	Path  string `yaml:"path" json:"path"`
	Owner string `yaml:"owner" json:"owner"`
}

// Mise is the generated mise configuration.
type Mise struct {
	TOML     string `yaml:"toml" json:"toml"`
	LockPath string `yaml:"lock_path" json:"lock_path"`
	Platform string `yaml:"platform" json:"platform"`
}

// Profile is one referenced profile with its winning tier.
type Profile struct {
	ID         string `yaml:"id" json:"id"`
	Tier       string `yaml:"tier" json:"tier"`
	SourceFile string `yaml:"source_file" json:"source_file"`
	AdaptedID  string `yaml:"adapted_id" json:"adapted_id"`
}

// Shell is the shell program, the path of the curated configuration in the
// image, its text and the components it references (manifest
// shell.components, sorted), which verify also checks in a session
// environment.
type Shell struct {
	Program    string   `yaml:"program" json:"program"`
	RCPath     string   `yaml:"rc_path,omitempty" json:"rc_path,omitempty"`
	RC         string   `yaml:"rc,omitempty" json:"rc,omitempty"`
	Components []string `yaml:"components,omitempty" json:"components,omitempty"`
}

// Harness is one baked harness: its 70-harness section, the build context
// files the section copies, its image needs and the MCP servers whose
// endpoints may need generated profiles (FR-038, FR-039).
type Harness struct {
	Name         string               `yaml:"name" json:"name"`
	Component    string               `yaml:"component" json:"component"`
	FragmentName string               `yaml:"fragment_name" json:"fragment_name"`
	Fragment     string               `yaml:"fragment" json:"fragment"`
	Files        []ContextFile        `yaml:"files,omitempty" json:"files,omitempty"`
	Env          []EnvVar             `yaml:"env,omitempty" json:"env,omitempty"`
	Dirs         []Dir                `yaml:"dirs,omitempty" json:"dirs,omitempty"`
	ReadOnly     []string             `yaml:"read_only,omitempty" json:"read_only,omitempty"`
	ReadWrite    []string             `yaml:"read_write,omitempty" json:"read_write,omitempty"`
	MCPServers   []manifest.MCPServer `yaml:"mcp_servers,omitempty" json:"mcp_servers,omitempty"`
}

// ContextFile is a host file or directory staged into the build context.
type ContextFile struct {
	// Path is relative to the build directory.
	Path string `yaml:"path" json:"path"`
	// Source is the host path.
	Source string `yaml:"source" json:"source"`
}

// Unresolved is a component the plan cannot install.
type Unresolved struct {
	Key      string `yaml:"key" json:"key"`
	Reason   string `yaml:"reason" json:"reason"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// Blocking returns the unresolved components that are not optional.
func (p *Plan) Blocking() []Unresolved {
	var out []Unresolved
	for _, u := range p.Unresolved {
		if !u.Optional {
			out = append(out, u)
		}
	}
	return out
}

// Component returns the planned component for key, or nil.
func (p *Plan) Component(key string) *Component {
	for i := range p.Components {
		if p.Components[i].Key == key {
			return &p.Components[i]
		}
	}
	return nil
}
