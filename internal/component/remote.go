package component

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/fsutil"
)

// Remote registry cache layout under <cache>/components/:
//
//	current          the tag of the last successful refresh
//	<tag>/*.yaml     verified component files
//	<tag>/rejected.txt  files rejected by the last refresh, "<path>\t<reason>"
const (
	remoteDir     = "components"
	currentFile   = "current"
	rejectedFile  = "rejected.txt"
	indexFileName = "index.yaml"
	maxFileBytes  = 1 << 20
)

// Index is the remote registry index (data-model "Component registry index").
type Index struct {
	SchemaVersion int         `yaml:"schema_version"`
	Release       string      `yaml:"release"`
	Files         []IndexFile `yaml:"files"`
}

// IndexFile is one component file with its checksum.
type IndexFile struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

// RefreshReport describes one refresh.
type RefreshReport struct {
	Tag      string   `json:"tag"`
	Dir      string   `json:"dir"`
	Accepted []string `json:"accepted"`
	Rejected []string `json:"rejected"`
	// Reasons maps each rejected path to why it was rejected.
	Reasons map[string]string `json:"reasons,omitempty"`
	Kept    []string          `json:"kept"`
	// Installed is false when a download failed and the cache was left
	// unchanged.
	Installed bool `json:"installed"`
}

// ErrInvalidTag means a registry tag that cannot name a cache directory or
// a URL path element.
var ErrInvalidTag = errors.New("invalid registry tag")

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidTag reports whether tag can be used as a registry release tag: a
// plain name that is not the cache's active-tag file.
func ValidTag(tag string) bool {
	return tagPattern.MatchString(tag) && tag != currentFile
}

// ErrIncomplete means at least one listed file could not be downloaded; the
// cache and the active tag are left unchanged.
var ErrIncomplete = errors.New("registry refresh incomplete")

// errTooLarge marks a registry file above maxFileBytes.
var errTooLarge = errors.New("larger than 1 MiB")

// Rejection is one file the last refresh rejected.
type Rejection struct {
	Path   string
	Reason string
}

// Refresh fetches <baseURL>/<tag>/index.yaml and every listed file, verifies
// each file's sha256 and installs the verified files into the cache. A file
// whose content is rejected (checksum mismatch, larger than 1 MiB, not a
// valid component) is listed with its reason; the previously cached copy of
// that file is kept when present (FR-017). A missing or unreadable index, a
// file that cannot be downloaded, or a cancelled context leaves the cache and
// the active tag untouched; a download failure returns the report together
// with ErrIncomplete.
func Refresh(ctx context.Context, client *http.Client, baseURL, tag, cacheDir string) (*RefreshReport, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if !ValidTag(tag) {
		return nil, fmt.Errorf("%w %q", ErrInvalidTag, tag)
	}
	root := strings.TrimSuffix(baseURL, "/") + "/" + tag + "/"
	raw, err := fetch(ctx, client, root+indexFileName)
	if err != nil {
		return nil, fmt.Errorf("fetch registry index: %w", err)
	}
	var idx Index
	if err := yaml.UnmarshalWithOptions(raw, &idx, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse registry index: %s", yaml.FormatError(err, false, false))
	}
	if idx.SchemaVersion != 1 {
		return nil, fmt.Errorf("registry index schema_version %d is not supported (want 1)", idx.SchemaVersion)
	}

	base := filepath.Join(cacheDir, remoteDir)
	dest := filepath.Join(base, tag)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	staging, err := os.MkdirTemp(base, ".refresh-*")
	if err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	report := &RefreshReport{Tag: tag, Dir: dest, Accepted: []string{}, Rejected: []string{}, Kept: []string{}}
	incomplete := false
	for _, f := range idx.Files {
		if !safeRelPath(f.Path) {
			return nil, fmt.Errorf("registry index lists unsafe path %q", f.Path)
		}
		data, err := fetch(ctx, client, root+f.Path)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var reason string
		switch {
		case errors.Is(err, errTooLarge):
			reason = errTooLarge.Error()
		case err != nil:
			reason = "download failed: " + err.Error()
			incomplete = true
		case checksum(data) != strings.ToLower(f.SHA256):
			reason = "checksum mismatch"
		default:
			if _, perr := Parse(data, f.Path, TierRegistry); perr != nil {
				reason = "invalid component: " + perr.Error()
			}
		}
		if reason == "" {
			if err := writeStaged(staging, f.Path, data); err != nil {
				return nil, err
			}
			report.Accepted = append(report.Accepted, f.Path)
			continue
		}
		report.Rejected = append(report.Rejected, f.Path)
		if report.Reasons == nil {
			report.Reasons = map[string]string{}
		}
		report.Reasons[f.Path] = oneLine(reason)
		if prev, rerr := os.ReadFile(filepath.Join(dest, filepath.Base(f.Path))); rerr == nil {
			if err := writeStaged(staging, f.Path, prev); err != nil {
				return nil, err
			}
			report.Kept = append(report.Kept, f.Path)
		}
	}
	sort.Strings(report.Rejected)
	if incomplete {
		// Nothing was installed: only the rejections are meaningful.
		report.Accepted, report.Kept = []string{}, []string{}
		return report, fmt.Errorf("%w: a listed file could not be downloaded; the cache and the active tag were left unchanged", ErrIncomplete)
	}
	if len(report.Rejected) > 0 {
		var b strings.Builder
		for _, r := range report.Rejected {
			b.WriteString(r + "\t" + report.Reasons[r] + "\n")
		}
		lines := b.String()
		if err := os.WriteFile(filepath.Join(staging, rejectedFile), []byte(lines), 0o644); err != nil {
			return nil, err
		}
	}
	if err := install(staging, dest); err != nil {
		return nil, err
	}
	if err := fsutil.AtomicWriteFile(filepath.Join(base, currentFile), []byte(tag+"\n"), 0o644); err != nil {
		return nil, err
	}
	report.Installed = true
	return report, nil
}

