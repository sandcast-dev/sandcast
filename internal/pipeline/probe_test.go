package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/probe"
)

// TestProbedDeltaReportsTheBaseImageAsIs checks that sandcast probe keeps
// its output: no lockfile comparison and no workspace rule, so a present
// component under the final user's home stays present and skipped.
func TestProbedDeltaReportsTheBaseImageAsIs(t *testing.T) {
	python := &component.Component{Key: "python", Installer: component.Installer{Mise: &component.MiseInstaller{Tool: "python"}}}
	p := &Probed{
		Target: manifest.TargetOpenShell,
		Wanted: []plan.Wanted{{Key: "python", Def: python}},
		Result: &probe.Result{
			Facts: probe.Facts{PackageManager: "apt-get", User: probe.User{Name: "sandbox", UID: 998, GID: 998, Home: "/sandbox"}},
			Checks: map[string]probe.CheckResult{"python": {
				ComponentKey: "python", Status: probe.StatusPresent, Version: "3.14.3",
				Path: "/sandbox/.venv/bin/python3", Paths: []string{"/sandbox/.venv/bin/python3"},
			}},
		},
	}
	entries, reasons := p.Delta()
	assert.Empty(t, reasons)
	assert.Equal(t, []plan.DeltaEntry{{ComponentKey: "python", Status: "present", InstalledVersion: "3.14.3", Action: plan.ActionSkip}}, entries)
}
