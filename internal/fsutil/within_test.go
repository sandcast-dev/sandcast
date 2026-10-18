package fsutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithin(t *testing.T) {
	root := filepath.FromSlash("/home/u")
	for _, tc := range []struct {
		p              string
		within, strict bool
	}{
		{"/home/u", true, false},
		{"/home/u/", true, false},
		{"/home/u/.gitconfig", true, true},
		{"/home/u/a/b", true, true},
		{"/home/u/..x", true, true},
		{"/home/u/a/../b", true, true},
		{"/home/u/../v", false, false},
		{"/home", false, false},
		{"/home/user", false, false},
		{"/home/u2/x", false, false},
		{"/etc/passwd", false, false},
		{"relative", false, false},
	} {
		p := filepath.FromSlash(tc.p)
		assert.Equal(t, tc.within, Within(root, p), "Within(%s, %s)", root, p)
		assert.Equal(t, tc.strict, StrictlyWithin(root, p), "StrictlyWithin(%s, %s)", root, p)
	}
	assert.True(t, Within("/", "/etc"), "everything is within /")
	assert.True(t, Within("rel/dir", "rel/dir/x"), "two relative paths compare lexically")
}
