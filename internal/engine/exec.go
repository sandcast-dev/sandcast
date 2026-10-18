package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rhuss/sandcast/internal/imageref"
)

// Commander runs one engine command. The default implementation uses
// os/exec; tests substitute a recorder.
type Commander interface {
	// Command runs bin with args. When out is non-nil, stdout and stderr are
	// streamed there instead of being captured.
	Command(ctx context.Context, bin string, args []string, out io.Writer) (stdout, stderr []byte, err error)
}

// OSCommander runs commands with os/exec.
type OSCommander struct{}

// Command implements Commander.
func (OSCommander) Command(ctx context.Context, bin string, args []string, out io.Writer) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	gracefulCancel(cmd)
	if out != nil {
		cmd.Stdout = out
		cmd.Stderr = out
		return nil, nil, cmd.Run()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Exec drives podman or docker through their command-line interface.
type Exec struct {
	// Bin is the engine binary name or path.
	Bin string
	// Kind is podman or docker; it selects the inspect template for digests.
	Kind string
	// Cmd runs the commands. Defaults to OSCommander.
	Cmd Commander
	// Trace, when set, receives every engine command line before it runs.
	Trace func(format string, args ...any)
	// Notice, when set, receives one-line remarks for the user, for example
	// that containers run without --init. Without it, Trace receives them.
	Notice func(format string, args ...any)

	// noInit is set once a run failed because the engine has no init
	// binary; later runs leave out --init.
	noInit atomic.Bool
}

// NewExec returns an Exec for kind (podman or docker).
func NewExec(kind string) *Exec {
	return &Exec{Bin: kind, Kind: kind, Cmd: OSCommander{}}
}

// Name implements Engine.
func (e *Exec) Name() string { return e.Kind }

func (e *Exec) trace(args []string) {
	if e.Trace != nil {
		e.Trace("+ %s %s", e.Bin, strings.Join(args, " "))
	}
}

// exec traces and runs one engine command; every engine call goes through it.
func (e *Exec) exec(ctx context.Context, args []string, out io.Writer) ([]byte, []byte, error) {
	cmd := e.Cmd
	if cmd == nil {
		cmd = OSCommander{}
	}
	e.trace(args)
	return cmd.Command(ctx, e.Bin, args, out)
}

func (e *Exec) run(ctx context.Context, args []string) ([]byte, []byte, error) {
	stdout, stderr, err := e.exec(ctx, args, nil)
	if err != nil {
		return stdout, stderr, WithStderr(fmt.Errorf("%s %s: %w", e.Bin, args[0], err), stderr)
	}
	return stdout, stderr, nil
}

// Pull implements Engine.
func (e *Exec) Pull(ctx context.Context, ref, platform string) error {
	_, _, err := e.run(ctx, pullArgs(ref, platform))
	return err
}

func pullArgs(ref, platform string) []string {
	args := []string{"pull"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	return append(args, ref)
}

// Digest implements Engine. podman reports the platform manifest digest in
// .Digest; docker only exposes repository digests.
func (e *Exec) Digest(ctx context.Context, ref string) (string, error) {
	out, _, err := e.run(ctx, digestArgs(e.Kind, ref))
	if err != nil {
		return "", err
	}
	return parseDigest(e.Kind, out)
}

func digestArgs(kind, ref string) []string {
	if kind == Docker {
		return []string{"image", "inspect", "--format", "{{json .RepoDigests}}", ref}
	}
	return []string{"image", "inspect", "--format", "{{.Digest}}", ref}
}

func parseDigest(kind string, out []byte) (string, error) {
	text := strings.TrimSpace(string(out))
	if kind == Docker {
		var repoDigests []string
		if err := json.Unmarshal([]byte(text), &repoDigests); err != nil {
			return "", fmt.Errorf("parse repo digests: %w", err)
		}
		if len(repoDigests) == 0 {
			return "", ErrNoDigest
		}
		sort.Strings(repoDigests)
		_, dig, ok := strings.Cut(repoDigests[0], "@")
		if !ok {
			return "", ErrNoDigest
		}
		text = dig
	}
	if !strings.HasPrefix(text, "sha256:") || len(text) != len("sha256:")+64 {
		return "", ErrNoDigest
	}
	return text, nil
}

// ImagePlatform implements Engine.
func (e *Exec) ImagePlatform(ctx context.Context, ref string) (string, error) {
	out, _, err := e.run(ctx, []string{"image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", ref})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ImageID implements Engine.
func (e *Exec) ImageID(ctx context.Context, ref string) (string, error) {
	out, _, err := e.run(ctx, []string{"image", "inspect", "--format", "{{.Id}}", ref})
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("%s image inspect %s: empty image id", e.Bin, ref)
	}
	return id, nil
}

// Run implements Engine. --init is best effort: when the engine cannot find
// its init binary (catatonit is optional in several podman packages,
// docker-init in some docker installations), the run is repeated once
// without it, a notice says so, and later runs of this Exec leave it out.
// The caller's deadline (RunWithin) still bounds every run.
func (e *Exec) Run(ctx context.Context, spec RunSpec) ([]byte, []byte, error) {
	if !e.noInit.Load() {
		stdout, stderr, err := e.run(ctx, runArgs(spec, true))
		if err == nil || ctx.Err() != nil || !initMissing(err, stderr) {
			return stdout, stderr, err
		}
		if e.noInit.CompareAndSwap(false, true) {
			e.notice("%s cannot find its init binary, so containers run without --init: a cancelled run may leave its container running until the container ends (%s)",
				e.Bin, firstLine(stderr))
		}
	}
	return e.run(ctx, runArgs(spec, false))
}

// initMarkers are the stderr texts of podman and docker when their init
// binary for --init is missing. They stay specific to the init binary so a
// plain "command not found" inside the container never triggers the fallback.
var initMarkers = []string{
	"container-init binary not found",
	`could not find "catatonit"`,
	"lookup init binary",
	`"docker-init": executable file not found`,
}

// initExitCodes are the exit codes under which an engine may report a missing
// init binary: podman's create-time error is 125, its start-time error 127,
// and the docker CLI maps "executable file not found" to 127.
var initExitCodes = []int{125, 126, 127}

// initMissing reports whether a run failed because the init binary for --init
// is missing. It requires both an engine-level exit code and a stderr marker
// so a genuine "command not found" inside the container does not retry.
func initMissing(err error, stderr []byte) bool {
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || !slices.Contains(initExitCodes, coded.ExitCode()) {
		return false
	}
	text := string(stderr)
	return slices.ContainsFunc(initMarkers, func(m string) bool { return strings.Contains(text, m) })
}

// notice passes one remark to Notice, or to Trace without it.
func (e *Exec) notice(format string, args ...any) {
	switch {
	case e.Notice != nil:
		e.Notice(format, args...)
	case e.Trace != nil:
		e.Trace(format, args...)
	}
}

// firstLine returns the first non-empty line of b, trimmed.
func firstLine(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// runArgs builds the run command line. --init (podman's catatonit, docker's
// tini) runs as PID 1 and forwards the SIGTERM that a cancelled client
// proxies to the container; without it the script is PID 1, ignores the
// signal, and the container keeps running after sandcast gave up on it.
func runArgs(spec RunSpec, withInit bool) []string {
	args := []string{"run", "--rm"}
	if withInit {
		args = append(args, "--init")
	}
	if spec.Platform != "" {
		args = append(args, "--platform", spec.Platform)
	}
	entry := spec.Entrypoint
	if entry == "" {
		entry = "/bin/sh"
	}
	args = append(args, "--entrypoint", entry)
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+spec.Env[k])
	}
	args = append(args, spec.Image)
	if spec.Script != "" {
		args = append(args, "-c", spec.Script)
	}
	return args
}

// Build implements Engine.
func (e *Exec) Build(ctx context.Context, spec BuildSpec) error {
	out := spec.Log
	if out == nil {
		out = io.Discard
	}
	_, _, err := e.exec(ctx, buildArgs(spec), out)
	if err != nil {
		return fmt.Errorf("%s build: %w", e.Bin, err)
	}
	return nil
}

func buildArgs(spec BuildSpec) []string {
	args := []string{"build"}
	if spec.Platform != "" {
		args = append(args, "--platform", spec.Platform)
	}
	args = append(args, "-f", spec.Containerfile)
	for _, t := range spec.Tags {
		args = append(args, "-t", t)
	}
	keys := make([]string, 0, len(spec.Labels))
	for k := range spec.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+spec.Labels[k])
	}
	if spec.NoCache {
		args = append(args, "--no-cache")
	}
	return append(args, spec.ContextDir)
}

