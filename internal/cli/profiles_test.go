package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/pipeline"
)

func builtProject(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.project(t)
	require.Equal(t, 0, ta.run("build"), ta.stderr.String())
	ta.eng.exportDir = filepath.Join(ta.dir, ".sandcast", "build", "openshell", "profiles")
	return ta
}

func TestProfilesImport(t *testing.T) {
	ta := builtProject(t)
	require.Equal(t, 0, ta.run("profiles", "import", "--json"), ta.stderr.String())
	var rep pipeline.ImportReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.ElementsMatch(t, []string{"github", "gitlab", "go-proxy"}, rep.Imported)
	assert.Empty(t, rep.Conflicts)
	imports := 0
	for _, c := range ta.gw.calls {
		if strings.Contains(strings.Join(c, " "), "profile import --file") {
			imports++
			assert.NotContains(t, strings.Join(c, " "), "--global")
		}
	}
	assert.Equal(t, 3, imports)
}

func TestProfilesImportConflictsAndUpdate(t *testing.T) {
	ta := builtProject(t)
	ta.gw.existing = []string{"github"}
	assert.Equal(t, ExitFailure, ta.run("profiles", "import"))
	assert.Contains(t, ta.stdout.String(), "exists: github (left unchanged)")
	assert.Contains(t, ta.stderr.String(), "--update")

	ta.gw.calls = nil
	require.Equal(t, 0, ta.run("profiles", "import", "--update"))
	assert.Contains(t, ta.stdout.String(), "updated: github")
	var sawUpdate bool
	for _, c := range ta.gw.calls {
		if strings.Contains(strings.Join(c, " "), "profile update github --file") {
			sawUpdate = true
		}
	}
	assert.True(t, sawUpdate)

	// The update carries the resource_version the gateway exported; fresh
	// imports carry none.
	sent := map[string]sentProfile{}
	for _, sp := range ta.gw.sent {
		sent[sp.id] = sp
	}
	require.Contains(t, sent, "github")
	assert.Equal(t, "update", sent["github"].op)
	assert.Equal(t, uint64(4), sent["github"].resourceVersion)
	require.Contains(t, sent, "go-proxy")
	assert.Equal(t, "import", sent["go-proxy"].op)
	assert.Zero(t, sent["go-proxy"].resourceVersion)
	assert.Contains(t, sent["go-proxy"].binaries, "/usr/local/bin/go", "binaries resolved in the image")
}

func TestProfilesImportPartialFailureReportsAccepted(t *testing.T) {
	ta := builtProject(t)
	ta.gw.failAfter = 1
	assert.Equal(t, ExitFailure, ta.run("profiles", "import", "--json"))
	var rep pipeline.ImportReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep), ta.stdout.String())
	assert.Len(t, rep.Imported, 1, "the profile the gateway accepted is reported")
	assert.Contains(t, rep.Error, "gateway unavailable")

	ta.gw.sent = nil
	assert.Equal(t, ExitFailure, ta.run("profiles", "import"))
	assert.Contains(t, ta.stdout.String(), "imported: ")
	assert.Contains(t, ta.stderr.String(), "gateway unavailable")
}

func TestProfilesImportGatewayAndWorkspacePassThrough(t *testing.T) {
	ta := builtProject(t)
	ta.gw.existing = []string{"github"}
	require.Equal(t, 0, ta.run("profiles", "import", "--gateway", "test", "--workspace", "team", "--update"), ta.stderr.String())
	ops := map[string]int{}
	for _, c := range ta.gw.calls[1:] { // the first call is the version check
		require.GreaterOrEqual(t, len(c), 5, strings.Join(c, " "))
		assert.Equal(t, []string{"--gateway", "test"}, c[1:3], strings.Join(c, " "))
		assert.Contains(t, strings.Join(c, " "), "--workspace team", strings.Join(c, " "))
		ops[c[3]+" "+c[4]]++
	}
	assert.Equal(t, map[string]int{"profile list": 1, "profile export": 1, "profile update": 1, "profile import": 2}, ops)
}

