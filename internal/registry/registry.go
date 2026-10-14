package registry

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/shell"
)

// FileName is the registry file name, shipped at the repository root and
// read from the user configuration directory.
const FileName = "base-images.yaml"

// SchemaVersion is the registry file schema this binary reads.
const SchemaVersion = 1

// Target types a registry lists base images for.
const (
	TargetOpenShell = "openshell"
	TargetContainer = "container"
)

// KnownTargets lists the target types in sorted order.
var KnownTargets = []string{TargetContainer, TargetOpenShell}

// Entry is one base image the registry allows (FR-010).
type Entry struct {
	// Name is unique per target type.
	Name string `yaml:"name" json:"name"`
	// Ref is the image reference.
	Ref string `yaml:"ref" json:"ref"`
	// Default marks the entry used when the manifest names no base image.
	Default bool `yaml:"default" json:"default"`
	// Notes is free text shown in the plan notices.
	Notes string `yaml:"notes,omitempty" json:"notes,omitempty"`
	// OvermountHome is the path the OpenShell runtime mounts the workspace
	// volume over in sandboxes from this image, which is also the image
	// WORKDIR. Nothing sandcast bakes may lie under it (spec 003). Optional,
	// for openshell entries only; empty means /sandbox, the path of the
	// OpenShell sandbox base images.
	OvermountHome string `yaml:"overmount_home,omitempty" json:"overmount_home,omitempty"`
}

// Registry maps a target type to its base image entries.
type Registry struct {
	// Targets maps a target type to its entries in file order.
	Targets map[string][]Entry `yaml:"targets" json:"targets"`
}

// file is the on-disk shape of a registry file.
type file struct {
	SchemaVersion int                `yaml:"schema_version"`
	Targets       map[string][]Entry `yaml:"targets"`
}

func newRegistry() *Registry {
	return &Registry{Targets: map[string][]Entry{}}
}

// Parse decodes a registry file strictly. source names the file in error
// messages. A file without entries yields an empty registry. Parse checks
// each entry but does not require a default, since a user file may only add
// entries; Validate checks the complete registry.
func Parse(data []byte, source string) (*Registry, error) {
	var f file
	if err := yaml.UnmarshalWithOptions(data, &f, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: parse base image registry: %w", source, err)
	}
	if f.SchemaVersion == 0 && len(f.Targets) == 0 {
		return newRegistry(), nil
	}
	if f.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: base image registry schema_version %d is not supported, this sandcast reads schema_version %d",
			source, f.SchemaVersion, SchemaVersion)
	}
	r := &Registry{Targets: f.Targets}
	if r.Targets == nil {
		r.Targets = map[string][]Entry{}
	}
	if err := r.check(false); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return r, nil
}

// LoadShipped reads and validates base-images.yaml at the root of fsys.
func LoadShipped(fsys fs.FS) (*Registry, error) {
	data, err := fs.ReadFile(fsys, FileName)
	if err != nil {
		return nil, fmt.Errorf("read shipped base image registry: %w", err)
	}
	source := FileName + " (shipped)"
	r, err := Parse(data, source)
	if err != nil {
		return nil, err
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return r, nil
}

// LoadUser reads the user registry file at path. A missing file yields an
// empty registry and no error.
func LoadUser(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newRegistry(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user base image registry: %w", err)
	}
	return Parse(data, path)
}

// UserPath returns the user registry file location,
// <config dir>/base-images.yaml.
func UserPath() (string, error) {
	dir, err := fsutil.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Merge combines the shipped and user registries by entry name per target
// type; the user entry wins. A user entry marked as default clears the
// shipped default of its target type. A user entry that replaces a shipped
// entry keeps the shipped default flag unless the user file marks a default
// of its own for that target type. The result is validated.
func Merge(shipped, user *Registry) (*Registry, error) {
	out := newRegistry()
	if shipped != nil {
		for t, entries := range shipped.Targets {
			out.Targets[t] = slices.Clone(entries)
		}
	}
	if user != nil {
		for _, t := range slices.Sorted(maps.Keys(user.Targets)) {
			entries := out.Targets[t]
			userDefault := slices.ContainsFunc(user.Targets[t], func(e Entry) bool { return e.Default })
			if userDefault {
				for i := range entries {
					entries[i].Default = false
				}
			}
			for _, ue := range user.Targets[t] {
				i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == ue.Name })
				if i < 0 {
					entries = append(entries, ue)
					continue
				}
				if !userDefault {
					ue.Default = entries[i].Default
				}
				entries[i] = ue
			}
			out.Targets[t] = entries
		}
	}
	if err := out.Validate(); err != nil {
		return nil, fmt.Errorf("merged base image registry: %w", err)
	}
	return out, nil
}

