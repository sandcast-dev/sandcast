package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/gateway"
	"github.com/rhuss/sandcast/internal/profile"
)

const importDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// adaptedDir writes an adapted set like a build does into
// <build dir>/profiles and returns that directory. go-proxy is flagged the
// way the build flags a profile with a writable binary.
func adaptedDir(t *testing.T) string {
	t.Helper()
	all, _, err := profile.Resolve(sandcast.UpstreamProfiles, sandcast.OverlayProfiles, nil)
	require.NoError(t, err)
	set, err := profile.Adapt(profile.Set{"github": all["github"], "go-proxy": all["go-proxy"]}, map[string][]string{
		"github":   {"/usr/bin/git"},
		"go-proxy": {"/opt/go/bin/*", "/usr/local/go/bin/go"},
	}, profile.AdaptOptions{
		ImageLabel: "demo",
		Image:      profile.ImageRef{Name: "localhost/demo", Tag: "latest", Digest: importDigest, Platform: "linux/arm64"},
	})
	require.NoError(t, err)
	for i := range set.Profiles {
		if set.Profiles[i].UpstreamID == "go-proxy" {
			set.Profiles[i].Flagged, set.Profiles[i].FlagReason = true, "writable binary"
			set.Index.Profiles[i].Flagged, set.Index.Profiles[i].FlagReason = true, "writable binary"
		}
	}
	dir := filepath.Join(t.TempDir(), "openshell", build.ProfilesDirName)
	require.NoError(t, set.WriteDir(dir))
	return dir
}

func TestProfilesProjectTierDoesNotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	l := fsutil.Layout{Root: root}
	require.NoError(t, os.MkdirAll(l.ProfilesDir(), 0o755))
	secret := filepath.Join(t.TempDir(), "credentials.yaml")
	require.NoError(t, os.WriteFile(secret, []byte("id: leak\ndisplay_name: topsecretvalue\n"), 0o600))
	// A project profile that links out of .sandcast/profiles.
	require.NoError(t, os.Symlink(secret, filepath.Join(l.ProfilesDir(), "x.yaml")))

	_, _, err := Profiles(l)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "x.yaml", "the error names the offending profile entry")
	assert.Contains(t, err.Error(), "escapes", "the rooted FS refuses to leave the profiles directory")
	assert.NotContains(t, err.Error(), "topsecretvalue", "a link out of the profiles directory is not followed")
}

func TestProfilesRefusesSymlinkedProjectTierDir(t *testing.T) {
	root := t.TempDir()
	l := fsutil.Layout{Root: root}
	require.NoError(t, os.MkdirAll(l.SandcastDir(), 0o755))
	// A directory of secrets outside the project, linked in as the whole
	// project profile tier. os.OpenRoot would follow the symlinked root.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.yaml"), []byte("id: leak\ndisplay_name: topsecretvalue\n"), 0o600))
	require.NoError(t, os.Symlink(outside, l.ProfilesDir()))

	_, _, err := Profiles(l)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a symlink")
	assert.NotContains(t, err.Error(), "topsecretvalue", "a symlinked profiles directory is not read")
}

func TestLoadAdaptedRefusesSymlinkedProfilesDir(t *testing.T) {
	dir := adaptedDir(t)
	// Replace the adapted-set directory itself with a link to a directory of
	// secrets outside the build directory.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, IndexFile), []byte("display_name: topsecretvalue\n"), 0o600))
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.Symlink(outside, dir))

	_, _, _, err := LoadAdapted(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a symlink")
	assert.NotContains(t, err.Error(), "topsecretvalue", "a symlinked adapted-set directory is not followed")
}

func TestLoadAdaptedRefusesSymlinkedProfileFile(t *testing.T) {
	dir := adaptedDir(t)
	secret := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(secret, []byte("aws_secret_access_key = topsecretvalue\n"), 0o600))
	// Replace an existing adapted profile file with a link to the secret.
	file := filepath.Join(dir, "github.yaml")
	require.NoError(t, os.Remove(file))
	require.NoError(t, os.Symlink(secret, file))

	_, _, _, err := LoadAdapted(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a symlink")
	assert.NotContains(t, err.Error(), "topsecretvalue", "the link target is never read")
}

