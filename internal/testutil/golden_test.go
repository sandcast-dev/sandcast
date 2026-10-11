package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Helper()               {}

func TestGoldenMatchAndMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.golden")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))

	ok := &recorder{TB: t}
	Golden(ok, path, []byte("hello\n"))
	assert.False(t, ok.failed)

	bad := &recorder{TB: t}
	Golden(bad, path, []byte("bye\n"))
	assert.True(t, bad.failed)
}

func TestGoldenUpdate(t *testing.T) {
	t.Setenv(UpdateEnv, "1")
	path := filepath.Join(t.TempDir(), "sub", "x.golden")
	Golden(t, path, []byte("new\n"))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(got))
}
