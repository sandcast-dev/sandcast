package plan

import (
	"fmt"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/probe"
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
	// Dotfiles are the resolved non-shell dotfiles baked outside the home
	// (spec 003 US3). Empty when the manifest lists none.
	Dotfiles []DotfileSelection `yaml:"dotfiles,omitempty" json:"dotfiles,omitempty"`
	// Relocations are the structured decisions sandcast made because of the
	// home overmount (spec 003 US4, contracts/relocation-notice.md). Each one
	// also appears as a human-readable line in Notices.
	Relocations []RelocationNotice `yaml:"relocations,omitempty" json:"relocations,omitempty"`
}

// DotfileSelection is one resolved non-shell dotfile baked outside the
// overmounted home (spec 003 data-model "DotfileSelection"). The content is
// already passed through internal/redact, so no credential value is stored.
type DotfileSelection struct {
	// Source records how the entry entered the plan: "allowlist" (a host
	// dotfile capture proposed) or "manifest" (an explicit manifest path).
	Source string `yaml:"source" json:"source"`
	// HostPath is the manifest path the content came from. It selects the
	// relocation target and is shown in the notices.
	HostPath string `yaml:"host_path" json:"host_path"`
	// MergedFrom lists further manifest paths whose content precedes
	// HostPath's in Content. Only git configuration is merged: git reads
	// both ~/.config/git/config and ~/.gitconfig (the latter wins), so both
	// are concatenated into /etc/gitconfig in that order instead of one
	// being dropped.
	MergedFrom []string `yaml:"merged_from,omitempty" json:"merged_from,omitempty"`
	// RelocatedTo is the image path the content is baked to; never under the
	// overmount home.
	RelocatedTo string `yaml:"relocated_to" json:"relocated_to"`
	// Env holds the relocation variables this dotfile needs (for example
	// STARSHIP_CONFIG). It may be empty when the tool reads a system path
	// natively (for example /etc/gitconfig).
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Content is the redacted file content baked into the image.
	Content string `yaml:"content" json:"content"`
}

// Dotfile source values.
const (
	DotfileSourceAllowlist = "allowlist"
	DotfileSourceManifest  = "manifest"
)

// RelocationNotice is one decision sandcast reported because of the home
// overmount (spec 003 contracts/relocation-notice.md).
type RelocationNotice struct {
	// Kind is NoticeRelocated, NoticeReprovisioned or NoticeHidden.
	Kind string `yaml:"kind" json:"kind"`
	// Subject is the component key or dotfile path the decision is about.
	Subject string `yaml:"subject" json:"subject"`
	// From is the overmount-shadowed location a reprovisioned tool was
	// probed at. It is empty for a relocation, whose source is the Subject.
	From string `yaml:"from,omitempty" json:"from,omitempty"`
	// To is the persistent location chosen outside the home. A hidden
	// notice (NoticeHidden) has none.
	To string `yaml:"to,omitempty" json:"to,omitempty"`
}

// Hidden reports whether the workspace rule found every copy of the tool
// under the overmount home: the tool counts as missing and its hits are the
// ignored paths. plan.Build reports such a tool as re-provisioned when its
// installer, or a stored fragment, provisions it, and as hidden when
// nothing can.
func (e DeltaEntry) Hidden() bool {
	return e.Status == string(probe.StatusMissing) && len(e.IgnoredPaths) > 0
}

// Relocation notice kinds (contracts/relocation-notice.md).
const (
	NoticeRelocated     = "relocated"
	NoticeReprovisioned = "reprovisioned"
	// NoticeHidden is a tool found only under the overmount home that no
	// definition or fragment can provision: it stays unresolved, and the
	// notice carries no destination.
	NoticeHidden = "hidden"
)

// String renders the notice as the one human-readable line that appears in
// plan.yaml, the sandcast plan output and the sandcast build output
// (contracts/relocation-notice.md).
func (n RelocationNotice) String() string {
	switch n.Kind {
	case NoticeReprovisioned:
		return fmt.Sprintf("component %s was found only under %s (hidden once the workspace is mounted over the home); it is provisioned to %s instead",
			n.Subject, n.From, n.To)
	case NoticeHidden:
		return fmt.Sprintf("component %s was found only under %s (hidden once the workspace is mounted over the home); no definition or fragment can provision it, so it is unresolved",
			n.Subject, n.From)
	default:
		return fmt.Sprintf("%s relocated to %s so it survives the workspace mount over the home", n.Subject, n.To)
	}
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
	// OvermountHome is the path the OpenShell runtime mounts the workspace
	// volume over, which is also the WORKDIR (spec 003). It comes from the
	// registry entry's overmount_home and defaults to DefaultOvermountHome;
	// it is empty for the container target, which is never overmounted.
	OvermountHome string `yaml:"overmount_home,omitempty" json:"overmount_home,omitempty"`
}

// ImageFacts are the probed base image facts without the probe time.
type ImageFacts struct {
	OS             OS       `yaml:"os" json:"os"`
	PackageManager string   `yaml:"package_manager" json:"package_manager"`
	User           User     `yaml:"user" json:"user"`
	Shells         []string `yaml:"shells" json:"shells"`
	// HomeRCResetsPath lists the files in the default user's home that
	// assign PATH without referencing $PATH (R-05).
	HomeRCResetsPath []string `yaml:"home_rc_resets_path,omitempty" json:"home_rc_resets_path,omitempty"`
	// Path lists the directories of the base image's PATH, in order.
	Path []string `yaml:"path,omitempty" json:"path,omitempty"`
	// Env lists the base image variables other than PATH whose value is an
	// absolute path or a colon list of them, sorted by name.
	Env []EnvVar `yaml:"env,omitempty" json:"env,omitempty"`
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
	// LockedVersion is the version mise.lock locks for the target platform
	// (mise components with a lock entry only).
	LockedVersion string `yaml:"locked_version,omitempty" json:"locked_version,omitempty"`
	// IgnoredPaths are binary paths the workspace rule discarded because
	// they lie under the overmount home. With Status missing they are every
	// hit of the tool (Hidden), which plan.Build reports as re-provisioned.
	IgnoredPaths []string `yaml:"ignored_paths,omitempty" json:"ignored_paths,omitempty"`
	Action       string   `yaml:"action" json:"action"`
	// Notices explain decisions that depart from a plain present or
	// missing result; plan.Build moves them into the plan's notices.
	Notices []string `yaml:"-" json:"-"`
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
	// Cleared names the base image variables that pointed under the
	// overmount home only: Env sets them to the empty value, and the
	// session script unsets them (#18). Sorted.
	Cleared []string `yaml:"cleared,omitempty" json:"cleared,omitempty"`
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
// image and its text.
type Shell struct {
	Program string `yaml:"program" json:"program"`
	RCPath  string `yaml:"rc_path,omitempty" json:"rc_path,omitempty"`
	RC      string `yaml:"rc,omitempty" json:"rc,omitempty"`
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