func TestProfilesImportPlatformScopeUpdate(t *testing.T) {
	ta := builtProject(t)
	ta.gw.existing = []string{"github-demo"}
	require.Equal(t, 0, ta.run("profiles", "import", "--scope", "platform", "--update"), ta.stderr.String())
	var sawExport, sawUpdate bool
	for _, c := range ta.gw.calls {
		joined := strings.Join(c, " ")
		if strings.Contains(joined, "profile export github-demo") {
			sawExport = true
			assert.Contains(t, c, "--global", joined)
		}
		if strings.Contains(joined, "profile update github-demo") {
			sawUpdate = true
			assert.Contains(t, c, "--global", joined)
		}
	}
	assert.True(t, sawExport && sawUpdate)
}

func TestProfilesImportPlatformScope(t *testing.T) {
	ta := builtProject(t)
	require.Equal(t, 0, ta.run("profiles", "import", "--scope", "platform", "--json"), ta.stderr.String())
	var rep pipeline.ImportReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, "platform", rep.Scope)
	assert.ElementsMatch(t, []string{"github-demo", "gitlab-demo", "go-proxy-demo"}, rep.Imported)

	var ids []string
	for _, sp := range ta.gw.sent {
		assert.Equal(t, "import", sp.op)
		assert.True(t, sp.global, "%s imported with --global", sp.id)
		ids = append(ids, sp.id)
	}
	assert.ElementsMatch(t, []string{"github-demo", "gitlab-demo", "go-proxy-demo"}, ids, "the files carry the suffixed ids")

	profileCalls := 0
	for _, c := range ta.gw.calls {
		if len(c) > 1 && c[1] == "profile" {
			profileCalls++
			assert.Contains(t, c, "--global", strings.Join(c, " "))
		}
	}
	assert.Equal(t, 4, profileCalls, "one list and three imports")
}

func TestProfilesImportPlatformScopeSuffixes(t *testing.T) {
	ta := builtProject(t)
	require.Equal(t, 0, ta.run("profiles", "import", "--scope", "platform", "--dry-run", "--json"))
	var rep pipeline.ImportReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Contains(t, rep.Imported, "github-demo")
	assert.Contains(t, rep.Mapping, pipeline.IDMap{UpstreamID: "github", AdaptedID: "github-demo"})
	for _, c := range ta.gw.calls {
		assert.NotContains(t, strings.Join(c, " "), "profile import", "dry run changes nothing")
	}
}

func TestProfilesImportVersionMismatchExit3(t *testing.T) {
	ta := builtProject(t)
	ta.gw.version = "0.1.3-dev.102"
	assert.Equal(t, ExitTool, ta.run("profiles", "import"))
	assert.Contains(t, ta.stderr.String(), "requires openshell v0.1.2")
	assert.Len(t, ta.gw.calls, 1, "no gateway call after the version check")
}

func TestProfilesExport(t *testing.T) {
	ta := builtProject(t)
	src := t.TempDir()
	// Serve a copy so export can overwrite the build directory.
	require.NoError(t, copyDir(ta.eng.exportDir, src))
	ta.eng.exportDir = src
	require.Equal(t, 0, ta.run("profiles", "export", "--suffix"), ta.stderr.String())
	assert.Contains(t, ta.stdout.String(), "github -> github-demo")
}

func TestProfilesExportRefusesWhileABuildRuns(t *testing.T) {
	ta := builtProject(t)
	src := t.TempDir()
	require.NoError(t, copyDir(ta.eng.exportDir, src))
	ta.eng.exportDir = src
	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	unlock, err := build.LockDir(dir)
	require.NoError(t, err)
	defer unlock()
	runs := ta.eng.CountOp("run")
	assert.Equal(t, ExitFailure, ta.run("profiles", "export", "--suffix"))
	assert.Contains(t, ta.stderr.String(), "for target openshell in "+dir+"; wait for it to finish")
	assert.Equal(t, runs, ta.eng.CountOp("run"))
	assert.FileExists(t, filepath.Join(dir, "profiles", "github.yaml"), "the set of the build is left alone")
}

