package pipeline

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/rhuss/sandcast/internal/harness"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
)

// HarnessComponents maps each manifest harness that has a registered
// adapter to the component key the adapter installs the agent with. It is
// the one source plan.Select uses for implicit harness components. Harness
// names the manifest schema accepts but no adapter in this release handles
// are left out; ValidateReferences reports them and harnessSections refuses
// them.
func HarnessComponents(m *manifest.Manifest) map[string]string {
	out := map[string]string{}
	for _, h := range m.Harnesses {
		if ad, err := harness.Get(h.Name); err == nil {
			out[h.Name] = ad.ComponentKey()
		}
	}
	return out
}

// harnessSections renders the 70-harness section of every manifest harness
// through its adapter (FR-038). A harness without a registered adapter is a
// usage error. The manifest schema accepts every planned harness name, also
// those without an adapter in this release, so this check is not redundant.
func harnessSections(env Env, m *manifest.Manifest, user plan.FinalUser) ([]plan.Harness, error) {
	var out []plan.Harness
	for _, h := range m.Harnesses {
		ad, err := harness.Get(h.Name)
		if err != nil {
			return nil, usage("harness %s: %v", h.Name, err)
		}
		sc, err := ad.Scaffold(harness.ScaffoldInput{Harness: h, User: user, Project: env.Layout.Root, Home: env.Home})
		if errors.Is(err, harness.ErrInvalidManifest) {
			return nil, &UsageError{Err: fmt.Errorf("scaffold harness %s: %w", h.Name, err)}
		}
		if err != nil {
			return nil, fmt.Errorf("scaffold harness %s: %w", h.Name, err)
		}
		files := make([]plan.ContextFile, 0, len(sc.Files))
		for path, src := range sc.Files {
			files = append(files, plan.ContextFile{Path: path, Source: src})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		servers := slices.Clone(h.MCPServers)
		sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
		out = append(out, plan.Harness{
			Name:         h.Name,
			Component:    ad.ComponentKey(),
			FragmentName: plan.SectionHarness + "/" + h.Name,
			Fragment:     sc.Fragment,
			Files:        files,
			Env:          sc.Env,
			Dirs:         sc.Dirs,
			ReadOnly:     sc.ReadOnly,
			ReadWrite:    sc.ReadWrite,
			MCPServers:   servers,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
