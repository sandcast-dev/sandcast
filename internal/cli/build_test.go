package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/verify"
)

func TestBuildVerifyEndToEnd(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	require.Equal(t, 0, ta.run("build", "--verify", "--json"), ta.stderr.String())
	var rep buildReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, "localhost/demo:latest", rep.Image)
	assert.Equal(t, ".sandcast/build/openshell/Containerfile", rep.Containerfile)
	require.NotNil(t, rep.Verify)
	assert.Zero(t, rep.Verify.Failed)
	assert.Positive(t, rep.Verify.Passed)

	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	for _, f := range []string{"Containerfile", "Containerfile.final", "build.log", "profiles/index.yaml", "profiles/go-proxy.yaml", "profiles/github.yaml", "verify.yaml"} {
		_, err := os.Stat(filepath.Join(dir, f))
		assert.NoError(t, err, f)
	}
	assert.Equal(t, 2, ta.eng.CountOp("build"))

	// verify alone re-reads the plan.
	require.Equal(t, 0, ta.run("verify"))
	assert.Contains(t, ta.stdout.String(), fmt.Sprintf(": %d passed, 0 failed\n", rep.Verify.Passed))

	// profiles lint on the adapted set.
	require.Equal(t, 0, ta.run("profiles", "lint"), ta.stdout.String())
	assert.Contains(t, ta.stdout.String(), "0 diagnostic(s)")
}

func TestBuildFailureExit1NoRetry(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.BuildFunc = func(spec engine.BuildSpec) error {
		_, _ = spec.Log.Write([]byte("RUN false\nError: exit status 1\n"))
		return errors.New("exit status 1")
	}
	assert.Equal(t, ExitFailure, ta.run("build"))
	assert.Equal(t, 1, ta.eng.CountOp("build"))
	assert.Contains(t, ta.stdout.String(), "build failed")
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "build.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "RUN false")
	assert.Contains(t, ta.stderr.String(), "last ", "the log tail is printed")
	assert.Contains(t, ta.stderr.String(), "  Error: exit status 1")
}

func TestBuildVerifyFailureExit1(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.failing["go"] = true
	assert.Equal(t, ExitFailure, ta.run("build", "--verify"))
	assert.Contains(t, ta.stdout.String(), "FAIL")
	assert.Contains(t, ta.stdout.String(), "1 failed")
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "verify.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "failed: 1")
}

func TestVerifyStandaloneExitCodes(t *testing.T) {
	ta := builtProject(t)
	ta.eng.failing["go"] = true
	runs := ta.eng.CountOp("run")
	assert.Equal(t, ExitFailure, ta.run("verify"), ta.stderr.String())
	assert.Greater(t, ta.eng.CountOp("run"), runs, "the checks ran in the image")
	assert.Regexp(t, `(?m)^go\s+FAIL \(`, ta.stdout.String())
	assert.Contains(t, ta.stdout.String(), ", 1 failed\n")
	data, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "verify.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "failed: 1")

	// No build yet: no plan to verify against.
	fresh := newTestApp(t)
	fresh.project(t)
	assert.Equal(t, ExitUsage, fresh.run("verify"))
	assert.Contains(t, fresh.stderr.String(), `run "sandcast build --target openshell" first`)
	assert.Zero(t, fresh.eng.CountOp("run"), "nothing runs without a plan")
}

func TestVerifyRefusesWhileABuildRuns(t *testing.T) {
	ta := builtProject(t)
	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	unlock, err := build.LockDir(dir)
	require.NoError(t, err)
	defer unlock()
	runs := ta.eng.CountOp("run")
	assert.Equal(t, ExitFailure, ta.run("verify"))
	assert.Contains(t, ta.stderr.String(), "another sandcast build is running for target openshell in "+dir+"; wait for it to finish")
	assert.Equal(t, runs, ta.eng.CountOp("run"), "no check runs while a build holds the directory")
	assert.NoFileExists(t, filepath.Join(dir, "verify.yaml"))
}

