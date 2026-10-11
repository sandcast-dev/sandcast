package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rhuss/sandcast/internal/fsutil"
)

// Fragment statuses.
const (
	FragmentActive     = "active"
	FragmentSuperseded = "superseded"
)

// FragmentExt is the stored fragment file extension.
const FragmentExt = ".containerfile"

const fragmentHeaderPrefix = "# sandcast-fragment:"

var (
	fragmentKeyRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	headerFieldRe = regexp.MustCompile(`(\w+)=(\S+)`)
	instructionRe = regexp.MustCompile(`^([A-Za-z]+)(\s|$)`)
	heredocRe     = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)
	// allowedInstructions are the Containerfile instructions a fragment may
	// use (the build skill's list). FROM is refused because a fragment is a
	// snippet, not a stage; ADD fetches URLs without a checksum and SHELL is
	// ignored by podman for OCI images, so both are refused too.
	allowedInstructions = map[string]bool{
		"RUN": true, "COPY": true, "ENV": true, "USER": true,
		"WORKDIR": true, "ARG": true, "LABEL": true,
	}
)

// Fragment is a stored fragment file (.sandcast/fragments/<key>.containerfile).
type Fragment struct {
	Key     string `json:"key" yaml:"key"`
	Path    string `json:"path" yaml:"path"`
	Written string `json:"written" yaml:"written"`
	Status  string `json:"status" yaml:"status"`
	Body    string `json:"-" yaml:"-"`
}

// FragmentPath returns <dir>/<key>.containerfile.
func FragmentPath(dir, key string) string { return filepath.Join(dir, key+FragmentExt) }

// ParseFragment splits a stored fragment into header fields and body.
func ParseFragment(key, path string, data []byte) (*Fragment, error) {
	text := string(data)
	first, rest, _ := strings.Cut(text, "\n")
	f := &Fragment{Key: key, Path: path, Status: FragmentActive, Body: text}
	if strings.HasPrefix(first, fragmentHeaderPrefix) {
		f.Body = rest
		for _, m := range headerFieldRe.FindAllStringSubmatch(first, -1) {
			switch m[1] {
			case "key":
				if m[2] != key {
					return nil, fmt.Errorf("%s: header key %q does not match the file name", path, m[2])
				}
			case "written":
				f.Written = m[2]
			case "status":
				f.Status = m[2]
			}
		}
	}
	if f.Status != FragmentActive && f.Status != FragmentSuperseded {
		return nil, fmt.Errorf("%s: unknown status %q", path, f.Status)
	}
	return f, nil
}

// ListFragments returns every stored fragment in dir sorted by key. A
// missing directory is an empty list.
func ListFragments(dir string) ([]*Fragment, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Fragment
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, FragmentExt) {
			continue
		}
		key := strings.TrimSuffix(name, FragmentExt)
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f, err := ParseFragment(key, path, data)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// ValidateFragment checks that body is a Containerfile snippet: known
// instructions only, no FROM, not empty, and not ending as root.
func ValidateFragment(body string) error {
	var problems []string
	lastUser := ""
	instructions := 0
	heredoc := ""
	continued := false
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if heredoc != "" {
			if line == heredoc {
				heredoc = ""
			}
			continue
		}
		if continued {
			// Comment and blank lines inside a continued instruction do not
			// end it.
			if line != "" && !strings.HasPrefix(line, "#") {
				continued = strings.HasSuffix(line, "\\")
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := instructionRe.FindStringSubmatch(line)
		word := ""
		if m != nil {
			word = strings.ToUpper(m[1])
		}
		switch {
		case word == "FROM":
			problems = append(problems, fmt.Sprintf("line %d: FROM is not allowed in a fragment", i+1))
		case !allowedInstructions[word]:
			problems = append(problems, fmt.Sprintf("line %d: %q is not a Containerfile instruction a fragment may use", i+1, firstWord(line)))
		default:
			instructions++
			if word == "USER" {
				lastUser = strings.TrimSpace(line[len(m[1]):])
				if lastUser == "" {
					problems = append(problems, fmt.Sprintf("line %d: USER needs a user name", i+1))
				}
			}
		}
		if hm := heredocRe.FindStringSubmatch(line); hm != nil && (word == "RUN" || word == "COPY") {
			heredoc = hm[1]
			continue
		}
		continued = strings.HasSuffix(line, "\\")
	}
	if heredoc != "" {
		problems = append(problems, fmt.Sprintf("heredoc %s is not terminated", heredoc))
	}
	if instructions == 0 {
		problems = append(problems, "the fragment has no instructions")
	}
	if u := strings.Fields(lastUser); len(u) > 0 && (u[0] == "root" || u[0] == "0" || strings.HasPrefix(u[0], "root:") || strings.HasPrefix(u[0], "0:")) {
		problems = append(problems, "the fragment ends as USER root; switch back to the final user after system changes")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid fragment:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// WriteFragment validates body and stores it as an active fragment for key,
// replacing an earlier version.
func WriteFragment(dir, key, body string, now time.Time) (string, error) {
	if !fragmentKeyRe.MatchString(key) {
		return "", fmt.Errorf("fragment key %q must be a kebab-case component key", key)
	}
	body = strings.TrimRight(stripHeader(body), "\n") + "\n"
	if err := ValidateFragment(body); err != nil {
		return "", err
	}
	path := FragmentPath(dir, key)
	header := fmt.Sprintf("%s key=%s written=%s status=%s\n", fragmentHeaderPrefix, key, now.UTC().Format("2006-01-02"), FragmentActive)
	if err := fsutil.AtomicWriteFile(path, []byte(header+body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// MarkSuperseded rewrites the header of key's fragment with
// status=superseded, keeping the body for reference.
func MarkSuperseded(dir, key string) error {
	path := FragmentPath(dir, key)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := ParseFragment(key, path, data)
	if err != nil {
		return err
	}
	written := f.Written
	if written == "" {
		written = "unknown"
	}
	header := fmt.Sprintf("%s key=%s written=%s status=%s\n", fragmentHeaderPrefix, key, written, FragmentSuperseded)
	return fsutil.AtomicWriteFile(path, []byte(header+f.Body), 0o644)
}

func stripHeader(body string) string {
	if first, rest, ok := strings.Cut(body, "\n"); ok && strings.HasPrefix(first, fragmentHeaderPrefix) {
		return rest
	}
	return body
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}
