package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/profile"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const communityBase = "ghcr.io/nvidia/openshell-community/sandboxes/base:latest"

var (
	checkLineRe   = regexp.MustCompile(`(?m)^_check '([^']*)' '([^']*)'`)
	resolveLineRe = regexp.MustCompile(`(?m)^_resolve '([^']*)' '([^']*)'`)
)

// scriptedEngine answers probe, resolve and check scripts like a real image
// would. present lists component keys the base image already has.
type scriptedEngine struct {
	*engine.Fake
	present map[string]string // key -> version line
	failing map[string]bool   // keys whose check fails in the built image
	built   bool
	// exportDir is the profile directory served to profiles export as the
	// image's /usr/share/sandcast/profiles; "" means no build.
	exportDir string
}

func newScriptedEngine() *scriptedEngine {
	s := &scriptedEngine{
		present: map[string]string{"curl": "curl 8.5.0", "ca-certificates": "", "iproute2": "ip utility, iproute2-6.1.0", "nftables": "nftables v1.0.9", "git": "git version 2.43.0"},
		failing: map[string]bool{},
	}
	s.Fake = &engine.Fake{Digests: map[string]string{communityBase: testDigest}}
	s.RunFunc = s.run
	s.BuildFunc = func(spec engine.BuildSpec) error {
		s.built = true
		_, _ = fmt.Fprintf(spec.Log, "STEP 1: FROM ...\nCOMMIT %v\n", spec.Tags)
		return nil
	}
	return s
}

func (s *scriptedEngine) run(spec engine.RunSpec) ([]byte, []byte, error) {
	var out strings.Builder
	if strings.Contains(spec.Script, "os-release") {
		out.WriteString(`{"type":"os","id":"ubuntu","id_like":"debian","name":"Ubuntu","version":"24.04"}` + "\n")
		out.WriteString(`{"type":"pkgmgr","name":"apt-get"}` + "\n")
		out.WriteString(`{"type":"user","name":"sandbox","uid":1000,"gid":1000,"group":"sandbox","home":"/sandbox","shell":"/bin/bash"}` + "\n")
		out.WriteString(`{"type":"shells","list":["/bin/bash","/bin/sh"]}` + "\n")
	}
	inBuilt := strings.Contains(spec.Image, "localhost/")
	for _, m := range checkLineRe.FindAllStringSubmatch(spec.Script, -1) {
		key, bin := m[1], m[2]
		line, ok := s.present[key]
		exit := 127
		if (ok || inBuilt) && !s.failing[key] {
			exit = 0
			if !ok {
				line = key + " 1.25.1"
			}
		} else {
			line = "sh: " + bin + ": not found"
		}
		fmt.Fprintf(&out, `{"type":"check","key":%q,"exit":%d,"path":"/usr/bin/%s","out":%q}`+"\n", key, exit, bin, line)
	}
	for _, m := range resolveLineRe.FindAllStringSubmatch(spec.Script, -1) {
		fmt.Fprintf(&out, `{"component":%q,"bin":%q,"path":"/usr/local/bin/%s","method":"command-v"}`+"\n", m[1], m[2], m[2])
	}
	if strings.Contains(spec.Script, "/usr/share/sandcast/profiles") {
		return s.exportFiles()
	}
	return []byte(out.String()), nil, nil
}

// exportFiles serves the profile directory of the last build for export.
func (s *scriptedEngine) exportFiles() ([]byte, []byte, error) {
	if s.exportDir == "" {
		return nil, nil, errors.New("no build")
	}
	entries, err := os.ReadDir(s.exportDir)
	if err != nil {
		return nil, nil, err
	}
	var out strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(s.exportDir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(&out, "=== %s\n%s\n", e.Name(), data)
	}
	return []byte(out.String()), nil, nil
}

// sentProfile is one profile file the fake gateway received through
// "profile import --file" or "profile update <id> --file".
type sentProfile struct {
	op              string // import or update
	id              string
	resourceVersion uint64
	binaries        []string
	global          bool
}

type fakeGateway struct {
	version  string
	existing []string
	calls    [][]string
	sent     []sentProfile
	// failAfter makes every import after the first failAfter imports fail
	// (0: never).
	failAfter int
}

