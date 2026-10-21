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

	"github.com/BurntSushi/toml"
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
	sessionMiseProjectKey     = "session.mise.project-config"
	// miseProjectDir is the project directory the mise project check
	// creates; miseProjectPin is the version it pins every image tool to.
	miseProjectDir = "/tmp/sandcast-check-project"
	miseProjectPin = "0.0.0-sandcast-check"
	// bashNoTTYNoise are the lines bash -i prints to stderr when it runs
	// without a terminal; the bash startup check ignores them.
	bashNoTTYNoise = "-e 'cannot set terminal process group' -e 'no job control in this shell'"
	// zshNoTTYNoise is the note zsh -i prints when startup code enables the
	// line editor (fzf key bindings do) without a terminal; a terminal
	// session never shows it, so the zsh startup check ignores it.
	zshNoTTYNoise = `-e "can't change option: zle"`
	sessionTerm   = "xterm-256color"
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
	// Notes name the binaries a session check skipped because they do not
	// resolve in the image environment either.
	Notes []string `yaml:"notes,omitempty" json:"notes,omitempty"`
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
// (spec 002 R-08, FR-010), sorted by key; none for the container target.
// Each component check runs again, unchanged and with the same
// expectations, in a login bash (bash -lc, as openshell sandbox exec runs)
// and in an interactive bash that reads it from stdin (as openshell sandbox
// connect runs), both started with OpenShell's cleared environment. The
// shell hooks of the image replace both before any file in the home is
// read (spec 003): the login bash by a bash without startup files that
// keeps the session environment, the interactive bash by the curated shell
// (zsh, the bash launcher, or bash without startup files), which reads the
// check from stdin with its binaries as positional parameters. A
// component's binary therefore has to resolve through the session PATH and
// run with the session's MISE_* settings. Before the check, every binary
// of the component that resolves in the image environment must resolve
// through the session PATH too (binsPrelude). In a zsh image
// (shell.program zsh, or a zsh component without a shell section; the
// shell kind the hooks use), the checks also run in an interactive login
// zsh (zsh -il), as the login hook starts it. The selected shell program's startup check
// (session.zsh.startup or session.bash.startup, an interactive shell in
// both cases) fails when the startup writes to stderr (apart from the notes
// a shell prints only because verify runs it without a terminal) or does
// not read the curated rc. When mise installs a checked component,
// session.mise.project-config runs the first such check again in a login
// bash from a project directory that pins every image tool to a version
// the lockfile lacks (miseProjectCheck), so a project's mise files cannot
// change or break the image tools (spec 002 T059).
func SessionRequests(p *plan.Plan) []probe.Request {
	if p.Target != manifest.TargetOpenShell {
		return nil
	}
	env := sessionEnv(p.FinalUser)
	zsh := plan.ShellKind(p) == "zsh"
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
		check := sessionBinsCheck + c.Check.Command
		pre, post := binsPrelude(c)
		add(sessionPrefix+"bash-login."+c.Key, pre+env+" bash -lc "+shell.Quote(check)+" sandcast-session $_sb"+post, c.Check, bin)
		// bash -i prints its prompts and job control notes to stderr; the
		// check's own stderr is kept through the group redirection. -s
		// passes the binaries as positional parameters.
		add(sessionPrefix+"bash-interactive."+c.Key,
			pre+"printf '%s\\n' "+shell.Quote("{ "+check+"\n} 2>&1")+" | "+env+" bash -i -s -- $_sb 2>/dev/null"+post, c.Check, bin)
		if zsh {
			add(sessionPrefix+"zsh."+c.Key, pre+env+" zsh -il -c "+shell.Quote(check)+" sandcast-session $_sb"+post, c.Check, bin)
		}
	}
	if c, ok := miseProjectComponent(p); ok {
		check := sessionBinsCheck + miseProjectCheck(miseProjectDir, c.Check.Command, miseTools(p))
		pre, post := binsPrelude(c)
		add(sessionMiseProjectKey, pre+env+" bash -lc "+shell.Quote(check)+" sandcast-session $_sb"+post, c.Check, c.Bins[0])
	}
	probeRC := ":"
	if p.Shell.RCPath != "" {
		probeRC = `[ -n "${` + plan.CuratedRCLoadedVar + `-}" ] || echo "sandcast: the curated rc ` + p.Shell.RCPath + ` was not read" >&2`
	}
	switch plan.ShellKind(p) {
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

// NotCheckedPrefix starts the output line of a session check for a binary
// that does not resolve in the image environment and is therefore not
// required in the session; Run moves these lines into Result.Notes.
const NotCheckedPrefix = "sandcast: not checked in the session: "

// sessionBinsCheck runs in the session shell before the component check:
// every binary passed as a positional parameter must resolve through the
// session PATH, else the check fails with exit 127 (FR-010).
const sessionBinsCheck = `for _b in "$@"; do command -v "$_b" >/dev/null || { echo "sandcast: $_b does not resolve through the session PATH" >&2; exit 127; }; done
`

// binsPrelude returns the shell code around a session check command. The
// prelude runs in the verify container's image environment and collects in
// $_sb the component's binaries that resolve there (the build-time image
// PATH, /usr/sbin included); the session shell must resolve exactly those.
// The postlude keeps the session shell's exit status and prints a note line
// for every binary that does not resolve in the image environment either. A
// component whose only binary is its key and does not resolve (a component
// without executables, such as ca-certificates) is checked by its own check
// alone, without a note.
func binsPrelude(c plan.Component) (string, string) {
	words := make([]string, len(c.Bins))
	for i, b := range c.Bins {
		words[i] = shell.Quote(b)
	}
	pre := "_sb= _sk=; for _b in " + strings.Join(words, " ") +
		`; do if command -v "$_b" >/dev/null 2>&1; then _sb="$_sb $_b"; else _sk="$_sk $_b"; fi; done; `
	post := "; _rc=$?"
	if len(c.Bins) != 1 || c.Bins[0] != c.Key {
		post += `; for _b in $_sk; do echo "` + NotCheckedPrefix + `$_b does not resolve in the image environment"; done`
	}
	post += `; exit "$_rc"`
	return pre, post
}

// miseProjectComponent returns the first component, by key, that mise
// installs and that has a check and a binary.
func miseProjectComponent(p *plan.Plan) (plan.Component, bool) {
	var found []plan.Component
	for _, c := range p.Components {
		if c.InstallerKind == "mise" && c.Action != plan.ActionSkip && c.Check.Command != "" && len(c.Bins) > 0 {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return plan.Component{}, false
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Key < found[j].Key })
	return found[0], true
}

// miseTools returns the tool names of the image mise.toml, sorted.
func miseTools(p *plan.Plan) []string {
	var cfg struct {
		Tools map[string]any `toml:"tools"`
	}
	if _, err := toml.Decode(p.Mise.TOML, &cfg); err != nil {
		return nil
	}
	tools := make([]string, 0, len(cfg.Tools))
	for t := range cfg.Tools {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	return tools
}

// miseProjectCheck wraps a component check: it creates the directory dir with
// a mise.toml and a .tool-versions that pin every tool to miseProjectPin
// (.tool-versions only names tools without a backend prefix), runs the
// check there and fails when mise warns or errs on its stderr. Without
// MISE_OVERRIDE_* in the session, mise reads both files, warns that the pin
// is not in the lockfile and leaves the shim without a version. mise's own
// update notice is not a failure. The check's stderr is kept otherwise.
func miseProjectCheck(dir, command string, tools []string) string {
	var cfg, versions strings.Builder
	cfg.WriteString("[tools]\n")
	for _, t := range tools {
		fmt.Fprintf(&cfg, "%q = %q\n", t, miseProjectPin)
		if !strings.Contains(t, ":") {
			fmt.Fprintf(&versions, "%s %s\n", t, miseProjectPin)
		}
	}
	d := shell.QuoteIfNeeded(dir)
	return `mkdir -p ` + d + ` && cd ` + d + ` || exit 1
printf '%s' ` + shell.Quote(cfg.String()) + ` > mise.toml
printf '%s' ` + shell.Quote(versions.String()) + ` > .tool-versions
{ ` + command + `
} 2>` + d + `/.stderr
_rc=$?
if grep -E '^mise (WARN|ERROR)' ` + d + `/.stderr | grep -v -e 'mise version .* available' -e 'To update, run' >&2; then exit 1; fi
cat ` + d + `/.stderr >&2
exit "$_rc"
`
}

// sessionEnv is the env -i prefix that reproduces the environment OpenShell
// gives a session: only HOME, USER, TERM and the default PATH.
func sessionEnv(u plan.FinalUser) string {
	return "env -i HOME=" + shell.QuoteIfNeeded(u.Home) + " USER=" + shell.QuoteIfNeeded(u.Name) +
		" TERM=" + sessionTerm + " PATH=" + strings.Join(plan.OpenShellDefaultPath, ":")
}

// condition maps a request key to its condition.
func condition(key string) string {
	switch {
	case strings.HasPrefix(key, sessionPrefix+"bash-login."), key == sessionMiseProjectKey:
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
		r.Output, r.Notes = splitNotes(r.Output)
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

// splitNotes moves the NotCheckedPrefix lines of a check output into notes.
func splitNotes(output string) (string, []string) {
	if !strings.Contains(output, NotCheckedPrefix) {
		return output, nil
	}
	var keep, notes []string
	for _, line := range strings.Split(output, "\n") {
		if note, ok := strings.CutPrefix(line, NotCheckedPrefix); ok {
			notes = append(notes, note+"; not checked in the session")
			continue
		}
		keep = append(keep, line)
	}
	return strings.TrimRight(strings.Join(keep, "\n"), "\n"), notes
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
