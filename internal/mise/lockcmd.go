package mise

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/version"
)

// ErrMiseMissing reports that no mise binary is on the host PATH.
var ErrMiseMissing = errors.New("mise not found on PATH")

// ErrMiseTooOld reports a host mise older than version.MiseMinHost.
var ErrMiseTooOld = errors.New("host mise is too old")

// Runner runs a host command and returns its output.
type Runner interface {
	// Run runs name with args in dir. A nil env inherits the process
	// environment; a non-nil env replaces it.
	Run(ctx context.Context, dir string, env []string, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// LockResult is the per-component outcome of Lock.
type LockResult struct {
	// Locked lists the tool names usable in locked mode on every requested
	// platform, sorted.
	Locked []string
	// Failed lists the components that are not, one entry per platform, in
	// tool name order.
	Failed []ToolError
	// Changed reports whether the lockfile at lockPath was written.
	Changed bool
}

// LockOption adjusts Lock.
type LockOption func(*lockOptions)

type lockOptions struct {
	bump bool
}

// WithBump re-resolves fuzzy selectors such as "latest" or "1.25" against
// the newest matching releases (mise lock --bump). Without it, versions
// already in the lockfile are kept and only their metadata is refreshed.
func WithBump() LockOption {
	return func(o *lockOptions) { o.bump = true }
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// HostVersion returns the version of the host mise, for example "2026.10.3".
func HostVersion(ctx context.Context, r Runner) (string, error) {
	stdout, stderr, err := r.Run(ctx, "", nil, "mise", "--version")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%w: install mise %s or newer", ErrMiseMissing, version.MiseMinHost)
		}
		return "", fmt.Errorf("run mise --version: %w%s", err, detail(stderr))
	}
	m := versionRe.FindString(string(stdout))
	if m == "" {
		return "", fmt.Errorf("cannot parse mise version from %q", strings.TrimSpace(string(stdout)))
	}
	return m, nil
}

// CheckHostVersion fails when mise is missing (ErrMiseMissing) or older than
// version.MiseMinHost (ErrMiseTooOld), the oldest release that writes
// lockfile v3 with per-platform checksums.
func CheckHostVersion(ctx context.Context, r Runner) error {
	v, err := HostVersion(ctx, r)
	if err != nil {
		return err
	}
	if compareVersions(v, version.MiseMinHost) < 0 {
		return fmt.Errorf("%w: found %s, need %s or newer for lockfile v%d with per-platform checksums",
			ErrMiseTooOld, v, version.MiseMinHost, LockFormatVersion)
	}
	return nil
}

// compareVersions compares two dotted numeric versions.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Lock creates or updates the lockfile at lockPath for tools on platforms
// (mise platform names) with the host mise.
//
// It checks the host mise version, then runs "mise lock --platform <list>"
// in a temporary workspace seeded with the rendered mise.toml and the
// existing lockfile, isolated from the host mise configuration, installs and
// state (see isolatedEnv). When that run fails, it retries one tool at a
// time so that one unresolvable tool (offline host, missing release) does not
// block the others. The resulting lockfile is checked for every tool and
// platform: components without a complete entry land in Failed with mise's
// reason when one is found in its output. Successful tools take mise's new
// entries, failed tools keep their entries from the existing lockfile (none
// for a new tool), and the result is written to lockPath atomically when it
// differs.
//
// An error is returned when the inputs are invalid, mise is missing or too
// old, the existing lockfile is unreadable, or there is no existing lockfile
// and no tool could be locked.
func Lock(ctx context.Context, r Runner, tools []Tool, platforms []string, lockPath string, opts ...LockOption) (*LockResult, error) {
	var o lockOptions
	for _, opt := range opts {
		opt(&o)
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("no mise tools to lock")
	}
	plats, err := normalizePlatforms(platforms)
	if err != nil {
		return nil, err
	}
	config, err := renderLockConfig(tools, plats)
	if err != nil {
		return nil, err
	}
	sorted, err := sortTools(tools)
	if err != nil {
		return nil, err
	}

	existing, err := os.ReadFile(lockPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read existing lockfile: %w", err)
	}
	if existing != nil {
		if _, err := ParseLock(existing); err != nil {
			return nil, fmt.Errorf("existing %s: %w (delete it and run sandcast lock again)", lockPath, err)
		}
	}

	if err := CheckHostVersion(ctx, r); err != nil {
		return nil, err
	}

	ws, err := newWorkspace()
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(ws.root) }()

	if err := fsutil.AtomicWriteFile(filepath.Join(ws.project, "mise.toml"), config, 0o644); err != nil {
		return nil, fmt.Errorf("write lock workspace: %w", err)
	}
	wsLock := filepath.Join(ws.project, fsutil.LockFile)
	if existing != nil {
		if err := fsutil.AtomicWriteFile(wsLock, existing, 0o644); err != nil {
			return nil, fmt.Errorf("write lock workspace: %w", err)
		}
	}

	env := isolatedEnv(os.Environ(), ws)
	baseArgs := []string{"lock", "--platform", strings.Join(plats, ",")}
	if o.bump {
		baseArgs = append(baseArgs, "--bump")
	}

	// logs collects the stderr of every mise run with workspace paths made
	// relative; runFailed maps a tool name to the reason its run failed.
	var logs [][]byte
	runFailed := map[string]string{}
	run := func(args ...string) ([]byte, error) {
		_, stderr, err := r.Run(ctx, ws.project, env, "mise", args...)
		stderr = bytes.ReplaceAll(stderr, []byte(ws.project+string(filepath.Separator)), nil)
		logs = append(logs, stderr)
		return stderr, err
	}

	if stderr, runErr := run(baseArgs...); runErr != nil {
		if err := runError(ctx, runErr); err != nil {
			return nil, err
		}
		if len(sorted) == 1 {
			runFailed[sorted[0].Tool] = failureReason(stderr, runErr)
		} else {
			for _, t := range sorted {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				toolStderr, toolErr := run(append(append([]string(nil), baseArgs...), t.Tool)...)
				if toolErr != nil {
					if err := runError(ctx, toolErr); err != nil {
						return nil, err
					}
					runFailed[t.Tool] = failureReason(toolStderr, toolErr)
				}
			}
		}
	}

	data, err := os.ReadFile(wsLock)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read lock workspace: %w", err)
	}
	var lock *Lockfile
	if data != nil {
		if lock, err = ParseLock(data); err != nil {
			return nil, fmt.Errorf("mise lock output: %w", err)
		}
	}

	res := &LockResult{}
	failed := map[string]bool{}
	for _, t := range sorted {
		var toolErrs []ToolError
		reason, runFailedTool := runFailed[t.Tool]
		if !runFailedTool && lock == nil {
			reason, runFailedTool = "mise lock wrote no lockfile", true
		}
		if runFailedTool {
			for _, p := range plats {
				toolErrs = append(toolErrs, ToolError{Key: t.Key, Tool: t.Tool, Platform: p, Reason: reason})
			}
		} else {
			for _, p := range plats {
				toolErrs = append(toolErrs, lock.Check([]Tool{t}, p)...)
			}
			if hint := lastMention(logs, toolIDs(t, lock)); hint != "" {
				for i := range toolErrs {
					toolErrs[i].Reason += " (mise: " + hint + ")"
				}
			}
		}
		if len(toolErrs) == 0 {
			res.Locked = append(res.Locked, t.Tool)
			continue
		}
		failed[t.Tool] = true
		res.Failed = append(res.Failed, toolErrs...)
	}
	if len(res.Locked) == 0 && existing == nil {
		return nil, noLockfileError(res.Failed)
	}

	// Failed tools keep their previous entries: what mise wrote for them is
	// incomplete, and an unresolved selector it locked verbatim (offline
	// "1.25" for go) would otherwise be preserved by every later run.
	out := mergeLock(data, existing, failed)
	if _, err := ParseLock(out); err != nil {
		return nil, fmt.Errorf("merge mise lock output: %w", err)
	}
	if !bytes.Equal(out, existing) {
		if err := fsutil.AtomicWriteFile(lockPath, out, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", lockPath, err)
		}
		res.Changed = true
	}
	return res, nil
}

