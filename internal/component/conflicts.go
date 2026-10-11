package component

import "sort"

// Conflict is one binary provided by more than one component.
type Conflict struct {
	Binary     string   `json:"binary" yaml:"binary"`
	Components []string `json:"components" yaml:"components"`
}

// Conflicts reports every binary that two or more of comps provide, sorted
// by binary name with sorted component keys (edge case "two components
// provide the same binary").
func Conflicts(comps []*Component) []Conflict {
	providers := map[string]map[string]bool{}
	for _, c := range comps {
		for _, bin := range c.ProvidedBinaries() {
			if providers[bin] == nil {
				providers[bin] = map[string]bool{}
			}
			providers[bin][c.Key] = true
		}
	}
	var out []Conflict
	for bin, keys := range providers {
		if len(keys) < 2 {
			continue
		}
		list := make([]string, 0, len(keys))
		for k := range keys {
			list = append(list, k)
		}
		sort.Strings(list)
		out = append(out, Conflict{Binary: bin, Components: list})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Binary < out[j].Binary })
	return out
}
