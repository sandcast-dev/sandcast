package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/probe"
	"github.com/rhuss/sandcast/internal/redact"
	"github.com/rhuss/sandcast/internal/shell"
)

// FileName is the verify report in build/<target>/.
const FileName = "verify.yaml"

// ErrChecksFailed means at least one component check failed; the CLI exits 1
// after reporting every result (FR-030).
var ErrChecksFailed = errors.New("component checks failed")

// Report is build/<target>/verify.yaml (data-model "Verify result").
type Report struct {
	Image   ImageRef `yaml:"image" json:"image"`
	Results []Result `yaml:"results" json:"results"`
	Passed  int      `yaml:"passed" json:"passed"`
	Failed  int      `yaml:"failed" json:"failed"`
}

// ImageRef identifies the verified image.
type ImageRef struct {
	Reference string `yaml:"reference" json:"reference"`
	Digest    string `yaml:"digest" json:"digest"`
}

// Result is the outcome of one component check. Mode is empty for the
// check in the image environment and ModeSession for the same check in an
// OpenShell session environment.
type Result struct {
	ComponentKey string `yaml:"component_key" json:"component_key"`
	Mode         string `yaml:"mode,omitempty" json:"mode,omitempty"`
	OK           bool   `yaml:"ok" json:"ok"`
	Status       string `yaml:"status" json:"status"`
	Version      string `yaml:"version,omitempty" json:"version,omitempty"`
	Output       string `yaml:"output" json:"output"`
}

// Session checks (issue #11). OpenShell starts interactive sessions with a
// cleared environment and bash; a component that works with the image ENV
// can still be missing there.
const (
	// ModeSession marks a result checked in a session environment.
	ModeSession = "session"
	// SessionSuffix turns a component key into the probe key of its session
	// check. Component keys cannot contain a dot.
	SessionSuffix = "." + ModeSession
	// SessionPath is the PATH OpenShell gives a session.
	SessionPath = "/usr/local/bin:/usr/bin:/bin"
)

// Requests turns the plan's components into check requests, sorted by key:
// one per component with a check, plus, for the OpenShell target, a session
// check for every shell component and the shell program.
func Requests(p *plan.Plan) []probe.Request {
	reqs := make([]probe.Request, 0, len(p.Components))
	for _, c := range p.Components {
		if c.Check.Command == "" {
			continue
		}
		bin := ""
		if len(c.Bins) > 0 {
			bin = c.Bins[0]
		}
		reqs = append(reqs, request(c.Key, bin, c.Check))
	}
	reqs = append(reqs, sessionRequests(p)...)
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Key < reqs[j].Key })
	return reqs
}

func request(key, bin string, c plan.Check) probe.Request {
	return probe.Request{
		Key:            key,
		Command:        c.Command,
		Bin:            bin,
		Selector:       c.Version,
		ExpectExit:     c.ExpectExit,
		StdoutContains: c.StdoutContains,
		Hash:           checkHash(c),
	}
}

// sessionRequests runs the checks of the shell components and the shell
// program the way an interactive OpenShell session would: with only HOME,
// USER, TERM and the default PATH, in a login shell. With shell.program zsh
// that is zsh, which the session switches to from bash; the program's own
// check also requires the switch hook in the system bashrc.
func sessionRequests(p *plan.Plan) []probe.Request {
	if p.Target != manifest.TargetOpenShell {
		return nil
	}
	keys := slices.Clone(p.Shell.Components)
	if p.Shell.Program == "zsh" || p.Shell.Program == "bash" {
		keys = append(keys, p.Shell.Program)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	login := sessionShell(p)
	var reqs []probe.Request
	for _, k := range keys {
		c := p.Component(k)
		if c == nil || c.Check.Command == "" {
			continue
		}
		check := c.Check
		check.Command = fmt.Sprintf("env -i HOME=%s USER=%s TERM=dumb PATH=%s %s -lc %s",
			shell.QuoteIfNeeded(p.FinalUser.Home), shell.QuoteIfNeeded(p.FinalUser.Name), SessionPath, login, shell.Quote(c.Check.Command))
		if k == "zsh" && p.Shell.Program == "zsh" {
			check.Command = "grep -qs SANDCAST_NO_ZSH /etc/bash.bashrc /etc/bashrc && " + check.Command
		}
		reqs = append(reqs, request(k+SessionSuffix, "", check))
	}
	return reqs
}

// sessionShell is the shell an interactive OpenShell session ends up in:
// zsh after the switch for shell.program zsh, else the first of bash and sh
// that OpenShell finds.
func sessionShell(p *plan.Plan) string {
	if p.Shell.Program == "zsh" {
		return "/bin/zsh"
	}
	if p.Component("bash") != nil {
		return "/bin/bash"
	}
	for _, s := range p.ImageFacts.Shells {
		if strings.HasSuffix(s, "/bash") {
			return "/bin/bash"
		}
	}
	return "/bin/sh"
}

// checkHash identifies a check the same way the probe cache does: the
// command and its expected outcome.
func checkHash(c plan.Check) string {
	h := sha256.New()
	for _, s := range []string{c.Command, strconv.Itoa(c.ExpectExit), c.Version, c.StdoutContains} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Run executes every component check of the plan inside image as the
// image's final user, under the container engine only (no gateway). It
// returns the full report; err wraps ErrChecksFailed when any check failed.
func Run(ctx context.Context, eng engine.Engine, image, platform string, p *plan.Plan) (*Report, error) {
	reqs := Requests(p)
	checks, err := probe.RunChecks(ctx, eng, image, platform, reqs)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", image, err)
	}
	report := &Report{Image: ImageRef{Reference: image}, Results: []Result{}}
	if id, err := eng.ImageID(ctx, image); err == nil {
		if !strings.HasPrefix(id, "sha256:") {
			id = "sha256:" + id
		}
		report.Image.Digest = id
	}
	var failed []string
	for _, req := range reqs {
		c := checks[req.Key]
		r := Result{
			ComponentKey: req.Key,
			OK:           c.Status == probe.StatusPresent,
			Status:       string(c.Status),
			Version:      c.Version,
			Output:       c.Output,
		}
		label := req.Key
		if key, ok := strings.CutSuffix(req.Key, SessionSuffix); ok {
			r.ComponentKey, r.Mode = key, ModeSession
			label = key + " (" + ModeSession + ")"
		}
		if r.Status == "" {
			r.Status = string(probe.StatusMissing)
		}
		report.Results = append(report.Results, r)
		if r.OK {
			report.Passed++
		} else {
			report.Failed++
			failed = append(failed, label)
		}
	}
	if len(failed) > 0 {
		return report, fmt.Errorf("%w: %s", ErrChecksFailed, strings.Join(failed, ", "))
	}
	return report, nil
}

// Write stores the report as YAML.
func (r *Report) Write(path string) error {
	data, err := yaml.MarshalWithOptions(r,
		yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return fmt.Errorf("encode verify report: %w", err)
	}
	// Check output comes from the image; a credential in it is not written.
	if hits := redact.Scan(string(data)); len(hits) > 0 {
		return fmt.Errorf("%s not written: check output contains a credential-shaped value (%s at line %d)", filepath.Base(path), hits[0].Kind, hits[0].Line)
	}
	return fsutil.AtomicWriteFile(path, data, 0o644)
}
