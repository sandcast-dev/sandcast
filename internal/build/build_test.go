package build

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/profile"
)

const resolveOutput = `{"component":"git","bin":"git","path":"/usr/bin/git","method":"command-v"}
{"component":"go","bin":"go","path":"/opt/mise/installs/go/1.25.1/bin/go","method":"mise-which"}
{"component":"mytool","bin":"mytool","path":"/usr/local/bin/mytool","method":"command-v"}
`

func testProfiles(t *testing.T) profile.Set {
	t.Helper()
	set, _, err := profile.Resolve(sandcast.UpstreamProfiles, sandcast.OverlayProfiles, nil)
	require.NoError(t, err)
	return set
}

func testOptions(t *testing.T, eng engine.Engine) Options {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.yaml"), []byte("version: 1\n"), 0o644))
	return Options{
		Dir: dir, Plan: testPlan(t), Engine: eng, Image: "localhost/demo", ManifestName: "demo",
		Profiles: testProfiles(t),
	}
}

func fakeEngine() *engine.Fake {
	return &engine.Fake{
		RunFunc: func(spec engine.RunSpec) ([]byte, []byte, error) {
			return []byte(resolveOutput), nil, nil
		},
	}
}

func TestRunSuccess(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	res, err := Run(context.Background(), o)
	require.NoError(t, err)
	assert.Equal(t, "localhost/demo:latest", res.Image)

	builds := []engine.Call{}
	for _, c := range eng.Calls() {
		if c.Op == "build" {
			builds = append(builds, c)
		}
	}
	require.Len(t, builds, 2)
	require.Len(t, builds[0].Build.Tags, 1)
	stage := builds[0].Build.Tags[0]
	assert.True(t, strings.HasPrefix(stage, "localhost/demo:sandcast-stage-"), "per-invocation stage tag, got %s", stage)
	assert.Equal(t, "linux/arm64", builds[0].Build.Platform)
	assert.Equal(t, []string{"localhost/demo:latest"}, builds[1].Build.Tags)
	labels := builds[1].Build.Labels
	assert.Equal(t, "demo", labels[LabelManifestName])
	assert.Equal(t, ProfilesPath, labels[LabelProfilesPath])
	assert.Contains(t, labels[LabelProfilesIndex], `"github"`)

	final, err := os.ReadFile(filepath.Join(o.Dir, FinalContainerfileName))
	require.NoError(t, err)
	assert.Contains(t, string(final), "FROM "+stage+"\nCOPY profiles/ /usr/share/sandcast/profiles/\n", "pass 2 builds on this invocation's stage")
	for _, c := range eng.Calls() {
		if c.Op == "run" {
			assert.Equal(t, stage, c.Run.Image, "binaries are resolved in this invocation's stage")
		}
	}

	idx, err := profile.ReadIndex(filepath.Join(o.Dir, "profiles", "index.yaml"))
	require.NoError(t, err)
	byID := map[string]profile.IndexProfile{}
	for _, ip := range idx.Profiles {
		byID[ip.UpstreamID] = ip
	}
	assert.Equal(t, []string{"/usr/bin/git"}, byID["github"].Binaries)
	assert.Equal(t, []string{"/opt/mise/installs/go/*/bin/*", "/opt/mise/installs/go/1.25.1/bin/go"}, byID["go-proxy"].Binaries)
	assert.False(t, byID["github"].Flagged)
	assert.Equal(t, "demo", idx.ImageLabel)

	assert.Equal(t, 1, eng.CountOp("rmi"), "stage tag removed")
	assert.NotContains(t, eng.ImageNames(), stage)
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:sandcast-stage")
	for _, f := range []string{ContainerfileName, BuildLogName, "shell/sandcast.zsh"} {
		_, err := os.Stat(filepath.Join(o.Dir, f))
		assert.NoError(t, err, f)
	}
}

func TestRunPass1FailureNoRetry(t *testing.T) {
	eng := fakeEngine()
	eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = io.WriteString(spec.Log, "STEP 5/9: RUN false\nError: exit status 1\n")
		return errors.New("exit status 1")
	}
	o := testOptions(t, eng)
	res, err := Run(context.Background(), o)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBuildFailed))
	assert.Equal(t, 1, eng.CountOp("build"), "never retried")
	assert.Equal(t, 0, eng.CountOp("tag"), "no stage image to keep after a pass-1 failure")
	logData, rerr := os.ReadFile(res.BuildLog)
	require.NoError(t, rerr)
	assert.Contains(t, string(logData), "RUN false")
	_, serr := os.Stat(res.Containerfile)
	assert.NoError(t, serr, "Containerfile left for inspection")
}