func TestBuildUnresolvedExit2(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "  - key: zsh\n", "  - key: zsh\n  - key: mystery\n", 1))
	assert.Equal(t, ExitUsage, ta.run("build"))
	assert.Contains(t, ta.stderr.String(), "unresolved: mystery")
	assert.Zero(t, ta.eng.CountOp("build"))
}

func TestBuildContainerTarget(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.Digests["registry.fedoraproject.org/fedora:42"] = testDigest
	require.Equal(t, 0, ta.run("build", "--target", "container", "--verify"), ta.stderr.String())
	dir := filepath.Join(ta.dir, ".sandcast", "build", "container")
	domains, err := os.ReadFile(filepath.Join(dir, "allowed-domains.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(domains), "proxy.golang.org\n")
	assert.Contains(t, string(domains), "github.com\n")
	cf, err := os.ReadFile(filepath.Join(dir, "Containerfile"))
	require.NoError(t, err)
	assert.NotContains(t, string(cf), "/etc/openshell/policy.yaml")
	assert.NotContains(t, string(cf), "nftables")
	_, err = os.Stat(filepath.Join(dir, "policy.yaml"))
	assert.True(t, os.IsNotExist(err))
}

func TestBuildWithClaudeCodeHarness(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", testManifest+`harnesses:
  - name: claude-code
    mcp_servers:
      - name: docs
        url: https://docs.example.com/mcp
        env:
          DOCS_TOKEN: ${SANDCAST_SECRET_1}
`)
	ta.write(t, ".sandcast/mise.lock", testLock+`
[[tools.claude]]
version = "2.1.0"
backend = "aqua:anthropics/claude-code"
specifiers = ["latest"]

[tools.claude."platforms.linux-arm64"]
checksum = "sha256:9bf234ea70ffec9347fdf6b22ce4add51717d3386a38a441e8c8743fceb5eaee"
url = "https://example.com/claude-linux-arm64.tar.gz"
`)
	require.Equal(t, 0, ta.run("build", "--json"), ta.stderr.String())
	dir := filepath.Join(ta.dir, ".sandcast", "build", "openshell")
	cf, err := os.ReadFile(filepath.Join(dir, "Containerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(cf), "# 70-harness")
	assert.Contains(t, string(cf), "CLAUDE_CONFIG_DIR")
	_, err = os.Stat(filepath.Join(dir, "profiles", "mcp-docs.yaml"))
	assert.NoError(t, err, "uncovered MCP endpoint gets a project-local profile")
	_, err = os.Stat(filepath.Join(dir, "profiles", "claude-code.yaml"))
	assert.NoError(t, err)
	pol, err := os.ReadFile(filepath.Join(dir, "policy.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(pol), "/opt/sandcast/claude-code")
}

func TestBuildInterruptedExit130KeepsDebugImage(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ctx, cancel := context.WithCancel(context.Background())
	ta.eng.BuildFunc = func(spec engine.BuildSpec) error {
		if strings.HasSuffix(spec.Containerfile, build.FinalContainerfileName) {
			cancel()
			return context.Canceled
		}
		return nil
	}
	code := ta.Run(ctx, []string{"--project", ta.dir, "build"})
	assert.Equal(t, ExitInterrupted, code)
	assert.Contains(t, ta.stderr.String(), "interrupted")
	assert.Equal(t, 1, ta.eng.CountOp("tag"), "the stage image is kept as the debug tag")
	assert.Contains(t, ta.eng.ImageNames(), "localhost/demo:sandcast-debug", "tagged although the context was cancelled")
	for _, name := range ta.eng.ImageNames() {
		assert.NotContains(t, name, "localhost/demo:sandcast-stage", "the per-run stage tag is removed")
	}
	assert.Contains(t, ta.stdout.String(), "stage image kept as localhost/demo:sandcast-debug")
}

func TestReadLineHonoursCancel(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	a := &App{Stdin: r}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.readLine(ctx)
	assert.ErrorIs(t, err, context.Canceled, "a prompt does not swallow Ctrl-C")

	a = &App{Stdin: strings.NewReader("yes\n")}
	line, err := a.readLine(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "yes\n", line)

	// A piped answer without a newline, and empty stdin, are read, not errors.
	line, err = (&App{Stdin: strings.NewReader("y")}).readLine(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "y", line)
	line, err = (&App{Stdin: strings.NewReader("")}).readLine(context.Background())
	require.NoError(t, err)
	assert.Empty(t, line)
}

// TestBuildPrintsPlanNotices checks sandcast build reports the plan notices,
// including each relocated dotfile, so a user who only runs build sees what
// was relocated or re-provisioned (spec 003 FR-010).
func TestBuildPrintsPlanNotices(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "targets:", "dotfiles:\n  - path: conf/.tmux.conf\ntargets:", 1))
	ta.write(t, "conf/.tmux.conf", "set -g mouse on\n")
	require.Equal(t, 0, ta.run("build"), ta.stderr.String())
	assert.Contains(t, ta.stdout.String(), "notice: conf/.tmux.conf relocated to /etc/tmux.conf", "the build report repeats the plan notices")
}

// TestBuildReportRepeatsPlanNotices checks that plan notices reach the JSON
// report and the console as notice: lines (FR-002).
func TestBuildReportRepeatsPlanNotices(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.opts.project = ta.dir
	env, err := ta.pipelineEnv()
	require.NoError(t, err)
	notice := plan.Notice(plan.NoticeLockfileShadow, "gh", "base image has 2.93.0, mise.lock pins 2.102.0 for linux/arm64; installing 2.102.0")
	planned := &pipeline.Planned{Plan: &plan.Plan{Notices: []string{notice}}}
	built := &pipeline.Built{Build: &build.Result{Image: "localhost/demo:latest", Containerfile: filepath.Join(ta.dir, ".sandcast", "build", "openshell", "Containerfile")}}
	rep := buildReportOf(env, planned, built, nil)
	assert.Equal(t, []string{notice}, rep.Notices)

	data, err := json.Marshal(rep)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"notices":["lockfile-shadow: gh: `)

	printBuildReport(ta.App, rep)
	assert.Contains(t, ta.stdout.String(), "notice: "+notice+"\n")

	empty := buildReportOf(env, nil, nil, nil)
	data, err = json.Marshal(empty)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"notices":[]`)
}

// TestVerifySessionStartupFailureExit1 checks that a failing session check
// fails verify and build --verify with exit status 1 and shows up in the
// session group and in the JSON condition field (FR-010).
func TestVerifySessionStartupFailureExit1(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.failing["session.zsh.startup"] = true
	assert.Equal(t, ExitFailure, ta.run("build", "--verify"))
	out := ta.stdout.String()
	assert.Contains(t, out, "session checks (cleared environment as in an OpenShell session):")
	assert.Regexp(t, `session\.zsh\.startup\s+session:zsh\s+FAIL`, out)
	assert.Regexp(t, `session\.bash-login\.go\s+session:bash-login\s+ok`, out)
	assert.Contains(t, out, "1 failed")

	ta.stdout.Reset()
	assert.Equal(t, ExitFailure, ta.run("verify", "--json"))
	var rep struct {
		Results []struct {
			ComponentKey string `json:"component_key"`
			Condition    string `json:"condition"`
			OK           bool   `json:"ok"`
		} `json:"results"`
		Failed int `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	assert.Equal(t, 1, rep.Failed)
	found := false
	for _, r := range rep.Results {
		if r.ComponentKey == "session.zsh.startup" {
			found = true
			assert.Equal(t, "session:zsh", r.Condition)
			assert.False(t, r.OK)
		}
		if r.ComponentKey == "go" {
			assert.Equal(t, "image", r.Condition)
		}
	}
	assert.True(t, found)

	ta.eng.failing = map[string]bool{}
	ta.stdout.Reset()
	assert.Equal(t, 0, ta.run("verify"))
	assert.Contains(t, ta.stdout.String(), " 0 failed")
}

// TestBuildReportImageRefAndPush covers FR-011, FR-013 and SC-006 for both
// targets and both push states.
func TestBuildReportImageRefAndPush(t *testing.T) {
	digest := "sha256:" + strings.Repeat("cd", 32)
	t.Run("openshell without push", func(t *testing.T) {
		ta := newTestApp(t)
		ta.project(t)
		require.Equal(t, 0, ta.run("build", "--json"), ta.stderr.String())
		var rep map[string]any
		require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
		assert.Equal(t, "localhost/demo:latest", rep["image_ref"])
		assert.Equal(t, "openshell sandbox create --from localhost/demo:latest", rep["sandbox_create"])
		assert.Equal(t, false, rep["pushed"], "pushed is always present")
		assert.NotContains(t, rep, "digest")

		ta.stdout.Reset()
		require.Equal(t, 0, ta.run("build"))
		out := ta.stdout.String()
		assert.Regexp(t, `image ref:\s+localhost/demo:latest`, out)
		assert.Regexp(t, `pushed:\s+no`, out)
		assert.Contains(t, out, "sandbox create: openshell sandbox create --from localhost/demo:latest\n")
	})
	t.Run("openshell docker hub without push", func(t *testing.T) {
		ta := newTestApp(t)
		ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "image: localhost/demo\n", "image: docker.io/rhuss/demo\n", 1))
		ta.write(t, ".sandcast/mise.lock", testLock)
		require.Equal(t, 0, ta.run("build", "--json"), ta.stderr.String())
		var rep buildReport
		require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
		assert.False(t, rep.Pushed)
		for _, n := range rep.Notices {
			assert.NotContains(t, n, "docker-hub", "the rate-limit notice needs a push (FR-013)")
		}
	})
	t.Run("openshell pushed to docker hub", func(t *testing.T) {
		ta := newTestApp(t)
		ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "image: localhost/demo\n", "image: docker.io/rhuss/demo\n    push: true\n", 1))
		ta.write(t, ".sandcast/mise.lock", testLock)
		ta.eng.PushDigests = map[string]string{"docker.io/rhuss/demo:latest": digest}
		require.Equal(t, 0, ta.run("build", "--json"), ta.stderr.String())
		var rep buildReport
		require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
		assert.True(t, rep.Pushed)
		assert.Equal(t, digest, rep.Digest)
		assert.Equal(t, "docker.io/rhuss/demo:latest", rep.ImageRef)
		assert.Equal(t, "openshell sandbox create --from docker.io/rhuss/demo:latest", rep.SandboxCreate)
		found := false
		for _, n := range rep.Notices {
			if strings.HasPrefix(n, "docker-hub: image: anonymous pulls from cluster nodes are rate limited") {
				found = true
				assert.Contains(t, n, "pull secret", "FR-013 names the pull secret")
			}
		}
		assert.True(t, found, "docker-hub notice: %v", rep.Notices)
	})
	t.Run("container target pushed elsewhere", func(t *testing.T) {
		ta := newTestApp(t)
		ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "image: localhost/demo-plain\n", "image: quay.io/rhuss/demo-plain\n    push: true\n", 1))
		ta.write(t, ".sandcast/mise.lock", testLock)
		ta.eng.PushDigests = map[string]string{"quay.io/rhuss/demo-plain:latest": digest}
		require.Equal(t, 0, ta.run("build", "--target", "container", "--json"), ta.stderr.String())
		var rep buildReport
		require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
		assert.True(t, rep.Pushed)
		assert.Equal(t, "quay.io/rhuss/demo-plain:latest", rep.ImageRef)
		assert.Empty(t, rep.SandboxCreate, "no sandbox command for the container target")
		for _, n := range rep.Notices {
			assert.NotContains(t, n, "docker-hub")
		}
	})
	t.Run("push fails", func(t *testing.T) {
		ta := newTestApp(t)
		ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "image: localhost/demo\n", "image: quay.io/rhuss/demo\n    push: true\n", 1))
		ta.write(t, ".sandcast/mise.lock", testLock)
		ta.eng.PushErr = map[string]error{"quay.io/rhuss/demo:latest": errors.New("unauthorized")}
		assert.Equal(t, ExitFailure, ta.run("build", "--json"))
		var rep buildReport
		require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
		assert.False(t, rep.Pushed)
		assert.Contains(t, rep.Error, "unauthorized")
		assert.Empty(t, rep.SandboxCreate, "no start command for a failed build")
		assert.Equal(t, "quay.io/rhuss/demo:latest", rep.ImageRef)
	})
}

