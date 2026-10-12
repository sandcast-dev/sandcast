package profile

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/testutil"
)

const adaptedGolden = "testdata/adapted"

var testDigest = "sha256:" + strings.Repeat("0123456789abcdef", 4)

const internalAPIProfile = `# A project-local profile.
id: internal-api
display_name: Internal API
description: Company API used by the build scripts
category: data
credentials:
  - name: api_key
    env_vars: [INTERNAL_API_TOKEN]
    auth_style: header
    header_name: x-api-key
endpoints:
  - host: api.internal.example.com
    port: 443
    protocol: rest
    access: read-only
    enforcement: enforce
binaries: [/usr/bin/curl]
`

// adaptFixture resolves the shipped tiers plus one project profile.
func adaptFixture(t *testing.T, project fstest.MapFS) Set {
	t.Helper()
	if project == nil {
		project = fstest.MapFS{"internal-api.yaml": {Data: []byte(internalAPIProfile)}}
	}
	set, _, err := Resolve(os.DirFS(upstreamDir), os.DirFS(overlayDir), project)
	require.NoError(t, err)
	return set
}

func fixtureBinaries() map[string][]string {
	return map[string][]string{
		"github": {"/usr/bin/git", "/usr/bin/gh", "/usr/bin/git"},
		"pypi": {
			"/opt/mise/installs/uv/0.5.0/uv",
			"/opt/mise/installs/python/3.12.7/bin/python3.12",
			"/opt/mise/installs/python/*/bin/python3*",
		},
		"npm":          nil,
		"internal-api": {"/usr/bin/curl"},
	}
}

func fixtureOptions() AdaptOptions {
	return AdaptOptions{
		ImageLabel: ImageLabel("Demo Sandbox"),
		Image: ImageRef{
			Name:     "quay.io/example/demo-sandbox",
			Tag:      "latest",
			Digest:   testDigest,
			Platform: "linux/amd64",
		},
	}
}

// readDir returns the files of dir by name.
func readDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := map[string][]byte{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		out[e.Name()] = data
	}
	return out
}

func TestAdaptGolden(t *testing.T) {
	tests := map[string]func(*AdaptOptions){
		"workspace": func(*AdaptOptions) {},
		"platform":  func(o *AdaptOptions) { o.ScopeHint = ScopePlatform },
	}
	for scenario, mutate := range tests {
		t.Run(scenario, func(t *testing.T) {
			opts := fixtureOptions()
			mutate(&opts)
			set, err := Adapt(adaptFixture(t, nil), fixtureBinaries(), opts)
			require.NoError(t, err)

			dir := t.TempDir()
			require.NoError(t, set.WriteDir(dir))
			files := readDir(t, dir)
			label, err := set.LabelJSON()
			require.NoError(t, err)
			files["label.json"] = []byte(label + "\n")

			goldenDir := filepath.Join(adaptedGolden, scenario)
			var names []string
			for name, data := range files {
				names = append(names, name)
				testutil.Golden(t, filepath.Join(goldenDir, name), data)
			}
			if os.Getenv(testutil.UpdateEnv) != "1" {
				golden, err := os.ReadDir(goldenDir)
				require.NoError(t, err)
				var want []string
				for _, g := range golden {
					want = append(want, g.Name())
				}
				sort.Strings(names)
				assert.Equal(t, want, names, "no golden file without output and no output without golden file")
			}
		})
	}
}

