package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheLayout(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	hash := strings.Repeat("0123456789abcdef", 4)
	require.NoError(t, c.StoreFacts(testDigest, "linux/arm64", Facts{PackageManager: "apk"}))
	require.NoError(t, c.StoreCheck(testDigest, "linux/arm64", CheckResult{ComponentKey: "go", CheckHash: hash, Status: StatusPresent}))
	require.NoError(t, c.StoreFacts(testDigest, "linux/arm/v7", Facts{}))

	hex := strings.TrimPrefix(testDigest, "sha256:")
	assert.FileExists(t, filepath.Join(c.Dir, "probe", "facts-v4", hex+"_linux-arm64.json"))
	assert.FileExists(t, filepath.Join(c.Dir, "probe", "facts-v4", hex+"_linux-arm-v7.json"))
	assert.FileExists(t, filepath.Join(c.Dir, "probe", "checks-v4", hex+"_linux-arm64", "go-0123456789ab.json"))

	f, ok := c.LookupFacts(testDigest, "linux/arm64")
	require.True(t, ok)
	assert.Equal(t, Facts{Digest: testDigest, Platform: "linux/arm64", PackageManager: "apk"}, *f)
	r, ok := c.LookupCheck(testDigest, "linux/arm64", "go", hash)
	require.True(t, ok)
	assert.Equal(t, CheckResult{ComponentKey: "go", CheckHash: hash, Status: StatusPresent}, *r)
}

// TestCacheFormatVersion checks that entries of format 1 (directories
// without a version suffix) are never read, and current entries round-trip.
func TestCacheFormatVersion(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	hash := strings.Repeat("a", 64)
	hex := strings.TrimPrefix(testDigest, "sha256:")
	old := filepath.Join(c.Dir, "probe", "facts", hex+"_linux-arm64.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o755))
	require.NoError(t, os.WriteFile(old, []byte(`{"digest":"`+testDigest+`","platform":"linux/arm64","package_manager":"apk"}`), 0o644))
	oldCheck := filepath.Join(c.Dir, "probe", "checks", hex+"_linux-arm64", "go-"+hash[:12]+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(oldCheck), 0o755))
	require.NoError(t, os.WriteFile(oldCheck, []byte(`{"component_key":"go","check_hash":"`+hash+`","status":"present"}`), 0o644))

	_, ok := c.LookupFacts(testDigest, "linux/arm64")
	assert.False(t, ok, "format 1 facts must not be read")
	_, ok = c.LookupCheck(testDigest, "linux/arm64", "go", hash)
	assert.False(t, ok, "format 1 checks must not be read")

	env := []EnvVar{{Name: "VIRTUAL_ENV", Value: "/sandbox/.venv"}}
	bins := []BinHits{{Name: "go", Paths: []string{"/usr/bin/go"}, Real: []string{"/usr/lib/go/bin/go"}}, {Name: "gofmt"}}
	require.NoError(t, c.StoreFacts(testDigest, "linux/arm64", Facts{PackageManager: "apt-get", HomeRCResetsPath: []string{".bashrc"}, Env: env}))
	require.NoError(t, c.StoreCheck(testDigest, "linux/arm64", CheckResult{ComponentKey: "go", CheckHash: hash, Status: StatusPresent, Paths: []string{"/usr/bin/go"}, Bins: bins}))
	f, ok := c.LookupFacts(testDigest, "linux/arm64")
	require.True(t, ok)
	assert.Equal(t, "apt-get", f.PackageManager)
	assert.Equal(t, []string{".bashrc"}, f.HomeRCResetsPath)
	assert.Equal(t, env, f.Env)
	r, ok := c.LookupCheck(testDigest, "linux/arm64", "go", hash)
	require.True(t, ok)
	assert.Equal(t, []string{"/usr/bin/go"}, r.Paths)
	assert.Equal(t, bins, r.Bins)
	assert.Equal(t, 4, FormatVersion)
}

