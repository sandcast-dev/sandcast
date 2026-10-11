package pipeline

import (
	"context"
	"fmt"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/probe"
)

// ProbeOptions select what ProbeBase probes.
type ProbeOptions struct {
	Target       string
	PlatformFlag string
	// Reference probes this image instead of the manifest's base image.
	Reference string
	NoCache   bool
}

// Probed is the outcome of ProbeBase, the first half of Plan. The probe
// command prints it; Plan continues from it.
type Probed struct {
	// Target is empty when there is no manifest.
	Target   string
	Platform plan.Selected
	Base     plan.BaseImage
	Set      *component.Set
	Wanted   []plan.Wanted
	// Notices are the base image, tier, selection and override notices.
	Notices []string
	Result  *probe.Result
}

// ProbeBase selects the target, the platform, the base image and the wanted
// components, then probes the base image for those components. It logs the
// probe notices and whether a container was started. env.Manifest may be nil
// when opts.Reference is set; the probe then only gathers facts.
func ProbeBase(ctx context.Context, env Env, opts ProbeOptions) (*Probed, error) {
	m := env.Manifest
	if m == nil && opts.Reference == "" {
		return nil, usage("no manifest: run sandcast init or pass an image reference")
	}
	manifestPlatform := ""
	if m != nil {
		manifestPlatform = m.Platform
	}
	platform, err := SelectPlatform(opts.PlatformFlag, manifestPlatform, env.HostPlatform)
	if err != nil {
		return nil, err
	}
	out := &Probed{Platform: platform, Base: plan.BaseImage{Reference: opts.Reference, Notes: []string{}}}
	if m != nil {
		if err := out.selectComponents(env, opts); err != nil {
			return nil, err
		}
	}

	var reqs []probe.Request
	for _, w := range out.Wanted {
		if w.Def != nil {
			reqs = append(reqs, probe.RequestFor(w.Def, w.Ref.Version))
		}
	}
	env.logf("probing %s for %s", out.Base.Reference, platform.Value)
	result, err := probe.Probe(ctx, env.Engine, out.Base.Reference, platform.Value, reqs,
		probe.Options{NoCache: opts.NoCache, Cache: &probe.Cache{Dir: env.CacheDir}})
	if err != nil {
		name := out.Base.Input
		if out.Base.RegistryEntry != nil {
			name = *out.Base.RegistryEntry
		}
		if name == "" || name == out.Base.Reference {
			return nil, fmt.Errorf("probe base image %s: %w", out.Base.Reference, err)
		}
		return nil, fmt.Errorf("probe base image %s (%s): %w", name, out.Base.Reference, err)
	}
	for _, n := range result.Notices {
		env.logf("notice: %s", n)
	}
	if !result.ContainerStarted {
		env.logf("probe cache hit: no container started")
	} else if len(result.Probed) > 0 {
		env.logf("probed %d component(s): %v", len(result.Probed), result.Probed)
	}
	out.Base.Digest = result.Facts.Digest
	out.Result = result
	return out, nil
}

// selectComponents resolves the target, the base image (unless a reference
// was given) and the wanted components of the manifest.
func (p *Probed) selectComponents(env Env, opts ProbeOptions) error {
	m := env.Manifest
	target, err := TargetOf(m, opts.Target)
	if err != nil {
		return err
	}
	p.Target = target
	if opts.Reference == "" {
		reg, err := Registry()
		if err != nil {
			return err
		}
		entry, inRegistry, notice, err := ResolveBase(reg, m.BaseImage, target)
		if err != nil {
			return err
		}
		p.Base = plan.BaseImage{Input: m.BaseImage, Reference: entry.Ref, Notes: []string{}}
		if inRegistry {
			name := entry.Name
			p.Base.RegistryEntry = &name
			if entry.Notes != "" {
				p.Base.Notes = []string{entry.Notes}
			}
		}
		if notice != "" {
			p.Notices = append(p.Notices, notice)
		}
	}

	set, compNotices, err := Components(env.Layout, env.CacheDir)
	if err != nil {
		return err
	}
	p.Set = set
	p.Notices = append(p.Notices, compNotices...)
	wanted, selNotices := plan.Select(m, set, target, HarnessComponents(m))
	p.Wanted = wanted
	p.Notices = append(p.Notices, selNotices...)
	wantedKeys := map[string]bool{}
	for _, w := range wanted {
		wantedKeys[w.Key] = true
	}
	for _, ov := range set.Overrides() {
		if wantedKeys[ov.Key] {
			p.Notices = append(p.Notices, fmt.Sprintf("component %s: %s (%s tier) overrides %s (%s tier)", ov.Key, ov.Winner, ov.WinnerTier, ov.Loser, ov.LoserTier))
		}
	}
	return nil
}

// Delta compares the probe result with every wanted component that has a
// definition. Reasons explain entries, one "key: reason" line each.
func (p *Probed) Delta() (entries []plan.DeltaEntry, reasons []string) {
	entries = []plan.DeltaEntry{}
	for _, w := range p.Wanted {
		if w.Def == nil {
			continue
		}
		e, reason := plan.DeltaFor(w.Def, w.Selector(), p.Result.Checks[w.Key], p.Result.Facts.PackageManager)
		entries = append(entries, e)
		if reason != "" {
			reasons = append(reasons, w.Key+": "+reason)
		}
	}
	return entries, reasons
}
