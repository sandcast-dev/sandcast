package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/version"
)

// noBinaryReason is the flag reason of an adapted profile whose binaries
// resolved to nothing in the built image.
const noBinaryReason = "no binary resolved in the built image; the profile matches no process"

var (
	imageLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	digestPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// AdaptOptions controls profile adaptation.
type AdaptOptions struct {
	// Suffix asks for ids suffixed with the image label. Platform scope
	// implies it (FR-022a).
	Suffix bool
	// ImageLabel is the sanitized manifest name (see ImageLabel).
	ImageLabel string
	// ScopeHint is ScopeWorkspace (the default when empty) or ScopePlatform.
	ScopeHint string
	// Image is the built image the binaries were resolved in.
	Image ImageRef
}

// AdaptedProfile is one profile of an adapted set.
type AdaptedProfile struct {
	UpstreamID string
	AdaptedID  string
	File       string
	SourceTier Tier
	SourceFile string
	Profile    *Profile
	Flagged    bool
	FlagReason string
}

// AdaptedSet is the adapted profile set of one build (FR-022).
type AdaptedSet struct {
	Index    Index
	Profiles []AdaptedProfile
}

// Adapt copies every profile referenced by binaries (keys are profile ids,
// values the resolved absolute paths and runtime globs) out of set, rewrites
// its binaries to the deduplicated, sorted list, applies the id suffix, and
// lints the result. A profile with no binary is kept and flagged. A
// referenced id missing from set, invalid options, or any lint diagnostic
// (returned as *LintError) is an error. The set itself is not modified.
func Adapt(set Set, binaries map[string][]string, opts AdaptOptions) (*AdaptedSet, error) {
	scope := opts.ScopeHint
	if scope == "" {
		scope = ScopeWorkspace
	}
	if scope != ScopeWorkspace && scope != ScopePlatform {
		return nil, fmt.Errorf("adapt profiles: scope hint %q is not %s or %s", opts.ScopeHint, ScopeWorkspace, ScopePlatform)
	}
	if !imageLabelPattern.MatchString(opts.ImageLabel) {
		return nil, fmt.Errorf("adapt profiles: image label %q must be lowercase kebab-case", opts.ImageLabel)
	}
	if !digestPattern.MatchString(opts.Image.Digest) {
		return nil, fmt.Errorf("adapt profiles: image digest %q is not a sha256 digest", opts.Image.Digest)
	}
	suffix := opts.Suffix || scope == ScopePlatform

	ids := make([]string, 0, len(binaries))
	for id := range binaries {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := &AdaptedSet{
		Index: Index{
			SchemaVersion: IndexSchemaVersion,
			Image:         opts.Image,
			ImageLabel:    opts.ImageLabel,
			ScopeHint:     scope,
			Profiles:      []IndexProfile{},
		},
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entry, ok := set[id]
		if !ok {
			return nil, fmt.Errorf("adapt profiles: provider profile %q is referenced but defined in no profile tier", id)
		}
		bins := uniqueSorted(binaries[id])
		adapted := *entry.Profile
		adapted.ID = AdaptedID(id, opts.ImageLabel, suffix)
		adapted.Binaries = nil
		if len(bins) > 0 {
			adapted.Binaries = Binaries(bins)
		}
		ap := AdaptedProfile{
			UpstreamID: id,
			AdaptedID:  adapted.ID,
			File:       adapted.ID + ".yaml",
			SourceTier: entry.Tier,
			SourceFile: entry.SourceFile,
			Profile:    &adapted,
		}
		if len(bins) == 0 {
			ap.Flagged = true
			ap.FlagReason = noBinaryReason
		}
		out.Profiles = append(out.Profiles, ap)
		out.Index.Profiles = append(out.Index.Profiles, IndexProfile{
			UpstreamID: ap.UpstreamID,
			AdaptedID:  ap.AdaptedID,
			File:       ap.File,
			SourceTier: ap.SourceTier,
			SourceFile: ap.SourceFile,
			Binaries:   bins,
			Flagged:    ap.Flagged,
			FlagReason: ap.FlagReason,
		})
		entries = append(entries, Entry{Profile: &adapted, Tier: entry.Tier, SourceFile: entry.SourceFile})
	}
	if diags := LintSet(entries); len(diags) > 0 {
		return nil, &LintError{Diagnostics: diags}
	}
	return out, nil
}

// WriteDir writes one <adapted id>.yaml per profile plus index.yaml into
// dir, creating it as needed. Stale *.yaml and *.yml files from an earlier
// build are removed so the directory holds exactly this set. Output bytes
// depend only on the set.
func (s *AdaptedSet) WriteDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write adapted profiles: %w", err)
	}
	keep := map[string]bool{IndexFile: true}
	for _, ap := range s.Profiles {
		keep[ap.File] = true
	}
	existing, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("write adapted profiles: %w", err)
	}
	for _, e := range existing {
		if e.IsDir() || !isYAML(e.Name()) || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("write adapted profiles: remove stale file: %w", err)
		}
	}
	for i := range s.Profiles {
		ap := &s.Profiles[i]
		body, err := Marshal(ap.Profile)
		if err != nil {
			return fmt.Errorf("write adapted profiles: %w", err)
		}
		data := append([]byte(ap.header()), body...)
		if err := fsutil.AtomicWriteFile(filepath.Join(dir, ap.File), data, 0o644); err != nil {
			return fmt.Errorf("write adapted profiles: %w", err)
		}
	}
	idx, err := marshalIndex(&s.Index)
	if err != nil {
		return fmt.Errorf("write adapted profiles: %w", err)
	}
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, IndexFile), idx, 0o644); err != nil {
		return fmt.Errorf("write adapted profiles: %w", err)
	}
	return nil
}

