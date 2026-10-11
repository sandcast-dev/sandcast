package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInit(t *testing.T) {
	ta := newTestApp(t)
	require.Equal(t, 0, ta.run("init", "--name", "demo"))
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "base_image: nvidia-community-base")
	assert.Contains(t, string(data), "image: localhost/demo")
	gi, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "build/\n", string(gi))

	assert.Equal(t, ExitUsage, ta.run("init", "--name", "demo"), "existing manifest")
	require.Equal(t, 0, ta.run("init", "--name", "demo", "--force", "--target", "container"))
	data, err = os.ReadFile(filepath.Join(ta.dir, ".sandcast", "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "base_image: fedora-42")
	assert.Contains(t, string(data), "container:")

	assert.Equal(t, ExitUsage, ta.run("init", "--force", "--target", "k8s"))
	assert.Equal(t, ExitUsage, ta.run("init", "--force", "--name", "Bad Name"))
}
