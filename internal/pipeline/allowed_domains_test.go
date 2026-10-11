package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/profile"
)

func TestAllowedDomainsPathStaysInBuildDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "build", "container")
	got, err := allowedDomainsPath(dir, "allowed-domains.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "allowed-domains.txt"), got)

	for _, name := range []string{"../../../.git/config", "../x", "sub/x", "/etc/passwd", ".", "..", ""} {
		_, err := allowedDomainsPath(dir, name)
		var ue *UsageError
		require.ErrorAs(t, err, &ue, "%q", name)
		assert.Contains(t, err.Error(), "allowed_domains_file")
	}
	// Names the build writes itself, in any case, and hidden names: the
	// rename would replace the lock or the log mid-build.
	for _, name := range append(fsutil.BuildEntries(), "BUILD.LOG", "containerfile.final", ".domains") {
		_, err := allowedDomainsPath(dir, name)
		var ue *UsageError
		require.ErrorAs(t, err, &ue, "%q", name)
		assert.Contains(t, err.Error(), "allowed_domains_file")
	}
}

// A manifest that skipped validation must not make Build rename the domain
// list over the lock or the log while the build runs.
func TestBuildRefusesReservedAllowedDomainsName(t *testing.T) {
	for _, name := range []string{build.LockFileName, build.BuildLogName} {
		root := t.TempDir()
		dir := filepath.Join(root, ".sandcast", "build", "container")
		m := manifest.Skeleton("demo", "x", manifest.TargetContainer)
		m.Targets.Container.AllowedDomainsFile = name
		_, err := Build(context.Background(), Env{Layout: fsutil.Layout{Root: root}, Manifest: m},
			&Planned{Plan: &plan.Plan{Target: manifest.TargetContainer}, Dir: dir}, BuildOptions{})
		var ue *UsageError
		require.ErrorAs(t, err, &ue, name)
		_, err = os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "nothing is written before the refusal")
	}
}

// A manifest that skipped validation (built in code, or edited between
// load and build) must still not let Build write outside the build directory.
func TestBuildRefusesAllowedDomainsOutsideBuildDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sandcast", "build", "container")
	victim := filepath.Join(root, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("keep\n"), 0o644))

	m := manifest.Skeleton("demo", "x", manifest.TargetContainer)
	m.Targets.Container.AllowedDomainsFile = "../../../victim"
	_, err := Build(context.Background(), Env{Manifest: m},
		&Planned{Plan: &plan.Plan{Target: manifest.TargetContainer}, Dir: dir}, BuildOptions{})
	var ue *UsageError
	require.ErrorAs(t, err, &ue)

	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "nothing is written before the refusal")
}

func TestWriteAllowedDomainsRefusesInvalidHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowed-domains.txt")
	set := &profile.AdaptedSet{Profiles: []profile.AdaptedProfile{{Profile: &profile.Profile{
		Endpoints: []profile.Endpoint{{Host: "github.com"}, {Host: "evil.com\n[core]\n\tfsmonitor = x"}},
	}}}}
	err := WriteAllowedDomains(path, set)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid host")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))

	set.Profiles[0].Profile.Endpoints = []profile.Endpoint{{Host: "github.com"}, {Host: "*.s3.amazonaws.com"}}
	require.NoError(t, WriteAllowedDomains(path, set))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "*.s3.amazonaws.com\ngithub.com\n", string(data))
}