func TestAdaptContent(t *testing.T) {
	set := adaptFixture(t, nil)
	upstreamBins := append(Binaries{}, set["github"].Profile.Binaries...)

	opts := fixtureOptions()
	opts.ScopeHint = ScopePlatform
	adapted, err := Adapt(set, fixtureBinaries(), opts)
	require.NoError(t, err)

	require.Len(t, adapted.Profiles, 4)
	byID := map[string]AdaptedProfile{}
	for _, ap := range adapted.Profiles {
		byID[ap.UpstreamID] = ap
	}
	gh := byID["github"]
	assert.Equal(t, "github-demo-sandbox", gh.AdaptedID)
	assert.Equal(t, "github-demo-sandbox.yaml", gh.File)
	assert.Equal(t, Binaries{"/usr/bin/gh", "/usr/bin/git"}, gh.Profile.Binaries, "deduplicated and sorted")
	assert.Equal(t, TierUpstream, gh.SourceTier)
	assert.Equal(t, "profiles/upstream/v0.1.3/github.yaml", gh.SourceFile)
	assert.False(t, gh.Flagged)

	npm := byID["npm"]
	assert.True(t, npm.Flagged)
	assert.Equal(t, noBinaryReason, npm.FlagReason)
	assert.Empty(t, npm.Profile.Binaries)
	assert.Equal(t, TierOverlay, npm.SourceTier)

	assert.Equal(t, TierProject, byID["internal-api"].SourceTier)
	assert.Equal(t, ".sandcast/profiles/internal-api.yaml", byID["internal-api"].SourceFile)

	// The resolved set is not modified.
	assert.Equal(t, "github", set["github"].Profile.ID)
	assert.Equal(t, upstreamBins, set["github"].Profile.Binaries)

	assert.Equal(t, ScopePlatform, adapted.Index.ScopeHint)
	assert.Equal(t, "demo-sandbox", adapted.Index.ImageLabel)
	assert.Equal(t, []string{}, adapted.Index.Profiles[2].Binaries, "npm index entry")
}

func TestAdaptSuffixWithoutPlatformScope(t *testing.T) {
	opts := fixtureOptions()
	opts.Suffix = true
	adapted, err := Adapt(adaptFixture(t, nil), map[string][]string{"pypi": {"/usr/bin/python3"}}, opts)
	require.NoError(t, err)
	assert.Equal(t, ScopeWorkspace, adapted.Index.ScopeHint)
	assert.Equal(t, "pypi-demo-sandbox", adapted.Profiles[0].AdaptedID)
}

func TestAdaptIsDeterministic(t *testing.T) {
	render := func() map[string][]byte {
		set, err := Adapt(adaptFixture(t, nil), fixtureBinaries(), fixtureOptions())
		require.NoError(t, err)
		dir := t.TempDir()
		require.NoError(t, set.WriteDir(dir))
		return readDir(t, dir)
	}
	assert.Equal(t, render(), render())
}

func TestAdaptEmpty(t *testing.T) {
	set, err := Adapt(adaptFixture(t, nil), nil, fixtureOptions())
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, set.WriteDir(dir))
	files := readDir(t, dir)
	require.Len(t, files, 1)
	assert.Contains(t, string(files[IndexFile]), "profiles: []\n")
	label, err := set.LabelJSON()
	require.NoError(t, err)
	assert.Equal(t, `{"ids":[],"flagged":[]}`, label)
}