// Validate checks that every target type is known and has exactly one
// default, and that every entry has a unique name and a reference.
func (r *Registry) Validate() error {
	return r.check(true)
}

// check reports all entry problems in a deterministic order. requireDefault
// selects between "exactly one" and "at most one" default per target type.
func (r *Registry) check(requireDefault bool) error {
	var errs []error
	for _, t := range slices.Sorted(maps.Keys(r.Targets)) {
		if err := checkTarget(t); err != nil {
			errs = append(errs, err)
			continue
		}
		seen := map[string]bool{}
		var defaults []string
		for i, e := range r.Targets[t] {
			switch {
			case strings.TrimSpace(e.Name) == "":
				errs = append(errs, fmt.Errorf("target %s, entry %d: name is empty", t, i+1))
			case seen[e.Name]:
				errs = append(errs, fmt.Errorf("target %s: duplicate entry name %q", t, e.Name))
			}
			seen[e.Name] = true
			if strings.TrimSpace(e.Ref) == "" {
				errs = append(errs, fmt.Errorf("target %s, entry %d (%s): ref is empty", t, i+1, e.Name))
			}
			if e.Default {
				defaults = append(defaults, e.Name)
			}
			if e.OvermountHome != "" {
				switch {
				case t != TargetOpenShell:
					errs = append(errs, fmt.Errorf("target %s, entry %d (%s): overmount_home applies to openshell entries only", t, i+1, e.Name))
				case !shell.SafeAbsPath(e.OvermountHome):
					errs = append(errs, fmt.Errorf("target %s, entry %d (%s): overmount_home %q must be an absolute path of letters, digits, '.', '_' and '-' without . or .. segments",
						t, i+1, e.Name, e.OvermountHome))
				}
			}
		}
		switch {
		case len(defaults) > 1:
			errs = append(errs, fmt.Errorf("target %s: %d entries are marked default (%s), want exactly one",
				t, len(defaults), strings.Join(defaults, ", ")))
		case len(defaults) == 0 && requireDefault:
			errs = append(errs, fmt.Errorf("target %s: no entry is marked default, want exactly one", t))
		}
	}
	return errors.Join(errs...)
}

// Default returns the default entry of a target type.
func (r *Registry) Default(target string) (Entry, error) {
	if err := checkTarget(target); err != nil {
		return Entry{}, err
	}
	for _, e := range r.Targets[target] {
		if e.Default {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("base image registry has no default entry for target %s", target)
}

// List returns the entries of a target type, the default first and the rest
// sorted by name.
func (r *Registry) List(target string) []Entry {
	entries := slices.Clone(r.Targets[target])
	slices.SortStableFunc(entries, func(a, b Entry) int {
		if a.Default != b.Default {
			if a.Default {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return entries
}

// Resolve maps a manifest base_image value to a registry entry. An empty
// input selects the target default; otherwise input matches an entry name,
// then an entry reference. Any other input is taken as an image reference
// outside the registry: the returned entry carries only Ref, inRegistry is
// false, and notice says that no registry notes apply.
func (r *Registry) Resolve(input, target string) (entry Entry, inRegistry bool, notice string, err error) {
	if err := checkTarget(target); err != nil {
		return Entry{}, false, "", err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		e, err := r.Default(target)
		if err != nil {
			return Entry{}, false, "", err
		}
		return e, true, "", nil
	}
	entries := r.List(target)
	if i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == input }); i >= 0 {
		return entries[i], true, "", nil
	}
	if i := slices.IndexFunc(entries, func(e Entry) bool { return e.Ref == input }); i >= 0 {
		return entries[i], true, "", nil
	}
	notice = fmt.Sprintf("base image %q is not in the registry; no registry notes apply", input)
	return Entry{Ref: input}, false, notice, nil
}

func checkTarget(target string) error {
	if !slices.Contains(KnownTargets, target) {
		return fmt.Errorf("unknown target %q, want one of %s", target, strings.Join(KnownTargets, ", "))
	}
	return nil
}