func TestRunPass2FailureKeepsDebugImage(t *testing.T) {
	eng := fakeEngine()
	n := 0
	eng.BuildFunc = func(spec engine.BuildSpec) error {
		n++
		if n == 2 {
			return errors.New("pass 2 broke")
		}
		return nil
	}
	o := testOptions(t, eng)
	res, err := Run(context.Background(), o)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBuildFailed))
	assert.Equal(t, "localhost/demo:sandcast-debug", res.StageImage)
	assert.Contains(t, eng.ImageNames(), "localhost/demo:sandcast-debug")
	assert.Equal(t, 2, eng.CountOp("build"))

	// The next successful build removes the stale debug image.
	eng.BuildFunc = nil
	_, err = Run(context.Background(), testOptions(t, eng))
	require.NoError(t, err)
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:sandcast-debug")
}

func TestRunDebugTagFailureReportsStage(t *testing.T) {
	eng := &tagFailEngine{Fake: fakeEngine()}
	n := 0
	eng.BuildFunc = func(engine.BuildSpec) error {
		n++
		if n == 2 {
			return errors.New("pass 2 broke")
		}
		return nil
	}
	res, err := Run(context.Background(), testOptions(t, eng))
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(res.StageImage, "localhost/demo:sandcast-stage-"), "the stage image is reported, not a debug tag that does not exist: %s", res.StageImage)
	assert.Contains(t, eng.ImageNames(), res.StageImage)
}

type tagFailEngine struct{ *engine.Fake }

func (e *tagFailEngine) Tag(context.Context, string, string) error { return errors.New("tag failed") }

func TestRunFlagsUnresolvedProfile(t *testing.T) {
	eng := &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) {
		return []byte(`{"component":"go","bin":"go","path":"/opt/mise/installs/go/1.25.1/bin/go","method":"mise-which"}` + "\n"), nil, nil
	}}
	o := testOptions(t, eng)
	res, err := Run(context.Background(), o)
	require.NoError(t, err)
	require.Len(t, res.Flagged, 1)
	assert.Equal(t, "github", res.Flagged[0].UpstreamID)
}

func TestRunKeepStageAndPush(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	o.KeepStage = true
	o.Push = true
	o.Tag = "v1"
	res, err := Run(context.Background(), o)
	require.NoError(t, err)
	assert.Equal(t, "localhost/demo:sandcast-stage", res.StageImage, "the kept stage gets the documented name")
	assert.Equal(t, []string{"localhost/demo:sandcast-stage", "localhost/demo:v1"}, eng.ImageNames(), "only the per-invocation tag is removed")
	assert.Equal(t, 1, eng.CountOp("push"))
	assert.Equal(t, "localhost/demo:v1", res.Image)
	assert.True(t, res.Pushed)
}

// TestRunPushResult covers FR-011 and the spec edge case "A push fails":
// the push and its output land in build.log, the result records whether the
// image was pushed and the registry digest.
func TestRunPushResult(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	t.Run("push on", func(t *testing.T) {
		eng := fakeEngine()
		eng.PushDigests = map[string]string{"localhost/demo:latest": digest}
		o := testOptions(t, eng)
		o.Push = true
		res, err := Run(context.Background(), o)
		require.NoError(t, err)
		assert.True(t, res.Pushed)
		assert.Equal(t, digest, res.Digest)
		log, err := os.ReadFile(res.BuildLog)
		require.NoError(t, err)
		assert.Contains(t, string(log), "### sandcast push: localhost/demo:latest\nPushing localhost/demo:latest\n")
	})
	t.Run("push off", func(t *testing.T) {
		eng := fakeEngine()
		res, err := Run(context.Background(), testOptions(t, eng))
		require.NoError(t, err)
		assert.False(t, res.Pushed)
		assert.Empty(t, res.Digest)
		assert.Zero(t, eng.CountOp("push"))
		log, err := os.ReadFile(res.BuildLog)
		require.NoError(t, err)
		assert.NotContains(t, string(log), "sandcast push")
	})
	t.Run("push fails", func(t *testing.T) {
		eng := fakeEngine()
		eng.PushErr = map[string]error{"localhost/demo:latest": errors.New("unauthorized: authentication required")}
		o := testOptions(t, eng)
		o.Push = true
		res, err := Run(context.Background(), o)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "push localhost/demo:latest: unauthorized")
		require.NotNil(t, res)
		assert.False(t, res.Pushed)
		log, err := os.ReadFile(res.BuildLog)
		require.NoError(t, err)
		assert.Contains(t, string(log), "### sandcast push: localhost/demo:latest")
	})
}

