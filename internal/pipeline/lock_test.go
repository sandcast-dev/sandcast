package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/verify"
)

// lockEnv returns an environment and a buildable plan for the openshell
// target of a fresh project, with a fake engine.
func lockEnv(t *testing.T) (Env, *Planned, *engine.Fake) {
	t.Helper()
	root := t.TempDir()
	// The plan has no components, so binary resolution finds nothing.
	eng := &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) { return nil, nil, nil }}
	env := Env{
		Layout:   fsutil.Layout{Root: root},
		Manifest: manifest.Skeleton("demo", "x", manifest.TargetOpenShell),
		Engine:   eng,
	}
	p := &plan.Plan{
		SchemaVersion: 1, SandcastVersion: "dev", Target: manifest.TargetOpenShell,
		Platform:   plan.Selected{Value: "linux/arm64", Source: "host"},
		Engine:     plan.EngineInfo{Name: "podman", Source: "detected"},
		BaseImage:  plan.BaseImage{Input: "x", Reference: "registry.example.com/base:1", Digest: importDigest},
		ImageFacts: plan.ImageFacts{PackageManager: "apt-get", User: plan.User{Name: "root"}},
		FinalUser:  plan.FinalUser{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/home/sandbox", Created: true},
		Policy:     "version: 1\n",
	}
	return env, &Planned{Plan: p, Dir: env.Layout.BuildDir(manifest.TargetOpenShell)}, eng
}

// holdBuildLock locks dir/.lock through its own open file, as another
// sandcast process would. It does not use build.LockDir, so the tests below
// still hold if that function changes.
func holdBuildLock(t *testing.T, dir string) func() {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	f, err := os.OpenFile(filepath.Join(dir, build.LockFileName), os.O_RDWR|os.O_CREATE, 0o600)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	return func() { _ = f.Close() }
}

// lockIsFree reports whether another process could lock dir/.lock now.
func lockIsFree(t *testing.T, dir string) bool {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, build.LockFileName), os.O_RDWR, 0)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false
	}
	require.NoError(t, err)
	return true
}

func TestWritePlanRefusesWhileABuildRuns(t *testing.T) {
	env, planned, _ := lockEnv(t)
	release := holdBuildLock(t, planned.Dir)
	err := WritePlan(env, planned)
	require.ErrorIs(t, err, build.ErrBuildRunning)
	assert.ErrorContains(t, err, "for target openshell in "+planned.Dir)
	_, serr := os.Stat(filepath.Join(planned.Dir, plan.PlanFile))
	assert.ErrorIs(t, serr, os.ErrNotExist, "no plan.yaml is written under a running build")

	release()
	require.NoError(t, WritePlan(env, planned), "the plan is written once the build ends")
	_, serr = os.Stat(filepath.Join(planned.Dir, plan.PlanFile))
	require.NoError(t, serr)
	assert.True(t, lockIsFree(t, planned.Dir), "WritePlan releases its lock")
}

func TestBuildHoldsLockAcrossPlanAndBuild(t *testing.T) {
	env, planned, eng := lockEnv(t)
	release := holdBuildLock(t, planned.Dir)
	_, err := Build(context.Background(), env, planned, BuildOptions{})
	require.ErrorIs(t, err, build.ErrBuildRunning)
	assert.ErrorContains(t, err, "for target openshell in "+planned.Dir)
	for _, name := range []string{plan.PlanFile, build.ContainerfileName} {
		_, serr := os.Stat(filepath.Join(planned.Dir, name))
		assert.ErrorIs(t, serr, os.ErrNotExist, "%s is not written under a running build", name)
	}
	assert.Zero(t, eng.CountOp("build"), "no engine build runs")
	release()

	// The plan outputs are written and both passes run under one lock.
	var freeDuringBuild []bool
	var planDuringBuild []bool
	eng.BuildFunc = func(engine.BuildSpec) error {
		freeDuringBuild = append(freeDuringBuild, lockIsFree(t, planned.Dir))
		_, serr := os.Stat(filepath.Join(planned.Dir, plan.PlanFile))
		planDuringBuild = append(planDuringBuild, serr == nil)
		return nil
	}
	_, err = Build(context.Background(), env, planned, BuildOptions{})
	require.NoError(t, err)
	assert.Equal(t, []bool{false, false}, freeDuringBuild, "the lock is held through both passes")
	assert.Equal(t, []bool{true, true}, planDuringBuild, "the plan was written under the same lock")
	assert.True(t, lockIsFree(t, planned.Dir), "Build releases its lock")
}

// A committed symlink at an entry sandcast writes in the build directory is
// refused before the lock is taken and before anything is written.
func TestBuildRefusesSymlinkedBuildDirEntries(t *testing.T) {
	for _, name := range fsutil.BuildEntries() {
		t.Run(name, func(t *testing.T) {
			env, planned, eng := lockEnv(t)
			require.NoError(t, os.MkdirAll(planned.Dir, 0o755))
			victim := filepath.Join(t.TempDir(), "bashrc")
			require.NoError(t, os.WriteFile(victim, []byte("keep\n"), 0o644))
			require.NoError(t, os.Symlink(victim, filepath.Join(planned.Dir, name)))

			_, err := Build(context.Background(), env, planned, BuildOptions{})
			var ue *UsageError
			require.ErrorAs(t, err, &ue)
			assert.ErrorContains(t, err, ".sandcast/build/openshell/"+name+" is a symlink")
			data, rerr := os.ReadFile(victim)
			require.NoError(t, rerr)
			assert.Equal(t, "keep\n", string(data), "the link target is untouched")
			assert.Empty(t, eng.Calls(), "the engine is never called")
			entries, rerr := os.ReadDir(planned.Dir)
			require.NoError(t, rerr)
			require.Len(t, entries, 1, "nothing but the link is in the build directory")

			require.ErrorAs(t, WritePlan(env, planned), &ue, "plan refuses it as well")
		})
	}

	// A dangling link (here at .lock and build.log) must not create its target.
	for _, name := range []string{build.LockFileName, build.BuildLogName} {
		env, planned, _ := lockEnv(t)
		require.NoError(t, os.MkdirAll(planned.Dir, 0o755))
		missing := filepath.Join(t.TempDir(), "created")
		require.NoError(t, os.Symlink(missing, filepath.Join(planned.Dir, name)))
		_, err := Build(context.Background(), env, planned, BuildOptions{})
		require.Error(t, err, name)
		_, serr := os.Lstat(missing)
		assert.ErrorIs(t, serr, os.ErrNotExist, "%s: the link target is not created", name)
	}
}

// The reserved list in fsutil (used by the manifest, which cannot import
// build) must name exactly what the build writes.
func TestBuildEntriesMatchBuildConstants(t *testing.T) {
	assert.ElementsMatch(t, []string{
		build.LockFileName, build.BuildLogName, build.ContainerfileName, build.FinalContainerfileName,
		build.ProfilesDirName, build.ShellContextDir, build.HarnessContextDir,
		plan.PlanFile, plan.PolicyFile, plan.MiseDir, verify.FileName,
	}, fsutil.BuildEntries())
	assert.Equal(t, plan.ShellDir, build.ShellContextDir)
}
