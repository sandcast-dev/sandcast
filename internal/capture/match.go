package capture

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rhuss/sandcast/internal/component"
)

// Proposal sources.
const (
	SourceProject = "project"
	SourceHost    = "host"
	SourceHarness = "harness"
	SourceAlways  = "always"
)

// MaxDepth limits how deep Match looks for ecosystem files.
const MaxDepth = 4

// alwaysSkipped are directories never searched for evidence.
var alwaysSkipped = []string{".git", ".sandcast", "node_modules", "vendor", ".venv", "venv", "target", "dist", "build", "__pycache__", ".tox", ".idea", ".vscode"}

// Proposal is one component capture proposes, with the evidence for it.
type Proposal struct {
	Key             string   `json:"key"`
	Name            string   `json:"name"`
	Source          string   `json:"source"`
	Evidence        []string `json:"evidence"`
	DetectedVersion string   `json:"detected_version,omitempty"`
}

// Available is a component capture can offer as an optional addition.
type Available struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Tier string `json:"tier"`
}

// Result is the output of Match (sandcast components match --json).
type Result struct {
	Project   string      `json:"project"`
	Proposals []Proposal  `json:"proposals"`
	Available []Available `json:"available"`
}

// Options control Match.
type Options struct {
	// Host enables host tool detection.
	Host bool
	// Harnesses are the selected harness names.
	Harnesses []string
	// LookPath finds host tools; defaults to exec.LookPath.
	LookPath func(string) (string, error)
	// Version returns the first output line of "<bin> --version"; defaults
	// to running the binary with a short timeout.
	Version func(ctx context.Context, bin string) string
}

// Match evaluates every component's match conditions against the project
// tree and the host (FR-034, FR-035). The project walk respects the root
// .gitignore and skips dependency and build directories.
func Match(ctx context.Context, project string, comps []*component.Component, o Options) (*Result, error) {
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Version == nil {
		o.Version = hostVersion
	}
	files, err := projectFiles(project)
	if err != nil {
		return nil, err
	}
	res := &Result{Project: project, Proposals: []Proposal{}, Available: []Available{}}
	for _, c := range comps {
		p, ok := matchOne(ctx, c, files, o)
		if ok {
			res.Proposals = append(res.Proposals, p)
			continue
		}
		res.Available = append(res.Available, Available{Key: c.Key, Name: c.Name, Tier: string(c.Tier)})
	}
	sort.Slice(res.Proposals, func(i, j int) bool { return res.Proposals[i].Key < res.Proposals[j].Key })
	sort.Slice(res.Available, func(i, j int) bool { return res.Available[i].Key < res.Available[j].Key })
	return res, nil
}

func matchOne(ctx context.Context, c *component.Component, files []string, o Options) (Proposal, bool) {
	p := Proposal{Key: c.Key, Name: c.Name, Evidence: []string{}}
	if c.Match == nil {
		return p, false
	}
	m := c.Match
	for _, h := range m.Harnesses {
		if slices.Contains(o.Harnesses, h) {
			p.Source = SourceHarness
			p.Evidence = append(p.Evidence, "harness "+h+" selected")
		}
	}
	for _, glob := range m.Files {
		for _, f := range files {
			if globMatch(glob, f) {
				if p.Source == "" {
					p.Source = SourceProject
				}
				p.Evidence = append(p.Evidence, f)
			}
		}
	}
	if o.Host {
		for _, tool := range m.HostTools {
			// Only bare names are looked up, and only an absolute result
			// from the PATH search is run: a name with a separator would
			// make LookPath return a path relative to the current directory.
			if strings.ContainsAny(tool, `/\`) {
				continue
			}
			bin, err := o.LookPath(tool)
			if err != nil || !filepath.IsAbs(bin) {
				continue
			}
			v := component.ParseVersion(o.Version(ctx, bin))
			if p.DetectedVersion == "" {
				p.DetectedVersion = v
			}
			if p.Source == "" {
				p.Source = SourceHost
			}
			ev := "host: " + bin
			if v != "" {
				ev += " (" + v + ")"
			}
			p.Evidence = append(p.Evidence, ev)
		}
	}
	if m.Always {
		if p.Source == "" {
			p.Source = SourceAlways
		}
		p.Evidence = append(p.Evidence, "always included")
	}
	return p, p.Source != ""
}

// globMatch matches a component glob against a project-relative path. A
// glob without a slash matches the file name at any depth.
func globMatch(glob, rel string) bool {
	if !strings.Contains(glob, "/") {
		ok, _ := path.Match(glob, path.Base(rel))
		return ok
	}
	ok, _ := path.Match(glob, rel)
	return ok
}

// projectFiles lists project-relative file paths (forward slashes, sorted)
// up to MaxDepth, honouring the root .gitignore.
func projectFiles(root string) ([]string, error) {
	ignore := readGitignore(filepath.Join(root, ".gitignore"))
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if slices.Contains(alwaysSkipped, d.Name()) || ignore.match(rel, true) || strings.Count(rel, "/") >= MaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !ignore.match(rel, false) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan project %s: %w", root, err)
	}
	sort.Strings(out)
	return out, nil
}

type gitignore []string

func readGitignore(path string) gitignore {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var pats gitignore
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		pats = append(pats, line)
	}
	return pats
}

// match implements the common .gitignore forms: name globs at any depth,
// anchored paths with a leading slash, directory-only patterns with a
// trailing slash, and leading "**/".
func (g gitignore) match(rel string, isDir bool) bool {
	for _, pat := range g {
		dirOnly := strings.HasSuffix(pat, "/")
		pat = strings.TrimSuffix(pat, "/")
		if dirOnly && !isDir {
			continue
		}
		pat = strings.TrimPrefix(pat, "**/")
		if strings.HasPrefix(pat, "/") || strings.Contains(pat, "/") {
			if ok, _ := path.Match(strings.TrimPrefix(pat, "/"), rel); ok {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, path.Base(rel)); ok {
			return true
		}
	}
	return false
}

func hostVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(first)
}