const rustManifest = `schema_version: 1
name: demo
base_image: nvidia-community-base
components:
  - key: rust
targets:
  openshell:
    image: localhost/demo
`

const rustLock = `lockfile_version = 3

[[tools.rust]]
version = "1.95.0"
backend = "core:rust"
specifiers = ["latest"]
`

// TestEmulationNotice covers FR-016: an emulation-unsafe component in a
// cross-architecture build prints a notice on plan and build and lands in
// the build report, while the plan file stays byte-identical; a matching
// engine host or an engine that cannot report its architecture yields none.
func TestEmulationNotice(t *testing.T) {
	planJSON := func(arch string) (string, string) {
		ta := newTestApp(t)
		ta.write(t, ".sandcast/manifest.yaml", rustManifest)
		ta.write(t, ".sandcast/mise.lock", rustLock)
		ta.eng.HostArchitecture = arch
		require.Equal(t, 0, ta.run("plan", "--platform", "linux/amd64", "--json"), ta.stderr.String())
		return ta.stdout.String(), ta.stderr.String()
	}
	crossOut, crossErr := planJSON("arm64")
	assert.Contains(t, crossErr, "notice: emulation: rust: building linux/amd64 on an arm64 engine host; rust is known to fail under QEMU user emulation, see docs/guides/cross-arch.md")
	nativeOut, nativeErr := planJSON("amd64")
	assert.NotContains(t, nativeErr, "emulation")
	unknownOut, unknownErr := planJSON("")
	assert.NotContains(t, unknownErr, "emulation", "a HostArch error yields no notice")
	assert.Equal(t, nativeOut, crossOut, "plan --json is byte-identical with and without the notice")
	assert.Equal(t, nativeOut, unknownOut)
	assert.NotContains(t, crossOut, "emulation")

	ta := newTestApp(t)
	ta.write(t, ".sandcast/manifest.yaml", rustManifest)
	ta.write(t, ".sandcast/mise.lock", rustLock)
	ta.eng.HostArchitecture = "arm64"
	require.Equal(t, 0, ta.run("build", "--platform", "linux/amd64", "--json"), ta.stderr.String())
	var rep buildReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
	found := false
	for _, n := range rep.Notices {
		found = found || strings.HasPrefix(n, "emulation: rust: ")
	}
	assert.True(t, found, "build report carries the emulation notice: %v", rep.Notices)
	errOut := ta.stderr.String()
	at := strings.Index(errOut, "notice: emulation: rust: ")
	require.GreaterOrEqual(t, at, 0, "build prints the notice on stderr: %s", errOut)
	assert.Less(t, at, strings.Index(errOut, "building "), "the notice appears before the build starts")
	plan, err := os.ReadFile(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "plan.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(plan), "emulation", "the plan file never carries engine facts")
}

const remoteGateways = `[{"active": true, "name": "cluster", "type": "remote", "is_remote": true}]`

// TestBuildGatewayWarning covers FR-014 for build: a localhost/ image with a
// remote active gateway is warned about when the openshell CLI is present;
// a failed lookup or an absent CLI never fails the build.
func TestBuildGatewayWarning(t *testing.T) {
	withCLI := func(ta *testApp) {
		ta.LookPath = func(file string) (string, error) {
			if file == "podman" || file == "openshell" {
				return "/usr/bin/" + file, nil
			}
			return "", errors.New("not found")
		}
	}
	notices := func(t *testing.T, ta *testApp) []string {
		t.Helper()
		require.Equal(t, 0, ta.run("build", "--json"), ta.stderr.String())
		var rep buildReport
		require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep))
		return rep.Notices
	}
	hasGateway := func(ns []string) bool {
		for _, n := range ns {
			if strings.HasPrefix(n, "gateway: image: the active gateway cluster is remote and cannot pull localhost/demo:latest") {
				assert.Contains(t, n, "targets.openshell.image", "the warning names the manifest fields (US3 scenario 5)")
				assert.Contains(t, n, "targets.openshell.push")
				return true
			}
		}
		return false
	}

	ta := newTestApp(t)
	ta.project(t)
	withCLI(ta)
	ta.gw.gateways = remoteGateways
	ns := notices(t, ta)
	assert.True(t, hasGateway(ns), "%v", ns)

	ta = newTestApp(t)
	ta.project(t)
	withCLI(ta)
	ta.gw.gateways = `[{"active": true, "name": "local", "type": "local", "is_remote": false}]`
	assert.False(t, hasGateway(notices(t, ta)), "a local gateway shares the engine storage")

	ta = newTestApp(t)
	ta.project(t)
	withCLI(ta)
	assert.False(t, hasGateway(notices(t, ta)), "a failed lookup yields no warning and the build succeeds")

	ta = newTestApp(t)
	ta.project(t)
	ta.gw.gateways = remoteGateways
	assert.False(t, hasGateway(notices(t, ta)), "without the openshell CLI there is no lookup")
	for _, c := range ta.gw.calls {
		assert.NotContains(t, strings.Join(c, " "), "gateway list")
	}
}

