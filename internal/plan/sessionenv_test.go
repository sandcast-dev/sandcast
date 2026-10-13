package plan

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/testutil"
)

func TestSessionEnvScriptGolden(t *testing.T) {
	testutil.Golden(t, "testdata/sandcast-env.sh.golden", []byte(SessionEnvScript))
}

// TestSessionEnvScriptCoversMiseEnv checks that every MISE_* variable the
// 30-mise layer sets with ENV is exported by the session script (FR-006).
func TestSessionEnvScriptCoversMiseEnv(t *testing.T) {
	text, err := MiseFragment(FinalUser{Name: "sandbox", Group: "sandbox"}, "v2026.10.4", "linux-arm64")
	require.NoError(t, err)
	start := strings.Index(text, "\nENV ")
	require.GreaterOrEqual(t, start, 0)
	envBlock := text[start+1:]
	end := strings.Index(envBlock, "\nCOPY")
	require.GreaterOrEqual(t, end, 0)
	envBlock = envBlock[:end]
	keys := regexp.MustCompile(`(MISE_[A-Z_]+)=(\S+)`).FindAllStringSubmatch(envBlock, -1)
	require.NotEmpty(t, keys)
	for _, k := range keys {
		assert.Contains(t, SessionEnvScript, "export "+k[1]+"='"+k[2]+"'\n", k[1])
	}
}

// TestSessionEnvScriptShells sources the script under every available
// shell with a PATH that lacks the shims, twice, and checks that the shims
// come first exactly once and the MISE_* variables are exported.
func TestSessionEnvScriptShells(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sandcast-env.sh")
	require.NoError(t, os.WriteFile(script, []byte(SessionEnvScript), 0o644))
	ran := 0
	for _, sh := range []string{"sh", "dash", "bash", "zsh"} {
		path, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		ran++
		cmd := exec.Command(path, "-c", `. "$1"; . "$1"; printf '%s\n' "$PATH" "$MISE_DATA_DIR"; env | grep -c '^MISE_'`, sh, script)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", sh, out)
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		require.Len(t, lines, 3, "%s: %s", sh, out)
		assert.Equal(t, MiseShimsDir+":/usr/bin:/bin", lines[0], sh)
		assert.Equal(t, MiseDataDir, lines[1], sh)
		assert.Equal(t, "6", strings.TrimSpace(lines[2]), sh)
	}
	require.Positive(t, ran)
}

// TestSessionEnvScriptBashHooks checks the bash hooks of R-05: a login
// non-interactive bash re-applies the shims after a later PATH reset, right
// before the first top-level command, and removes its DEBUG trap; an
// interactive bash appends _sandcast_path to PROMPT_COMMAND once.
func TestSessionEnvScriptBashHooks(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	if out, _ := exec.Command(bash, "-c", `echo "${BASH_VERSINFO[0]}${BASH_VERSINFO[1]}"`).Output(); strings.TrimSpace(string(out)) < "51" {
		t.Skip("bash older than 5.1")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "sandcast-env.sh")
	require.NoError(t, os.WriteFile(script, []byte(SessionEnvScript), 0o644))
	// --login reads ~/.bash_profile: source the script, then reset PATH as
	// the community base image's ~/.bashrc does.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".bash_profile"),
		[]byte(". "+script+"\nexport PATH=/usr/bin:/bin\n"), 0o644))
	login := exec.Command(bash, "-l", "-c", `command -v true >/dev/null; printf '%s\n' "$PATH"; trap -p DEBUG`)
	login.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir}
	out, err := login.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Equal(t, MiseShimsDir+":/usr/bin:/bin\n", string(out), "shims re-applied and trap removed")

	inter := exec.Command(bash, "--norc", "-i")
	inter.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "PS1=$ "}
	inter.Stdin = strings.NewReader(". " + script + "\n. " + script + "\nPROMPT_COMMAND='echo x >/dev/null'\nexport PATH=/usr/bin:/bin\necho \"P=$PATH\"\necho \"PC=${PROMPT_COMMAND[*]}\"\n")
	out, err = inter.Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "P="+MiseShimsDir+":/usr/bin:/bin\n")
	assert.Contains(t, string(out), "PC=echo x >/dev/null _sandcast_path\n")
}

