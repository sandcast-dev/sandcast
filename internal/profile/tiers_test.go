package profile

import (
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func profileFile(id, name string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("id: " + id + "\ndisplay_name: " + name + "\n")}
}

func TestTierPrefix(t *testing.T) {
	assert.Equal(t, "profiles/upstream/v0.1.3", TierUpstream.Prefix())
	assert.Equal(t, "profiles/overlay", TierOverlay.Prefix())
	assert.Equal(t, ".sandcast/profiles", TierProject.Prefix())
	assert.Equal(t, "custom", Tier("custom").Prefix())
}

func TestResolveLaterTierWins(t *testing.T) {
	upstream := fstest.MapFS{
		"github.yaml": profileFile("github", "GitHub upstream"),
		"pypi.yaml":   profileFile("pypi", "PyPI"),
	}
	overlay := fstest.MapFS{
		"npm.yaml":    profileFile("npm", "npm"),
		"github.yaml": profileFile("github", "GitHub overlay"),
	}
	project := fstest.MapFS{
		"my-github.yaml": profileFile("github", "GitHub project"),
		"internal.yaml":  profileFile("internal-api", "Internal"),
	}

	set, overrides, err := Resolve(upstream, overlay, project)
	require.NoError(t, err)
	assert.Equal(t, []string{"github", "internal-api", "npm", "pypi"}, set.IDs())

	gh := set["github"]
	assert.Equal(t, "GitHub project", gh.Profile.DisplayName)
	assert.Equal(t, TierProject, gh.Tier)
	assert.Equal(t, ".sandcast/profiles/my-github.yaml", gh.SourceFile)
	assert.Equal(t, TierUpstream, set["pypi"].Tier)
	assert.Equal(t, "profiles/upstream/v0.1.3/pypi.yaml", set["pypi"].SourceFile)
	assert.Equal(t, "profiles/overlay/npm.yaml", set["npm"].SourceFile)

	assert.Equal(t, []Override{
		{ID: "github", Loser: "profiles/upstream/v0.1.3/github.yaml", Winner: "profiles/overlay/github.yaml"},
		{ID: "github", Loser: "profiles/overlay/github.yaml", Winner: ".sandcast/profiles/my-github.yaml"},
	}, overrides)

	entries := set.Entries()
	require.Len(t, entries, 4)
	assert.Equal(t, "github", entries[0].Profile.ID)
	assert.Equal(t, "pypi", entries[3].Profile.ID)
}

func TestResolveSkipsNilTiers(t *testing.T) {
	set, overrides, err := Resolve(nil, fstest.MapFS{"npm.yaml": profileFile("npm", "npm")}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"npm"}, set.IDs())
	assert.Empty(t, overrides)

	set, _, err = Resolve(nil, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, set)
}

func TestResolveErrors(t *testing.T) {
	tests := map[string]struct {
		upstream, overlay, project fs.FS
		want                       string
	}{
		"duplicate id within a tier": {
			overlay: fstest.MapFS{
				"a.yaml": profileFile("npm", "A"),
				"b.yaml": profileFile("npm", "B"),
			},
			want: `duplicate provider profile id "npm" in overlay tier: profiles/overlay/a.yaml and profiles/overlay/b.yaml`,
		},
		"parse error names the file": {
			project: fstest.MapFS{"broken.yaml": {Data: []byte("id: x\nhomepage: y\n")}},
			want:    ".sandcast/profiles/broken.yaml",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := Resolve(tc.upstream, tc.overlay, tc.project)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestResolveShippedTiers(t *testing.T) {
	set, overrides, err := Resolve(os.DirFS(upstreamDir), os.DirFS(overlayDir), nil)
	require.NoError(t, err)
	assert.Empty(t, overrides, "the overlay adds ids and shadows none")
	assert.Len(t, set, 22)
	for _, id := range []string{"npm", "crates-io", "go-proxy", "gitlab"} {
		assert.Equal(t, TierOverlay, set[id].Tier, id)
	}
	assert.Equal(t, TierUpstream, set["pypi"].Tier)
}