func TestProfilesImportRefusesSymlinkedBuildDir(t *testing.T) {
	ta := builtProject(t)
	buildRoot := filepath.Join(ta.dir, ".sandcast", "build")
	elsewhere := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.Rename(buildRoot, elsewhere))
	require.NoError(t, os.Symlink(elsewhere, buildRoot))
	index := filepath.Join(elsewhere, "openshell", "profiles", pipeline.IndexFile)
	before, err := os.ReadFile(index)
	require.NoError(t, err)

	assert.Equal(t, ExitUsage, ta.run("profiles", "import", "--scope", "platform"))
	assert.Contains(t, ta.stderr.String(), "is a symlink")
	assert.Empty(t, ta.gw.calls, "nothing reaches the gateway")
	after, err := os.ReadFile(index)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the directory the symlink points to is left alone")
}

func TestProfilesLintRefusesSymlinkedBuildDir(t *testing.T) {
	ta := builtProject(t)
	buildRoot := filepath.Join(ta.dir, ".sandcast", "build")
	elsewhere := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.Rename(buildRoot, elsewhere))
	require.NoError(t, os.Symlink(elsewhere, buildRoot))

	assert.Equal(t, ExitUsage, ta.run("profiles", "lint"))
	assert.Contains(t, ta.stderr.String(), "is a symlink")
}

func TestProfilesLintRefusesSymlinkedProfileFile(t *testing.T) {
	ta := builtProject(t)
	secret := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(secret, []byte("aws_secret_access_key = topsecretvalue\n"), 0o600))
	link := filepath.Join(ta.dir, ".sandcast", "build", "openshell", "profiles", "leak.yaml")
	require.NoError(t, os.Symlink(secret, link))

	assert.Equal(t, ExitUsage, ta.run("profiles", "lint"))
	out := ta.stdout.String() + ta.stderr.String()
	assert.Contains(t, out, "is a symlink")
	assert.NotContains(t, out, "topsecretvalue", "the link target is never read")
}

func TestProfilesLintBadFile(t *testing.T) {
	ta := newTestApp(t)
	ta.write(t, "bad.yaml", "id: Bad_Id\ndisplay_name: X\ncategory: nonsense\nbinaries: []\n")
	assert.Equal(t, ExitUsage, ta.run("profiles", "lint", filepath.Join(ta.dir, "bad.yaml")))
	assert.Contains(t, ta.stdout.String(), "Bad_Id")
}

func TestProfilesImportRefusesEditedProfileWithCredential(t *testing.T) {
	ta := builtProject(t)
	file := filepath.Join(ta.eng.exportDir, "github.yaml")
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	// A comment would not survive re-adaptation; a field value does.
	edited := strings.Replace(string(data), "description: GitHub API and Git operations", "description: use ghp_"+strings.Repeat("F", 36), 1)
	require.NotEqual(t, string(data), edited)
	require.NoError(t, os.WriteFile(file, []byte(edited), 0o644))
	assert.NotEqual(t, 0, ta.run("profiles", "import"))
	assert.Contains(t, ta.stderr.String(), "credential")
	assert.Empty(t, ta.gw.sent, "nothing reaches the gateway")
}

func TestProfilesExportRefusesImageProfileWithCredential(t *testing.T) {
	ta := builtProject(t)
	src := t.TempDir()
	require.NoError(t, copyDir(ta.eng.exportDir, src))
	data, err := os.ReadFile(filepath.Join(src, "github.yaml"))
	require.NoError(t, err)
	edited := strings.Replace(string(data), "description: GitHub API and Git operations", "description: use ghp_"+strings.Repeat("G", 36), 1)
	require.NotEqual(t, string(data), edited)
	require.NoError(t, os.WriteFile(filepath.Join(src, "github.yaml"), []byte(edited), 0o644))
	ta.eng.exportDir = src
	before, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "profiles", "github.yaml"))
	require.NoError(t, err)
	assert.NotEqual(t, 0, ta.run("profiles", "export"))
	assert.Contains(t, ta.stderr.String(), "credential", "refused by the scan, not by a parse error")
	after, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "profiles", "github.yaml"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the build directory is left alone")
}
