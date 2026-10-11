package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/redact"
	"github.com/rhuss/sandcast/internal/version"
)

// Target names.
const (
	TargetOpenShell = "openshell"
	TargetContainer = "container"
)

// DefaultTag is the image tag used when a target sets none.
const DefaultTag = "latest"

// DefaultAllowedDomainsFile is the container target's allowed-domains file.
const DefaultAllowedDomainsFile = "allowed-domains.txt"

// Manifest is .sandcast/manifest.yaml (contracts/manifest.schema.yaml).
type Manifest struct {
	SchemaVersion int            `yaml:"schema_version" json:"schema_version"`
	Name          string         `yaml:"name" json:"name"`
	BaseImage     string         `yaml:"base_image" json:"base_image"`
	Engine        string         `yaml:"engine,omitempty" json:"engine,omitempty"`
	Platform      string         `yaml:"platform,omitempty" json:"platform,omitempty"`
	Components    []ComponentRef `yaml:"components" json:"components"`
	Harnesses     []HarnessRef   `yaml:"harnesses,omitempty" json:"harnesses,omitempty"`
	Shell         *Shell         `yaml:"shell,omitempty" json:"shell,omitempty"`
	Targets       Targets        `yaml:"targets" json:"targets"`
}

// ComponentRef selects one component by key.
type ComponentRef struct {
	Key      string `yaml:"key" json:"key"`
	Version  string `yaml:"version,omitempty" json:"version,omitempty"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// HarnessRef selects an agent harness and the extensions baked with it.
type HarnessRef struct {
	Name       string      `yaml:"name" json:"name"`
	MCPServers []MCPServer `yaml:"mcp_servers,omitempty" json:"mcp_servers,omitempty"`
	Plugins    []Extension `yaml:"plugins,omitempty" json:"plugins,omitempty"`
	Skills     []Extension `yaml:"skills,omitempty" json:"skills,omitempty"`
}

// MCPServer is one MCP server configuration. Env values are literals or
// ${SANDCAST_SECRET_N} placeholders, never credentials (FR-008).
type MCPServer struct {
	Name    string            `yaml:"name" json:"name"`
	Command []string          `yaml:"command,omitempty" json:"command,omitempty"`
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// Extension is a harness plugin or skill with its source.
type Extension struct {
	Name   string `yaml:"name" json:"name"`
	Source string `yaml:"source" json:"source"`
}

// Shell is the curated shell configuration.
type Shell struct {
	Program    string   `yaml:"program" json:"program"`
	RC         string   `yaml:"rc,omitempty" json:"rc,omitempty"`
	Components []string `yaml:"components,omitempty" json:"components,omitempty"`
}

// Targets holds the per-target image settings.
type Targets struct {
	OpenShell *Target `yaml:"openshell,omitempty" json:"openshell,omitempty"`
	Container *Target `yaml:"container,omitempty" json:"container,omitempty"`
}

// Target is the image name and options for one target.
type Target struct {
	Image              string `yaml:"image" json:"image"`
	Tag                string `yaml:"tag,omitempty" json:"tag,omitempty"`
	Push               bool   `yaml:"push,omitempty" json:"push,omitempty"`
	AllowedDomainsFile string `yaml:"allowed_domains_file,omitempty" json:"allowed_domains_file,omitempty"`
}

// TagOrDefault returns the tag, defaulting to latest.
func (t *Target) TagOrDefault() string {
	if t == nil || t.Tag == "" {
		return DefaultTag
	}
	return t.Tag
}

// Ref returns image:tag.
func (t *Target) Ref() string { return t.Image + ":" + t.TagOrDefault() }

// AllowedDomains returns the allowed-domains file name with its default.
func (t *Target) AllowedDomains() string {
	if t == nil || t.AllowedDomainsFile == "" {
		return DefaultAllowedDomainsFile
	}
	return t.AllowedDomainsFile
}

// Target returns the settings for name, or nil when the manifest does not
// declare that target.
func (m *Manifest) Target(name string) *Target {
	switch name {
	case TargetOpenShell:
		return m.Targets.OpenShell
	case TargetContainer:
		return m.Targets.Container
	}
	return nil
}

// TargetNames returns the declared targets in fixed order.
func (m *Manifest) TargetNames() []string {
	var out []string
	if m.Targets.OpenShell != nil {
		out = append(out, TargetOpenShell)
	}
	if m.Targets.Container != nil {
		out = append(out, TargetContainer)
	}
	return out
}

// DefaultTarget returns openshell when declared, else container.
func (m *Manifest) DefaultTarget() string {
	if names := m.TargetNames(); len(names) > 0 {
		return names[0]
	}
	return TargetOpenShell
}

// ComponentKeys returns the referenced component keys in manifest order.
func (m *Manifest) ComponentKeys() []string {
	out := make([]string, 0, len(m.Components))
	for _, c := range m.Components {
		out = append(out, c.Key)
	}
	return out
}

// Harness returns the harness entry for name, or nil.
func (m *Manifest) Harness(name string) *HarnessRef {
	for i := range m.Harnesses {
		if m.Harnesses[i].Name == name {
			return &m.Harnesses[i]
		}
	}
	return nil
}

// Diagnostic is one validation finding with its location.
type Diagnostic struct {
	Path    string `json:"path" yaml:"path"`
	Line    int    `json:"line,omitempty" yaml:"line,omitempty"`
	Message string `json:"message" yaml:"message"`
}

func (d Diagnostic) String() string {
	if d.Line > 0 {
		return fmt.Sprintf("line %d: %s: %s", d.Line, d.Path, d.Message)
	}
	return fmt.Sprintf("%s: %s", d.Path, d.Message)
}

// ValidationError carries every diagnostic of a failed validation.
type ValidationError struct {
	Source      string
	Diagnostics []Diagnostic
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		parts = append(parts, d.String())
	}
	return fmt.Sprintf("%s: invalid manifest:\n  %s", e.Source, strings.Join(parts, "\n  "))
}

var (
	namePattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	keyPattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	platformPattern = regexp.MustCompile(`^linux/(amd64|arm64)$`)
	knownHarnesses  = map[string]bool{"claude-code": true, "codex": true, "opencode": true, "pi": true}
)

// Load reads and validates a manifest file.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no manifest at %s: run \"sandcast init\" first: %w", path, err)
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return Parse(data, path)
}

// Parse decodes and validates manifest bytes. source names the file in
// diagnostics.
func Parse(data []byte, source string) (*Manifest, error) {
	if err := checkVersionField(data, source); err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.UnmarshalWithOptions(data, &m, yaml.Strict()); err != nil {
		// A decode error can quote the offending source snippet (an unknown
		// field name, for example). Scan the raw bytes for credential shapes
		// first, so a token pasted into the manifest is refused with a safe
		// message instead of echoed back in the decode error.
		if serr := refuseSecretsRaw(data, source); serr != nil {
			return nil, serr
		}
		return nil, &ValidationError{Source: source, Diagnostics: []Diagnostic{decodeDiagnostic(err)}}
	}
	// Scan for credential shapes before Validate, whose diagnostics quote
	// field values. A token in any field is refused here, never quoted.
	if serr := refuseSecrets(data, source); serr != nil {
		return nil, serr
	}
	if diags := m.Validate(); len(diags) > 0 {
		locate(data, diags)
		return nil, &ValidationError{Source: source, Diagnostics: diags}
	}
	return &m, nil
}

// Validate checks every rule that does not need the component or profile
// tiers. It returns diagnostics without line numbers.
func (m *Manifest) Validate() []Diagnostic {
	var diags []Diagnostic
	add := func(path, format string, args ...any) {
		diags = append(diags, Diagnostic{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if err := CheckVersion(m.SchemaVersion); err != nil {
		add("$.schema_version", "%v", err)
	}
	if !namePattern.MatchString(m.Name) {
		add("$.name", "%s must be kebab-case, 1 to 63 characters", safeValue(m.Name))
	}
	if strings.TrimSpace(m.BaseImage) == "" {
		add("$.base_image", "required: a registry entry name or an image reference")
	}
	if m.Engine != "" && m.Engine != "podman" && m.Engine != "docker" {
		add("$.engine", "%s is not podman or docker", safeValue(m.Engine))
	}
	if m.Platform != "" && !platformPattern.MatchString(m.Platform) {
		add("$.platform", "%s is not linux/amd64 or linux/arm64", safeValue(m.Platform))
	}
	seen := map[string]int{}
	for i, c := range m.Components {
		path := fmt.Sprintf("$.components[%d].key", i)
		if !keyPattern.MatchString(c.Key) {
			add(path, "%s is not a valid component key", safeValue(c.Key))
		}
		if j, dup := seen[c.Key]; dup {
			add(path, "duplicate component key %s (also at components[%d])", safeValue(c.Key), j)
		}
		seen[c.Key] = i
	}
	for i, h := range m.Harnesses {
		base := fmt.Sprintf("$.harnesses[%d]", i)
		if !knownHarnesses[h.Name] {
			add(base+".name", "unknown harness %s (known: claude-code, codex, opencode, pi)", safeValue(h.Name))
		}
		for j, s := range h.MCPServers {
			p := fmt.Sprintf("%s.mcp_servers[%d]", base, j)
			if s.Name == "" {
				add(p+".name", "required")
			}
			if len(s.Command) == 0 && s.URL == "" {
				add(p, "needs a command or a url")
			}
			if len(s.Command) > 0 && s.URL != "" {
				add(p, "has both a command and a url; choose one")
			}
		}
		for j, e := range h.Plugins {
			if e.Name == "" || e.Source == "" {
				add(fmt.Sprintf("%s.plugins[%d]", base, j), "name and source are required")
			}
		}
		for j, e := range h.Skills {
			if e.Name == "" || e.Source == "" {
				add(fmt.Sprintf("%s.skills[%d]", base, j), "name and source are required")
			}
		}
	}
	if m.Shell != nil && m.Shell.Program != "zsh" && m.Shell.Program != "bash" {
		add("$.shell.program", "%s is not zsh or bash", safeValue(m.Shell.Program))
	}
	if m.Targets.OpenShell == nil && m.Targets.Container == nil {
		add("$.targets", "at least one target (openshell or container) is required")
	}
	if t := m.Targets.OpenShell; t != nil {
		if t.Image == "" {
			add("$.targets.openshell.image", "required")
		}
		if t.AllowedDomainsFile != "" {
			add("$.targets.openshell.allowed_domains_file", "only the container target writes an allowed-domains file")
		}
	}
	if t := m.Targets.Container; t != nil {
		if t.Image == "" {
			add("$.targets.container.image", "required")
		}
		if t.AllowedDomainsFile != "" {
			if err := CheckAllowedDomainsFile(t.AllowedDomainsFile); err != nil {
				add("$.targets.container.allowed_domains_file", "%v", err)
			}
		}
	}
	return diags
}

// CheckAllowedDomainsFile fails when name cannot be the container target's
// allowed-domains file. The build renames the file into the build directory
// while it runs, so name must be a plain file name (IsPlainFileName), must
// not start with a dot (the lock and the temporary files of atomic writes
// do), and must not be an entry sandcast writes there itself
// (fsutil.BuildEntries, compared without case): over .lock the rename would
// defeat the build lock, over build.log it would hide the pass-2 log from
// the credential scan.
func CheckAllowedDomainsFile(name string) error {
	// safeValue keeps a credential pasted into this field out of the error.
	switch {
	case !IsPlainFileName(name):
		return fmt.Errorf("%s must be a plain file name; the file is written inside the build directory", safeValue(name))
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("%s must not start with a dot; sandcast keeps hidden files of its own in the build directory", safeValue(name))
	case fsutil.IsBuildEntry(name):
		return fmt.Errorf("%s is a name sandcast writes in the build directory itself; choose another file name", safeValue(name))
	}
	return nil
}

// IsPlainFileName reports whether name is a single path element that stays
// inside the directory it is joined to: not blank, not "." or "..", and
// free of path separators, NUL and other control characters.
func IsPlainFileName(name string) bool {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return false
		}
	}
	return filepath.Base(name) == name
}

// Marshal encodes the manifest with fixed encoder settings.
func Marshal(m *Manifest) ([]byte, error) {
	data, err := yaml.MarshalWithOptions(m,
		yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return data, nil
}

// Save validates and writes the manifest atomically. A manifest that fails
// validation or carries a credential shape is never written.
func Save(path string, m *Manifest) error {
	data, err := Marshal(m)
	if err != nil {
		return err
	}
	// Scan for credential shapes before Validate, whose diagnostics quote
	// field values. A token in any field is refused here, never quoted.
	if err := refuseSecrets(data, path); err != nil {
		return err
	}
	if diags := m.Validate(); len(diags) > 0 {
		return &ValidationError{Source: path, Diagnostics: diags}
	}
	return fsutil.AtomicWriteFile(path, data, 0o644)
}

// CheckSecrets fails when the manifest carries a credential-shaped value
// (FR-008). plan calls it, so a hand-edited manifest never reaches a plan.
func CheckSecrets(m *Manifest, source string) error {
	data, err := Marshal(m)
	if err != nil {
		return err
	}
	return refuseSecrets(data, source)
}

// ErrSecret marks a manifest that carries a credential-shaped value.
var ErrSecret = errors.New("the manifest contains a credential-shaped value")

// redactedValue stands in for a credential-shaped field value in a diagnostic.
const redactedValue = "[redacted]"

// safeValue renders value for a validation diagnostic. When value matches a
// credential shape it is replaced with redactedValue, so a token pasted into a
// field never reaches the error output (CI logs, agent context). It reuses the
// same detector refuseSecrets does (redact.Scan via ScanYAML). Other values
// are quoted with %q as before.
func safeValue(value string) string {
	if len(redact.Scan(value)) > 0 {
		return redactedValue
	}
	return fmt.Sprintf("%q", value)
}

// refuseSecretsRaw scans the raw manifest bytes for credential shapes. Parse
// uses it when the YAML will not decode, so the key-path aware ScanYAML (which
// must parse the document first) is unavailable and its parse error could
// itself quote a source line. Like refuseSecrets it reports the kind and line,
// never the value.
func refuseSecretsRaw(data []byte, path string) error {
	hits := redact.Scan(string(data))
	if len(hits) == 0 {
		return nil
	}
	diags := make([]Diagnostic, 0, len(hits))
	for _, h := range hits {
		diags = append(diags, Diagnostic{Path: "$", Line: h.Line,
			Message: fmt.Sprintf("%v (%s); use a ${SANDCAST_SECRET_N} placeholder and supply the value as a provider credential at runtime", ErrSecret, h.Kind)})
	}
	return &ValidationError{Source: path, Diagnostics: diags}
}

// refuseSecrets runs the redaction scan on the encoded manifest before it is
// written (FR-008, R-18). It reports the key path and line, never the value.
func refuseSecrets(data []byte, path string) error {
	hits, err := redact.ScanYAML(data)
	if err != nil {
		return fmt.Errorf("scan manifest for credentials: %w", err)
	}
	if len(hits) == 0 {
		return nil
	}
	diags := make([]Diagnostic, 0, len(hits))
	for _, h := range hits {
		diags = append(diags, Diagnostic{Path: "$." + h.Path, Line: h.Line,
			Message: fmt.Sprintf("%v (%s); use a ${SANDCAST_SECRET_N} placeholder and supply the value as a provider credential at runtime", ErrSecret, h.Kind)})
	}
	return &ValidationError{Source: path, Diagnostics: diags}
}

var positionPrefix = regexp.MustCompile(`^\[(\d+):\d+\]\s*`)

// decodeDiagnostic turns a goccy decode error ("[3:1] unknown field ...")
// into a diagnostic with the line number.
func decodeDiagnostic(err error) Diagnostic {
	first, _, _ := strings.Cut(strings.TrimSpace(yaml.FormatError(err, false, false)), "\n")
	line := 0
	if m := positionPrefix.FindStringSubmatch(first); m != nil {
		_, _ = fmt.Sscanf(m[1], "%d", &line)
		first = strings.TrimPrefix(first, m[0])
	}
	return Diagnostic{Path: "$", Line: line, Message: first}
}

// locate fills in line numbers for diagnostics whose path exists in data.
func locate(data []byte, diags []Diagnostic) {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return
	}
	for i := range diags {
		diags[i].Line = lineOf(file, diags[i].Path)
	}
}

func lineOf(file *ast.File, path string) int {
	for p := path; p != "" && p != "$"; p = parentPath(p) {
		yp, err := yaml.PathString(p)
		if err != nil {
			continue
		}
		node, err := yp.FilterFile(file)
		if err == nil && node != nil && node.GetToken() != nil {
			return node.GetToken().Position.Line
		}
	}
	return 0
}

func parentPath(p string) string {
	if strings.HasSuffix(p, "]") {
		if i := strings.LastIndex(p, "["); i > 0 {
			return p[:i]
		}
	}
	if i := strings.LastIndex(p, "."); i > 0 {
		return p[:i]
	}
	return ""
}

// SortedKeys returns the keys of a string map in order; used by renderers
// that iterate maps from the manifest.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Skeleton returns the manifest sandcast init writes.
func Skeleton(name, baseImage, target string) *Manifest {
	m := &Manifest{
		SchemaVersion: version.ManifestSchemaVersion,
		Name:          name,
		BaseImage:     baseImage,
		Components:    []ComponentRef{},
	}
	image := "localhost/" + name
	if target == TargetContainer {
		m.Targets.Container = &Target{Image: image}
	} else {
		m.Targets.OpenShell = &Target{Image: image}
	}
	return m
}

// Equal reports whether two manifests encode to the same bytes.
func Equal(a, b *Manifest) bool {
	da, errA := Marshal(a)
	db, errB := Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(da, db)
}