// TestSessionEnvScriptForPlan checks that the plan's component and harness
// environment joins the MISE_* settings, without PATH and duplicates.
func TestSessionEnvScriptForPlan(t *testing.T) {
	p := &Plan{Image: ImageSetup{Env: []EnvVar{
		{Name: "CARGO_HOME", Value: "/opt/cargo"},
		{Name: "MISE_DATA_DIR", Value: "/elsewhere"},
		{Name: "PATH", Value: "/x:$PATH"},
		{Name: "RUSTUP_HOME", Value: "/opt/rustup"},
		{Name: "GREETING", Value: "it's here"},
	}}}
	s := SessionEnvScriptFor(p)
	assert.Contains(t, s, "export MISE_DATA_DIR='/opt/mise'\nexport MISE_CONFIG_DIR=")
	assert.NotContains(t, s, "/elsewhere")
	assert.NotContains(t, s, "export PATH=/x")
	assert.Contains(t, s, "export CARGO_HOME='/opt/cargo'\n")
	assert.Contains(t, s, "export RUSTUP_HOME='/opt/rustup'\n")
	assert.Contains(t, s, "export GREETING='it'\\''s here'\n")
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	out, err := exec.Command(sh, "-c", s+`printf '%s|%s' "$GREETING" "$RUSTUP_HOME"`).Output()
	require.NoError(t, err)
	assert.Equal(t, "it's here|/opt/rustup", string(out))
}

// TestSessionPathDirs: the base image's PATH directories beyond the
// OpenShell default PATH and the shims are kept in the base image's order,
// once each; a directory the script would have to quote is left out (I-1).
func TestSessionPathDirs(t *testing.T) {
	assert.Equal(t, []string{"/usr/local/sbin", "/usr/sbin", "/opt/x/bin", "/sbin", "/sandbox/.local/bin"},
		SessionPathDirs([]string{"/opt/mise/shims", "/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/opt/x/bin/", "/usr/bin",
			"/sbin", "/bin", "/usr/sbin", "/opt/has space", "relative/bin", "/opt/$HOME", "/sandbox/.local/bin"}))
	assert.Empty(t, SessionPathDirs([]string{"/usr/local/bin", "/usr/bin", "/bin"}))
	assert.Empty(t, SessionPathDirs(nil))
}

// TestSessionEnvScriptBasePath sources the OpenShell script with the
// OpenShell default PATH under every available shell, twice: the shims come
// first, then the base image's extra directories in their order, then the
// default PATH, each once. The container target gets no extra directories.
func TestSessionEnvScriptBasePath(t *testing.T) {
	p := &Plan{Target: "openshell", ImageFacts: ImageFacts{Path: []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/opt/x/bin", "/bin"}}}
	text := SessionEnvScriptFor(p)
	assert.NotContains(t, SessionEnvScriptFor(&Plan{Target: "container", ImageFacts: p.ImageFacts}), "/opt/x/bin")
	dir := t.TempDir()
	script := filepath.Join(dir, "sandcast-env.sh")
	require.NoError(t, os.WriteFile(script, []byte(text), 0o644))
	ran := 0
	for _, sh := range []string{"sh", "dash", "bash", "zsh"} {
		path, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		ran++
		cmd := exec.Command(path, "-c", `. "$1"; PATH=/usr/bin:/bin; _sandcast_path; . "$1"; printf '%s\n' "$PATH"`, sh, script)
		cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + dir}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", sh, out)
		assert.Equal(t, MiseShimsDir+":/usr/local/sbin:/usr/sbin:/opt/x/bin:/usr/bin:/bin", strings.TrimSpace(string(out)),
			"%s: a PATH reset is repaired with the base image directories", sh)
	}
	assert.Positive(t, ran)
}