func TestLoadAdaptedRefusesSymlinkedIndex(t *testing.T) {
	dir := adaptedDir(t)
	secret := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(secret, []byte("aws_secret_access_key = topsecretvalue\n"), 0o600))
	index := filepath.Join(dir, IndexFile)
	require.NoError(t, os.Remove(index))
	require.NoError(t, os.Symlink(secret, index))

	_, _, _, err := LoadAdapted(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a symlink")
	assert.NotContains(t, err.Error(), "topsecretvalue", "the link target is never read")
}

// fakeOpenShell answers the openshell CLI calls of an import.
type fakeOpenShell struct {
	existing string
	calls    [][]string
	// failImport makes every "profile import" fail.
	failImport bool
}

func (f *fakeOpenShell) Run(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
	f.calls = append(f.calls, args)
	switch {
	case len(args) == 1 && args[0] == "--version":
		return []byte("openshell " + gateway.RequiredVersion()), nil, nil
	case len(args) > 1 && args[1] == "list":
		if f.existing != "" {
			return []byte(`[{"id":"` + f.existing + `"}]`), nil, nil
		}
		return []byte("[]"), nil, nil
	case len(args) > 1 && args[1] == "import" && f.failImport:
		return nil, []byte("gateway unavailable"), errors.New("exit status 1")
	}
	return nil, nil, nil
}

func readIndex(t *testing.T, dir string) *profile.Index {
	t.Helper()
	idx, err := profile.ReadIndex(filepath.Join(dir, IndexFile))
	require.NoError(t, err)
	return idx
}

func TestImportRecordsSuffixedIDsInTheBuildDirectory(t *testing.T) {
	dir := adaptedDir(t)
	gw := &fakeOpenShell{}
	rep, err := Import(context.Background(), &gateway.Client{Runner: gw}, dir, ImportOptions{Scope: gateway.ScopePlatform})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"github-demo", "go-proxy-demo"}, rep.Imported)
	assert.Equal(t, filepath.Join(dir, IndexFile), rep.Index)

	// FR-022a: the runtime finds the imported ids in the build directory.
	idx := readIndex(t, dir)
	assert.Equal(t, profile.ScopePlatform, idx.ScopeHint)
	assert.Equal(t, importDigest, idx.Image.Digest, "the image reference is kept")
	byID := map[string]profile.IndexProfile{}
	for _, ip := range idx.Profiles {
		byID[ip.UpstreamID] = ip
	}
	assert.Equal(t, "github-demo", byID["github"].AdaptedID)
	assert.Equal(t, "go-proxy-demo", byID["go-proxy"].AdaptedID)
	assert.Equal(t, []string{"/opt/go/bin/*", "/usr/local/go/bin/go"}, byID["go-proxy"].Binaries)
	assert.True(t, byID["go-proxy"].Flagged, "a flag of the build survives the re-adaptation")
	assert.Equal(t, "writable binary", byID["go-proxy"].FlagReason)

	names := dirNames(t, dir)
	assert.Equal(t, []string{"github-demo.yaml", "go-proxy-demo.yaml", "index.yaml"}, names, "the files carry the imported ids")
	siblings := dirNames(t, filepath.Dir(dir))
	assert.NotContains(t, strings.Join(siblings, " "), ".profiles", "no temporary directory is left behind")

	// A later workspace import without --suffix goes back to plain ids.
	_, err = Import(context.Background(), &gateway.Client{Runner: &fakeOpenShell{}}, dir, ImportOptions{Scope: gateway.ScopeWorkspace})
	require.NoError(t, err)
	idx = readIndex(t, dir)
	assert.Equal(t, profile.ScopeWorkspace, idx.ScopeHint)
	assert.Equal(t, []string{"github.yaml", "go-proxy.yaml", "index.yaml"}, dirNames(t, dir))
}

