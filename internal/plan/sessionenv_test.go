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
	testutil.Golden(t, "testdata/sandcast-env.sh.golden", []byte(defaultSessionEnvScript))
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
		assert.Contains(t, defaultSessionEnvScript, "export "+k[1]+"='"+k[2]+"'\n", k[1])
	}
}

// TestSessionEnvScriptShells sources the script under every available
// shell with a PATH that lacks the shims, twice, and checks that the shims
// come first exactly once and the MISE_* variables are exported.
func TestSessionEnvScriptShells(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sandcast-env.sh")
	require.NoError(t, os.WriteFile(script, []byte(defaultSessionEnvScript), 0o644))
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
	require.NoError(t, os.WriteFile(script, []byte(defaultSessionEnvScript), 0o644))
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
// once each; a directory the script would have to quote is left out (I-1),
// and so is a directory under the overmount home (the workspace mounted
// there may come from an untrusted project; a command looked up in it during
// shell startup would run project code), which the second result names.
// SessionPathDirsFor applies the plan's own target and overmount home, so no
// caller can leave the prefix out.
func TestSessionPathDirs(t *testing.T) {
	base := []string{"/opt/mise/shims", "/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/opt/x/bin/", "/usr/bin",
		"/sbin", "/bin", "/usr/sbin", "/opt/has space", "relative/bin", "/opt/$HOME", "/sandbox/.local/bin", "/sandbox/.venv/bin", "/sandbox/.local/bin"}
	all := []string{"/usr/local/sbin", "/usr/sbin", "/opt/x/bin", "/sbin", "/sandbox/.local/bin", "/sandbox/.venv/bin"}
	assert.Equal(t, all, sessionPathDirs(base, ""), "no overmount: every plain extra directory")
	assert.Equal(t, []string{"/usr/local/sbin", "/usr/sbin", "/opt/x/bin", "/sbin"}, sessionPathDirs(base, "/sandbox"))
	assert.Equal(t, all, sessionPathDirs(base, "/sandboxes"), "the overmount home is matched as a path prefix, not a string prefix")
	assert.Empty(t, sessionPathDirs([]string{"/usr/local/bin", "/usr/bin", "/bin"}, "/sandbox"))
	assert.Empty(t, sessionPathDirs(nil, "/sandbox"))

	// The split the image PATH and the notices use: outside keeps every
	// entry once, cleaned, in order; hidden collects the entries under the
	// overmount home and the ones that are not absolute (a relative entry,
	// a tilde entry, and the empty entry, which is the current directory),
	// which resolve into the mounted workspace. Without an overmount home
	// nothing is hidden.
	outside, hidden := splitOvermountPath([]string{"/sandbox/.venv/bin", "/usr/local/bin", "/usr/bin/", "~/.local/bin", ".", "", "bin", "/usr/local/bin", "/sandbox", "/bin", "/sandboxes/bin", "/sandbox/.venv/bin"}, "/sandbox")
	assert.Equal(t, []string{"/usr/local/bin", "/usr/bin", "/bin", "/sandboxes/bin"}, outside)
	assert.Equal(t, []string{"/sandbox/.venv/bin", "~/.local/bin", ".", "bin", "/sandbox"}, hidden, "once each, in order; the empty entry is the current directory")
	outside, hidden = splitOvermountPath([]string{"/sandbox/.venv/bin", ".", "/usr/bin"}, "")
	assert.Equal(t, []string{"/sandbox/.venv/bin", ".", "/usr/bin"}, outside)
	assert.Empty(t, hidden)

	p := &Plan{Target: "openshell", ImageFacts: ImageFacts{Path: base}}
	assert.Equal(t, []string{"/usr/local/sbin", "/usr/sbin", "/opt/x/bin", "/sbin"}, SessionPathDirsFor(p), "the default overmount home applies")
	p.BaseImage.OvermountHome = "/home/dev"
	assert.Equal(t, all, SessionPathDirsFor(p), "a base image with another overmount home keeps /sandbox directories")
	p.ImageFacts.Path = append(p.ImageFacts.Path, "/home/dev/.local/bin")
	assert.Equal(t, all, SessionPathDirsFor(p), "and drops its own")
	assert.Nil(t, SessionPathDirsFor(&Plan{Target: "container", ImageFacts: ImageFacts{Path: base}}), "the container target gets no extra directories")
}

// TestSessionEnvScriptStripsOvermountPath: a session that keeps the image
// environment (the OpenShell default agent) or a runtime that set PATH
// itself inherits a PATH; the OpenShell script drops every entry equal to
// or under the overmount home from it once, in the script body, before
// _sandcast_path puts the shims and the base image directories first (I1 of
// the round 2 review). The strip is not part of _sandcast_path, so the bash
// prompt repair leaves a directory under the home that a curated rc or the
// user put on PATH later. The container target's script has no strip.
func TestSessionEnvScriptStripsOvermountPath(t *testing.T) {
	p := &Plan{Target: "openshell", ImageFacts: ImageFacts{Path: []string{"/usr/local/sbin", "/usr/local/bin", "/usr/bin", "/sandbox/.venv/bin", "/bin"}}}
	text := SessionEnvScriptFor(p)
	i := strings.Index(text, "case $_sandcast_pd in \"\" | [!/]* | /sandbox | /sandbox/*) ;;")
	require.GreaterOrEqual(t, i, 0, text)
	assert.Less(t, i, strings.Index(text, "_sandcast_path() {"), "the strip runs before the function is defined and called")
	assert.NotContains(t, strings.SplitN(text, "_sandcast_path() {", 2)[1], "_sandcast_pd", "the strip is not part of _sandcast_path")
	assert.Contains(t, text, `if [ -z "${SANDCAST_PATH_STRIPPED-}" ]; then`, "the strip runs once per session")
	assert.Contains(t, text, "PATH=$_sandcast_pnew SANDCAST_PATH_STRIPPED=1\n\texport PATH SANDCAST_PATH_STRIPPED\n")
	assert.NotContains(t, SessionEnvScriptFor(&Plan{Target: "container", ImageFacts: p.ImageFacts}), "_sandcast_pd", "no strip on the container target")

	dir := t.TempDir()
	script := filepath.Join(dir, "sandcast-env.sh")
	require.NoError(t, os.WriteFile(script, []byte(text), 0o644))
	// The inherited PATH as the community base image sets it, plus the home
	// itself, a directory whose name only starts like the home, an empty
	// entry (the current directory, the workspace in a session), a relative
	// entry and a tilde entry, which resolve into the workspace as well.
	inherited := "PATH=/sandbox/.venv/bin:/usr/local/bin:/sandbox:/usr/bin::bin:~/.local/bin:/sandboxes/bin:/bin:/sandbox/.local/bin"
	stripped := MiseShimsDir + ":/usr/local/sbin:/usr/local/bin:/usr/bin:/sandboxes/bin:/bin"
	ran := 0
	for _, sh := range []string{"sh", "dash", "bash", "zsh"} {
		path, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		ran++
		cmd := exec.Command(path, "-c", `. "$1"; printf '%s\n' "$PATH"; PATH=/sandbox/bin:$PATH; _sandcast_path; printf '%s\n' "$PATH"; printf '%s\n' "${_sandcast_pd-unset}${_sandcast_prest-unset}${_sandcast_pnew-unset}"; env | grep '^SANDCAST_PATH_STRIPPED='`, sh, script)
		cmd.Env = []string{inherited, "HOME=/sandbox"}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", sh, out)
		assert.Equal(t, stripped+"\n"+
			MiseShimsDir+":/sandbox/bin:"+stripped+"\n"+
			"unsetunsetunset\nSANDCAST_PATH_STRIPPED=1\n", string(out),
			"%s: the inherited entries under the overmount home and the ones that are not absolute are gone; a later one survives the repair, which only puts the shims first again; the loop variables are unset; the marker is exported", sh)

		// Once per session: a child shell that sources the script again (a
		// zsh through the global zshenv, a login bash, the launchers) keeps
		// the directory under the home that the session put on PATH later,
		// such as an activated virtual environment, and a shell started with
		// a cleared environment or the image environment, which never carry
		// the marker, strips again.
		cmd = exec.Command(path, "-c", `. "$1"; PATH=/sandbox/.venv/bin:$PATH; "$0" -c '. "$1"; printf "%s\n" "$PATH"' "$0" "$1"; env -i PATH="$PATH" HOME=/sandbox "$0" -c '. "$1"; printf "%s\n" "$PATH"' "$0" "$1"`, sh, script)
		cmd.Env = []string{inherited, "HOME=/sandbox"}
		out, err = cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", sh, out)
		assert.Equal(t, MiseShimsDir+":/sandbox/.venv/bin:"+stripped+"\n"+stripped+"\n", string(out),
			"%s: a child shell keeps the activated venv; a fresh environment strips once more", sh)
	}
	assert.Positive(t, ran)
	if zsh, err := exec.LookPath("zsh"); err == nil {
		// zsh -f reads no startup file, so sourcing the script is the only
		// thing the child does, as the global zshenv block would.
		cmd := exec.Command(zsh, "-f", "-c", `. "$1"; PATH=/sandbox/.venv/bin:$PATH; zsh -f -c '. "$1"; print -r -- "$PATH"' zsh "$1"`, "zsh", script)
		cmd.Env = []string{inherited, "HOME=/sandbox"}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		assert.Equal(t, MiseShimsDir+":/sandbox/.venv/bin:"+stripped+"\n", string(out), "a child zsh keeps the activated venv")
	}
}

// TestSessionEnvScriptBasePath sources the OpenShell script with the
// OpenShell default PATH under every available shell, twice: the shims come
// first, then the base image's extra directories in their order, then the
// default PATH, each once. The container target gets no extra directories.
func TestSessionEnvScriptBasePath(t *testing.T) {
	p := &Plan{Target: "openshell", ImageFacts: ImageFacts{Path: []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/opt/x/bin", "/sandbox/.venv/bin", "/bin"}}}
	text := SessionEnvScriptFor(p)
	assert.NotContains(t, text, "/sandbox/.venv/bin", "a base image PATH directory under the overmount home never reaches a session PATH")
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
