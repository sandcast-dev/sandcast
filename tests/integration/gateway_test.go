//go:build gateway

package integration

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startGateway runs gateway.sh up and registers the teardown. Without
// podman, its socket or the pinned openshell CLI it skips, or fails when
// SANDCAST_REQUIRE_TOOLS=1 or CI is set (missingTool), so a run that must
// not skip silently does not. The gateway tests are the only check of the
// shell hooks against the real runtime and are opt-in: ci.yml runs
// make test-integration, not make test-gateway.
func startGateway(t *testing.T) []string {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		missingTool(t, "podman not on PATH")
	}
	out, err := exec.Command("openshell", "--version").Output()
	if err != nil || !strings.Contains(string(out), "0.1.") {
		missingTool(t, fmt.Sprintf("openshell CLI 0.1.x required for the gateway test (got %q, %v)", strings.TrimSpace(string(out)), err))
	}
	if sock, err := exec.Command("podman", "info", "--format", "{{.Host.RemoteSocket.Path}}").Output(); err != nil || strings.TrimSpace(string(sock)) == "" {
		missingTool(t, "podman socket not available")
	}
	up := exec.Command("sh", "gateway.sh", "up")
	endpoint, err := up.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 && !toolsRequired() {
			t.Skip("podman socket not available")
		}
		t.Fatalf("gateway.sh up: %v\n%s", err, endpoint)
	}
	t.Cleanup(func() { _ = exec.Command("sh", "gateway.sh", "down").Run() })
	return []string{"OPENSHELL_GATEWAY_ENDPOINT=" + strings.TrimSpace(string(endpoint))}
}

func openshell(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("openshell", args...)
	cmd.Env = append(cmd.Environ(), env...)
	out, err := cmd.CombinedOutput()
	t.Logf("openshell %s\n%s", strings.Join(args, " "), out)
	return string(out), err
}

// TestGatewayImportAndSandbox covers quickstart scenario 2 without
// credentials: the adapted profiles import at workspace scope, a second
// import reports the existing ids, and a sandbox started from the built
// image runs the Go toolchain installed by mise.
func TestGatewayImportAndSandbox(t *testing.T) {
	env := startGateway(t)
	dir := project(t, `schema_version: 1
name: sc-gateway
base_image: nvidia-community-base
components:
  - key: go
targets:
  openshell:
    image: localhost/sandcast-it-gateway
`)
	removeImages(t, "podman", "localhost/sandcast-it-gateway:latest")
	require.Zero(t, sandcast(t, dir, nil, "lock", "--platform", hostPlatform()).code)
	require.Zero(t, sandcast(t, dir, nil, "build", "--verify").code)

	require.Zero(t, sandcast(t, dir, env, "profiles", "import", "--scope", "workspace").code)
	out, err := openshell(t, env, "profile", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "go-proxy")

	r := sandcast(t, dir, env, "profiles", "import", "--scope", "workspace")
	assert.Equal(t, 1, r.code, "existing ids are reported, not overwritten")
	assert.Contains(t, r.stdout, "exists: go-proxy")

	_, err = openshell(t, env, "provider", "create", "--name", "sc-go", "--type", "go-proxy")
	require.NoError(t, err)
	out, err = openshell(t, env, "sandbox", "create", "--name", "sc-gateway", "--no-keep", "--no-tty", "--no-auto-providers",
		"--from", "localhost/sandcast-it-gateway:latest", "--provider", "sc-go", "--", "go", "version")
	require.NoError(t, err)
	assert.Contains(t, out, "go version go")
}