func TestCacheMisses(t *testing.T) {
	hash := strings.Repeat("a", 64)
	samePrefix := strings.Repeat("a", 12) + strings.Repeat("b", 52)
	tests := []struct {
		name     string
		platform string
		facts    string // replaces the stored facts file when set
		check    string // replaces the stored check file when set
		hitFacts bool
		hitCheck bool
	}{
		{name: "hit", hitFacts: true, hitCheck: true},
		{name: "other platform", platform: "linux/amd64"},
		{name: "corrupt files", facts: "{not json", check: " "},
		{
			name:  "entries for another digest and check hash",
			facts: `{"digest":"sha256:` + strings.Repeat("0", 64) + `","platform":"linux/arm64"}`,
			check: `{"component_key":"go","check_hash":"` + samePrefix + `","status":"present"}`,
		},
		{
			name:     "unknown status",
			check:    `{"component_key":"go","check_hash":"` + hash + `","status":"maybe"}`,
			hitFacts: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{Dir: t.TempDir()}
			require.NoError(t, c.StoreFacts(testDigest, testPlatform, Facts{}))
			require.NoError(t, c.StoreCheck(testDigest, testPlatform, CheckResult{ComponentKey: "go", CheckHash: hash, Status: StatusPresent}))
			if tt.facts != "" {
				path, err := c.factsPath(testDigest, testPlatform)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(tt.facts), 0o644))
			}
			if tt.check != "" {
				path, err := c.checkPath(testDigest, testPlatform, "go", hash)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(tt.check), 0o644))
			}
			platform := testPlatform
			if tt.platform != "" {
				platform = tt.platform
			}
			_, ok := c.LookupFacts(testDigest, platform)
			assert.Equal(t, tt.hitFacts, ok, "facts")
			_, ok = c.LookupCheck(testDigest, platform, "go", hash)
			assert.Equal(t, tt.hitCheck, ok, "check")
		})
	}
}

func TestCacheInvalidate(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	hash := strings.Repeat("c", 64)
	for _, p := range []string{"linux/arm64", "linux/amd64"} {
		require.NoError(t, c.StoreFacts(testDigest, p, Facts{}))
		require.NoError(t, c.StoreCheck(testDigest, p, CheckResult{ComponentKey: "go", CheckHash: hash, Status: StatusMissing}))
	}
	require.NoError(t, c.Invalidate(testDigest, "linux/arm64"))
	require.NoError(t, c.Invalidate(testDigest, "linux/arm64"), "idempotent")

	_, ok := c.LookupFacts(testDigest, "linux/arm64")
	assert.False(t, ok)
	_, ok = c.LookupCheck(testDigest, "linux/arm64", "go", hash)
	assert.False(t, ok)
	_, ok = c.LookupFacts(testDigest, "linux/amd64")
	assert.True(t, ok, "other platform kept")
	_, ok = c.LookupCheck(testDigest, "linux/amd64", "go", hash)
	assert.True(t, ok, "other platform kept")
}

func TestCacheRejectsUnsafeKeys(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	hash := strings.Repeat("d", 64)
	tests := []struct {
		name     string
		digest   string
		platform string
		key      string
		hash     string
		err      string
	}{
		{name: "empty digest", platform: "linux/arm64", key: "go", hash: hash, err: "invalid image digest"},
		{name: "short digest", digest: "sha256:abc", platform: "linux/arm64", key: "go", hash: hash, err: "invalid image digest"},
		{name: "empty platform", digest: testDigest, key: "go", hash: hash, err: "invalid platform"},
		{name: "traversal platform", digest: testDigest, platform: "linux/../x", key: "go", hash: hash, err: "invalid platform"},
		{name: "traversal key", digest: testDigest, platform: "linux/arm64", key: "../go", hash: hash, err: "invalid component key"},
		{name: "short hash", digest: testDigest, platform: "linux/arm64", key: "go", hash: "abc", err: "invalid check hash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.StoreCheck(tt.digest, tt.platform, CheckResult{ComponentKey: tt.key, CheckHash: tt.hash, Status: StatusPresent})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.err)
			_, ok := c.LookupCheck(tt.digest, tt.platform, tt.key, tt.hash)
			assert.False(t, ok)
		})
	}
	entries, err := os.ReadDir(c.Dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