func TestRunStageTagIsPerInvocation(t *testing.T) {
	eng := fakeEngine()
	stages := map[string]bool{}
	for range 2 {
		o := testOptions(t, eng)
		_, err := Run(context.Background(), o)
		require.NoError(t, err)
		final, err := os.ReadFile(filepath.Join(o.Dir, FinalContainerfileName))
		require.NoError(t, err)
		from, _, _ := strings.Cut(strings.SplitN(string(final), "FROM ", 2)[1], "\n")
		stages[from] = true
	}
	assert.Len(t, stages, 2, "two builds of the same image never share a stage tag")
}

// holdLock locks dir/.lock through its own open file, as another sandcast
// process would.
func holdLock(t *testing.T, dir string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, LockFileName), os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	return func() { _ = f.Close() }
}

func TestLockTargetRefusesConcurrentBuild(t *testing.T) {
	dir := t.TempDir()
	release := holdLock(t, dir)
	_, err := LockTarget(dir, "openshell")
	require.ErrorIs(t, err, ErrBuildRunning)
	assert.ErrorContains(t, err, "for target openshell in "+dir+"; wait for it to finish")

	release()
	unlock, err := LockTarget(dir, "openshell")
	require.NoError(t, err, "the lock is free once the other build ends")
	_, err = LockTarget(dir, "openshell")
	require.ErrorIs(t, err, ErrBuildRunning, "a second lock fails at once, in the same process too")
	unlock()
	unlock, err = LockTarget(dir, "openshell")
	require.NoError(t, err, "the returned function releases the lock")
	unlock()
}

// Run leaves the lock to its caller (pipeline.Build holds it across the plan
// outputs, both passes and verify); locking again would refuse that caller.
func TestRunLeavesTheLockToTheCaller(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	unlock, err := LockTarget(o.Dir, o.Plan.Target)
	require.NoError(t, err)
	defer unlock()
	_, err = Run(context.Background(), o)
	require.NoError(t, err)
}

// A committed symlink at build.log must not make the build truncate the
// link target and stream the engine log into it.
func TestRunRefusesSymlinkedBuildLog(t *testing.T) {
	eng := fakeEngine()
	eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = io.WriteString(spec.Log, "attacker-controlled output\n")
		return nil
	}
	o := testOptions(t, eng)
	victim := filepath.Join(t.TempDir(), "bashrc")
	require.NoError(t, os.WriteFile(victim, []byte("keep\n"), 0o644))
	require.NoError(t, os.Symlink(victim, filepath.Join(o.Dir, BuildLogName)))

	_, err := Run(context.Background(), o)
	require.Error(t, err)
	assert.ErrorContains(t, err, BuildLogName+" is a symlink")
	data, rerr := os.ReadFile(victim)
	require.NoError(t, rerr)
	assert.Equal(t, "keep\n", string(data), "the link target is neither truncated nor written")
	assert.Zero(t, eng.CountOp("build"), "nothing is built")

	// A dangling link must not create its target either.
	missing := filepath.Join(t.TempDir(), "created")
	require.NoError(t, os.Remove(filepath.Join(o.Dir, BuildLogName)))
	require.NoError(t, os.Symlink(missing, filepath.Join(o.Dir, BuildLogName)))
	_, err = Run(context.Background(), o)
	require.ErrorContains(t, err, BuildLogName+" is a symlink")
	_, serr := os.Lstat(missing)
	assert.ErrorIs(t, serr, os.ErrNotExist)
}

