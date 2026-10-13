package build

import (
	"os/exec"
	"slices"
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
	assert.Equal(t, "/bin/zsh", loginShell(p))
	p.Shell = plan.Shell{Program: "none"}
	p.Components = nil
	assert.Equal(t, "/bin/bash", loginShell(p))
	p.ImageFacts.Shells = []string{"/bin/sh"}
	assert.Equal(t, "/bin/sh", loginShell(p))
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

// TestShellSection covers the 60-shell variants of contracts/image-shell.md.
func TestShellSection(t *testing.T) {
	envLine := "[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh"
	tests := []struct {
		name    string
		mutate  func(p *plan.Plan)
		present []string
		absent  []string
		golden  string
		order   []string
	}{
		{
			name:    "openshell zsh with rc and created user",
			present: []string{"COPY shell/sandcast.zsh", SessionEnvMarker, ShellSwitchMarker, ShellSwitchHook, "zsh-newuser-install() { :; }", envLine},
			absent:  []string{"usermod -s"},
		},
		{
			name:    "openshell zsh without rc and kept user",
			mutate:  func(p *plan.Plan) { p.Shell.RC, p.Shell.RCPath = "", ""; p.FinalUser.Created = false },
			present: []string{"# 60-shell: session environment\n", SessionEnvMarker, ShellSwitchHook, "zsh-newuser-install", "usermod -s /bin/zsh sandbox", "chsh -s /bin/zsh sandbox", `sed -i 's#^\(sandbox:.*:\)[^:]*$#\1/bin/zsh#' /etc/passwd`},
			absent:  []string{"COPY shell/sandcast.zsh"},
			golden:  "testdata/shell-zsh-kept-user.golden",
		},
		{
			name: "openshell without mise but with image env ships the env script",
			mutate: func(p *plan.Plan) {
				p.Shell = plan.Shell{Program: "none"}
				p.Components = slices.DeleteFunc(p.Components, func(c plan.Component) bool { return c.Key == "zsh" || c.Key == "go" })
			},
			present: []string{"COPY shell/sandcast-env.sh", SessionEnvMarker},
			absent:  []string{ShellSwitchMarker, "zshenv"},
		},
		{
			name: "openshell bash program renders no switch hook",
			mutate: func(p *plan.Plan) {
				p.Shell = plan.Shell{Program: "bash", RCPath: plan.BashRCPath, RC: "alias ll='ls -l'\n"}
				p.Components = slices.DeleteFunc(p.Components, func(c plan.Component) bool { return c.Key == "zsh" })
			},
			present: []string{"COPY shell/sandcast.sh /etc/profile.d/sandcast.sh", SessionEnvMarker, envLine},
			absent:  []string{ShellSwitchMarker, "zshenv", "usermod"},
			golden:  "testdata/shell-bash.golden",
			// The curated bash rc runs after the session environment in an
			// interactive bash, so its tools are on PATH.
			order: []string{SessionEnvMarker, "/etc/profile.d/sandcast.sh ] && . /etc/profile.d/sandcast.sh"},
		},
		{
			name: "zsh in the base image gets the zshenv block",
			mutate: func(p *plan.Plan) {
				p.Shell = plan.Shell{Program: "none"}
				p.ImageFacts.Shells = []string{"/bin/bash", "/usr/bin/zsh"}
			},
			present: []string{"zsh-newuser-install", SessionEnvMarker},
			absent:  []string{ShellSwitchMarker, "usermod"},
		},
		{
			name: "container target has no bash rc block",
			mutate: func(p *plan.Plan) {
				p.Target = "container"
				p.Policy = ""
				p.FinalUser = plan.FinalUser{Name: "root", Group: "root", Home: "/root"}
			},
			present: []string{"zsh-newuser-install", "COPY shell/sandcast-env.sh /etc/profile.d/sandcast-env.sh", "usermod -s /bin/zsh root"},
			absent:  []string{"/etc/bash.bashrc", ShellSwitchMarker},
		},
		{
			name: "nothing to do renders no section",
			mutate: func(p *plan.Plan) {
				p.Target = "container"
				p.Policy = ""
				p.Shell = plan.Shell{Program: "none"}
				p.Components = slices.DeleteFunc(p.Components, func(c plan.Component) bool { return c.Key == "zsh" || c.Key == "go" })
			},
			absent: []string{"# 60-shell"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := testPlan(t)
			if tt.mutate != nil {
				tt.mutate(p)
			}
			got, err := Assemble(p)
			require.NoError(t, err)
			for _, s := range tt.present {
				assert.Contains(t, got, s)
			}
			for _, s := range tt.absent {
				assert.NotContains(t, got, s)
			}
			last := -1
			for _, o := range tt.order {
				i := strings.Index(got, o)
				require.GreaterOrEqual(t, i, 0, o)
				assert.Greater(t, i, last, "order: %s", o)
				last = i
			}
			if tt.golden != "" {
				section, err := shellSection(p)
				require.NoError(t, err)
				testutil.Golden(t, tt.golden, []byte(section))
			}
		})
	}
}

// TestShellSwitchHookBehavior runs the hook in bash: without a terminal it
// must not switch shells (FR-008, acceptance scenario 4).
func TestShellSwitchHookBehavior(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	out, err := exec.Command(bash, "--norc", "-i", "-c", ShellSwitchHook+"\necho stayed-in-bash").CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "stayed-in-bash")
	out, err = exec.Command(bash, "-c", ShellSwitchHook+"\necho non-interactive").CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "non-interactive")
}

func TestCuratedRC(t *testing.T) {
	p := testPlan(t)
	assert.Equal(t, "_sandcast_path 2>/dev/null || true\nSANDCAST_RC_LOADED=1\nautoload -Uz compinit && compinit -C\n", CuratedRC(p))
}
