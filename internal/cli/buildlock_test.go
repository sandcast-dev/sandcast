package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
)

// holdBuildLock locks dir/.lock the way another sandcast process would: a
// raw flock on a file descriptor of its own, released at the end of the
// test.
func holdBuildLock(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	fd, err := syscall.Open(filepath.Join(dir, build.LockFileName), syscall.O_RDWR|syscall.O_CREAT, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Close(fd) })
	require.NoError(t, syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB))
}

func TestCommandsRefuseWhileABuildRuns(t *testing.T) {
	for _, argv := range [][]string{{"plan"}, {"build"}} {
		t.Run(argv[0], func(t *testing.T) {
			ta := newTestApp(t)
			ta.project(t)
			dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
			holdBuildLock(t, dir)
			assert.Equal(t, ExitFailure, ta.run(argv...))
			assert.Contains(t, ta.stderr.String(), "another sandcast build is running for target openshell in "+dir+"; wait for it to finish")
			assert.Zero(t, ta.eng.CountOp("build"), "no image is built")
			assert.NoFileExists(t, filepath.Join(dir, "plan.yaml"), "nothing is written into the locked directory")
		})
	}
}

func TestCommandsRefuseSymlinkedBuildEntries(t *testing.T) {
	for _, entry := range []string{build.LockFileName, build.BuildLogName} {
		for _, argv := range [][]string{{"plan"}, {"build"}, {"verify"}, {"profiles", "export"}, {"profiles", "import"}} {
			t.Run(entry+"/"+strings.Join(argv, " "), func(t *testing.T) {
				ta := builtProject(t)
				dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
				outside := filepath.Join(t.TempDir(), "target")
				require.NoError(t, os.Remove(filepath.Join(dir, entry)))
				require.NoError(t, os.Symlink(outside, filepath.Join(dir, entry)))
				builds, runs := ta.eng.CountOp("build"), ta.eng.CountOp("run")

				assert.Equal(t, ExitUsage, ta.run(argv...), ta.stdout.String())
				assert.Contains(t, ta.stderr.String(), ".sandcast/build/openshell/"+entry+" is a symlink")
				assert.NoFileExists(t, outside, "nothing is created through the link")
				assert.Equal(t, builds, ta.eng.CountOp("build"))
				assert.Equal(t, runs, ta.eng.CountOp("run"), "no container runs")
				assert.Empty(t, ta.gw.calls, "nothing reaches the gateway")
			})
		}
	}
}

func TestManifestValidateRefusesBuildEntryAsAllowedDomainsFile(t *testing.T) {
	for _, name := range []string{build.LockFileName, build.BuildLogName} {
		t.Run(name, func(t *testing.T) {
			ta := newTestApp(t)
			ta.project(t)
			ta.write(t, ".sandcast/manifest.yaml", testManifest+"    allowed_domains_file: \""+name+"\"\n")
			assert.Equal(t, ExitUsage, ta.run("manifest", "validate"), ta.stdout.String())
			assert.Contains(t, ta.stdout.String()+ta.stderr.String(), "allowed_domains_file")
		})
	}
}