func TestImportLeavesTheBuildDirectoryWhenNothingChanged(t *testing.T) {
	for name, tc := range map[string]struct {
		opts          ImportOptions
		gw            *fakeOpenShell
		wantErr       string
		wantConflicts []string
		wantImported  []string
	}{
		"dry run": {opts: ImportOptions{Scope: gateway.ScopePlatform, DryRun: true}, gw: &fakeOpenShell{}},
		"conflict": {
			opts: ImportOptions{Scope: gateway.ScopePlatform}, gw: &fakeOpenShell{existing: "github-demo"},
			// github-demo already exists, so it is reported as a conflict and
			// the build directory is left untouched; go-proxy-demo is still
			// imported to the gateway before the conflict short-circuits the
			// directory write.
			wantConflicts: []string{"github-demo"},
			wantImported:  []string{"go-proxy-demo"},
		},
		"gateway failure": {
			opts: ImportOptions{Scope: gateway.ScopePlatform}, gw: &fakeOpenShell{failImport: true},
			wantErr: "gateway unavailable",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := adaptedDir(t)
			before, err := os.ReadFile(filepath.Join(dir, IndexFile))
			require.NoError(t, err)
			rep, err := Import(context.Background(), &gateway.Client{Runner: tc.gw}, dir, tc.opts)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.NotNil(t, rep, "the report says what the gateway accepted")
				assert.Contains(t, rep.Error, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, rep.Index)
			if tc.wantConflicts != nil {
				assert.ElementsMatch(t, tc.wantConflicts, rep.Conflicts, "the conflicting ids are reported")
			}
			if tc.wantImported != nil {
				assert.ElementsMatch(t, tc.wantImported, rep.Imported, "the non-conflicting ids still reach the gateway")
			}
			after, err := os.ReadFile(filepath.Join(dir, IndexFile))
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "the index still matches the image")
		})
	}
}

func TestReplaceAdaptedKeepsThePreviousSetWhenBothRenamesFail(t *testing.T) {
	dir := adaptedDir(t)
	set, err := Readapt(dir, true, gateway.ScopePlatform)
	require.NoError(t, err)

	// The first rename moves the old set aside, then both the staged-set
	// rename and its rollback fail. replaceAdapted must keep the staging
	// directory and point the error at the preserved previous set rather
	// than leave the build directory without any profile set.
	calls := 0
	orig := renameFn
	renameFn = func(from, to string) error {
		calls++
		if calls == 1 {
			return orig(from, to) // dir -> <tmp>/previous
		}
		return errors.New("rename blocked")
	}
	t.Cleanup(func() { renameFn = orig })

	err = replaceAdapted(dir, set)
	require.Error(t, err)
	assert.Equal(t, 3, calls, "the swap and then the rollback are both attempted")

	const marker = "the previous set is at "
	require.ErrorContains(t, err, marker)
	previous := err.Error()[strings.Index(err.Error(), marker)+len(marker):]
	_, statErr := os.Stat(filepath.Join(previous, IndexFile))
	require.NoError(t, statErr, "the previous set survives at the path named in the error")
}

func TestImportRefusesWhileABuildRuns(t *testing.T) {
	dir := adaptedDir(t)
	buildDir := filepath.Dir(dir)
	unlock, err := build.LockDir(buildDir)
	require.NoError(t, err)
	defer unlock()
	gw := &fakeOpenShell{}
	_, err = Import(context.Background(), &gateway.Client{Runner: gw}, dir, ImportOptions{Scope: gateway.ScopePlatform})
	require.ErrorIs(t, err, build.ErrBuildRunning)
	assert.ErrorContains(t, err, "for target openshell in "+buildDir+"; wait for it to finish", "the same message as a build")
	assert.Empty(t, gw.calls, "nothing reaches the gateway")
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

func TestExportFromImageIsBounded(t *testing.T) {
	eng := &deadlineEngine{Fake: &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) {
		return []byte("=== other.yaml\nx: 1\n"), nil, nil
	}}}
	_, err := ExportFromImage(context.Background(), eng, "localhost/demo:latest", "", filepath.Join(t.TempDir(), "openshell", build.ProfilesDirName), false)
	require.ErrorContains(t, err, "was it built by sandcast")
	assert.Greater(t, eng.deadline, time.Duration(0), "the export container has a deadline")
	assert.LessOrEqual(t, eng.deadline, engine.HelperTimeout)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
