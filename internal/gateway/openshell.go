package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rhuss/sandcast/internal/version"
)

// Gateway scopes (spec "Gateway scope").
const (
	ScopeWorkspace = "workspace"
	ScopePlatform  = "platform"
)

var (
	// ErrMissing means the openshell CLI is not on PATH.
	ErrMissing = errors.New("openshell CLI not found")
	// ErrVersion means the openshell CLI is not the pinned release.
	ErrVersion = errors.New("openshell CLI version mismatch")
)

// Runner runs the openshell CLI. Gateway selection flags and environment
// (OPENSHELL_GATEWAY, OPENSHELL_GATEWAY_ENDPOINT, the active gateway file)
// pass through unchanged because the process inherits the environment.
type Runner interface {
	Run(ctx context.Context, bin string, args []string) (stdout, stderr []byte, err error)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	gracefulCancel(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Client wraps the openshell CLI for the profile commands sandcast uses.
type Client struct {
	// Runner runs the CLI. Defaults to ExecRunner.
	Runner Runner
	// Bin is the CLI name or path. Defaults to openshell.
	Bin string
	// Extra are flags passed before every subcommand, e.g. --gateway <name>.
	Extra []string
}

// RemoteProfile is one entry of "openshell profile list -o json".
type RemoteProfile struct {
	ID              string `json:"id"`
	ResourceVersion int    `json:"resource_version"`
	Scope           string `json:"scope"`
	Source          string `json:"source"`
}

func (c *Client) bin() string {
	if c.Bin == "" {
		return "openshell"
	}
	return c.Bin
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	r := c.Runner
	if r == nil {
		r = ExecRunner{}
	}
	full := append(append([]string(nil), c.Extra...), args...)
	stdout, stderr, err := r.Run(ctx, c.bin(), full)
	if err != nil {
		var nf *exec.Error
		if errors.As(err, &nf) {
			return nil, fmt.Errorf("%w: install openshell %s", ErrMissing, version.OpenShellPin)
		}
		return stdout, fmt.Errorf("openshell %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(stderr)))
	}
	return stdout, nil
}

// RequiredVersion is the pinned CLI version without the leading v.
func RequiredVersion() string { return strings.TrimPrefix(version.OpenShellPin, "v") }

// CheckVersion fails unless "openshell --version" reports the pinned
// release. It runs before any gateway call (R-11).
func (c *Client) CheckVersion(ctx context.Context) error {
	r := c.Runner
	if r == nil {
		r = ExecRunner{}
	}
	out, _, err := r.Run(ctx, c.bin(), []string{"--version"})
	if err != nil {
		return fmt.Errorf("%w (%v): the import step requires openshell %s", ErrMissing, err, version.OpenShellPin)
	}
	got := strings.TrimSpace(string(out))
	if f := strings.Fields(got); len(f) > 0 {
		got = strings.TrimPrefix(f[len(f)-1], "v")
	}
	if got != RequiredVersion() {
		return fmt.Errorf("%w: found %s, the import step requires openshell %s", ErrVersion, got, version.OpenShellPin)
	}
	return nil
}

func scopeArgs(scope, workspace string) []string {
	if scope == ScopePlatform {
		return []string{"--global"}
	}
	if workspace != "" {
		return []string{"--workspace", workspace}
	}
	return nil
}

// ListProfiles returns the sorted profile ids visible at scope.
func (c *Client) ListProfiles(ctx context.Context, scope, workspace string) ([]RemoteProfile, error) {
	args := append([]string{"profile", "list", "-o", "json"}, scopeArgs(scope, workspace)...)
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var list []RemoteProfile
	if text := strings.TrimSpace(string(out)); text != "" && text != "null" {
		if err := json.Unmarshal([]byte(text), &list); err != nil {
			return nil, fmt.Errorf("parse openshell profile list: %w", err)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// Import imports every profile file in dir at scope. Upstream import is
// create-only, so existing ids fail instead of being overwritten.
func (c *Client) Import(ctx context.Context, dir, scope, workspace string) error {
	args := append([]string{"profile", "import", "--from", dir}, scopeArgs(scope, workspace)...)
	_, err := c.run(ctx, args...)
	return err
}

// ImportFile imports one profile file at scope.
func (c *Client) ImportFile(ctx context.Context, file, scope, workspace string) error {
	args := append([]string{"profile", "import", "--file", file}, scopeArgs(scope, workspace)...)
	_, err := c.run(ctx, args...)
	return err
}

// Export returns the gateway's YAML for id, which carries resource_version.
func (c *Client) Export(ctx context.Context, id, scope, workspace string) ([]byte, error) {
	args := append([]string{"profile", "export", id, "-o", "yaml"}, scopeArgs(scope, workspace)...)
	return c.run(ctx, args...)
}

// Update replaces the custom profile id with file at scope.
func (c *Client) Update(ctx context.Context, id, file, scope, workspace string) error {
	args := append([]string{"profile", "update", id, "--file", file}, scopeArgs(scope, workspace)...)
	_, err := c.run(ctx, args...)
	return err
}

// gracefulCancel makes a cancelled context stop the child with SIGTERM and
// wait up to 15 seconds before killing it, so podman can remove its build
// containers and openshell can finish a request.
func gracefulCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second
}
