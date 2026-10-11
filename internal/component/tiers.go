package component

import (
	"fmt"
	"io/fs"
	"sort"
)

// Sources are the component directories of the three tiers. A nil FS is
// skipped. The project tier has two directories: hand-written components and
// promoted recipes; a recipe overrides a component with the same key.
type Sources struct {
	Shipped        fs.FS
	ShippedPrefix  string
	Registry       fs.FS
	RegistryPrefix string
	Project        fs.FS
	ProjectPrefix  string
	Recipes        fs.FS
	RecipesPrefix  string
}

// Override records that a later definition replaced an earlier one.
type Override struct {
	Key        string `json:"key" yaml:"key"`
	Winner     string `json:"winner" yaml:"winner"`
	WinnerTier Tier   `json:"winner_tier" yaml:"winner_tier"`
	Loser      string `json:"loser" yaml:"loser"`
	LoserTier  Tier   `json:"loser_tier" yaml:"loser_tier"`
}

// Set is the resolved component set: one winning definition per key.
type Set struct {
	byKey     map[string]*Component
	overrides []Override
}

// NewSet builds a set from components in precedence order (later wins).
func NewSet(layers ...[]*Component) *Set {
	s := &Set{byKey: map[string]*Component{}}
	for _, layer := range layers {
		for _, c := range layer {
			if prev, ok := s.byKey[c.Key]; ok {
				s.overrides = append(s.overrides, Override{
					Key: c.Key, Winner: c.SourceFile, WinnerTier: c.Tier,
					Loser: prev.SourceFile, LoserTier: prev.Tier,
				})
			}
			s.byKey[c.Key] = c
		}
	}
	sort.SliceStable(s.overrides, func(i, j int) bool { return s.overrides[i].Key < s.overrides[j].Key })
	return s
}

// Resolve loads the three tiers and merges them by key (FR-016).
func Resolve(src Sources) (*Set, error) {
	shipped, err := LoadDir(src.Shipped, TierShipped, src.ShippedPrefix)
	if err != nil {
		return nil, fmt.Errorf("shipped tier: %w", err)
	}
	registry, err := LoadDir(src.Registry, TierRegistry, src.RegistryPrefix)
	if err != nil {
		return nil, fmt.Errorf("registry tier: %w", err)
	}
	project, err := LoadDir(src.Project, TierProject, src.ProjectPrefix)
	if err != nil {
		return nil, fmt.Errorf("project tier: %w", err)
	}
	recipes, err := LoadDir(src.Recipes, TierProject, src.RecipesPrefix)
	if err != nil {
		return nil, fmt.Errorf("project recipes: %w", err)
	}
	return NewSet(shipped, registry, project, recipes), nil
}

// Get returns the winning definition for key.
func (s *Set) Get(key string) (*Component, bool) {
	c, ok := s.byKey[key]
	return c, ok
}

// Keys returns every key in sorted order.
func (s *Set) Keys() []string {
	keys := make([]string, 0, len(s.byKey))
	for k := range s.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// All returns every winning definition sorted by key.
func (s *Set) All() []*Component {
	out := make([]*Component, 0, len(s.byKey))
	for _, k := range s.Keys() {
		out = append(out, s.byKey[k])
	}
	return out
}

// Filter returns the definitions of a tier sorted by key.
func (s *Set) Filter(tier Tier) []*Component {
	var out []*Component
	for _, c := range s.All() {
		if c.Tier == tier {
			out = append(out, c)
		}
	}
	return out
}

// Overrides returns the shadowed definitions, sorted by key.
func (s *Set) Overrides() []Override { return append([]Override(nil), s.overrides...) }

// Len returns the number of components.
func (s *Set) Len() int { return len(s.byKey) }