func TestAdaptErrors(t *testing.T) {
	tests := map[string]struct {
		project  fstest.MapFS
		binaries map[string][]string
		mutate   func(*AdaptOptions)
		want     string
		lint     bool
	}{
		"referenced id missing": {
			binaries: map[string][]string{"github": {"/usr/bin/git"}, "nope": {"/usr/bin/nope"}},
			want:     `provider profile "nope" is referenced but defined in no profile tier`,
		},
		"scope hint": {
			mutate: func(o *AdaptOptions) { o.ScopeHint = "global" },
			want:   `scope hint "global"`,
		},
		"empty image label": {
			mutate: func(o *AdaptOptions) { o.ImageLabel = "" },
			want:   `image label "" must be lowercase kebab-case`,
		},
		"invalid image label": {
			mutate: func(o *AdaptOptions) { o.ImageLabel = "Demo_Sandbox" },
			want:   "must be lowercase kebab-case",
		},
		"digest": {
			mutate: func(o *AdaptOptions) { o.Image.Digest = "latest" },
			want:   `image digest "latest" is not a sha256 digest`,
		},
		"empty resolved path": {
			binaries: map[string][]string{"github": {"/usr/bin/git", ""}},
			want:     "binaries[0]: binary path must not be empty",
			lint:     true,
		},
		"project profile fails lint": {
			project: fstest.MapFS{"internal-api.yaml": {Data: []byte(
				"id: internal-api\ndisplay_name: Internal\nendpoints:\n  - host: localhost\n    port: 8080\n")}},
			binaries: map[string][]string{"internal-api": {"/usr/bin/curl"}},
			want:     ".sandcast/profiles/internal-api.yaml: internal-api: endpoints[0].host: endpoint host 'localhost' is always blocked",
			lint:     true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opts := fixtureOptions()
			if tc.mutate != nil {
				tc.mutate(&opts)
			}
			bins := tc.binaries
			if bins == nil {
				bins = fixtureBinaries()
			}
			_, err := Adapt(adaptFixture(t, tc.project), bins, opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			var lintErr *LintError
			assert.Equal(t, tc.lint, errors.As(err, &lintErr))
		})
	}
}

func TestWriteDirRemovesStaleProfiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stale.yaml"), []byte("id: stale\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "old.yml"), []byte("id: old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep\n"), 0o644))

	set, err := Adapt(adaptFixture(t, nil), map[string][]string{"pypi": {"/usr/bin/python3"}}, fixtureOptions())
	require.NoError(t, err)
	require.NoError(t, set.WriteDir(dir))

	var names []string
	for name := range readDir(t, dir) {
		names = append(names, name)
	}
	sort.Strings(names)
	assert.Equal(t, []string{"index.yaml", "notes.txt", "pypi.yaml"}, names)
}

func TestWrittenProfilesParseAndLint(t *testing.T) {
	set, err := Adapt(adaptFixture(t, nil), fixtureBinaries(), fixtureOptions())
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, set.WriteDir(dir))
	for _, ap := range set.Profiles {
		p, err := LoadFile(filepath.Join(dir, ap.File))
		require.NoError(t, err)
		assert.Equal(t, ap.Profile, p)
		assert.Empty(t, Lint(p, ap.File))
	}
}

func TestReadIndex(t *testing.T) {
	set, err := Adapt(adaptFixture(t, nil), fixtureBinaries(), fixtureOptions())
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, set.WriteDir(dir))

	idx, err := ReadIndex(filepath.Join(dir, IndexFile))
	require.NoError(t, err)
	assert.Equal(t, &set.Index, idx)

	bad := map[string]struct {
		doc  string
		want string
	}{
		"unknown field":  {"schema_version: 1\nextra: true\n", `unknown field "extra"`},
		"schema version": {"schema_version: 2\n", "unsupported schema_version 2"},
	}
	for name, tc := range bad {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), IndexFile)
			require.NoError(t, os.WriteFile(path, []byte(tc.doc), 0o644))
			_, err := ReadIndex(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	_, err = ReadIndex(filepath.Join(t.TempDir(), "absent.yaml"))
	require.Error(t, err)
}

func TestImageLabel(t *testing.T) {
	for in, want := range map[string]string{
		"demo":          "demo",
		"Demo Sandbox":  "demo-sandbox",
		"my_project.v2": "my-project-v2",
		"--x--":         "x",
		"A  B":          "a-b",
		"already-ok-1":  "already-ok-1",
		"Ünïcode":       "n-code",
		"":              "",
		"___":           "",
	} {
		assert.Equal(t, want, ImageLabel(in), in)
		if want != "" {
			assert.Regexp(t, imageLabelPattern, want)
		}
	}
}

func TestAdaptedID(t *testing.T) {
	assert.Equal(t, "github", AdaptedID("github", "demo", false))
	assert.Equal(t, "github-demo", AdaptedID("github", "demo", true))
	assert.Equal(t, "github", AdaptedID("github", "", true))
}
