package build

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/testutil"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// testPlan returns a small OpenShell plan with every section populated.
func testPlan(t *testing.T) *plan.Plan {
	t.Helper()
	user := plan.FinalUser{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/home/sandbox", Created: true}
	pkgs, err := plan.PackageFragment("apt-get", []string{"zsh", "iproute2", "nftables"})
	require.NoError(t, err)
	miseText, err := plan.MiseFragment(user, "v2026.10.4", "linux-arm64")
	require.NoError(t, err)
	script, err := plan.ScriptFragment(&component.Component{
		Key: "mytool", SourceFile: "components/mytool.yaml",
		Installer: component.Installer{Script: &component.ScriptInstaller{Run: "echo mytool", AsRoot: true}},
	}, user)
	require.NoError(t, err)
	return &plan.Plan{
		SchemaVersion: 1, SandcastVersion: "dev", Target: "openshell",
		Platform:   plan.Selected{Value: "linux/arm64", Source: "host"},
		Engine:     plan.EngineInfo{Name: "podman", Source: "detected"},
		BaseImage:  plan.BaseImage{Input: "nvidia-ubuntu", Reference: "nvcr.io/nvidia/base/ubuntu:24.04", Digest: testDigest},
		ImageFacts: plan.ImageFacts{PackageManager: "apt-get", User: plan.User{Name: "root"}, Shells: []string{"/bin/bash", "/bin/sh"}},
		FinalUser:  user,
		Image: plan.ImageSetup{
			Env:  []plan.EnvVar{{Name: "GOPATH", Value: "/opt/go"}, {Name: "GREETING", Value: "hello world"}},
			Dirs: []plan.Dir{{Path: "/opt/go", Owner: "user"}, {Path: "/opt/shared", Owner: "root"}},
		},
		Components: []plan.Component{
			{Key: "git", InstallerKind: "package", Action: plan.ActionSkip, Bins: []string{"git"}, Profiles: []string{"github"}},
			{Key: "go", InstallerKind: "mise", Action: plan.ActionInstall, FragmentName: plan.SectionMise, Fragment: miseText, Bins: []string{"go"}, Profiles: []string{"go-proxy"}, RuntimeGlobs: []string{"/opt/mise/installs/go/*/bin/*"}},
			{Key: "mytool", InstallerKind: "script", Action: plan.ActionInstall, FragmentName: plan.SectionRecipes + "/mytool", Fragment: script, Bins: []string{"mytool"}},
			{Key: "other-tool", InstallerKind: plan.KindFragment, Action: plan.ActionInstall, FragmentName: plan.SectionFragments + "/other-tool",
				Fragment: plan.StoredFragmentText("other-tool", ".sandcast/fragments/other-tool.containerfile", "USER root\nRUN echo other\nUSER sandbox\n"), Bins: []string{"other-tool"}},
			{Key: "zsh", InstallerKind: "package", Action: plan.ActionInstall, FragmentName: plan.SectionPackages, Fragment: pkgs, Bins: []string{"zsh"}},
		},
		Profiles:   []plan.Profile{{ID: "github", Tier: "upstream"}, {ID: "go-proxy", Tier: "overlay"}},
		Policy:     "version: 1\n",
		Shell:      plan.Shell{Program: "zsh", RCPath: plan.ZshRCPath, RC: "autoload -Uz compinit && compinit -C\n"},
		Unresolved: []plan.Unresolved{},
		Conflicts:  []component.Conflict{},
	}
}

func TestAssembleGolden(t *testing.T) {
	got, err := Assemble(testPlan(t))
	require.NoError(t, err)
	testutil.Golden(t, "testdata/Containerfile.golden", []byte(got))

	order := []string{"# 00-header", "# 10-system-packages", "# 20-user", "# 30-mise", "# 40-recipes: mytool", "# 50-fragments: other-tool", "# 60-shell", "# 80-policy", "# 90-footer"}
	last := -1
	for _, marker := range order {
		i := strings.Index(got, marker)
		require.GreaterOrEqual(t, i, 0, marker)
		assert.Greater(t, i, last, "section order: %s", marker)
		last = i
	}
	assert.Contains(t, got, "FROM nvcr.io/nvidia/base/ubuntu:24.04@"+testDigest)
	assert.Contains(t, got, `ENV GREETING="hello world"`)
	assert.Contains(t, got, `CMD ["/bin/zsh", "-l"]`)
	assert.Contains(t, got, "USER root\nRUN mkdir -p /var/lib/apt/lists/partial")
	assert.Equal(t, 1, strings.Count(got, "# 30-mise"), "aggregated mise layer appears once")
}

func TestAssembleRefusesConflictsAndUnresolved(t *testing.T) {
	p := testPlan(t)
	p.Conflicts = []component.Conflict{{Binary: "rg", Components: []string{"a", "b"}}}
	_, err := Assemble(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), plan.ConflictProblem(p.Conflicts[0]), "the shared problem text")

	p = testPlan(t)
	p.Unresolved = []plan.Unresolved{{Key: "x", Reason: "r"}}
	_, err = Assemble(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), plan.UnresolvedProblem(p.Unresolved[0]))
	assert.Contains(t, err.Error(), `sandcast fragments write x`)

	p.Unresolved = []plan.Unresolved{{Key: "x", Reason: "r", Optional: true}}
	_, err = Assemble(p)
	require.NoError(t, err)
}

