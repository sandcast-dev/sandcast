package component

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type registryServer struct {
	files map[string]string // path under /<tag>/ -> body
}

func (r *registryServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, ok := r.files[req.URL.Path]
	if !ok {
		http.NotFound(w, req)
		return
	}
	_, _ = w.Write([]byte(body))
}

func TestRefreshGoodTamperedMissing(t *testing.T) {
	jq := comp("jq", "jq")
	yq := comp("yq", "yq")
	index := fmt.Sprintf("schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: %s}\n  - {path: yq.yaml, sha256: %s}\n", sum(jq), sum(yq))
	srv := &registryServer{files: map[string]string{
		"/v1/index.yaml": index,
		"/v1/jq.yaml":    jq,
		"/v1/yq.yaml":    yq,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cache := t.TempDir()
	ctx := context.Background()

	// Good refresh.
	rep, err := Refresh(ctx, ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)
	assert.Equal(t, []string{"jq.yaml", "yq.yaml"}, rep.Accepted)
	assert.Empty(t, rep.Rejected)
	assert.Equal(t, "v1", ActiveTag(cache, "v0"))
	fsys, rejected, err := LoadCached(cache, "v1")
	require.NoError(t, err)
	assert.Empty(t, rejected)
	comps, err := LoadDir(fsys, TierRegistry, CachePrefix("v1"))
	require.NoError(t, err)
	assert.Len(t, comps, 2)

	// Tampered yq: rejected, previous copy kept.
	srv.files["/v1/yq.yaml"] = comp("yq", "evil")
	rep, err = Refresh(ctx, ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)
	assert.Equal(t, []string{"yq.yaml"}, rep.Rejected)
	assert.Equal(t, map[string]string{"yq.yaml": "checksum mismatch"}, rep.Reasons)
	assert.Equal(t, []string{"yq.yaml"}, rep.Kept)
	kept, err := os.ReadFile(filepath.Join(cache, "components", "v1", "yq.yaml"))
	require.NoError(t, err)
	assert.Equal(t, yq, string(kept))
	_, rejected, err = LoadCached(cache, "v1")
	require.NoError(t, err)
	assert.Equal(t, []Rejection{{Path: "yq.yaml", Reason: "checksum mismatch"}}, rejected)

	// Missing index: error, cache untouched.
	delete(srv.files, "/v1/index.yaml")
	_, err = Refresh(ctx, ts.Client(), ts.URL, "v1", cache)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index")
	_, err = fs.Stat(os.DirFS(filepath.Join(cache, "components", "v1")), "jq.yaml")
	assert.NoError(t, err)
}

func TestRefreshRejectsOversizedFile(t *testing.T) {
	jq := comp("jq", "jq")
	big := strings.Repeat("#", maxFileBytes+1)
	index := "schema_version: 1\nrelease: v1\nfiles:\n" +
		"  - {path: jq.yaml, sha256: " + checksum([]byte(jq)) + "}\n" +
		"  - {path: big.yaml, sha256: " + checksum([]byte(big)) + "}\n"
	ts := httptest.NewServer(&registryServer{files: map[string]string{"/v1/index.yaml": index, "/v1/jq.yaml": jq, "/v1/big.yaml": big}})
	defer ts.Close()
	cache := t.TempDir()
	rep, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)
	assert.True(t, rep.Installed)
	assert.Equal(t, []string{"big.yaml"}, rep.Rejected)
	assert.Equal(t, "larger than 1 MiB", rep.Reasons["big.yaml"], "an oversized file is not reported as tampered")

	_, rejected, err := LoadCached(cache, "v1")
	require.NoError(t, err)
	assert.Equal(t, []Rejection{{Path: "big.yaml", Reason: "larger than 1 MiB"}}, rejected)
}

func TestRefreshDownloadFailureLeavesCacheUnchanged(t *testing.T) {
	jq := comp("jq", "jq")
	ctx := context.Background()
	srv := &registryServer{files: map[string]string{
		"/v1/index.yaml": "schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: " + checksum([]byte(jq)) + "}\n",
		"/v1/jq.yaml":    jq,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cache := t.TempDir()
	_, err := Refresh(ctx, ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)

	// v2 lists a file the server does not have: nothing changes.
	srv.files["/v2/index.yaml"] = "schema_version: 1\nrelease: v2\nfiles:\n" +
		"  - {path: jq.yaml, sha256: " + checksum([]byte(jq)) + "}\n" +
		"  - {path: gone.yaml, sha256: " + checksum([]byte("x")) + "}\n"
	srv.files["/v2/jq.yaml"] = jq
	rep, err := Refresh(ctx, ts.Client(), ts.URL, "v2", cache)
	require.ErrorIs(t, err, ErrIncomplete)
	require.NotNil(t, rep)
	assert.False(t, rep.Installed)
	assert.Empty(t, rep.Accepted, "nothing was installed")
	assert.Empty(t, rep.Kept)
	assert.Contains(t, rep.Reasons["gone.yaml"], "download failed")
	assert.Contains(t, rep.Reasons["gone.yaml"], "404")
	assert.Equal(t, "v1", ActiveTag(cache, "pin"), "the active tag is not switched")
	_, err = os.Stat(filepath.Join(cache, "components", "v2"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	entries, err := os.ReadDir(filepath.Join(cache, "components"))
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), ".refresh-"), "staging directory removed: %s", e.Name())
	}
}

func TestRefreshCancelledLeavesCacheUnchanged(t *testing.T) {
	jq := comp("jq", "jq")
	ctx, cancel := context.WithCancel(context.Background())
	srv := &registryServer{files: map[string]string{
		"/v1/index.yaml": "schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: " + checksum([]byte(jq)) + "}\n",
	}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/jq.yaml" {
			cancel()
		}
		srv.ServeHTTP(w, r)
	}))
	defer ts.Close()
	cache := t.TempDir()
	_, err := Refresh(ctx, ts.Client(), ts.URL, "v1", cache)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "pin", ActiveTag(cache, "pin"))
	_, err = os.Stat(filepath.Join(cache, "components", "v1"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoadCachedLegacyRejectedLines(t *testing.T) {
	cache := t.TempDir()
	dir := filepath.Join(cache, "components", "v1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rejected.txt"), []byte("yq.yaml\n"), 0o644))
	_, rejected, err := LoadCached(cache, "v1")
	require.NoError(t, err)
	assert.Equal(t, []Rejection{{Path: "yq.yaml", Reason: "checksum mismatch"}}, rejected)
}