// A dangling symlink at .lock must not make LockDir create a file at the
// link target.
func TestLockDirRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "created")
	require.NoError(t, os.Symlink(missing, filepath.Join(dir, LockFileName)))
	_, err := LockDir(dir)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrBuildRunning)
	assert.ErrorContains(t, err, LockFileName+" is a symlink")
	_, serr := os.Lstat(missing)
	assert.ErrorIs(t, serr, os.ErrNotExist, "the link target is not created")

	existing := filepath.Join(t.TempDir(), "existing")
	require.NoError(t, os.WriteFile(existing, []byte("keep\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(dir, LockFileName)))
	require.NoError(t, os.Symlink(existing, filepath.Join(dir, LockFileName)))
	_, err = LockTarget(dir, "openshell")
	require.ErrorContains(t, err, LockFileName+" is a symlink")
}

func TestRunFlagsProfileWithWritableBinary(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	// /opt/go is created owned by the final user (testPlan), so anything
	// the sandbox drops there would get the go-proxy egress.
	o.Plan.Components[1].RuntimeGlobs = append(o.Plan.Components[1].RuntimeGlobs, "/opt/go/pkg/mod/golang.org/toolchain@*/bin/*")
	res, err := Run(context.Background(), o)
	require.NoError(t, err)
	var flagged []string
	for _, ip := range res.Flagged {
		flagged = append(flagged, ip.UpstreamID)
		if ip.UpstreamID == "go-proxy" {
			assert.Contains(t, ip.FlagReason, "/opt/go/pkg/mod/golang.org/toolchain@*/bin/*")
			assert.Contains(t, ip.FlagReason, "the sandbox can write")
		}
	}
	assert.Equal(t, []string{"go-proxy"}, flagged)

	idx, err := profile.ReadIndex(filepath.Join(o.Dir, "profiles", "index.yaml"))
	require.NoError(t, err)
	for _, ip := range idx.Profiles {
		assert.Equal(t, ip.UpstreamID == "go-proxy", ip.Flagged, ip.UpstreamID)
	}
	data, err := os.ReadFile(filepath.Join(o.Dir, "profiles", "go-proxy.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "# Flagged:      binaries under paths the sandbox can write")
	var final engine.Call
	for _, c := range eng.Calls() {
		if c.Op == "build" {
			final = c
		}
	}
	assert.Contains(t, final.Build.Labels[LabelProfilesIndex], `"flagged":["go-proxy"]`)
}

func TestRunRefusesSecretsInOutputs(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	o.Plan.Shell.RC = "export ANTHROPIC_API_KEY=sk-ant-api03-" + strings.Repeat("x", 90) + "\n"
	_, err := Run(context.Background(), o)
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Contains(t, se.Error(), "shell/sandcast.zsh")
	assert.NotContains(t, se.Error(), strings.Repeat("x", 40), "the secret itself is never printed")
	assert.Zero(t, eng.CountOp("build"), "the context is scanned before any image is built")
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:latest")
}

func TestRunUntagsImageWhenOutputsLeakASecret(t *testing.T) {
	token := "ghp_" + strings.Repeat("B", 36)
	eng := fakeEngine()
	eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = io.WriteString(spec.Log, "pulling with "+token+"\n")
		return nil
	}
	o := testOptions(t, eng)
	_, err := Run(context.Background(), o)
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Contains(t, se.Error(), "build.log")
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:latest", "the final tag is removed")
	assert.ErrorContains(t, err, "localhost/demo:latest was removed because it failed the credential scan")
}

func TestRunHarnessFilesAndMCPProfiles(t *testing.T) {
	eng := &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) {
		return []byte(resolveOutput + `{"component":"claude-code","bin":"claude","path":"/opt/mise/installs/claude/2.1.0/claude","method":"mise-which"}` + "\n"), nil, nil
	}}
	o := testOptions(t, eng)
	skill := filepath.Join(t.TempDir(), "my-skill")
	require.NoError(t, os.MkdirAll(skill, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: my-skill\n---\n"), 0o644))
	o.Plan.Components = append(o.Plan.Components, plan.Component{Key: "claude-code", InstallerKind: "mise", Action: plan.ActionSkip, Bins: []string{"claude"}})
	o.Plan.Harnesses = []plan.Harness{{
		Name: "claude-code", Component: "claude-code", FragmentName: plan.SectionHarness + "/claude-code",
		Fragment: "# 70-harness: claude-code\nUSER root\n",
		Files:    []plan.ContextFile{{Path: "harness/claude-code/skills/my-skill", Source: skill}},
		MCPServers: []manifest.MCPServer{
			{Name: "docs", URL: "https://docs.example.com/mcp", Env: map[string]string{"DOCS_TOKEN": "${SANDCAST_SECRET_1}"}},
			{Name: "gh", URL: "https://api.github.com/mcp"},
			{Name: "local", Command: []string{"local-mcp"}},
		},
	}}
	_, err := Run(context.Background(), o)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(o.Dir, "harness", "claude-code", "skills", "my-skill", "SKILL.md"))
	require.NoError(t, err, "harness files are staged into the build context")

	idx, err := profile.ReadIndex(filepath.Join(o.Dir, "profiles", "index.yaml"))
	require.NoError(t, err)
	byID := map[string]profile.IndexProfile{}
	for _, ip := range idx.Profiles {
		byID[ip.UpstreamID] = ip
	}
	docs, ok := byID["mcp-docs"]
	require.True(t, ok, "an uncovered MCP endpoint gets a generated profile")
	assert.Equal(t, []string{"/opt/mise/installs/claude/2.1.0/claude"}, docs.Binaries)
	_, generated := byID["mcp-gh"]
	assert.True(t, generated, "the github profile is bound to gh and git, not to the agent, so the agent gets its own profile")
	data, err := os.ReadFile(filepath.Join(o.Dir, "profiles", docs.File))
	require.NoError(t, err)
	assert.Contains(t, string(data), "DOCS_TOKEN")
	assert.NotContains(t, string(data), "SANDCAST_SECRET")
}

func TestStageFileKeepsModesAndConfinesSymlinks(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "hooks", "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("x"), 0o600))
	require.NoError(t, os.Symlink("SKILL.md", filepath.Join(src, "alias.md")))
	dir := t.TempDir()
	require.NoError(t, stageFile(dir, plan.ContextFile{Path: "harness/s", Source: src}))
	fi, err := os.Stat(filepath.Join(dir, "harness", "s", "hooks", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), "executable bit survives")
	fi, err = os.Stat(filepath.Join(dir, "harness", "s", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	data, err := os.ReadFile(filepath.Join(dir, "harness", "s", "alias.md"))
	require.NoError(t, err)
	assert.Equal(t, "x", string(data), "an internal symlink is followed")

	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("s"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(src, "escape")))
	target := t.TempDir()
	err = stageFile(target, plan.ContextFile{Path: "harness/s", Source: src})
	require.ErrorContains(t, err, "escape", "a symlink leaving the source fails instead of being copied")
	_, err = os.Stat(filepath.Join(target, "harness", "s", "escape"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRunRefusesSecretInStagedHarnessFile(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	skill := filepath.Join(t.TempDir(), "my-skill")
	require.NoError(t, os.MkdirAll(skill, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("use ghp_"+strings.Repeat("Z", 36)+"\n"), 0o644))
	o.Plan.Harnesses = []plan.Harness{{
		Name: "claude-code", Component: "claude-code", FragmentName: plan.SectionHarness + "/claude-code",
		Fragment: "# 70-harness: claude-code\nUSER root\n",
		Files:    []plan.ContextFile{{Path: "harness/claude-code/skills/my-skill", Source: skill}},
	}}
	_, err := Run(context.Background(), o)
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Contains(t, se.Error(), "harness/claude-code/skills/my-skill/SKILL.md")
	assert.NotContains(t, se.Error(), strings.Repeat("Z", 36))
	assert.Zero(t, eng.CountOp("build"), "a host file with a credential never reaches an image")
}

func TestStageFileSkipsVCSAndRefusesEscapes(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, ".git", "config"), []byte("[remote]\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("x"), 0o644))
	dir := t.TempDir()
	require.NoError(t, stageFile(dir, plan.ContextFile{Path: "harness/s", Source: src}))
	_, err := os.Stat(filepath.Join(dir, "harness", "s", "SKILL.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "harness", "s", ".git"))
	assert.ErrorIs(t, err, os.ErrNotExist, "version control metadata is not staged")

	err = stageFile(dir, plan.ContextFile{Path: "../outside", Source: src})
	assert.ErrorContains(t, err, "leaves the build directory")
	_, err = os.Stat(filepath.Join(filepath.Dir(dir), "outside"))
	assert.ErrorIs(t, err, os.ErrNotExist)

	for _, p := range []string{"..", "../", "a/../../x"} {
		err = stageFile(dir, plan.ContextFile{Path: p, Source: src})
		assert.ErrorContains(t, err, "leaves the build directory", p)
	}
	// A name that merely starts with two dots stays inside.
	require.NoError(t, stageFile(dir, plan.ContextFile{Path: "..config/s", Source: src}))
	_, err = os.Stat(filepath.Join(dir, "..config", "s", "SKILL.md"))
	require.NoError(t, err)

	linked := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(linked, "sub"), 0o755))
	require.NoError(t, os.Symlink("sub", filepath.Join(linked, "linkdir")))
	err = stageFile(t.TempDir(), plan.ContextFile{Path: "harness/l", Source: linked})
	assert.ErrorContains(t, err, "symlink to a directory")
}

func TestRunRemovesKeptStageWhenOutputsLeakASecret(t *testing.T) {
	token := "ghp_" + strings.Repeat("D", 36)
	eng := fakeEngine()
	eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = io.WriteString(spec.Log, "RUN echo "+token+"\n")
		return nil
	}
	o := testOptions(t, eng)
	o.KeepStage = true
	res, err := Run(context.Background(), o)
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Empty(t, res.StageImage)
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:sandcast-stage", "the kept stage holds the same content")
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:latest")
}

func TestRunScansProfilesBeforePass2(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	o.AfterResolve = func(*profile.AdaptedSet) error {
		return os.WriteFile(filepath.Join(o.Dir, "allowed-domains.txt"), []byte("ghp_"+strings.Repeat("E", 36)+"\n"), 0o644)
	}
	_, err := Run(context.Background(), o)
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Equal(t, 1, eng.CountOp("build"), "pass 2 never runs")
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:latest")
}

func TestRunReportsImageItCouldNotRemove(t *testing.T) {
	token := "ghp_" + strings.Repeat("H", 36)
	eng := &rmiFailEngine{Fake: fakeEngine()}
	eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = io.WriteString(spec.Log, "RUN echo "+token+"\n")
		return nil
	}
	_, err := Run(context.Background(), testOptions(t, eng))
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.ErrorContains(t, err, "remove it before pushing")
}

type rmiFailEngine struct{ *engine.Fake }

func (e *rmiFailEngine) RemoveImage(ctx context.Context, ref string) error {
	if strings.HasSuffix(ref, ":latest") {
		return errors.New("image is in use")
	}
	return e.Fake.RemoveImage(ctx, ref)
}

func TestRunReportsKeptStageItCouldNotRemove(t *testing.T) {
	token := "ghp_" + strings.Repeat("K", 36)
	eng := &stageRmiFailEngine{Fake: fakeEngine()}
	eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = io.WriteString(spec.Log, "RUN echo "+token+"\n")
		return nil
	}
	o := testOptions(t, eng)
	o.KeepStage = true
	res, err := Run(context.Background(), o)
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.ErrorContains(t, err, "could not remove the kept stage")
	assert.Equal(t, "localhost/demo:sandcast-stage", res.StageImage, "the stage that is still there is reported")
}

type stageRmiFailEngine struct{ *engine.Fake }

func (e *stageRmiFailEngine) RemoveImage(ctx context.Context, ref string) error {
	if strings.HasSuffix(ref, ":sandcast-stage") {
		return errors.New("image is in use")
	}
	return e.Fake.RemoveImage(ctx, ref)
}

func TestRunScansAdaptedProfilesBeforePass2(t *testing.T) {
	eng := fakeEngine()
	o := testOptions(t, eng)
	gh := o.Profiles["github"]
	require.NotNil(t, gh.Profile)
	edited := *gh.Profile
	edited.Description = "use ghp_" + strings.Repeat("L", 36)
	o.Profiles["github"] = profile.Entry{Profile: &edited, Tier: gh.Tier, SourceFile: gh.SourceFile}
	_, err := Run(context.Background(), o)
	var se *SecretError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Contains(t, se.Error(), "profiles/")
	assert.Equal(t, 1, eng.CountOp("build"), "pass 2 never copies the profiles")
	assert.NotContains(t, eng.ImageNames(), "localhost/demo:latest")
}