// Tag implements Engine.
func (e *Exec) Tag(ctx context.Context, src, dst string) error {
	_, _, err := e.run(ctx, []string{"tag", src, dst})
	return err
}

// RemoveImage implements Engine.
func (e *Exec) RemoveImage(ctx context.Context, ref string) error {
	_, _, err := e.run(ctx, []string{"rmi", ref})
	return err
}

// ImageExists implements Engine. It uses image inspect, which both engines
// support, rather than podman's image exists.
func (e *Exec) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, _, err := e.exec(ctx, []string{"image", "inspect", "--format", "{{.Id}}", ref}, nil)
	if err != nil {
		var exitErr *exec.ExitError
		if isExit(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("%s image inspect: %w", e.Bin, err)
	}
	return true, nil
}

// Labels implements Engine.
func (e *Exec) Labels(ctx context.Context, ref string) (map[string]string, error) {
	out, _, err := e.run(ctx, []string{"image", "inspect", "--format", "{{json .Config.Labels}}", ref})
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	text := strings.TrimSpace(string(out))
	if text == "" || text == "null" {
		return labels, nil
	}
	if err := json.Unmarshal([]byte(text), &labels); err != nil {
		return nil, fmt.Errorf("parse labels of %s: %w", ref, err)
	}
	return labels, nil
}

