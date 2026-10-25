package component

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
)

// ErrEndpointsForbidden marks a component that carries network endpoints;
// endpoints belong in OpenShell profiles (FR-013).
var ErrEndpointsForbidden = errors.New(`"endpoints" is not allowed in a component; reference OpenShell profile ids under "profiles" instead`)

var (
	keyPattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	profilePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// hostToolPattern accepts bare command names only. capture runs host
	// tools by name on the host, so a path would let a repository-supplied
	// component execute a file from the checkout.
	hostToolPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	// binPattern is the binary name pattern of contracts/manifest-component.md,
	// the same as manifest's, so a manifest entry promoted to a recipe keeps
	// its binaries valid.
	binPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)
	// The patterns below keep declarative fields declarative (FR-013): their
	// values are rendered into Containerfile instructions and RUN lines, so
	// they must not carry line breaks or shell syntax.
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	dirPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._@+/-]*$`)
	// packagePattern admits version and architecture pins such as
	// curl=8.5.0-1 (apt), libc6:amd64 (apt), curl~8.5 (apk) and
	// ripgrep@edge (apk), but no option, glob, redirect or separator.
	packagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:~=@-]*$`)
)

// HeredocDelimiter ends the heredoc that wraps a script installer's run text
// in the rendered Containerfile.
const HeredocDelimiter = "SANDCAST_EOF"

// ValidEnvName reports whether name is a portable environment variable name.
func ValidEnvName(name string) bool { return envNamePattern.MatchString(name) }

// ValidDirPath reports whether p is an absolute path made of characters that
// need no shell quoting.
func ValidDirPath(p string) bool { return dirPathPattern.MatchString(p) }

// ValidPackageName reports whether name is a package name, optionally with a
// version or architecture pin, that a package manager command line can take
// unquoted.
func ValidPackageName(name string) bool { return packagePattern.MatchString(name) }

// HasControl reports whether s contains a control character such as a line
// break, carriage return, tab or NUL.
func HasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// EndsHeredoc reports whether run has a line that would end the heredoc
// around it early. Surrounding whitespace is ignored, so no variant of the
// delimiter line passes.
func EndsHeredoc(run string) bool {
	for _, line := range strings.Split(run, "\n") {
		if strings.TrimSpace(line) == HeredocDelimiter {
			return true
		}
	}
	return false
}

// Parse decodes and validates one component file. source names the file in
// errors and is recorded as SourceFile.
func Parse(data []byte, source string, tier Tier) (*Component, error) {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("%s: %s", source, yaml.FormatError(err, false, false))
	}
	if _, ok := top["endpoints"]; ok {
		return nil, fmt.Errorf("%s: %w", source, ErrEndpointsForbidden)
	}
	var c Component
	if err := yaml.UnmarshalWithOptions(data, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %s", source, yaml.FormatError(err, false, false))
	}
	c.Tier = tier
	c.SourceFile = source
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &c, nil
}

// validBinName reports whether b is a bare command name that a component
// may list as a binary. The probe passes the binaries as one
// space-separated list, and verify runs command -v on each.
func validBinName(b string) bool {
	return binPattern.MatchString(b) && b != "." && b != ".."
}

