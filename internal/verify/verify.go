package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
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

// Conditions a check runs under (R-08, FR-010). ConditionImage runs with
// the image environment; the session conditions reproduce the cleared
// environment of an OpenShell session.
const (
	ConditionImage            = "image"
	ConditionSessionBashLogin = "session:bash-login"
	ConditionSessionBashInter = "session:bash-interactive"
	ConditionSessionZsh       = "session:zsh"
	sessionPrefix             = "session."
	sessionStartupKey         = "session.zsh.startup"
	sessionBashStartupKey     = "session.bash.startup"
	// bashNoTTYNoise are the lines bash -i prints to stderr when it runs
	// without a terminal; the bash startup check ignores them.
	bashNoTTYNoise = "-e 'cannot set terminal process group' -e 'no job control in this shell'"
	// zshNoTTYNoise is the note zsh -i prints when startup code enables the
	// line editor (fzf key bindings do) without a terminal; a terminal
	// session never shows it, so the zsh startup check ignores it.
	zshNoTTYNoise      = `-e "can't change option: zle"`
	sessionTerm        = "xterm-256color"
	sessionDefaultPath = "/usr/local/bin:/usr/bin:/bin"
)

// Result is the outcome of one component check.
type Result struct {
	ComponentKey string `yaml:"component_key" json:"component_key"`
	// Condition is ConditionImage or one of the session conditions.
	Condition string `yaml:"condition" json:"condition"`
	OK        bool   `yaml:"ok" json:"ok"`
	Status    string `yaml:"status" json:"status"`
	Version   string `yaml:"version,omitempty" json:"version,omitempty"`
	Output    string `yaml:"output" json:"output"`
}

// Requests turns the plan's components into check requests, sorted by key.
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
		reqs = append(reqs, probe.Request{
			Key:            c.Key,
			Command:        c.Check.Command,
			Bin:            bin,
			Selector:       c.Check.Version,
			ExpectExit:     c.Check.ExpectExit,
			StdoutContains: c.Check.StdoutContains,
			Hash:           checkHash(c.Check),
		})
	}
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Key < reqs[j].Key })
	return reqs
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

// SessionRequests returns the session-condition checks of an OpenShell plan
// (R-08, FR-010), sorted by key; none for the container target. Each
// component check runs again, unchanged and with the same expectations, in
// a login bash (bash -lc, as openshell sandbox exec runs) and in an
// interactive bash that reads it from stdin (as openshell sandbox connect
// runs, so the home rc files and PROMPT_COMMAND run first), both started
// with OpenShell's cleared environment. A component's binary therefore has
// to resolve through the session PATH and run with the session's MISE_*
// settings. With shell.program zsh, the checks also run in an interactive
// login zsh (zsh -il), as the switch hook starts it. The selected shell program's startup check (session.zsh.startup or
// session.bash.startup, an interactive shell in both cases) fails when the
// startup writes to stderr (apart from the notes a shell prints only
// because verify runs it without a terminal) or does not read the curated
// rc.
func SessionRequests(p *plan.Plan) []probe.Request {
	if p.Target != manifest.TargetOpenShell {
		return nil
	}
	env := sessionEnv(p.FinalUser)
	zsh := p.Shell.Program == "zsh"
	var reqs []probe.Request
	add := func(key, command string, c plan.Check, bin string) {
		c.Command = command
		reqs = append(reqs, probe.Request{
			Key:            key,
			Command:        command,
			Bin:            bin,
			Selector:       c.Version,
			ExpectExit:     c.ExpectExit,
			StdoutContains: c.StdoutContains,
			Hash:           checkHash(c),
		})
	}
	for _, c := range p.Components {
		if c.Check.Command == "" {
			continue
		}
		bin := ""
		if len(c.Bins) > 0 {
			bin = c.Bins[0]
		}
		check := c.Check.Command
		add(sessionPrefix+"bash-login."+c.Key, env+" bash -lc "+shell.Quote(check), c.Check, bin)
		// bash -i prints its prompts and job control notes to stderr; the
		// check's own stderr is kept through the group redirection.
		add(sessionPrefix+"bash-interactive."+c.Key,
			"printf '%s\\n' "+shell.Quote("{ "+check+"\n} 2>&1")+" | "+env+" bash -i 2>/dev/null", c.Check, bin)
		if zsh {
			add(sessionPrefix+"zsh."+c.Key, env+" zsh -il -c "+shell.Quote(check), c.Check, bin)
		}
	}
	probeRC := ":"
	if p.Shell.RCPath != "" {
		probeRC = `[ -n "${` + plan.CuratedRCLoadedVar + `-}" ] || echo "sandcast: the curated rc ` + p.Shell.RCPath + ` was not read" >&2`
	}
	switch p.Shell.Program {
	case "zsh":
		add(sessionStartupKey,
			`_err=$(`+env+` zsh -il -c `+shell.Quote(probeRC)+` 2>&1 >/dev/null </dev/null | grep -v `+zshNoTTYNoise+`); `+
				`if [ -n "$_err" ]; then printf '%s\n' "$_err"; exit 1; fi`, plan.Check{}, "zsh")
	case "bash":
		add(sessionBashStartupKey,
			`_err=$(`+env+` bash -i -c `+shell.Quote(probeRC)+` 2>&1 >/dev/null </dev/null | grep -v `+bashNoTTYNoise+`); `+
				`if [ -n "$_err" ]; then printf '%s\n' "$_err"; exit 1; fi`, plan.Check{}, "bash")
	}
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Key < reqs[j].Key })
	return reqs
}

// sessionEnv is the env -i prefix that reproduces the environment OpenShell
// gives a session: only HOME, USER, TERM and the default PATH.
func sessionEnv(u plan.FinalUser) string {
	return "env -i HOME=" + shell.QuoteIfNeeded(u.Home) + " USER=" + shell.QuoteIfNeeded(u.Name) +
		" TERM=" + sessionTerm + " PATH=" + sessionDefaultPath
}

// condition maps a request key to its condition.
func condition(key string) string {
	switch {
	case strings.HasPrefix(key, sessionPrefix+"bash-login."):
		return ConditionSessionBashLogin
	case strings.HasPrefix(key, sessionPrefix+"bash-interactive."), key == sessionBashStartupKey:
		return ConditionSessionBashInter
	case strings.HasPrefix(key, sessionPrefix+"zsh."):
		return ConditionSessionZsh
	}
	return ConditionImage
}

// IsSession reports whether r ran under session conditions.
func (r Result) IsSession() bool { return r.Condition != "" && r.Condition != ConditionImage }

// Run executes every component check of the plan inside image as the
// image's final user, under the container engine only (no gateway), and for
// the OpenShell target the session-condition checks in the same container
// run. It returns the full report; err wraps ErrChecksFailed when any check
// failed, session checks included.
func Run(ctx context.Context, eng engine.Engine, image, platform string, p *plan.Plan) (*Report, error) {
	reqs := append(Requests(p), SessionRequests(p)...)
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
			Condition:    condition(req.Key),
			OK:           c.Status == probe.StatusPresent,
			Status:       string(c.Status),
			Version:      c.Version,
			Output:       c.Output,
		}
		if r.Status == "" {
			r.Status = string(probe.StatusMissing)
		}
		report.Results = append(report.Results, r)
		if r.OK {
			report.Passed++
		} else {
			report.Failed++
			failed = append(failed, req.Key)
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