// runError returns a non-nil error when a failed mise run must abort Lock
// instead of being reported per tool: a cancelled context or a missing
// binary.
func runError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w: install mise %s or newer", ErrMiseMissing, version.MiseMinHost)
	}
	return nil
}

// noLockfileError reports a Lock run without an existing lockfile in which
// no tool could be locked, listing each distinct reason once.
func noLockfileError(failed []ToolError) error {
	seen := map[string]bool{}
	var parts []string
	for _, e := range failed {
		part := fmt.Sprintf("%s (%s): %s", e.Key, e.Tool, e.Reason)
		if !seen[part] {
			seen[part] = true
			parts = append(parts, part)
		}
	}
	return fmt.Errorf("mise lock produced no usable lockfile: %s", strings.Join(parts, "; "))
}

// lockBlock is a run of consecutive lockfile tables that belong to one tool
// (tool is "" for the preamble and for tables outside [tools]).
type lockBlock struct {
	tool string
	text string
}

// splitLock splits mise lockfile text into blocks at table headers, keeping
// every byte, so joining the blocks' text reproduces data.
func splitLock(data []byte) []lockBlock {
	var blocks []lockBlock
	for _, line := range strings.SplitAfter(string(data), "\n") {
		tool := ""
		isHeader := strings.HasPrefix(line, "[")
		if isHeader {
			tool = headerTool(line)
		}
		if len(blocks) == 0 || (isHeader && tool != blocks[len(blocks)-1].tool) {
			blocks = append(blocks, lockBlock{tool: tool})
		}
		blocks[len(blocks)-1].text += line
	}
	return blocks
}