func TestRefreshRejectsUnsafePaths(t *testing.T) {
	index := "schema_version: 1\nrelease: v1\nfiles:\n  - {path: ../escape.yaml, sha256: x}\n"
	ts := httptest.NewServer(&registryServer{files: map[string]string{"/v1/index.yaml": index}})
	defer ts.Close()
	_, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe path")

	_, err = Refresh(context.Background(), ts.Client(), ts.URL, "../x", t.TempDir())
	require.Error(t, err)
}

func TestLoadCachedMissing(t *testing.T) {
	fsys, rejected, err := LoadCached(t.TempDir(), "v9")
	require.NoError(t, err)
	assert.Nil(t, fsys)
	assert.Nil(t, rejected)
	assert.Equal(t, "v9", ActiveTag(t.TempDir(), "v9"))
}

func TestRefreshRejectsInvalidComponentKeepsPrevious(t *testing.T) {
	jq := comp("jq", "jq")
	bad := "key: Bad_Key\nname: x\n"
	srv := &registryServer{files: map[string]string{
		"/v1/index.yaml": "schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: " + sum(jq) + "}\n",
		"/v1/jq.yaml":    jq,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cache := t.TempDir()
	_, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)

	// The next release ships a schema-invalid jq.yaml with a matching checksum.
	srv.files["/v1/index.yaml"] = "schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: " + sum(bad) + "}\n"
	srv.files["/v1/jq.yaml"] = bad
	rep, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)
	assert.Equal(t, []string{"jq.yaml"}, rep.Rejected)
	assert.True(t, strings.HasPrefix(rep.Reasons["jq.yaml"], "invalid component"), rep.Reasons["jq.yaml"])
	assert.Equal(t, []string{"jq.yaml"}, rep.Kept)
	fsys, _, err := LoadCached(cache, "v1")
	require.NoError(t, err)
	comps, err := LoadDir(fsys, TierRegistry, CachePrefix("v1"))
	require.NoError(t, err, "the cache still loads")
	require.Len(t, comps, 1)
	assert.Equal(t, "jq", comps[0].Key)
}

