package probe

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rhuss/sandcast/internal/fsutil"
)

// FormatVersion is the probe cache format. It is part of the facts and
// checks directory names, so entries written by an older format (format 1
// recorded no binary paths and no home rc facts, format 2 no PATH, format 3
// no image environment paths and no hits of a component's other binaries)
// are never read. Bump it whenever the probe script records new facts or
// check fields.
const FormatVersion = 4

// Cache directory names below the sandcast cache directory.
const (
	cacheDirName  = "probe"
	hashPrefixLen = 12
)

var (
	factsDirName  = fmt.Sprintf("facts-v%d", FormatVersion)
	checksDirName = fmt.Sprintf("checks-v%d", FormatVersion)
)

var (
	digestPattern   = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)
	platformPattern = regexp.MustCompile(`^[a-z0-9]+(/[a-zA-Z0-9._-]+)+$`)
	keyPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hashPattern     = regexp.MustCompile(`^[0-9a-f]{12,}$`)
)

// Cache stores probe facts and component checks per image digest and
// platform (FR-012). Entries are keyed by digest and platform together
// because docker reports the index digest, which is the same for every
// architecture of a multi-platform image. Layout below Dir:
//
//	probe/facts-v<format>/<digest-hex>_<os>-<arch>.json
//	probe/checks-v<format>/<digest-hex>_<os>-<arch>/<component-key>-<check-hash[:12]>.json
type Cache struct {
	// Dir is the sandcast cache directory, see fsutil.CacheDir.
	Dir string
}

// DefaultCache returns the cache in the per-user sandcast cache directory.
func DefaultCache() (*Cache, error) {
	dir, err := fsutil.CacheDir()
	if err != nil {
		return nil, err
	}
	return &Cache{Dir: dir}, nil
}

// LookupFacts returns the cached facts of digest and platform. A missing,
// unreadable or corrupt entry is a miss.
func (c *Cache) LookupFacts(digest, platform string) (*Facts, bool) {
	path, err := c.factsPath(digest, platform)
	if err != nil {
		return nil, false
	}
	var f Facts
	if !readJSON(path, &f) || f.Digest != digest || f.Platform != platform {
		return nil, false
	}
	return &f, true
}

// StoreFacts writes the facts of digest and platform.
func (c *Cache) StoreFacts(digest, platform string, f Facts) error {
	path, err := c.factsPath(digest, platform)
	if err != nil {
		return err
	}
	f.Digest, f.Platform = digest, platform
	return writeJSON(path, f)
}

// LookupCheck returns the cached check of component key with check hash in
// digest and platform. A missing, unreadable or corrupt entry is a miss, and
// so is an entry for another check hash with the same prefix.
func (c *Cache) LookupCheck(digest, platform, key, hash string) (*CheckResult, bool) {
	path, err := c.checkPath(digest, platform, key, hash)
	if err != nil {
		return nil, false
	}
	var r CheckResult
	if !readJSON(path, &r) || r.ComponentKey != key || r.CheckHash != hash || !r.Status.Valid() {
		return nil, false
	}
	return &r, true
}

// StoreCheck writes the check r of digest and platform.
func (c *Cache) StoreCheck(digest, platform string, r CheckResult) error {
	path, err := c.checkPath(digest, platform, r.ComponentKey, r.CheckHash)
	if err != nil {
		return err
	}
	return writeJSON(path, r)
}

// Invalidate removes the facts and all checks of digest and platform. Other
// platforms of the same digest are kept.
func (c *Cache) Invalidate(digest, platform string) error {
	facts, err := c.factsPath(digest, platform)
	if err != nil {
		return err
	}
	checks, err := c.checksDir(digest, platform)
	if err != nil {
		return err
	}
	if err := os.Remove(facts); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("invalidate probe cache: %w", err)
	}
	if err := os.RemoveAll(checks); err != nil {
		return fmt.Errorf("invalidate probe cache: %w", err)
	}
	return nil
}

func (c *Cache) root() string { return filepath.Join(c.Dir, cacheDirName) }

func (c *Cache) factsPath(digest, platform string) (string, error) {
	name, err := entryName(digest, platform)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.root(), factsDirName, name+".json"), nil
}

func (c *Cache) checksDir(digest, platform string) (string, error) {
	name, err := entryName(digest, platform)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.root(), checksDirName, name), nil
}

func (c *Cache) checkPath(digest, platform, key, hash string) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", fmt.Errorf("probe cache: invalid component key %q", key)
	}
	if !hashPattern.MatchString(hash) {
		return "", fmt.Errorf("probe cache: invalid check hash %q for %s", hash, key)
	}
	dir, err := c.checksDir(digest, platform)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, key+"-"+hash[:hashPrefixLen]+".json"), nil
}

// entryName returns <digest-hex>_<os>-<arch>[-<variant>] after validating
// both parts, so that no engine output can escape the cache directory or
// produce an empty key.
func entryName(digest, platform string) (string, error) {
	m := digestPattern.FindStringSubmatch(digest)
	if m == nil {
		return "", fmt.Errorf("probe cache: invalid image digest %q", digest)
	}
	if !platformPattern.MatchString(platform) || strings.Contains(platform, "..") {
		return "", fmt.Errorf("probe cache: invalid platform %q", platform)
	}
	return m[1] + "_" + strings.ReplaceAll(platform, "/", "-"), nil
}

func readJSON(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := fsutil.AtomicWriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