// headerTool returns the tool of a table header such as [[tools.go]],
// [[tools."aqua:jqlang/jq"]] or [tools.go."platforms.linux-x64"], or "" for
// a header outside [tools]. mise quotes keys that are not bare; tool names
// never contain quotes or backslashes, so no escape handling is needed.
func headerTool(line string) string {
	s := strings.TrimLeft(line, "[")
	if !strings.HasPrefix(s, "tools.") {
		return ""
	}
	s = strings.TrimPrefix(s, "tools.")
	if rest, ok := strings.CutPrefix(s, `"`); ok {
		name, _, found := strings.Cut(rest, `"`)
		if !found {
			return ""
		}
		return name
	}
	end := strings.IndexAny(s, ".]")
	if end <= 0 {
		return ""
	}
	return s[:end]
}

// mergeLock returns the lockfile mise produced with the tables of every tool
// in revert replaced by that tool's tables from previous, or dropped when
// previous has none. A tool only in previous is inserted in name order, the
// order mise writes. A nil produced yields previous.
func mergeLock(produced, previous []byte, revert map[string]bool) []byte {
	if produced == nil {
		return previous
	}
	if len(revert) == 0 {
		return produced
	}
	// Previous tables per tool, separated from the next block by one blank
	// line like the tables mise writes.
	prev := map[string]string{}
	for _, b := range splitLock(previous) {
		if b.tool != "" {
			prev[b.tool] += strings.TrimRight(b.text, "\n") + "\n\n"
		}
	}

	var out []lockBlock
	done := map[string]bool{}
	for _, b := range splitLock(produced) {
		if b.tool == "" || !revert[b.tool] {
			out = append(out, b)
			continue
		}
		if !done[b.tool] && prev[b.tool] != "" {
			out = append(out, lockBlock{tool: b.tool, text: prev[b.tool]})
		}
		done[b.tool] = true
	}
	missing := make([]string, 0, len(revert))
	for tool := range revert {
		missing = append(missing, tool)
	}
	sort.Strings(missing)
	for _, tool := range missing {
		if done[tool] || prev[tool] == "" {
			continue
		}
		at, lastTool := len(out), -1
		for i, b := range out {
			if b.tool == "" {
				continue
			}
			lastTool = i
			if b.tool > tool {
				at = i
				break
			}
		}
		if at == len(out) && lastTool >= 0 {
			at = lastTool + 1
		}
		block := lockBlock{tool: tool, text: prev[tool]}
		out = append(out[:at], append([]lockBlock{block}, out[at:]...)...)
	}

	var b strings.Builder
	for _, blk := range out {
		if blk.tool != "" && b.Len() > 0 {
			for !strings.HasSuffix(b.String(), "\n\n") {
				b.WriteString("\n")
			}
		}
		b.WriteString(blk.text)
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

// workspace is the temporary directory tree Lock runs mise in.
type workspace struct {
	root    string // parent of all others; the ceiling for config discovery
	project string // mise.toml and mise.lock
	config  string // empty MISE_CONFIG_DIR
	system  string // empty MISE_SYSTEM_CONFIG_DIR
	data    string // empty MISE_DATA_DIR
	state   string // empty MISE_STATE_DIR
}

func newWorkspace() (workspace, error) {
	root, err := os.MkdirTemp("", "sandcast-lock-")
	if err != nil {
		return workspace{}, fmt.Errorf("create lock workspace: %w", err)
	}
	// mise compares trusted and ceiling paths after resolving symlinks
	// (macOS /var is /private/var).
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	ws := workspace{
		root:    root,
		project: filepath.Join(root, "project"),
		config:  filepath.Join(root, "config"),
		system:  filepath.Join(root, "system"),
		data:    filepath.Join(root, "data"),
		state:   filepath.Join(root, "state"),
	}
	for _, d := range []string{ws.project, ws.config, ws.system, ws.data, ws.state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			_ = os.RemoveAll(root)
			return workspace{}, fmt.Errorf("create lock workspace: %w", err)
		}
	}
	return ws, nil
}

// keepHostMiseVar reports whether a host MISE_* variable is passed to mise
// lock. Only network, authentication, cache and parallelism settings pass;
// they change how mise reaches the network, not what it resolves.
func keepHostMiseVar(key string) bool {
	switch key {
	case "MISE_CACHE_DIR", "MISE_OFFLINE", "MISE_JOBS", "MISE_HTTP_TIMEOUT",
		"MISE_HTTP_RETRIES", "MISE_FETCH_REMOTE_VERSIONS_TIMEOUT":
		return true
	}
	return strings.HasSuffix(key, "_TOKEN")
}

// isolatedEnv derives the mise lock environment from base. It drops host
// MISE_* variables except keepHostMiseVar ones, points the global and system
// configuration directories at empty directories (so ~/.config/mise and
// /etc/mise do not add tools or settings), uses empty data and state
// directories (otherwise "latest" resolves to a version installed on the
// host), stops config discovery at the workspace root, and trusts the
// workspace project directory without "mise trust".
func isolatedEnv(base []string, ws workspace) []string {
	env := make([]string, 0, len(base)+8)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "MISE_") && !keepHostMiseVar(key) {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"MISE_CONFIG_DIR="+ws.config,
		"MISE_SYSTEM_CONFIG_DIR="+ws.system,
		"MISE_DATA_DIR="+ws.data,
		"MISE_STATE_DIR="+ws.state,
		"MISE_CEILING_PATHS="+ws.root,
		"MISE_TRUSTED_CONFIG_PATHS="+ws.project,
		"MISE_COLOR=false",
		"NO_COLOR=1",
	)
}