func (g *fakeGateway) Run(_ context.Context, bin string, argv []string) ([]byte, []byte, error) {
	g.calls = append(g.calls, append([]string{bin}, argv...))
	// Global flags come first, as the openshell CLI expects them.
	for len(argv) >= 2 && argv[0] == "--gateway" {
		argv = argv[2:]
	}
	joined := strings.Join(argv, " ")
	switch {
	case joined == "--version":
		return []byte("openshell " + g.version + "\n"), nil, nil
	case strings.HasPrefix(joined, "profile list"):
		var items []string
		for _, id := range g.existing {
			items = append(items, fmt.Sprintf(`{"id":%q,"resource_version":4,"scope":"workspace","source":"custom"}`, id))
		}
		return []byte("[" + strings.Join(items, ",") + "]"), nil, nil
	case strings.HasPrefix(joined, "profile export"):
		return []byte("id: " + argv[2] + "\nresource_version: 4\ndisplay_name: X\n"), nil, nil
	case strings.HasPrefix(joined, "profile import --file"), strings.HasPrefix(joined, "profile update"):
		if g.failAfter > 0 && len(g.sent) >= g.failAfter {
			return nil, []byte("gateway unavailable"), errors.New("exit status 1")
		}
		if err := g.receive(argv); err != nil {
			return nil, []byte(err.Error()), err
		}
	default:
		// An unknown call fails, so a test cannot pass by accident.
		return nil, []byte("unexpected openshell call: " + joined), errors.New("exit status 2")
	}
	return nil, nil, nil
}

// receive parses the profile file of an import or update call the way the
// gateway would and records what it carries.
func (g *fakeGateway) receive(argv []string) error {
	sp := sentProfile{op: argv[1]}
	file := ""
	for i, a := range argv {
		switch {
		case a == "--file" && i+1 < len(argv):
			file = argv[i+1]
		case a == "--global":
			sp.global = true
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	p, err := profile.Parse(data, file)
	if err != nil {
		return err
	}
	if sp.op == "update" && argv[2] != p.ID {
		return fmt.Errorf("profile update %s got a file for %s", argv[2], p.ID)
	}
	sp.id, sp.resourceVersion, sp.binaries = p.ID, p.ResourceVersion, p.Binaries
	g.sent = append(g.sent, sp)
	return nil
}

type testApp struct {
	*App
	eng *scriptedEngine
	gw  *fakeGateway
	dir string
	// home is the HOME the default Getenv returns, an empty per-test
	// directory, so no test reads the developer's or runner's home.
	home   string
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	t.Setenv("SANDCAST_CACHE_DIR", t.TempDir())
	t.Setenv("SANDCAST_CONFIG_DIR", t.TempDir())
	ta := &testApp{eng: newScriptedEngine(), gw: &fakeGateway{version: "0.1.2"}, dir: t.TempDir(), home: t.TempDir(), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	app := NewApp(ta.stdout, ta.stderr, strings.NewReader(""))
	app.NewEngine = func(string) engine.Engine { return ta.eng }
	app.LookPath = func(file string) (string, error) {
		if file == "podman" {
			return "/usr/bin/podman", nil
		}
		return "", errors.New("not found")
	}
	// The process environment never leaks in: HOME is the test home, every
	// other variable is unset unless a test overrides Getenv.
	app.Getenv = func(k string) string {
		if k == "HOME" {
			return ta.home
		}
		return ""
	}
	// Host tools are never executed.
	app.HostVersion = func(_ context.Context, bin string) string {
		t.Errorf("unexpected host version query for %s", bin)
		return ""
	}
	app.HostPlatform = func() string { return "linux/arm64" }
	app.Gateway = ta.gw
	ta.App = app
	return ta
}

// run executes a command line in the test project and returns the exit code.
func (ta *testApp) run(argv ...string) int {
	ta.stdout.Reset()
	ta.stderr.Reset()
	ta.opts = globalOptions{}
	return ta.Run(context.Background(), append([]string{"--project", ta.dir}, argv...))
}

func (ta *testApp) write(t *testing.T, rel, content string) {
	t.Helper()
	path := filepath.Join(ta.dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

const testManifest = `schema_version: 1
name: demo
base_image: nvidia-community-base
components:
  - key: go
  - key: git
  - key: zsh
shell:
  program: zsh
  rc: |
    autoload -Uz compinit && compinit -C
targets:
  openshell:
    image: localhost/demo
  container:
    image: localhost/demo-plain
`

const testLock = `lockfile_version = 3

[[tools.go]]
version = "1.25.14"
backend = "core:go"
specifiers = ["latest"]

[tools.go."platforms.linux-arm64"]
checksum = "sha256:9bf234ea70ffec9347fdf6b22ce4add51717d3386a38a441e8c8743fceb5eaee"
url = "https://dl.google.com/go/go1.25.14.linux-arm64.tar.gz"
`

// project writes the standard manifest and lockfile.
func (ta *testApp) project(t *testing.T) {
	t.Helper()
	ta.write(t, ".sandcast/manifest.yaml", testManifest)
	ta.write(t, ".sandcast/mise.lock", testLock)
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
