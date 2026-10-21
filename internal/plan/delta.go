package plan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/probe"
)

// PackageManagerNone is the probe value for images without a supported
// package manager.
const PackageManagerNone = "none"

// DeltaOptions are the plan inputs DeltaFor uses besides the component and
// its probe check.
type DeltaOptions struct {
	// PackageManager is the probed package manager.
	PackageManager string
	// LockedVersion is the version mise.lock locks for the component on the
	// target platform. Empty means no lock entry (or not a mise component),
	// and today's rules apply.
	LockedVersion string
	// Platform is the target platform (linux/arm64), named in notices.
	Platform string
	// OvermountHome is the base image's overmount home for the OpenShell
	// target (plan.OvermountPrefix: the registry entry's overmount_home,
	// recorded as base_image.overmount_home, /sandbox by default), the path
	// the workspace volume is mounted over. Empty (the container target)
	// turns the workspace rule off.
	OvermountHome string
}

// DeltaFor derives the probe delta entry for one component from its probe
// check (data-model "Delta entry"). The returned reason is non-empty when
// the action is unresolvable. Decisions that depart from a plain present or
// missing result carry their notices in DeltaEntry.Notices (FR-002).
//
// The workspace rule (FR-003) runs first, for every binary of the component
// and with each PATH hit judged by its path and by the file it resolves to
// (T060): when every hit of one binary lies under the overmount home, the
// component counts as missing; when only the first PATH hit of a binary
// does, the probed version may have come from the hidden copy and counts as
// unknown. A binary without any hit changes nothing. Then, for a mise
// component with a lock entry, a present version
// equal to the locked one means skip, and a different or unknown one means
// shadow with the locked version (FR-001).
func DeltaFor(c *component.Component, selector string, check probe.CheckResult, o DeltaOptions) (DeltaEntry, string) {
	e := DeltaEntry{
		ComponentKey:     c.Key,
		Status:           string(check.Status),
		InstalledVersion: check.Version,
		RequiredVersion:  selector,
		LockedVersion:    o.LockedVersion,
	}
	if selector == "latest" {
		e.RequiredVersion = ""
	}
	kind := c.Kind()
	found := check.Status == probe.StatusPresent || check.Status == probe.StatusIncompatible
	unknownVersion := check.Version == ""
	mixed := false
	if found && o.OvermountHome != "" {
		// inside collects the hits under the overmount home; hiddenBins the
		// hits of the binaries that have no copy outside it.
		var inside, hiddenBins []string
		for i, b := range binHitsOf(check) {
			var in, out []string
			for _, h := range b.hits {
				if h.under(o.OvermountHome) {
					in = append(in, h.String(o.OvermountHome))
				} else {
					out = append(out, h.raw)
				}
			}
			inside = append(inside, in...)
			switch {
			case len(b.hits) == 0:
			case len(out) == 0:
				// Every copy of this binary is hidden once the workspace is
				// mounted: the tool counts as missing.
				hiddenBins = append(hiddenBins, in...)
			case b.hits[0].under(o.OvermountHome):
				first := "first PATH hit"
				if i > 0 {
					first = "first PATH hit of " + b.name
				}
				e.Notices = append(e.Notices, Notice(NoticeWorkspacePath, c.Key, fmt.Sprintf(
					"%s %s lies under the overmount home %s and is ignored; %s decides",
					first, b.hits[0].String(o.OvermountHome), o.OvermountHome, out[0])))
				mixed = true
			}
		}
		switch {
		case len(hiddenBins) > 0:
			// The plan reports the re-provisioning with its target
			// (plan.Build, RelocationNotice), so no notice here.
			e.IgnoredPaths = hiddenBins
			e.Notices = nil
			found = false
			mixed = false
			e.InstalledVersion = ""
		case mixed:
			e.IgnoredPaths = inside
			unknownVersion = true
			e.InstalledVersion = ""
		}
	}
	if !found {
		e.Status = string(probe.StatusMissing)
		if kind == component.KindPackage {
			if reason := packageProblem(c, o.PackageManager); reason != "" {
				e.Action = ActionUnresolvable
				return e, reason
			}
		}
		e.Action = ActionInstall
		return e, ""
	}
	if kind == component.KindMise && o.LockedVersion != "" {
		switch {
		case unknownVersion:
			e.Action = ActionShadow
			why := "base image version could not be read"
			if mixed {
				why = "the probed version came from the ignored copy under the overmount home"
			}
			e.Notices = append(e.Notices, Notice(NoticeUnknownVersion, c.Key, fmt.Sprintf(
				"%s; installing locked %s", why, o.LockedVersion)))
		case sameVersion(check.Version, o.LockedVersion):
			e.Action = ActionSkip
		default:
			e.Action = ActionShadow
			e.Notices = append(e.Notices, Notice(NoticeLockfileShadow, c.Key, fmt.Sprintf(
				"base image has %s, mise.lock pins %s for %s; installing %s",
				check.Version, o.LockedVersion, orUnknown(o.Platform), o.LockedVersion)))
		}
		return e, ""
	}
	// Without a lock entry, a copy outside the overmount home counts as
	// present whatever version the hidden copy reported.
	if check.Status == probe.StatusPresent || mixed {
		e.Action = ActionSkip
		return e, ""
	}
	if kind == component.KindMise {
		e.Action = ActionShadow
		return e, ""
	}
	e.Action = ActionUnresolvable
	return e, fmt.Sprintf("the base image has %s %s, which does not satisfy %q; a %s installer cannot install a second version",
		c.Key, orUnknown(check.Version), selector, kind)
}