// TestGatewayHarness covers the US4 smoke test without credentials: an
// image with the Claude Code harness and one MCP server imports
// claude-code and the generated mcp-docs profile, and a sandbox from it
// runs claude --version with the configuration outside the home directory.
// The inference call itself needs an Anthropic credential and stays a
// manual step (quickstart scenario 2, SC-007).
func TestGatewayHarness(t *testing.T) {
	env := startGateway(t)
	dir := project(t, `schema_version: 1
name: sc-harness
base_image: nvidia-community-base
components: []
harnesses:
  - name: claude-code
    mcp_servers:
      - name: docs
        url: https://docs.example.com/mcp
        env:
          DOCS_TOKEN: ${SANDCAST_SECRET_1}
targets:
  openshell:
    image: localhost/sandcast-it-harness
`)
	removeImages(t, "podman", "localhost/sandcast-it-harness:latest")
	require.Zero(t, sandcast(t, dir, nil, "lock", "--platform", hostPlatform()).code)
	require.Zero(t, sandcast(t, dir, nil, "build", "--verify").code)
	require.Zero(t, sandcast(t, dir, env, "profiles", "import").code)
	out, err := openshell(t, env, "profile", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "claude-code")
	assert.Contains(t, out, "mcp-docs")

	out, err = openshell(t, env, "sandbox", "create", "--name", "sc-harness", "--no-keep", "--no-tty", "--no-auto-providers",
		"--from", "localhost/sandcast-it-harness:latest", "--", "sh", "-c", "claude --version && echo config=$CLAUDE_CONFIG_DIR")
	require.NoError(t, err)
	assert.Contains(t, out, "Claude Code")
	assert.Contains(t, out, "config=/opt/sandcast/claude-code")
}

// TestGatewayShellHooks runs the shell checks against the real OpenShell
// runtime instead of a simulation: a sandbox from a bash image built by
// sandcast, workspace startup files planted through an exec that sources no
// startup file, then OpenShell's default exec (a login shell started after
// clearing the environment) and an interactive bash -i under a pty. No
// planted file may run, the exit status must survive, and the curated
// configuration under /opt/sandcast must be readable under the image policy
// (landlock) and active in the interactive shell (spec 003 FR-001, FR-002,
// SC-002).
func TestGatewayShellHooks(t *testing.T) {
	env := startGateway(t)
	dir := project(t, `schema_version: 1
name: sc-hooks
base_image: nvidia-community-base
shell:
  program: bash
  rc: |
    alias myll='ls -l'
targets:
  openshell:
    image: localhost/sandcast-it-hooks
`)
	removeImages(t, "podman", "localhost/sandcast-it-hooks:latest")
	require.Zero(t, sandcast(t, dir, nil, "build").code)

	// Keep the sandbox running while the checks exec into it.
	create := exec.Command("openshell", "sandbox", "create", "--name", "sc-hooks", "--no-tty", "--no-auto-providers",
		"--from", "localhost/sandcast-it-hooks:latest", "--", "sleep", "600")
	create.Env = append(create.Environ(), env...)
	require.NoError(t, create.Start())
	t.Cleanup(func() {
		_, _ = openshell(t, env, "sandbox", "delete", "sc-hooks")
		_ = create.Process.Kill()
		_ = create.Wait()
	})
	ready := false
	for range 100 {
		get := exec.Command("openshell", "sandbox", "get", "sc-hooks")
		get.Env = append(get.Environ(), env...)
		out, _ := get.CombinedOutput()
		if strings.Contains(string(out), "Phase: Ready") {
			ready = true
			break
		}
		time.Sleep(3 * time.Second)
	}
	require.True(t, ready, "the sandbox becomes ready")

	pwned := func() string {
		t.Helper()
		out, err := openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--no-login-shell", "--", "sh", "-c",
			`ls /tmp | grep '^pwned-' | tr '\n' ' '; rm -f /tmp/pwned-*; true`)
		require.NoError(t, err, out)
		return strings.TrimSpace(out)
	}

	out, err := openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--no-login-shell", "--", "sh", "-c",
		`for f in .bashrc .bash_profile .bash_login .bash_logout .profile; do echo "touch /tmp/pwned-${f#.}" > /sandbox/$f; done; rm -f /tmp/pwned-*`)
	require.NoError(t, err, out)

	// Positive control: sourcing a planted file leaves its sentinel.
	out, err = openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--no-login-shell", "--", "sh", "-c", ". /sandbox/.bashrc")
	require.NoError(t, err, out)
	assert.Contains(t, pwned(), "pwned-bashrc", "the planted files are in the workspace home")

	// OpenShell's default exec sources login startup files.
	out, err = openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--", "sh", "-c", "echo LOGIN-OK; exit 3")
	assert.Contains(t, out, "LOGIN-OK")
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 3, ee.ExitCode(), "the exit status survives the login hook")
	assert.Empty(t, pwned(), "the login exec runs no workspace startup or logout file")

	// The caller's options survive the replacement: a login bash with -e
	// (started inside the login exec) stops at the failing command.
	out, err = openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--", "bash", "-le", "-c", "false; echo X-LE; exit 3")
	assert.NotContains(t, out, "X-LE", "-e survives the login hook")
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode(), "the -e exit status survives the login hook")
	out, err = openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--", "bash", "-l", "-o", "pipefail", "-c", "false | true")
	require.ErrorAs(t, err, &ee, out)
	assert.Equal(t, 1, ee.ExitCode(), "pipefail survives the login hook")
	assert.Empty(t, pwned(), "the option cases run no workspace startup or logout file")

	// An interactive bash under a pty gets the curated rcfile, not ~/.bashrc.
	out, err = openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--tty", "--no-login-shell", "--", "/bin/bash", "-i", "-c", "alias myll")
	require.NoError(t, err, out)
	assert.Contains(t, out, "alias myll='ls -l'")
	assert.Empty(t, pwned(), "the interactive bash runs no workspace startup file")

	// The default login exec under a pty: the login hook replaces the login
	// bash and keeps the command and its exit status, and the interactive
	// bash the command starts gets the curated rcfile. A zsh image is not
	// built here (build cost); the overmount simulation covers that kind.
	out, err = openshell(t, env, "sandbox", "exec", "-n", "sc-hooks", "--tty", "--", "/bin/bash", "-i", "-c", "alias myll; exit 5")
	assert.Contains(t, out, "alias myll='ls -l'")
	require.ErrorAs(t, err, &ee, out)
	assert.Equal(t, 5, ee.ExitCode(), "the exit status survives the pty login exec")
	assert.Empty(t, pwned(), "the pty login exec runs no workspace startup or logout file")
}

