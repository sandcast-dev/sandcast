package component

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"
)

// Tier is the source of a component definition (FR-016).
type Tier string

// Component tiers in precedence order: a later tier overrides an earlier one.
const (
	TierShipped  Tier = "shipped"
	TierRegistry Tier = "registry"
	TierProject  Tier = "project"
)

// Installer kinds.
const (
	KindMise    = "mise"
	KindPackage = "package"
	KindScript  = "script"
)

// Package manager names accepted in package installers.
var PackageManagers = []string{"apt-get", "dnf", "yum", "apk"}

// Component is one component definition (contracts/component.schema.yaml).
type Component struct {
	Key          string      `yaml:"key" json:"key"`
	Name         string      `yaml:"name" json:"name"`
	Description  string      `yaml:"description,omitempty" json:"description,omitempty"`
	Match        *Match      `yaml:"match,omitempty" json:"match,omitempty"`
	Installer    Installer   `yaml:"installer" json:"installer"`
	Check        Check       `yaml:"check" json:"check"`
	Image        *ImageNeeds `yaml:"image,omitempty" json:"image,omitempty"`
	Provides     []string    `yaml:"provides,omitempty" json:"provides,omitempty"`
	Profiles     []string    `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Targets      []string    `yaml:"targets,omitempty" json:"targets,omitempty"`
	PromotedFrom string      `yaml:"promoted_from,omitempty" json:"promoted_from,omitempty"`
	// EmulationUnsafe marks a tool known to fail under user-mode emulation
	// (QEMU). plan and build print a notice when the build platform differs
	// from the engine host's architecture (FR-016).
	EmulationUnsafe bool `yaml:"emulation_unsafe,omitempty" json:"emulation_unsafe,omitempty"`

	// Tier and SourceFile are set by the loader and never serialized.
	Tier       Tier   `yaml:"-" json:"-"`
	SourceFile string `yaml:"-" json:"-"`
}

// Match lists the conditions under which capture proposes the component.
type Match struct {
	Files     []string `yaml:"files,omitempty" json:"files,omitempty"`
	HostTools []string `yaml:"host_tools,omitempty" json:"host_tools,omitempty"`
	Harnesses []string `yaml:"harnesses,omitempty" json:"harnesses,omitempty"`
	Always    bool     `yaml:"always,omitempty" json:"always,omitempty"`
}

// Installer holds exactly one of the three installer kinds.
type Installer struct {
	Mise    *MiseInstaller    `yaml:"mise,omitempty" json:"mise,omitempty"`
	Package *PackageInstaller `yaml:"package,omitempty" json:"package,omitempty"`
	Script  *ScriptInstaller  `yaml:"script,omitempty" json:"script,omitempty"`
}

// MiseInstaller installs a tool through mise.
type MiseInstaller struct {
	Tool    string   `yaml:"tool" json:"tool"`
	Version string   `yaml:"version,omitempty" json:"version,omitempty"`
	Bins    []string `yaml:"bins,omitempty" json:"bins,omitempty"`
}

// PackageInstaller installs system packages through the probed package
// manager.
type PackageInstaller struct {
	Packages map[string][]string `yaml:"packages" json:"packages"`
	Bins     []string            `yaml:"bins,omitempty" json:"bins,omitempty"`
}

// ScriptInstaller runs a shell script; it is the recipe escape hatch.
type ScriptInstaller struct {
	Run    string   `yaml:"run" json:"run"`
	AsRoot bool     `yaml:"as_root,omitempty" json:"as_root,omitempty"`
	Bins   []string `yaml:"bins,omitempty" json:"bins,omitempty"`
}

// Check is the probe check run in base images and built images.
type Check struct {
	Command string `yaml:"command" json:"command"`
	Expect  Expect `yaml:"expect,omitempty" json:"expect,omitempty"`
}

// Expect is the expected outcome of a check.
type Expect struct {
	Exit           int    `yaml:"exit,omitempty" json:"exit,omitempty"`
	Version        string `yaml:"version,omitempty" json:"version,omitempty"`
	StdoutContains string `yaml:"stdout_contains,omitempty" json:"stdout_contains,omitempty"`
}

// ImageNeeds lists what the component needs from the image.
type ImageNeeds struct {
	ReadOnly     []string          `yaml:"read_only,omitempty" json:"read_only,omitempty"`
	ReadWrite    []string          `yaml:"read_write,omitempty" json:"read_write,omitempty"`
	Env          map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Dirs         []Dir             `yaml:"dirs,omitempty" json:"dirs,omitempty"`
	RuntimeGlobs []string          `yaml:"runtime_globs,omitempty" json:"runtime_globs,omitempty"`
}

// Dir is a directory to create with its owner (root or user).
type Dir struct {
	Path  string `yaml:"path" json:"path"`
	Owner string `yaml:"owner,omitempty" json:"owner,omitempty"`
}

// OwnerOrDefault returns the owner, defaulting to user.
func (d Dir) OwnerOrDefault() string {
	if d.Owner == "" {
		return "user"
	}
	return d.Owner
}

// Clone returns a deep copy.
func (n *ImageNeeds) Clone() *ImageNeeds {
	if n == nil {
		return nil
	}
	out := *n
	out.ReadOnly = slices.Clone(n.ReadOnly)
	out.ReadWrite = slices.Clone(n.ReadWrite)
	out.RuntimeGlobs = slices.Clone(n.RuntimeGlobs)
	out.Dirs = slices.Clone(n.Dirs)
	out.Env = maps.Clone(n.Env)
	return &out
}

// Kind returns mise, package or script, or "" when no installer is set.
func (c *Component) Kind() string {
	switch {
	case c.Installer.Mise != nil:
		return KindMise
	case c.Installer.Package != nil:
		return KindPackage
	case c.Installer.Script != nil:
		return KindScript
	}
	return ""
}

// Bins returns the binaries the component installs: the installer's bins,
// else provides, else the key.
func (c *Component) Bins() []string {
	var bins []string
	switch {
	case c.Installer.Mise != nil:
		bins = c.Installer.Mise.Bins
	case c.Installer.Package != nil:
		bins = c.Installer.Package.Bins
	case c.Installer.Script != nil:
		bins = c.Installer.Script.Bins
	}
	if len(bins) == 0 {
		bins = c.Provides
	}
	if len(bins) == 0 {
		bins = []string{c.Key}
	}
	return bins
}

// ProvidedBinaries returns provides, falling back to the installer bins; it
// is the set used for conflict detection.
func (c *Component) ProvidedBinaries() []string {
	if len(c.Provides) > 0 {
		return c.Provides
	}
	return c.Bins()
}

// AppliesTo reports whether the component is used for target.
func (c *Component) AppliesTo(target string) bool {
	return len(c.Targets) == 0 || slices.Contains(c.Targets, target)
}

// VersionSelector returns the effective version selector: override when
// non-empty, else the mise installer version, else the check expectation.
func (c *Component) VersionSelector(override string) string {
	if override != "" {
		return override
	}
	if c.Installer.Mise != nil && c.Installer.Mise.Version != "" {
		return c.Installer.Mise.Version
	}
	return c.Check.Expect.Version
}

// CheckHash is the sha256 of the check command and its expected outcome; it
// keys per-component probe cache entries (FR-012).
func (c *Component) CheckHash(selector string) string {
	h := sha256.New()
	for _, s := range []string{c.Check.Command, strconv.Itoa(c.Check.Expect.Exit), selector, c.Check.Expect.StdoutContains} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Clone returns a deep copy.
func (c *Component) Clone() *Component {
	out := *c
	if c.Match != nil {
		m := *c.Match
		m.Files = slices.Clone(m.Files)
		m.HostTools = slices.Clone(m.HostTools)
		m.Harnesses = slices.Clone(m.Harnesses)
		out.Match = &m
	}
	if c.Installer.Mise != nil {
		m := *c.Installer.Mise
		m.Bins = slices.Clone(m.Bins)
		out.Installer.Mise = &m
	}
	if c.Installer.Package != nil {
		p := *c.Installer.Package
		p.Packages = map[string][]string{}
		for k, v := range c.Installer.Package.Packages {
			p.Packages[k] = slices.Clone(v)
		}
		p.Bins = slices.Clone(p.Bins)
		out.Installer.Package = &p
	}
	if c.Installer.Script != nil {
		s := *c.Installer.Script
		s.Bins = slices.Clone(s.Bins)
		out.Installer.Script = &s
	}
	out.Image = c.Image.Clone()
	out.Provides = slices.Clone(c.Provides)
	out.Profiles = slices.Clone(c.Profiles)
	out.Targets = slices.Clone(c.Targets)
	return &out
}