// binHit is one PATH hit of a binary with its resolved target ("" when the
// probe could not resolve it).
type binHit struct{ raw, real string }

// under reports whether the hit lies under the overmount home, as the path
// PATH names or as the file it resolves to.
func (h binHit) under(overmount string) bool {
	return UnderOvermount(overmount, h.raw) || (h.real != "" && UnderOvermount(overmount, h.real))
}

// String names the hit, with its target when only the target lies under
// the overmount home.
func (h binHit) String(overmount string) string {
	if h.real != "" && h.real != h.raw && !UnderOvermount(overmount, h.raw) && UnderOvermount(overmount, h.real) {
		return h.raw + " -> " + h.real
	}
	return h.raw
}

type binHits struct {
	name string
	hits []binHit
}

// binHitsOf returns the PATH hits of every binary of the check, the first
// one primary. A result without per-binary hits falls back to the PATH hits
// of the first binary, or its one resolved path.
func binHitsOf(check probe.CheckResult) []binHits {
	var out []binHits
	for _, b := range check.Bins {
		bh := binHits{name: b.Name}
		for i, p := range b.Paths {
			h := binHit{raw: p}
			if i < len(b.Real) {
				h.real = b.Real[i]
			}
			bh.hits = append(bh.hits, h)
		}
		out = append(out, bh)
	}
	if len(out) > 0 {
		return out
	}
	paths := check.Paths
	if len(paths) == 0 && check.Path != "" {
		paths = []string{check.Path}
	}
	bh := binHits{}
	for _, p := range paths {
		bh.hits = append(bh.hits, binHit{raw: p})
	}
	return []binHits{bh}
}

// sameVersion compares a probed and a locked version, ignoring a leading v.
func sameVersion(present, locked string) bool {
	return strings.TrimPrefix(present, "v") == strings.TrimPrefix(locked, "v")
}

// packageProblem explains why a package component cannot be installed with
// the probed package manager, or returns "".
func packageProblem(c *component.Component, packageManager string) string {
	if packageManager == "" || packageManager == PackageManagerNone {
		return "the base image has no supported package manager (apt-get, dnf, yum, apk)"
	}
	if len(c.Installer.Package.Packages[packageManager]) == 0 {
		var known []string
		for pm := range c.Installer.Package.Packages {
			known = append(known, pm)
		}
		slices.Sort(known)
		return fmt.Sprintf("no %s packages defined for %s (defined for: %v)", packageManager, c.Key, known)
	}
	return ""
}

func orUnknown(v string) string {
	if v == "" {
		return "(unknown version)"
	}
	return v
}