// TestRefreshDropsInvalidPreviousCopy covers a cached copy that a rule
// added since it was cached rejects (bins must be bare command names): a
// rejected download does not put it back, so the cache still loads.
func TestRefreshDropsInvalidPreviousCopy(t *testing.T) {
	jq := comp("jq", "jq")
	srv := &registryServer{files: map[string]string{
		"/v1/index.yaml": "schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: " + sum(jq) + "}\n",
		"/v1/jq.yaml":    jq,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cache := t.TempDir()
	_, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)
	cached, err := filepath.Glob(filepath.Join(cache, "*", "*", "jq.yaml"))
	require.NoError(t, err)
	if len(cached) == 0 {
		cached, err = filepath.Glob(filepath.Join(cache, "*", "jq.yaml"))
		require.NoError(t, err)
	}
	require.Len(t, cached, 1)
	require.NoError(t, os.WriteFile(cached[0], []byte("key: jq\nname: jq\nprovides: [\"j q\"]\ninstaller: {mise: {tool: jq}}\ncheck: {command: jq --version}\n"), 0o644))

	bad := "key: Bad_Key\nname: x\n"
	srv.files["/v1/index.yaml"] = "schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: " + sum(bad) + "}\n"
	srv.files["/v1/jq.yaml"] = bad
	rep, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)
	assert.Equal(t, []string{"jq.yaml"}, rep.Rejected)
	assert.Contains(t, rep.Reasons["jq.yaml"], "the cached copy is dropped as well")
	assert.Empty(t, rep.Kept)
	fsys, _, err := LoadCached(cache, "v1")
	require.NoError(t, err)
	comps, err := LoadDir(fsys, TierRegistry, CachePrefix("v1"))
	require.NoError(t, err, "the cache still loads")
	assert.Empty(t, comps)
}

func TestValidTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"v0.1.0": true, "2026-10": true, "current": false, "..": false, "a/b": false, "#": false, "": false, "-x": false,
	} {
		assert.Equal(t, want, ValidTag(tag), tag)
	}
	_, err := Refresh(context.Background(), nil, "http://127.0.0.1:1", "current", t.TempDir())
	assert.ErrorIs(t, err, ErrInvalidTag)
}

func TestRefreshIncompleteReportsNothingKept(t *testing.T) {
	jq := comp("jq", "jq")
	yq := comp("yq", "yq")
	srv := &registryServer{files: map[string]string{
		"/v1/index.yaml": "schema_version: 1\nrelease: v1\nfiles:\n  - {path: jq.yaml, sha256: " + sum(jq) + "}\n  - {path: yq.yaml, sha256: " + sum(yq) + "}\n",
		"/v1/jq.yaml":    jq,
		"/v1/yq.yaml":    yq,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cache := t.TempDir()
	_, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", cache)
	require.NoError(t, err)

	// Same tag again: yq is tampered (a cached copy exists) and a listed
	// file is missing, so nothing is installed and nothing counts as kept.
	srv.files["/v1/yq.yaml"] = comp("yq", "evil")
	srv.files["/v1/index.yaml"] += "  - {path: gone.yaml, sha256: " + sum("x") + "}\n"
	rep, err := Refresh(context.Background(), ts.Client(), ts.URL, "v1", cache)
	require.ErrorIs(t, err, ErrIncomplete)
	assert.False(t, rep.Installed)
	assert.Empty(t, rep.Kept)
	assert.Empty(t, rep.Accepted)
}