// header is the generated comment block at the top of an adapted profile.
func (ap *AdaptedProfile) header() string {
	changed := "binaries (resolved in the built image)"
	if ap.AdaptedID != ap.UpstreamID {
		changed = "id (suffixed with the image label), " + changed
	}
	lines := []string{
		"# Generated by sandcast from an OpenShell provider profile. Do not edit;",
		"# rebuild the image to regenerate it.",
		"#",
		"# Upstream id:  " + ap.UpstreamID,
		"# Source tier:  " + string(ap.SourceTier),
		"# Source file:  " + ap.SourceFile,
		fmt.Sprintf("# Schema pin:   OpenShell %s (%s)", version.OpenShellPin, version.OpenShellCommit),
		"# Changed:      " + changed,
	}
	if ap.Flagged {
		lines = append(lines, "# Flagged:      "+ap.FlagReason)
	}
	return strings.Join(lines, "\n") + "\n\n"
}

// LabelJSON returns the value of the io.sandcast.profiles-index image label:
// {"ids":[...],"flagged":[...]} with sorted adapted ids.
func (s *AdaptedSet) LabelJSON() (string, error) {
	label := struct {
		IDs     []string `json:"ids"`
		Flagged []string `json:"flagged"`
	}{IDs: []string{}, Flagged: []string{}}
	for _, ap := range s.Profiles {
		label.IDs = append(label.IDs, ap.AdaptedID)
		if ap.Flagged {
			label.Flagged = append(label.Flagged, ap.AdaptedID)
		}
	}
	sort.Strings(label.IDs)
	sort.Strings(label.Flagged)
	out, err := json.Marshal(label)
	if err != nil {
		return "", fmt.Errorf("encode profiles index label: %w", err)
	}
	return string(out), nil
}

// ImageLabel sanitizes a manifest name to lowercase kebab-case matching
// ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$: runs of other characters become one '-'
// and leading or trailing separators are dropped. It returns "" when name
// has no letter or digit.
func ImageLabel(name string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
			continue
		}
		pendingDash = true
	}
	return b.String()
}

// AdaptedID returns id, or <id>-<label> when suffix is set and label is not
// empty (FR-022a).
func AdaptedID(id, label string, suffix bool) string {
	if !suffix || label == "" {
		return id
	}
	return id + "-" + label
}

// uniqueSorted returns the sorted distinct values of in, never nil.
func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
