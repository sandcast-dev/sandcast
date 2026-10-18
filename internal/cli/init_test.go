package cli

import (
	"os"
	"path/filepath"
	"strings"
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

// TestInitRemoteGatewayFlags covers FR-015: --image (with a tag split off),
// --platform and --push go into the manifest without a note; without
// --image the localhost/ default comes with the note.
func TestInitRemoteGatewayFlags(t *testing.T) {
	ta := newTestApp(t)
	require.Equal(t, 0, ta.run("init", "--name", "demo"))
	assert.Contains(t, ta.stdout.String(), "note: targets.openshell.image is localhost/demo; this works only with a local OpenShell gateway that shares this engine's image storage. For a remote gateway, rerun with --force --image <registry>/demo --platform <os/arch> --push.\n")

	ta.stdout.Reset()
	require.Equal(t, 0, ta.run("init", "--name", "demo", "--force", "--image", "quay.io/rhuss/demo:v2", "--platform", "linux/amd64", "--push"), ta.stderr.String())
	assert.NotContains(t, ta.stdout.String(), "note:")
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "manifest.yaml"))
	require.NoError(t, err)
	m := string(data)
	assert.Contains(t, m, "platform: linux/amd64")
	assert.Contains(t, m, "image: quay.io/rhuss/demo\n")
	assert.Contains(t, m, "tag: v2")
	assert.Contains(t, m, "push: true")
	require.Equal(t, 0, ta.run("manifest", "validate"), ta.stdout.String())

	ta.stdout.Reset()
	require.Equal(t, 0, ta.run("init", "--name", "demo", "--force", "--image", "localhost:5000/demo/pushdemo", "--target", "container"))
	assert.NotContains(t, ta.stdout.String(), "note:")
	data, err = os.ReadFile(filepath.Join(ta.dir, ".sandcast", "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "image: localhost:5000/demo/pushdemo\n")
	assert.NotContains(t, string(data), "tag:")

	ta.stdout.Reset()
	require.Equal(t, 0, ta.run("init", "--name", "demo", "--force", "--target", "container"))
	assert.NotContains(t, ta.stdout.String(), "note:", "the note concerns the OpenShell target only")

	assert.Equal(t, ExitUsage, ta.run("init", "--force", "--name", "demo", "--platform", "linux/s390x"))
	assert.Equal(t, ExitUsage, ta.run("init", "--force", "--name", "demo", "--image", "quay.io/x/y@sha256:"+strings.Repeat("a", 64)))
	assert.Equal(t, ExitUsage, ta.run("init", "--force", "--name", "demo", "--image", "Bad Image"))
}
