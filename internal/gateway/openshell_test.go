package gateway

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRunner struct {
	calls   [][]string
	answers map[string]string // first two args joined -> stdout
	err     error
}

func (f *fakeRunner) Run(_ context.Context, bin string, args []string) ([]byte, []byte, error) {
	f.calls = append(f.calls, append([]string{bin}, args...))
	if f.err != nil {
		return nil, []byte("boom"), f.err
	}
	key := strings.Join(args, " ")
	for prefix, out := range f.answers {
		if strings.Contains(key, prefix) {
			return []byte(out), nil, nil
		}
	}
	return nil, nil, nil
}

func TestCheckVersion(t *testing.T) {
	ctx := context.Background()
	ok := &Client{Runner: &fakeRunner{answers: map[string]string{"--version": "openshell 0.1.2\n"}}}
	require.NoError(t, ok.CheckVersion(ctx))

	old := &Client{Runner: &fakeRunner{answers: map[string]string{"--version": "openshell 0.1.3-dev.102\n"}}}
	err := old.CheckVersion(ctx)
	require.ErrorIs(t, err, ErrVersion)
	assert.Contains(t, err.Error(), "requires openshell v0.1.2")

	missing := &Client{Runner: &fakeRunner{err: &exec.Error{Name: "openshell", Err: exec.ErrNotFound}}}
	err = missing.CheckVersion(ctx)
	require.ErrorIs(t, err, ErrMissing)
	assert.Contains(t, err.Error(), "v0.1.2")
}

func TestListProfiles(t *testing.T) {
	r := &fakeRunner{answers: map[string]string{
		"profile list": `[{"id":"pypi","resource_version":1,"scope":"workspace","source":"custom"},{"id":"github","resource_version":3,"scope":"workspace","source":"custom"}]`,
	}}
	c := &Client{Runner: r, Extra: []string{"--gateway", "test"}}
	got, err := c.ListProfiles(context.Background(), ScopeWorkspace, "team")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "github", got[0].ID)
	assert.Equal(t, 3, got[0].ResourceVersion)
	assert.Equal(t, []string{"openshell", "--gateway", "test", "profile", "list", "-o", "json", "--workspace", "team"}, r.calls[0])
}

func TestImportExportUpdateArgs(t *testing.T) {
	r := &fakeRunner{}
	c := &Client{Runner: r}
	ctx := context.Background()
	require.NoError(t, c.Import(ctx, "/b/profiles", ScopePlatform, "ignored"))
	require.NoError(t, c.ImportFile(ctx, "/b/profiles/x.yaml", ScopeWorkspace, ""))
	_, err := c.Export(ctx, "github", ScopeWorkspace, "")
	require.NoError(t, err)
	require.NoError(t, c.Update(ctx, "github", "/b/profiles/github.yaml", ScopePlatform, ""))
	assert.Equal(t, [][]string{
		{"openshell", "profile", "import", "--from", "/b/profiles", "--global"},
		{"openshell", "profile", "import", "--file", "/b/profiles/x.yaml"},
		{"openshell", "profile", "export", "github", "-o", "yaml"},
		{"openshell", "profile", "update", "github", "--file", "/b/profiles/github.yaml", "--global"},
	}, r.calls)
}

func TestRunErrorIncludesStderr(t *testing.T) {
	c := &Client{Runner: &fakeRunner{err: errors.New("exit status 1")}}
	err := c.Import(context.Background(), "/x", ScopeWorkspace, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}