// TestGatewaySession covers FR-025 and SC-003 against a real v0.1.3
// gateway: a sandbox started from the zsh session image resolves every
// component binary through openshell sandbox exec (bash -lc), and an
// interactive terminal session switches to zsh, resolves the mise tools and
// prints no error lines.
func TestGatewaySession(t *testing.T) {
	env := startGateway(t)
	img := "localhost/sandcast-it-gateway-session:latest"
	dir := project(t, sessionManifest("sc-gw-session", "localhost/sandcast-it-gateway-session"))
	removeImages(t, "podman", img)
	require.Zero(t, sandcast(t, dir, nil, "lock", "--platform", hostPlatform()).code)
	require.Zero(t, sandcast(t, dir, nil, "build", "--verify").code)

	name := "sc-gw-session"
	_, err := openshell(t, env, "sandbox", "create", "--name", name, "--detach", "--no-auto-providers", "--from", img)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = openshell(t, env, "sandbox", "delete", name) })

	out, err := openshell(t, env, "sandbox", "exec", "-n", name, "--no-tty", "--", "command", "-v", "starship")
	require.NoError(t, err)
	assert.Contains(t, out, "/opt/mise/shims/starship")

	// 100% of the component binaries resolve in an exec session.
	bins := planBins(t, dir, "openshell")
	var script strings.Builder
	for _, b := range bins {
		script.WriteString("command -v " + b + " >/dev/null || echo MISSING " + b + "\n")
	}
	script.WriteString("echo RESOLVED\n")
	out, err = openshell(t, env, "sandbox", "exec", "-n", name, "--no-tty", "--", "sh", "-c", script.String())
	require.NoError(t, err)
	assert.Contains(t, out, "RESOLVED")
	assert.NotContains(t, out, "MISSING", "every component binary resolves in the session")

	// An interactive terminal session: OpenShell starts bash -i, the hook
	// switches to zsh, and the curated rc loads without error lines.
	cmd := exec.Command("openshell", "sandbox", "exec", "--tty", "-n", name, "--", "bash", "-i")
	cmd.Env = append(cmd.Environ(), env...)
	cmd.Stdin = strings.NewReader("echo ZSH=$ZSH_VERSION; command -v starship; exit\n")
	raw, err := cmd.CombinedOutput()
	t.Logf("interactive session:\n%s", raw)
	require.NoError(t, err)
	session := string(raw)
	assert.Regexp(t, `ZSH=5\.`, session, "the interactive session runs zsh")
	assert.Contains(t, session, "/opt/mise/shims/starship")
	for _, bad := range []string{"command not found", "not found:", "error", "zsh-newuser-install", "Z Shell configuration function"} {
		assert.NotContains(t, strings.ToLower(session), strings.ToLower(bad), "no error lines on session start")
	}
}
