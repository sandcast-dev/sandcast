package profile

import (
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/version"
)

// Tier is a profile source. Later tiers override earlier ones by id (FR-021).
type Tier string

// Profile tiers in precedence order, lowest first.
const (
	TierUpstream Tier = "upstream"
	TierOverlay  Tier = "overlay"
	TierProject  Tier = "project"
)

// Prefix is the display directory recorded in Entry.SourceFile for the tier:
// the repository path for the embedded tiers and the project-relative path
// for the project tier.
func (t Tier) Prefix() string {
	switch t {
	case TierUpstream:
		return path.Join("profiles", "upstream", version.OpenShellPin)
	case TierOverlay:
		return path.Join("profiles", "overlay")
	case TierProject:
		return path.Join(fsutil.DirName, fsutil.ProfilesDir)
	default:
		return string(t)
	}
}

// Entry is a loaded profile together with where it came from.
type Entry struct {
	Profile    *Profile
	Tier       Tier
	SourceFile string
}

// Set maps a profile id to the winning entry.
type Set map[string]Entry

// IDs returns the profile ids of the set, sorted.
func (s Set) IDs() []string {
	ids := make([]string, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Entries returns the entries of the set sorted by id.
func (s Set) Entries() []Entry {
	ids := s.IDs()
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, s[id])
	}
	return out
}

// Override reports that a profile of a later tier shadows one with the same
// id from an earlier tier.
type Override struct {
	ID     string
	Loser  string
	Winner string
}

// Resolve loads the three tiers in precedence order and merges them by id,
// later tier winning. A nil FS skips its tier. Two files with the same id in
// one tier are an error. Overrides are reported in load order.
func Resolve(upstream, overlay, project fs.FS) (Set, []Override, error) {
	set := Set{}
	var overrides []Override
	tiers := []struct {
		fsys fs.FS
		tier Tier
	}{
		{upstream, TierUpstream},
		{overlay, TierOverlay},
		{project, TierProject},
	}
	for _, t := range tiers {
		if t.fsys == nil {
			continue
		}
		entries, err := LoadDir(t.fsys, t.tier)
		if err != nil {
			return nil, nil, err
		}
		seen := map[string]string{}
		for _, e := range entries {
			id := e.Profile.ID
			if first, dup := seen[id]; dup {
				return nil, nil, fmt.Errorf("duplicate provider profile id %q in %s tier: %s and %s",
					id, t.tier, first, e.SourceFile)
			}
			seen[id] = e.SourceFile
			if prev, ok := set[id]; ok {
				overrides = append(overrides, Override{ID: id, Loser: prev.SourceFile, Winner: e.SourceFile})
			}
			set[id] = e
		}
	}
	return set, overrides, nil
}
