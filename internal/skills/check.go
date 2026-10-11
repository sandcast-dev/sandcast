package skills

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/harness"
)

// Check statuses.
const (
	StatusOK       = "ok"
	StatusMissing  = "missing"
	StatusOutdated = "outdated"
	StatusNewer    = "newer"
)

// CheckResult is the version comparison for one skill.
type CheckResult struct {
	Skill            string `json:"skill"`
	Status           string `json:"status"`
	Path             string `json:"path,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	BundleVersion    string `json:"bundle_version"`
	Hint             string `json:"hint,omitempty"`
}

// Report is the result of Check.
type Report struct {
	Harness string        `json:"harness"`
	OK      bool          `json:"ok"`
	Results []CheckResult `json:"results"`
}

// Message summarizes a failed report: which side to update (FR-004).
func (r *Report) Message() string {
	var parts []string
	for _, c := range r.Results {
		if c.Status != StatusOK {
			parts = append(parts, fmt.Sprintf("%s: %s", c.Skill, c.Hint))
		}
	}
	return strings.Join(parts, "\n")
}

// Check compares the installed skills (project scope first, then user scope)
// with the embedded bundle version.
func Check(b *Bundle, a harness.Adapter, env harness.Env) (*Report, error) {
	rep := &Report{Harness: a.Name(), OK: true}
	var dirs []string
	for _, scope := range []harness.Scope{harness.ScopeProject, harness.ScopeUser} {
		if scope == harness.ScopeProject && env.Project == "" {
			continue
		}
		d, err := a.SkillInstallDir(scope, env)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, d)
	}
	install := fmt.Sprintf("run \"sandcast skills install --harness %s\"", a.Name())
	for _, s := range b.Skills {
		name := InstalledName(s.Dir)
		res := CheckResult{Skill: name, Status: StatusMissing, BundleVersion: b.Version,
			Hint: "not installed; " + install}
		for _, d := range dirs {
			target := filepath.Join(d, name)
			v, ok := installedVersion(target, a.SkillFileName())
			if !ok {
				continue
			}
			res.Path = target
			res.InstalledVersion = v
			switch c := component.Compare(v, b.Version); {
			case v == b.Version || c == 0 && v != "unknown":
				res.Status, res.Hint = StatusOK, ""
			case c < 0 || v == "unknown":
				res.Status = StatusOutdated
				res.Hint = fmt.Sprintf("installed skill %s is older than the sandcast binary (%s); %s to update the skill", v, b.Version, install)
			default:
				res.Status = StatusNewer
				res.Hint = fmt.Sprintf("installed skill %s is newer than the sandcast binary (%s); update the sandcast binary", v, b.Version)
			}
			break
		}
		if res.Status != StatusOK {
			rep.OK = false
		}
		rep.Results = append(rep.Results, res)
	}
	return rep, nil
}
