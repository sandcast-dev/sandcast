package plan

import (
	"fmt"
	"slices"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/probe"
)

// PackageManagerNone is the probe value for images without a supported
// package manager.
const PackageManagerNone = "none"

// DeltaFor derives the probe delta entry for one component from its probe
// check (data-model "ProbeDelta"). The returned reason is non-empty when the
// action is unresolvable.
func DeltaFor(c *component.Component, selector string, check probe.CheckResult, packageManager string) (DeltaEntry, string) {
	e := DeltaEntry{
		ComponentKey:     c.Key,
		Status:           string(check.Status),
		InstalledVersion: check.Version,
		RequiredVersion:  selector,
	}
	if selector == "latest" {
		e.RequiredVersion = ""
	}
	kind := c.Kind()
	switch check.Status {
	case probe.StatusPresent:
		e.Action = ActionSkip
		return e, ""
	case probe.StatusIncompatible:
		if kind == component.KindMise {
			e.Action = ActionShadow
			return e, ""
		}
		e.Action = ActionUnresolvable
		return e, fmt.Sprintf("the base image has %s %s, which does not satisfy %q; a %s installer cannot install a second version",
			c.Key, orUnknown(check.Version), selector, kind)
	default:
		e.Status = string(probe.StatusMissing)
		if kind == component.KindPackage {
			if reason := packageProblem(c, packageManager); reason != "" {
				e.Action = ActionUnresolvable
				return e, reason
			}
		}
		e.Action = ActionInstall
		return e, ""
	}
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