// ActiveTag returns the tag of the last refresh, or pin when the cache has
// never been refreshed.
func ActiveTag(cacheDir, pin string) string {
	data, err := os.ReadFile(filepath.Join(cacheDir, remoteDir, currentFile))
	if err != nil {
		return pin
	}
	if tag := strings.TrimSpace(string(data)); tag != "" {
		return tag
	}
	return pin
}

// LoadCached returns the cached registry tier for tag, or nil when it has
// never been refreshed, plus the files the last refresh rejected.
func LoadCached(cacheDir, tag string) (fs.FS, []Rejection, error) {
	dir := filepath.Join(cacheDir, remoteDir, tag)
	fi, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !fi.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", dir)
	}
	var rejected []Rejection
	if data, err := os.ReadFile(filepath.Join(dir, rejectedFile)); err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			p, reason, ok := strings.Cut(line, "\t")
			if !ok || reason == "" {
				reason = "checksum mismatch"
			}
			rejected = append(rejected, Rejection{Path: p, Reason: reason})
		}
	}
	return os.DirFS(dir), rejected, nil
}

// CachePrefix is the SourceFile prefix for registry tier components.
func CachePrefix(tag string) string { return path.Join("registry", tag) }

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		return nil, errTooLarge
	}
	return data, nil
}

// install replaces dest with staging. The old directory is moved aside first
// and restored when the rename fails, so dest never goes missing.
func install(staging, dest string) error {
	backup := ""
	if _, err := os.Lstat(dest); err == nil {
		backup = staging + ".old"
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("replace cache %s: %w", dest, err)
		}
	}
	if err := os.Rename(staging, dest); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dest)
		}
		return fmt.Errorf("install cache %s: %w", dest, err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// oneLine keeps a rejection reason on one line of rejected.txt.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func safeRelPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "/") {
		return false
	}
	ext := path.Ext(p)
	return p != indexFileName && (ext == ".yaml" || ext == ".yml") && path.Clean(p) == p
}

func writeStaged(staging, rel string, data []byte) error {
	// rel passed safeRelPath: a single file name without separators.
	target := filepath.Join(staging, filepath.Base(rel))
	if err := os.WriteFile(target, data, 0o644); err != nil { // #nosec G703 -- validated file name
		return fmt.Errorf("stage %s: %w", rel, err)
	}
	return nil
}
