package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/mise"
	"github.com/rhuss/sandcast/internal/probe"
)

func miseComp(key string) *component.Component {
	return &component.Component{Key: key, Installer: component.Installer{Mise: &component.MiseInstaller{Tool: key}}}
}

func pkgComp(key string, pms ...string) *component.Component {
	pk := map[string][]string{}
	for _, pm := range pms {
		pk[pm] = []string{key}
	}
	return &component.Component{Key: key, Installer: component.Installer{Package: &component.PackageInstaller{Packages: pk}}}
}

func scriptComp(key string) *component.Component {
	return &component.Component{Key: key, Installer: component.Installer{Script: &component.ScriptInstaller{Run: "true"}}}
}

func TestDeltaFor(t *testing.T) {
	present := probe.CheckResult{Status: probe.StatusPresent, Version: "1.25.1"}
	missing := probe.CheckResult{Status: probe.StatusMissing}
	incompatible := probe.CheckResult{Status: probe.StatusIncompatible, Version: "1.20.0"}
	tests := []struct {
		name       string
		comp       *component.Component
		check      probe.CheckResult
		pm         string
		wantAction string
		wantReason string
	}{
		{"present skips", miseComp("go"), present, "apt-get", ActionSkip, ""},
		{"missing mise installs", miseComp("go"), missing, "none", ActionInstall, ""},
		{"incompatible mise shadows", miseComp("go"), incompatible, "apt-get", ActionShadow, ""},
		{"incompatible package unresolvable", pkgComp("git", "apt-get"), incompatible, "apt-get", ActionUnresolvable, "cannot install a second version"},
		{"incompatible script unresolvable", scriptComp("tool"), incompatible, "apt-get", ActionUnresolvable, "script installer"},
		{"missing package installs", pkgComp("zsh", "apt-get"), missing, "apt-get", ActionInstall, ""},
		{"package without manager", pkgComp("zsh", "apt-get"), missing, "none", ActionUnresolvable, "no supported package manager"},
		{"package without entry for manager", pkgComp("zsh", "apt-get"), missing, "apk", ActionUnresolvable, "no apk packages"},
		{"missing script installs", scriptComp("tool"), missing, "none", ActionInstall, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, reason := DeltaFor(tt.comp, "1.25", tt.check, tt.pm)
			assert.Equal(t, tt.wantAction, e.Action)
			assert.Equal(t, tt.comp.Key, e.ComponentKey)
			if tt.wantReason == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tt.wantReason)
			}
		})
	}
	e, _ := DeltaFor(miseComp("go"), "latest", missing, "apt-get")
	assert.Empty(t, e.RequiredVersion, "latest is not recorded as a requirement")
}

// TestDeltaForTimeout: a check that did not finish is treated like a
// missing component, so the build installs it.
func TestDeltaForTimeout(t *testing.T) {
	timedOut := probe.CheckResult{Status: probe.StatusTimeout, Output: "timeout: the check did not finish within 2m0s", Exit: 124}
	for _, comp := range []*component.Component{miseComp("go"), pkgComp("zsh", "apt-get")} {
		t.Run(comp.Key, func(t *testing.T) {
			e, reason := DeltaFor(comp, "1.25", timedOut, "apt-get")
			assert.Empty(t, reason)
			assert.Equal(t, string(probe.StatusMissing), e.Status)
			assert.Equal(t, ActionInstall, e.Action)
			assert.Empty(t, e.InstalledVersion)
		})
	}
}

func TestFinalUserFor(t *testing.T) {
	root := FinalUserFor(probe.Facts{User: probe.User{Name: "root", UID: 0}}, "openshell")
	assert.Equal(t, FinalUser{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/home/sandbox", Created: true}, root)

	kept := FinalUserFor(probe.Facts{User: probe.User{Name: "sandbox", UID: 1001, GID: 1001, Group: "sandbox", Home: "/sandbox"}}, "openshell")
	assert.Equal(t, FinalUser{Name: "sandbox", UID: 1001, GID: 1001, Group: "sandbox", Home: "/sandbox"}, kept)

	noGroup := FinalUserFor(probe.Facts{User: probe.User{Name: "dev", UID: 1000, GID: 1000}}, "openshell")
	assert.Equal(t, "dev", noGroup.Group)
	assert.Equal(t, "/home/dev", noGroup.Home)

	container := FinalUserFor(probe.Facts{User: probe.User{Name: "root", UID: 0, GID: 0, Group: "root", Home: "/root"}}, "container")
	assert.Equal(t, FinalUser{Name: "root", UID: 0, GID: 0, Group: "root", Home: "/root"}, container, "the container target keeps the base user")

	numeric := FinalUserFor(probe.Facts{User: probe.User{Name: "", UID: 1001, GID: 0}}, "container")
	assert.Equal(t, FinalUser{Name: "1001", UID: 1001, GID: 0, Group: "0", Home: "/"}, numeric, "a numeric user without a name stays numeric")
}

func TestMiseToolsSortedWithVersionRule(t *testing.T) {
	jq := miseComp("jq")
	jq.Installer.Mise.Version = "1.7"
	got := MiseTools([]Wanted{
		{Key: "zig", Def: miseComp("zig"), Ref: manifest.ComponentRef{Key: "zig", Version: "0.13"}},
		{Key: "apt-only", Def: pkgComp("apt-only", "apt")},
		{Key: "jq", Def: jq},
		{Key: "missing"},
	})
	assert.Equal(t, []mise.Tool{
		{Key: "jq", Tool: "jq", Version: "1.7"},
		{Key: "zig", Tool: "zig", Version: "0.13"},
	}, got)
}

func TestMiseToolUsesCheckVersionWhenInstallerHasNone(t *testing.T) {
	c := miseComp("go")
	c.Check.Expect.Version = "1.25"
	got := MiseTools([]Wanted{{Key: "go", Def: c}})
	assert.Equal(t, []mise.Tool{{Key: "go", Tool: "go", Version: "1.25"}}, got, "the installed version is the one the checks expect")
}