// Push implements Engine. podman writes the registry digest to a file
// (push --digestfile), which also works through a remote connection.
// docker prints "digest: sha256:..." in its push output; without that line
// the digest comes from the image's repository digests for the pushed
// repository. The local image digest is no substitute: a registry may
// store a recompressed manifest with a different digest.
func (e *Exec) Push(ctx context.Context, ref string, log io.Writer) (string, error) {
	if log == nil {
		log = io.Discard
	}
	var output bytes.Buffer
	out := io.MultiWriter(log, &output)
	if e.Kind != Docker {
		f, err := os.CreateTemp("", "sandcast-digest-*")
		if err != nil {
			return "", fmt.Errorf("push %s: %w", ref, err)
		}
		path := f.Name()
		_ = f.Close()
		defer func() { _ = os.Remove(path) }()
		if _, _, err := e.exec(ctx, []string{"push", "--digestfile", path, ref}, out); err != nil {
			return "", WithStderr(fmt.Errorf("%s push: %w", e.Bin, err), lastLines(output.Bytes(), 5))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil
		}
		return validDigest(strings.TrimSpace(string(data))), nil
	}
	if _, _, err := e.exec(ctx, []string{"push", ref}, out); err != nil {
		return "", WithStderr(fmt.Errorf("%s push: %w", e.Bin, err), lastLines(output.Bytes(), 5))
	}
	if m := pushDigestRe.FindSubmatch(output.Bytes()); m != nil {
		return string(m[1]), nil
	}
	inspect, _, err := e.run(ctx, digestArgs(Docker, ref))
	if err != nil {
		return "", nil
	}
	return repoDigestFor(ref, inspect), nil
}

// HostArch implements Engine.
func (e *Exec) HostArch(ctx context.Context) (string, error) {
	format := "{{.Host.Arch}}"
	if e.Kind == Docker {
		format = "{{.Architecture}}"
	}
	out, _, err := e.run(ctx, []string{"info", "--format", format})
	if err != nil {
		return "", err
	}
	arch := NormalizeArch(string(out))
	if arch == "" {
		return "", fmt.Errorf("%s info: no host architecture", e.Bin)
	}
	return arch, nil
}

var pushDigestRe = regexp.MustCompile(`digest: (sha256:[0-9a-f]{64})`)

// validDigest returns d when it has the sha256:<64 hex> form, else "".
func validDigest(d string) string {
	if strings.HasPrefix(d, "sha256:") && len(d) == len("sha256:")+64 {
		return d
	}
	return ""
}

// repoDigestFor picks the digest of the repository ref names from docker's
// RepoDigests JSON, comparing normalized names.
func repoDigestFor(ref string, inspect []byte) string {
	want, err := imageref.Normalize(ref)
	if err != nil {
		return ""
	}
	var repoDigests []string
	if json.Unmarshal(bytes.TrimSpace(inspect), &repoDigests) != nil {
		return ""
	}
	for _, rd := range repoDigests {
		name, dig, ok := strings.Cut(rd, "@")
		if !ok {
			continue
		}
		got, err := imageref.Normalize(name)
		if err == nil && got.Name() == want.Name() {
			return validDigest(dig)
		}
	}
	return ""
}

// lastLines returns the last n lines of out.
func lastLines(out []byte, n int) []byte {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return []byte(strings.Join(lines, "\n"))
}

// gracefulCancel makes a cancelled context stop the child with SIGTERM and
// wait up to 15 seconds before killing it, so podman can remove its build
// containers and openshell can finish a request.
func gracefulCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second
}
