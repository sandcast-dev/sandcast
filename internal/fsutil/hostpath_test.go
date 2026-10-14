package fsutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHostPath(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	project := filepath.FromSlash("/work/p")
	for _, tt := range []struct {
		in   string
		kind HostPathKind
		want string
	}{
		{"~", HostPathHome, home},
		{"~/.gitconfig", HostPathHome, filepath.Join(home, ".gitconfig")},
		{"~/.config/../.ssh/id", HostPathHome, filepath.Join(home, ".ssh/id")},
		{"/etc/hosts", HostPathAbsolute, filepath.FromSlash("/etc/hosts")},
		{"/work/p/./conf", HostPathAbsolute, filepath.Join(project, "conf")},
		{"conf/tool.conf", HostPathProject, filepath.Join(project, "conf/tool.conf")},
		{"./x", HostPathProject, filepath.Join(project, "x")},
	} {
		kind, got, err := ResolveHostPath(tt.in, home, project)
		require.NoError(t, err, tt.in)
		assert.Equal(t, tt.kind, kind, tt.in)
		assert.Equal(t, tt.want, got, tt.in)
	}

	for _, tt := range []struct{ in, home, project, want string }{
		{"", home, project, "empty"},
		{"~root/.ssh/id_rsa", home, project, "only ~/ names the home"},
		{"~/.gitconfig", "", project, "needs the home directory"},
		{"conf", home, "", "needs the project directory"},
	} {
		_, _, err := ResolveHostPath(tt.in, tt.home, tt.project)
		require.Error(t, err, tt.in)
		assert.Contains(t, err.Error(), tt.want, tt.in)
	}
	assert.Equal(t, "home", HostPathHome.String())
	assert.Equal(t, "project", HostPathProject.String())
	assert.Equal(t, "absolute", HostPathAbsolute.String())
}
