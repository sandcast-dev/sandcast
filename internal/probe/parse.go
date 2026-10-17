package probe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Line types of the probe output.
const (
	lineOS     = "os"
	linePkgMgr = "pkgmgr"
	lineUser   = "user"
	lineShells = "shells"
	lineCheck  = "check"
	factsLines = 4
)

// rawCheck is one check line of the probe output. Timeout is set when the
// check was stopped at the time limit, by timeout(1) or the script's
// watchdog.
type rawCheck struct {
	Key     string `json:"key"`
	Exit    int    `json:"exit"`
	Path    string `json:"path"`
	Out     string `json:"out"`
	Timeout bool   `json:"timeout"`
}

// errNoProbeLines is the Parse error for an output without any probe line.
var errNoProbeLines = errors.New("probe output has no probe lines")

type pkgMgrLine struct {
	Name string `json:"name"`
}

type shellsLine struct {
	List []string `json:"list"`
}

// Parse reads the output of a probe script. facts is nil when the output has
// no facts lines (a partial probe); when one facts line is present, all four
// must be. Lines that do not start with "{" are ignored, unknown line types
// too. A malformed JSON line, a duplicate line or an output without any probe
// line is an error.
func Parse(stdout []byte) (*Facts, []rawCheck, error) {
	var (
		facts  Facts
		seen   = map[string]bool{}
		checks []rawCheck
		keys   = map[string]bool{}
	)
	for i, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			return nil, nil, fmt.Errorf("probe output line %d: %w", i+1, err)
		}
		var err error
		switch head.Type {
		case lineOS:
			err = json.Unmarshal(line, &facts.OS)
		case linePkgMgr:
			var pm pkgMgrLine
			err = json.Unmarshal(line, &pm)
			facts.PackageManager = pm.Name
		case lineUser:
			err = json.Unmarshal(line, &facts.User)
		case lineShells:
			var sh shellsLine
			err = json.Unmarshal(line, &sh)
			facts.Shells = dedupe(sh.List)
		case lineCheck:
			var c rawCheck
			if c, err = parseCheck(line, keys); err == nil {
				checks = append(checks, c)
			}
		default:
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("probe output line %d (%s): %w", i+1, head.Type, err)
		}
		if head.Type != lineCheck {
			if seen[head.Type] {
				return nil, nil, fmt.Errorf("probe output line %d: duplicate %s line", i+1, head.Type)
			}
			seen[head.Type] = true
		}
	}
	if len(seen) == 0 && len(checks) == 0 {
		return nil, nil, errNoProbeLines
	}
	if len(seen) == 0 {
		return nil, checks, nil
	}
	if len(seen) != factsLines {
		var missing []string
		for _, t := range []string{lineOS, linePkgMgr, lineUser, lineShells} {
			if !seen[t] {
				missing = append(missing, t)
			}
		}
		return nil, nil, fmt.Errorf("probe output is incomplete: no %s line", strings.Join(missing, ", "))
	}
	if facts.PackageManager == "" {
		facts.PackageManager = PackageManagerNone
	}
	return &facts, checks, nil
}

// parseCheck decodes one check line and records its key in keys.
func parseCheck(line []byte, keys map[string]bool) (rawCheck, error) {
	var c rawCheck
	if err := json.Unmarshal(line, &c); err != nil {
		return c, err
	}
	switch {
	case c.Key == "":
		return c, errors.New("check line without key")
	case keys[c.Key]:
		return c, fmt.Errorf("duplicate check line for %s", c.Key)
	}
	keys[c.Key] = true
	return c, nil
}

// dedupe drops empty and repeated entries and keeps the first occurrence.
func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
