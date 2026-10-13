package plan

import (
	"strings"
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
			e, reason := DeltaFor(tt.comp, "1.25", tt.check, DeltaOptions{PackageManager: tt.pm})
			assert.Equal(t, tt.wantAction, e.Action)
			assert.Equal(t, tt.comp.Key, e.ComponentKey)
			if tt.wantReason == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tt.wantReason)
			}
		})
	}
	e, _ := DeltaFor(miseComp("go"), "latest", missing, DeltaOptions{PackageManager: "apt-get"})
	assert.Empty(t, e.RequiredVersion, "latest is not recorded as a requirement")
}

// TestDeltaForTimeout: a check that did not finish is treated like a
// missing component, so the build installs it.
func TestDeltaForTimeout(t *testing.T) {
	timedOut := probe.CheckResult{Status: probe.StatusTimeout, Output: "timeout: the check did not finish within 2m0s", Exit: 124}
	for _, comp := range []*component.Component{miseComp("go"), pkgComp("zsh", "apt-get")} {
		t.Run(comp.Key, func(t *testing.T) {
			e, reason := DeltaFor(comp, "1.25", timedOut, DeltaOptions{PackageManager: "apt-get"})
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

// TestDeltaForLockAndWorkspace covers every row of the decision table in
// data-model "Delta entry" plus the rules without a lock entry (FR-001 to
// FR-003, acceptance scenarios 1 to 5 of User Story 2).
func TestDeltaForLockAndWorkspace(t *testing.T) {
	ws := "/sandbox"
	check := func(status probe.Status, version string, paths ...string) probe.CheckResult {
		c := probe.CheckResult{Status: status, Version: version, Paths: paths}
		if len(paths) > 0 {
			c.Path = paths[0]
		}
		return c
	}
	tests := []struct {
		name       string
		comp       *component.Component
		check      probe.CheckResult
		locked     string
		workspace  string
		wantAction string
		wantStatus string
		wantIgnore []string
		wantKinds  []string
	}{
		{name: "missing installs", comp: miseComp("gh"), check: check(probe.StatusMissing, ""), locked: "2.102.0", workspace: ws, wantAction: ActionInstall, wantStatus: "missing"},
		{name: "only under workspace installs", comp: miseComp("python"), check: check(probe.StatusPresent, "3.14.3", "/sandbox/.uv/python/bin/python3"), locked: "3.14.5", workspace: ws,
			wantAction: ActionInstall, wantStatus: "missing", wantIgnore: []string{"/sandbox/.uv/python/bin/python3"}, wantKinds: []string{NoticeWorkspacePath}},
		{name: "incompatible only under workspace installs", comp: miseComp("python"), check: check(probe.StatusIncompatible, "3.10.0", "/sandbox/.venv/bin/python3"), locked: "3.14.5", workspace: ws,
			wantAction: ActionInstall, wantStatus: "missing", wantIgnore: []string{"/sandbox/.venv/bin/python3"}, wantKinds: []string{NoticeWorkspacePath}},
		{name: "first hit under workspace shadows with unknown version", comp: miseComp("python"), check: check(probe.StatusPresent, "3.14.5", "/sandbox/.venv/bin/python3", "/usr/bin/python3"), locked: "3.14.5", workspace: ws,
			wantAction: ActionShadow, wantStatus: "present", wantIgnore: []string{"/sandbox/.venv/bin/python3"}, wantKinds: []string{NoticeWorkspacePath, NoticeUnknownVersion}},
		{name: "outside and equal skips", comp: miseComp("gh"), check: check(probe.StatusPresent, "2.102.0", "/usr/bin/gh"), locked: "2.102.0", workspace: ws, wantAction: ActionSkip, wantStatus: "present"},
		{name: "equal with v prefix skips", comp: miseComp("gh"), check: check(probe.StatusPresent, "v2.102.0", "/usr/bin/gh"), locked: "2.102.0", workspace: ws, wantAction: ActionSkip, wantStatus: "present"},
		{name: "outside and different shadows", comp: miseComp("gh"), check: check(probe.StatusPresent, "2.93.0", "/usr/bin/gh"), locked: "2.102.0", workspace: ws,
			wantAction: ActionShadow, wantStatus: "present", wantKinds: []string{NoticeLockfileShadow}},
		{name: "incompatible and different shadows", comp: miseComp("node"), check: check(probe.StatusIncompatible, "20.1.0", "/usr/bin/node"), locked: "24.3.0", workspace: ws,
			wantAction: ActionShadow, wantStatus: "incompatible", wantKinds: []string{NoticeLockfileShadow}},
		{name: "unknown present version shadows", comp: miseComp("codex"), check: check(probe.StatusPresent, "", "/usr/local/bin/codex"), locked: "0.162.1", workspace: ws,
			wantAction: ActionShadow, wantStatus: "present", wantKinds: []string{NoticeUnknownVersion}},
		{name: "workspace copy later on PATH is no notice", comp: miseComp("gh"), check: check(probe.StatusPresent, "2.102.0", "/usr/bin/gh", "/sandbox/bin/gh"), locked: "2.102.0", workspace: ws,
			wantAction: ActionSkip, wantStatus: "present"},
		{name: "no lock entry present skips", comp: miseComp("gh"), check: check(probe.StatusPresent, "2.93.0", "/usr/bin/gh"), workspace: ws, wantAction: ActionSkip, wantStatus: "present"},
		{name: "no lock entry unknown version skips", comp: miseComp("gh"), check: check(probe.StatusPresent, "", "/usr/bin/gh"), workspace: ws, wantAction: ActionSkip, wantStatus: "present"},
		{name: "no lock entry incompatible shadows", comp: miseComp("gh"), check: check(probe.StatusIncompatible, "2.0.0", "/usr/bin/gh"), workspace: ws, wantAction: ActionShadow, wantStatus: "incompatible"},
		{name: "no lock entry mixed paths counts as present", comp: miseComp("python"), check: check(probe.StatusIncompatible, "3.10.0", "/sandbox/.venv/bin/python3", "/usr/bin/python3"), workspace: ws,
			wantAction: ActionSkip, wantStatus: "incompatible", wantIgnore: []string{"/sandbox/.venv/bin/python3"}, wantKinds: []string{NoticeWorkspacePath}},
		{name: "package only under workspace installs", comp: pkgComp("git", "apt-get"), check: check(probe.StatusPresent, "2.43.0", "/sandbox/bin/git"), workspace: ws,
			wantAction: ActionInstall, wantStatus: "missing", wantIgnore: []string{"/sandbox/bin/git"}, wantKinds: []string{NoticeWorkspacePath}},
		{name: "package mixed paths counts as present", comp: pkgComp("git", "apt-get"), check: check(probe.StatusPresent, "2.43.0", "/sandbox/bin/git", "/usr/bin/git"), workspace: ws,
			wantAction: ActionSkip, wantStatus: "present", wantIgnore: []string{"/sandbox/bin/git"}, wantKinds: []string{NoticeWorkspacePath}},
		{name: "container target keeps a home binary", comp: miseComp("python"), check: check(probe.StatusPresent, "3.14.5", "/sandbox/.uv/python/bin/python3"), locked: "3.14.5",
			wantAction: ActionSkip, wantStatus: "present"},
		{name: "workspace prefix is not a parent", comp: miseComp("gh"), check: check(probe.StatusPresent, "2.102.0", "/sandboxes/bin/gh"), locked: "2.102.0", workspace: ws, wantAction: ActionSkip, wantStatus: "present"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, reason := DeltaFor(tt.comp, "latest", tt.check, DeltaOptions{PackageManager: "apt-get", LockedVersion: tt.locked, Platform: "linux/arm64", WorkspacePath: tt.workspace})
			assert.Empty(t, reason)
			assert.Equal(t, tt.wantAction, e.Action)
			assert.Equal(t, tt.wantStatus, e.Status)
			assert.Equal(t, tt.wantIgnore, e.IgnoredPaths)
			assert.Equal(t, tt.locked, e.LockedVersion)
			var kinds []string
			for _, n := range e.Notices {
				kind, _, _ := strings.Cut(n, ": ")
				kinds = append(kinds, kind)
				assert.True(t, strings.HasPrefix(n, kind+": "+tt.comp.Key+": "), n)
			}
			assert.Equal(t, tt.wantKinds, kinds)
		})
	}

	e, _ := DeltaFor(miseComp("gh"), "latest", check(probe.StatusPresent, "2.93.0", "/usr/bin/gh"), DeltaOptions{LockedVersion: "2.102.0", Platform: "linux/arm64", WorkspacePath: ws})
	assert.Equal(t, []string{"lockfile-shadow: gh: base image has 2.93.0, mise.lock pins 2.102.0 for linux/arm64; installing 2.102.0"}, e.Notices)
	e, _ = DeltaFor(miseComp("python"), "latest", check(probe.StatusPresent, "3.14.3", "/sandbox/.uv/python/bin/python3.14"), DeltaOptions{LockedVersion: "3.14.5", WorkspacePath: ws})
	assert.Equal(t, []string{"workspace-path: python: found only under /sandbox (workspace path): /sandbox/.uv/python/bin/python3.14; installing it"}, e.Notices)
}

func TestWorkspacePath(t *testing.T) {
	community := probe.Facts{User: probe.User{Name: "sandbox", UID: 998, GID: 998, Home: "/sandbox"}}
	assert.Equal(t, "/sandbox", WorkspacePath(community, manifest.TargetOpenShell))
	assert.Empty(t, WorkspacePath(community, manifest.TargetContainer), "no workspace volume for the container target")
	other := probe.Facts{User: probe.User{Name: "dev", UID: 1001, GID: 1001, Home: "/workspace"}}
	assert.Equal(t, "/workspace", WorkspacePath(other, manifest.TargetOpenShell), "taken from the facts, not hard-coded")
	root := probe.Facts{User: probe.User{Name: "root", UID: 0, Home: "/root"}}
	assert.Equal(t, CreatedUserHome, WorkspacePath(root, manifest.TargetOpenShell), "the created user's home")
	slash := probe.Facts{User: probe.User{Name: "app", UID: 1234, GID: 1234, Home: "/"}}
	assert.Empty(t, WorkspacePath(slash, manifest.TargetOpenShell), "a home of / never hides the whole image")
}
