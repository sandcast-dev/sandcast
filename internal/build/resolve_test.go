package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
)

func TestResolveScriptQuotesAndModes(t *testing.T) {
	p := &plan.Plan{Components: []plan.Component{
		{Key: "go", InstallerKind: "mise", Bins: []string{"go", "gofmt"}},
		{Key: "git", InstallerKind: "package", Bins: []string{"git"}},
		{Key: "odd", InstallerKind: "script", Bins: []string{"it's"}},
	}}
	s := ResolveScript(p)
	assert.Contains(t, s, "_resolve 'go' 'go' 1\n")
	assert.Contains(t, s, "_resolve 'go' 'gofmt' 1\n")
	assert.Contains(t, s, "_resolve 'git' 'git' 0\n")
	assert.Contains(t, s, `_resolve 'odd' 'it'\''s' 0`)
}

// TestResolveScriptRuns executes the script with the host /bin/sh against a
// fake bin directory, so the shell code is exercised for real.
func TestResolveScriptRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "realtool")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\necho hi\n"), 0o755))
	require.NoError(t, os.Symlink(tool, filepath.Join(dir, "linked")))
	p := &plan.Plan{Components: []plan.Component{
		{Key: "a", InstallerKind: "script", Bins: []string{"linked"}},
		{Key: "b", InstallerKind: "mise", Bins: []string{"absent-tool"}},
	}}
	cmd := exec.Command("sh", "-c", ResolveScript(p))
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	out, err := cmd.Output()
	require.NoError(t, err)
	res, err := ParseResolutions(out)
	require.NoError(t, err)
	require.Len(t, res, 2)
	real, err := filepath.EvalSymlinks(tool)
	require.NoError(t, err)
	assert.Equal(t, Resolution{Component: "a", Bin: "linked", Path: real, Method: MethodCommandV}, res[0])
	assert.Equal(t, Resolution{Component: "b", Bin: "absent-tool", Path: "", Method: MethodMissing}, res[1])
}

// TestResolveScriptWithoutCoreutils runs the script with a PATH that holds
// only the tool, so sed, tr and readlink are absent; a path with a quote
// still comes back as valid JSON.
func TestResolveScriptWithoutCoreutils(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := filepath.Join(t.TempDir(), `odd"dir`)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o755))
	p := &plan.Plan{Components: []plan.Component{{Key: "a", InstallerKind: "script", Bins: []string{"tool"}}}}
	cmd := exec.Command(sh, "-c", ResolveScript(p))
	cmd.Env = []string{"PATH=" + dir}
	out, err := cmd.Output()
	require.NoError(t, err)
	res, err := ParseResolutions(out)
	require.NoError(t, err, "output %q", out)
	require.Len(t, res, 1)
	assert.Equal(t, filepath.Join(dir, "tool"), res[0].Path)
	assert.Equal(t, MethodCommandV, res[0].Method)
}

// deadlineEngine records whether runs carry a deadline.
type deadlineEngine struct {
	*engine.Fake
	deadline time.Duration
}

func (e *deadlineEngine) Run(ctx context.Context, spec engine.RunSpec) ([]byte, []byte, error) {
	if d, ok := ctx.Deadline(); ok {
		e.deadline = time.Until(d)
	}
	return e.Fake.Run(ctx, spec)
}

func TestResolveIsBounded(t *testing.T) {
	eng := &deadlineEngine{Fake: fakeEngine()}
	_, err := Resolve(context.Background(), eng, "img", "", &plan.Plan{})
	require.NoError(t, err)
	assert.Greater(t, eng.deadline, time.Duration(0), "the resolution container has a deadline")
	assert.LessOrEqual(t, eng.deadline, engine.HelperTimeout)
}

func TestParseResolutionsRejectsGarbage(t *testing.T) {
	_, err := ParseResolutions([]byte("{not json\n"))
	require.Error(t, err)
	res, err := ParseResolutions([]byte("noise\n" + `{"component":"x","bin":"x","path":"/x","method":"command-v"}` + "\n"))
	require.NoError(t, err)
	assert.Len(t, res, 1)
}