// Validate checks the component against the schema rules.
func (c *Component) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if !keyPattern.MatchString(c.Key) {
		add("key %q must be kebab-case", c.Key)
	}
	if strings.TrimSpace(c.Name) == "" {
		add("name is required")
	}
	checkBins := func(field string, bins []string) {
		for _, b := range bins {
			if !validBinName(b) {
				add("%s: %q must be a bare command name", field, b)
			}
		}
	}
	checkBins("provides", c.Provides)
	n := 0
	if c.Installer.Mise != nil {
		n++
		if strings.TrimSpace(c.Installer.Mise.Tool) == "" {
			add("installer.mise.tool is required")
		}
		checkBins("installer.mise.bins", c.Installer.Mise.Bins)
	}
	if c.Installer.Package != nil {
		n++
		checkBins("installer.package.bins", c.Installer.Package.Bins)
		if len(c.Installer.Package.Packages) == 0 {
			add("installer.package.packages needs at least one package manager")
		}
		for _, pm := range slices.Sorted(maps.Keys(c.Installer.Package.Packages)) {
			pkgs := c.Installer.Package.Packages[pm]
			if !slices.Contains(PackageManagers, pm) {
				add("installer.package.packages: unknown package manager %q (known: %s)", pm, strings.Join(PackageManagers, ", "))
			}
			if len(pkgs) == 0 {
				add("installer.package.packages.%s is empty", pm)
			}
			for _, p := range pkgs {
				if !ValidPackageName(p) {
					add("installer.package.packages.%s: %q is not a valid package name", pm, p)
				}
			}
		}
	}
	if c.Installer.Script != nil {
		n++
		checkBins("installer.script.bins", c.Installer.Script.Bins)
		if strings.TrimSpace(c.Installer.Script.Run) == "" {
			add("installer.script.run is required")
		}
		if EndsHeredoc(c.Installer.Script.Run) {
			add("installer.script.run must not contain a line equal to the heredoc delimiter %s", HeredocDelimiter)
		}
	}
	if n != 1 {
		add("installer must have exactly one of mise, package, script (found %d)", n)
	}
	if strings.TrimSpace(c.Check.Command) == "" {
		add("check.command is required")
	}
	if c.Match != nil {
		for _, t := range c.Match.HostTools {
			if !hostToolPattern.MatchString(t) {
				add("match.host_tools: %q must be a bare command name, not a path", t)
			}
		}
	}
	for _, t := range c.Targets {
		if t != "openshell" && t != "container" {
			add("targets: unknown target %q", t)
		}
	}
	for _, p := range c.Profiles {
		if !profilePattern.MatchString(p) {
			add("profiles: %q is not a valid profile id", p)
		}
	}
	if c.Image != nil {
		for _, list := range [][]string{c.Image.ReadOnly, c.Image.ReadWrite, c.Image.RuntimeGlobs} {
			for _, p := range list {
				if !strings.HasPrefix(p, "/") {
					add("image path %q must be absolute", p)
				}
				if HasControl(p) {
					add("image path %q contains a control character", p)
				}
			}
		}
		for _, d := range c.Image.Dirs {
			switch {
			case !strings.HasPrefix(d.Path, "/"):
				add("image.dirs path %q must be absolute", d.Path)
			case !ValidDirPath(d.Path):
				add("image.dirs path %q may only contain letters, digits and . _ @ + / -", d.Path)
			}
			if d.Owner != "" && d.Owner != "root" && d.Owner != "user" {
				add("image.dirs owner %q must be root or user", d.Owner)
			}
		}
		for _, k := range slices.Sorted(maps.Keys(c.Image.Env)) {
			if !ValidEnvName(k) {
				add("image.env name %q must match [A-Za-z_][A-Za-z0-9_]*", k)
			}
			if HasControl(c.Image.Env[k]) {
				add("image.env %q: value contains a control character", k)
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("component %q: %s", c.Key, strings.Join(problems, "; "))
	}
	return nil
}

// LoadDir loads every *.yaml and *.yml file at the root of fsys as a
// component of tier. prefix is prepended to file names for SourceFile, for
// example "components" or ".sandcast/recipes". index.yaml is skipped (it is
// the remote registry index). Duplicate keys inside one tier are an error.
func LoadDir(fsys fs.FS, tier Tier, prefix string) ([]*Component, error) {
	if fsys == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s components: %w", tier, err)
	}
	var out []*Component
	byKey := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == "index.yaml" {
			continue
		}
		ext := path.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		source := name
		if prefix != "" {
			source = path.Join(prefix, name)
		}
		c, err := Parse(data, source, tier)
		if err != nil {
			return nil, err
		}
		if prev, dup := byKey[c.Key]; dup {
			return nil, fmt.Errorf("component key %q defined twice in the %s tier: %s and %s", c.Key, tier, prev, source)
		}
		byKey[c.Key] = source
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Marshal encodes a component with fixed encoder settings.
func Marshal(c *Component) ([]byte, error) {
	data, err := yaml.MarshalWithOptions(c,
		yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return nil, fmt.Errorf("encode component %s: %w", c.Key, err)
	}
	return data, nil
}