// failureReason extracts the error messages of a failed mise run, without
// mise's boilerplate lines.
func failureReason(stderr []byte, runErr error) string {
	var msgs []string
	for _, line := range strings.Split(string(stderr), "\n") {
		_, msg, ok := strings.Cut(line, "mise ERROR ")
		if !ok {
			continue
		}
		msg = strings.TrimSpace(msg)
		if msg == "" || strings.HasPrefix(msg, "Version:") || strings.HasPrefix(msg, "Run with --verbose") {
			continue
		}
		msgs = append(msgs, msg)
	}
	if len(msgs) == 0 {
		return "mise lock failed: " + runErr.Error()
	}
	return strings.Join(msgs, ": ")
}

// toolIDs returns the names under which mise output may mention t: the tool
// name, its backend, and the backend's package (the part after "backend:").
func toolIDs(t Tool, lock *Lockfile) []string {
	ids := []string{t.Tool}
	if _, pkg, ok := strings.Cut(t.Tool, ":"); ok {
		ids = append(ids, pkg)
	}
	for _, e := range lock.Tools[t.Tool] {
		if e.Backend == "" {
			continue
		}
		ids = append(ids, e.Backend)
		if _, pkg, ok := strings.Cut(e.Backend, ":"); ok {
			ids = append(ids, pkg)
		}
	}
	return ids
}

// lastMention returns the last WARN or ERROR message in logs that mentions
// one of ids as a whole word, or "".
func lastMention(logs [][]byte, ids []string) string {
	found := ""
	for _, log := range logs {
		for _, line := range strings.Split(string(log), "\n") {
			msg, ok := miseMessage(line)
			if !ok {
				continue
			}
			for _, id := range ids {
				if mentions(msg, id) {
					found = msg
					break
				}
			}
		}
	}
	return found
}

// miseMessage returns the message of a "mise WARN" or "mise ERROR" line.
func miseMessage(line string) (string, bool) {
	for _, level := range []string{"mise WARN ", "mise ERROR "} {
		if _, msg, ok := strings.Cut(line, level); ok {
			return strings.TrimSpace(msg), true
		}
	}
	return "", false
}

// mentions reports whether s contains id delimited by characters that cannot
// be part of a tool name, so "go" matches "/go/" but not "google".
func mentions(s, id string) bool {
	if id == "" {
		return false
	}
	for off := 0; ; {
		i := strings.Index(s[off:], id)
		if i < 0 {
			return false
		}
		start, end := off+i, off+i+len(id)
		if (start == 0 || !nameChar(s[start-1])) && (end == len(s) || !nameChar(s[end])) {
			return true
		}
		off = start + 1
	}
}

func nameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// detail formats command stderr for an error message.
func detail(stderr []byte) string {
	s := strings.TrimSpace(string(stderr))
	if s == "" {
		return ""
	}
	return ": " + s
}