func TestEnvValue(t *testing.T) {
	assert.Equal(t, "/opt/go", envValue("/opt/go"))
	assert.Equal(t, `"a b"`, envValue("a b"))
	assert.Equal(t, `"say \"hi\" \$HOME"`, envValue(`say "hi" $HOME`))
	assert.Equal(t, `""`, envValue(""))
}

func TestLoginShell(t *testing.T) {
	p := testPlan(t)
	assert.Equal(t, loginShellSpec{Path: "/bin/zsh", CmdArgs: []string{"-l"}}, loginShell(p))
	assert.Equal(t, `"/bin/zsh", "-l"`, loginShell(p).cmd(), "zsh starts as a login shell")
	p.Shell = plan.Shell{Program: "none"}
	p.Components = nil
	// A bash login shell uses the baked launcher, never /bin/bash directly,
	// and the launcher is the sole CMD argument (no -l).
	assert.Equal(t, loginShellSpec{Path: plan.ShellLoginPath}, loginShell(p))
	assert.Equal(t, `"/opt/sandcast/shell-login"`, loginShell(p).cmd())
	p.ImageFacts.Shells = []string{"/bin/sh"}
	// The sh fallback uses the baked launcher too, so a login shell never
	// reads a startup file from the workspace home (spec 003 FR-002/SC-002).
	assert.Equal(t, loginShellSpec{Path: plan.ShLoginPath}, loginShell(p))
	assert.Equal(t, `"/opt/sandcast/sh-login"`, loginShell(p).cmd())
}

func TestAssembleContainerTargetGolden(t *testing.T) {
	p := testPlan(t)
	p.Target = "container"
	p.Policy = ""
	p.FinalUser = plan.FinalUser{Name: "root", UID: 0, GID: 0, Group: "root", Home: "/root"}
	got, err := Assemble(p)
	require.NoError(t, err)
	testutil.Golden(t, "testdata/Containerfile.container.golden", []byte(got))
	assert.NotContains(t, got, "# 80-policy")
	assert.NotContains(t, got, PolicyPath)
	assert.NotContains(t, got, "/sandbox", "no sandbox conventions for the container target (FR-040)")
	assert.NotContains(t, got, "created by sandcast")
	assert.Contains(t, got, "WORKDIR /root")
	assert.Contains(t, got, "ENTRYPOINT []\nCMD [\"/bin/zsh\", \"-l\"]")
}

func TestAssembleHarnessSectionOrder(t *testing.T) {
	p := testPlan(t)
	p.Harnesses = []plan.Harness{{Name: "claude-code", FragmentName: plan.SectionHarness + "/claude-code", Fragment: "# 70-harness: claude-code\nUSER root\nRUN true\n"}}
	got, err := Assemble(p)
	require.NoError(t, err)
	shell := strings.Index(got, "# 60-shell")
	harness := strings.Index(got, "# 70-harness")
	policy := strings.Index(got, "# 80-policy")
	assert.True(t, shell < harness && harness < policy, "70-harness sits between 60-shell and 80-policy")
}

func TestFromReferenceReplacesPinnedDigest(t *testing.T) {
	assert.Equal(t, "ghcr.io/x/y:1@"+testDigest, fromReference("ghcr.io/x/y:1", testDigest))
	assert.Equal(t, "ghcr.io/x/y:1@"+testDigest, fromReference("ghcr.io/x/y:1@sha256:"+strings.Repeat("a", 64), testDigest))
	assert.Equal(t, "localhost:5000/y@"+testDigest, fromReference("localhost:5000/y", testDigest))
}
