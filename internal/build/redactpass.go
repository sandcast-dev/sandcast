package build

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/rhuss/sandcast/internal/redact"
)

// SecretError lists credential-shaped values found in build outputs.
type SecretError struct {
	Findings []string
}

func (e *SecretError) Error() string {
	return "credential-shaped values in build outputs (constitution IV):\n  " + strings.Join(e.Findings, "\n  ")
}

// RedactPass scans every file under dir (the Containerfiles, the plan, the
// adapted profiles, mise files, the shell configuration, harness files and
// build.log) and fails with the file and key path of every hit (R-18).
// Entries named in skip (top-level files or directories) are not scanned.
func RedactPass(dir string, skip ...string) error {
	var findings []string
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("redaction pass: %w", err)
	}
	defer func() { _ = root.Close() }()
	fsys := root.FS()
	err = fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		top, _, _ := strings.Cut(rel, "/")
		if rel != "." && slices.Contains(skip, top) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return err
		}
		var hits []redact.Hit
		if ext := path.Ext(rel); ext == ".yaml" || ext == ".yml" {
			hits, err = redact.ScanYAML(data)
			if err != nil {
				hits = redact.Scan(string(data))
			}
		} else {
			hits = redact.Scan(string(data))
		}
		for _, h := range hits {
			loc := rel
			if h.Path != "" {
				loc += " " + h.Path
			}
			if h.Line > 0 {
				loc += fmt.Sprintf(" (line %d)", h.Line)
			}
			findings = append(findings, fmt.Sprintf("%s: %s %s", loc, h.Kind, h.Excerpt))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("redaction pass: %w", err)
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		return &SecretError{Findings: findings}
	}
	return nil
}