func TestBuildReportNoticeOrder(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.opts.project = ta.dir
	env, err := ta.pipelineEnv()
	require.NoError(t, err)
	planned := &pipeline.Planned{Plan: &plan.Plan{Target: "openshell", Notices: []string{"lockfile-shadow: gh: x"}}}
	built := &pipeline.Built{Build: &build.Result{Image: "docker.io/rhuss/demo:latest", Pushed: true}}
	rep := buildReportOf(env, planned, built, []string{"emulation: rust: y"})
	require.Len(t, rep.Notices, 3)
	assert.True(t, strings.HasPrefix(rep.Notices[0], "lockfile-shadow: "))
	assert.True(t, strings.HasPrefix(rep.Notices[1], "emulation: "))
	assert.True(t, strings.HasPrefix(rep.Notices[2], "docker-hub: "))
}

// TestPrintVerifyNotes: a session note is printed once per component, not
// once per session condition.
func TestPrintVerifyNotes(t *testing.T) {
	ta := newTestApp(t)
	note := "nft does not resolve in the image environment; not checked in the session"
	printVerify(ta.App, &verify.Report{Image: verify.ImageRef{Reference: "localhost/demo:latest"}, Results: []verify.Result{
		{ComponentKey: "nftables", Condition: verify.ConditionImage, OK: true, Status: "present"},
		{ComponentKey: "session.bash-login.nftables", Condition: verify.ConditionSessionBashLogin, OK: true, Status: "present", Notes: []string{note}},
		{ComponentKey: "session.zsh.nftables", Condition: verify.ConditionSessionZsh, OK: true, Status: "present", Notes: []string{note}},
	}, Passed: 3})
	out := ta.stdout.String()
	assert.Equal(t, 1, strings.Count(out, "note: nftables: "+note+"\n"), out)
}