func TestProfileBinaries(t *testing.T) {
	p := &plan.Plan{
		Profiles: []plan.Profile{{ID: "github"}, {ID: "go-proxy"}, {ID: "pypi"}},
		Components: []plan.Component{
			{Key: "git", Profiles: []string{"github"}},
			{Key: "gh", Profiles: []string{"github"}},
			{Key: "go", Profiles: []string{"go-proxy"}, RuntimeGlobs: []string{"/opt/mise/installs/go/*/bin/*"}},
		},
	}
	res := []Resolution{
		{Component: "git", Bin: "git", Path: "/usr/bin/git"},
		{Component: "gh", Bin: "gh", Path: "/opt/mise/installs/gh/2.0/bin/gh"},
		{Component: "go", Bin: "go", Path: "/opt/mise/installs/go/1.25.1/bin/go"},
		{Component: "go", Bin: "gofmt", Path: ""},
	}
	got := ProfileBinaries(p, res)
	assert.Equal(t, []string{"/opt/mise/installs/gh/2.0/bin/gh", "/usr/bin/git"}, got["github"])
	assert.Equal(t, []string{"/opt/mise/installs/go/*/bin/*", "/opt/mise/installs/go/1.25.1/bin/go"}, got["go-proxy"])
	assert.Equal(t, []string{}, got["pypi"], "a referenced profile without binaries stays in the map, empty")
	assert.False(t, strings.Contains(strings.Join(got["go-proxy"], ","), ",,"))
}

func TestWritableBinaries(t *testing.T) {
	p := &plan.Plan{
		Image: plan.ImageSetup{Dirs: []plan.Dir{
			{Path: "/opt/go", Owner: "user"},
			{Path: "/opt/rustup", Owner: "user"},
			{Path: "/opt/shared", Owner: "root"},
		}},
		Policy: "version: 1\nfilesystem_policy:\n  read_only:\n    - /usr\n  read_write:\n    - /dev/null\n    - /opt/cargo\n    - /tmp\n",
	}
	got, err := WritableBinaries(p, map[string][]string{
		"go-proxy": {"/opt/go/pkg/mod/golang.org/toolchain@*/bin/*", "/opt/mise/installs/go/*/bin/*", "/opt/mise/installs/go/1.25.1/bin/go"},
		"crates":   {"/opt/cargo/bin/cargo", "/opt/rustup/toolchains/*/bin/*"},
		"github":   {"/usr/bin/git", "/opt/shared/bin/tool", "/opt/gopher/bin/x"},
		"wide":     {"/opt/*/bin/tool"},
		"deep":     {"/opt/**/tool"},
		"tmp":      {"/tmp"},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"go-proxy": {"/opt/go/pkg/mod/golang.org/toolchain@*/bin/*"},
		"crates":   {"/opt/cargo/bin/cargo", "/opt/rustup/toolchains/*/bin/*"},
		"wide":     {"/opt/*/bin/tool"},
		"deep":     {"/opt/**/tool"},
		"tmp":      {"/tmp"},
	}, got, "root-owned, read-only and look-alike paths (/opt/gopher) are not writable")

	_, err = WritableBinaries(&plan.Plan{Policy: "filesystem_policy: [unclosed"}, nil)
	assert.ErrorContains(t, err, "policy")
}

// TestWritableBinariesWorkdir: the workdir is writable at runtime when the
// policy includes it (and the OpenShell target creates /sandbox owned by the
// final user), so a base-image tool under the default user's home in
// /sandbox is flagged. Binaries under /opt/mise are not: the policy lists it
// read-only.
func TestWritableBinariesWorkdir(t *testing.T) {
	binaries := map[string][]string{
		"github":    {"/sandbox/.local/bin/gh"},
		"glob":      {"/sandbox/*/bin/*"},
		"go-proxy":  {"/opt/mise/installs/go/1.25.1/bin/go", "/opt/mise/installs/go/*/bin/*"},
		"lookalike": {"/sandboxes/bin/tool"},
	}
	policy := func(includeWorkdir bool) string {
		return fmt.Sprintf("version: 1\nfilesystem_policy:\n  include_workdir: %t\n  read_only: [/usr, /opt/mise]\n  read_write: [/tmp]\n", includeWorkdir)
	}
	want := map[string][]string{"github": {"/sandbox/.local/bin/gh"}, "glob": {"/sandbox/*/bin/*"}}

	got, err := WritableBinaries(&plan.Plan{Target: manifest.TargetOpenShell, Policy: policy(true)}, binaries)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	got, err = WritableBinaries(&plan.Plan{Target: manifest.TargetOpenShell, Policy: policy(false)}, binaries)
	require.NoError(t, err)
	assert.Equal(t, want, got, "/sandbox is owned by the final user even when the policy leaves it out")

	got, err = WritableBinaries(&plan.Plan{Target: manifest.TargetContainer, FinalUser: plan.FinalUser{Name: "root", Home: "/root"}}, binaries)
	require.NoError(t, err)
	assert.Empty(t, got, "the container target has no sandbox workdir")
}
