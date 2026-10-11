package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/engine"
)

// imageServing returns a fake engine whose export run prints the files of
// src the way the export script prints /usr/share/sandcast/profiles.
func imageServing(t *testing.T, src string) *engine.Fake {
	t.Helper()
	var out strings.Builder
	for _, name := range dirNames(t, src) {
		data, err := os.ReadFile(filepath.Join(src, name))
		require.NoError(t, err)
		fmt.Fprintf(&out, "=== %s\n%s\n", name, data)
	}
	return imageOutput(out.String())
}

// imageOutput returns a fake engine whose runs print out.
func imageOutput(out string) *engine.Fake {
	return &engine.Fake{RunFunc: func(engine.RunSpec) ([]byte, []byte, error) {
		return []byte(out), nil, nil
	}}
}

// snapshot reads every file of dir.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range dirNames(t, dir) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		files[name] = string(data)
	}
	return files
}

func TestExportFromImageReplacesTheSet(t *testing.T) {
	image := adaptedDir(t)
	eng := imageServing(t, image)

	t.Run("existing set", func(t *testing.T) {
		dir := adaptedDir(t)
		set, err := ExportFromImage(context.Background(), eng, "localhost/demo:latest", "", dir, true)
		require.NoError(t, err)
		assert.Len(t, set.Profiles, 2)
		assert.Equal(t, []string{"github-demo.yaml", "go-proxy-demo.yaml", "index.yaml"}, dirNames(t, dir))
		idx := readIndex(t, dir)
		assert.Equal(t, importDigest, idx.Image.Digest, "the image reference is kept")
		assert.NotContains(t, strings.Join(dirNames(t, filepath.Dir(dir)), " "), ".profiles", "no temporary directory is left behind")
	})

	t.Run("no set yet", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "openshell", build.ProfilesDirName)
		_, err := ExportFromImage(context.Background(), eng, "localhost/demo:latest", "", dir, false)
		require.NoError(t, err)
		assert.Equal(t, []string{"github.yaml", "go-proxy.yaml", "index.yaml"}, dirNames(t, dir))
	})
}

func TestExportFromImageRefusesWhileABuildRuns(t *testing.T) {
	dir := adaptedDir(t)
	before := snapshot(t, dir)
	eng := imageServing(t, adaptedDir(t))
	buildDir := filepath.Dir(dir)
	unlock, err := build.LockDir(buildDir)
	require.NoError(t, err)
	defer unlock()

	_, err = ExportFromImage(context.Background(), eng, "localhost/demo:latest", "", dir, true)
	require.ErrorIs(t, err, build.ErrBuildRunning)
	assert.ErrorContains(t, err, "for target openshell in "+buildDir+"; wait for it to finish")
	assert.Zero(t, eng.CountOp("run"), "no container runs while a build holds the directory")
	assert.Equal(t, before, snapshot(t, dir), "the set the build reads is left alone")
}

func TestExportFromImageKeepsTheOldSetWhenWritingFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := adaptedDir(t)
	before := snapshot(t, dir)
	eng := imageServing(t, adaptedDir(t))
	buildDir := filepath.Dir(dir)
	// The lock file exists, as after any build; then nothing new can be
	// created next to the profile set.
	unlock, err := build.LockDir(buildDir)
	require.NoError(t, err)
	unlock()
	require.NoError(t, os.Chmod(buildDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(buildDir, 0o755) })

	_, err = ExportFromImage(context.Background(), eng, "localhost/demo:latest", "", dir, true)
	require.Error(t, err)
	assert.Equal(t, before, snapshot(t, dir), "a failed write leaves the previous set complete")
}

func TestExportFromImageRejectsUnsafeFileNames(t *testing.T) {
	image := adaptedDir(t)
	var valid strings.Builder
	for _, name := range dirNames(t, image) {
		data, err := os.ReadFile(filepath.Join(image, name))
		require.NoError(t, err)
		fmt.Fprintf(&valid, "=== %s\n%s\n", name, data)
	}
	for _, name := range []string{"../escape.yaml", "sub/github.yaml", "notes.txt", "github.yml", ".yaml", ".."} {
		t.Run(name, func(t *testing.T) {
			dir := adaptedDir(t)
			before := snapshot(t, dir)
			eng := imageOutput(valid.String() + "=== " + name + "\nid: x\n")
			_, err := ExportFromImage(context.Background(), eng, "localhost/demo:latest", "", dir, false)
			require.Error(t, err)
			assert.ErrorContains(t, err, fmt.Sprintf("%q", name))
			assert.ErrorContains(t, err, "plain file name ending in .yaml")
			assert.Equal(t, before, snapshot(t, dir), "nothing is written")
		})
	}
}
