package sandcast

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedDirectoriesAreNonEmpty(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"components":        Components,
		"profiles/upstream": UpstreamProfiles,
		"profiles/overlay":  OverlayProfiles,
		"policy":            Policy,
		"skills":            Skills,
	} {
		entries, err := fs.ReadDir(fsys, ".")
		require.NoError(t, err, name)
		assert.NotEmpty(t, entries, name)
	}
}

func TestEmbeddedAssets(t *testing.T) {
	_, err := fs.Stat(BaseImages, "base-images.yaml")
	require.NoError(t, err)
	_, err = fs.Stat(Policy, "default-v0.1.3.yaml")
	require.NoError(t, err)
	_, err = fs.Stat(UpstreamProfiles, "github.yaml")
	require.NoError(t, err)
	assert.NotEqual(t, "unknown", SkillsVersion())
}
