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
// The workspace rule (FR-003) runs first: when every binary path lies under
// the overmount home, the component counts as missing; when only the first
// PATH hit does, the probed version came from the hidden copy and counts as
// unknown. Then, for a mise component with a lock entry, a present version
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
	// The PATH hits of the probe, or the one resolved path of a result
	// written before the probe recorded every hit.
	paths := check.Paths
	if len(paths) == 0 && check.Path != "" {
		paths = []string{check.Path}
	}
	if found && o.OvermountHome != "" && len(paths) > 0 {
		var inside, outside []string
		for _, p := range paths {
			if UnderOvermount(o.OvermountHome, p) {
				inside = append(inside, p)
			} else {
				outside = append(outside, p)
			}
		}
		switch {
		case len(outside) == 0:
			// Every copy is hidden once the workspace is mounted: the tool
			// counts as missing. The plan reports the re-provisioning with
			// its target (plan.Build, RelocationNotice), so no notice here.
			e.IgnoredPaths = inside
			found = false
			e.InstalledVersion = ""
		case UnderOvermount(o.OvermountHome, paths[0]):
			e.IgnoredPaths = inside
			e.Notices = append(e.Notices, Notice(NoticeWorkspacePath, c.Key, fmt.Sprintf(
				"first PATH hit %s lies under the overmount home %s and is ignored; %s decides",
				paths[0], o.OvermountHome, outside[0])))
			unknownVersion = true
			mixed = true
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
