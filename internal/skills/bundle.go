package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast"
)

// Prefix is prepended to installed skill directory names, so the skills
// are invoked as /sandcast-capture and /sandcast-build (R-16).
const Prefix = "sandcast-"

// EntryFile is the skill entry file in the portable bundle.
const EntryFile = "SKILL.md"

// Frontmatter is the YAML header of a SKILL.md.
type Frontmatter struct {
	Name            string `yaml:"name"`
	Description     string `yaml:"description"`
	SandcastVersion string `yaml:"sandcast_version"`
}

// Skill is one skill of the embedded bundle.
type Skill struct {
	// Dir is the bundle directory name, e.g. capture.
	Dir string
	// Meta is the parsed frontmatter.
	Meta Frontmatter
}

// Bundle is the embedded skill bundle.
type Bundle struct {
	FS      fs.FS
	Version string
	Skills  []Skill
}

// Embedded returns the bundle compiled into the binary.
func Embedded() (*Bundle, error) {
	return Load(sandcast.Skills)
}

// Load reads a bundle: VERSION and every <dir>/SKILL.md.
func Load(fsys fs.FS) (*Bundle, error) {
	v, err := fs.ReadFile(fsys, "VERSION")
	if err != nil {
		return nil, fmt.Errorf("skill bundle VERSION: %w", err)
	}
	b := &Bundle{FS: fsys, Version: strings.TrimSpace(string(v))}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name()+"/"+EntryFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		meta, err := ParseFrontmatter(data)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", e.Name(), err)
		}
		b.Skills = append(b.Skills, Skill{Dir: e.Name(), Meta: meta})
	}
	sort.Slice(b.Skills, func(i, j int) bool { return b.Skills[i].Dir < b.Skills[j].Dir })
	return b, nil
}

// ParseFrontmatter reads the YAML block between the leading "---" lines.
func ParseFrontmatter(data []byte) (Frontmatter, error) {
	var fm Frontmatter
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return fm, errors.New("SKILL.md has no frontmatter")
	}
	body, _, ok := strings.Cut(text[4:], "\n---")
	if !ok {
		return fm, errors.New("SKILL.md frontmatter is not terminated")
	}
	if err := yaml.Unmarshal([]byte(body), &fm); err != nil {
		return fm, fmt.Errorf("parse frontmatter: %w", err)
	}
	return fm, nil
}

// InstalledName is the directory name of a bundle skill once installed.
func InstalledName(dir string) string { return Prefix + dir }
