package skills

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snippet is one sh fenced block of a SKILL.md.
type snippet struct {
	skill string
	line  int
	text  string
}

// shellSnippets extracts every ```sh block of every embedded SKILL.md.
func shellSnippets(t *testing.T) []snippet {
	t.Helper()
	var out []snippet
	files := skillFiles(t)
	dirs := make([]string, 0, len(files))
	for d := range files {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		lines := strings.Split(files[dir], "\n")
		for i := 0; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != "```sh" {
				continue
			}
			start := i + 1
			var body []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
				body = append(body, lines[i])
			}
			out = append(out, snippet{skill: dir, line: start, text: strings.Join(body, "\n") + "\n"})
		}
	}
	return out
}

var (
	placeholderRe = regexp.MustCompile(`<([^<>\n]+)>`)
	// sandcastLineRe finds a line that runs the sandcast command.
	sandcastLineRe = regexp.MustCompile(`(?m)^\s*sandcast `)
)

// fillPlaceholders replaces <a|b> with a and any other <...> token with a
// fixed word, so a snippet becomes runnable.
func fillPlaceholders(s string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		switch {
		case inner == "harness":
			return "claude-code"
		case inner == "key":
			return "helix"
		case strings.Contains(inner, "|"):
			first, _, _ := strings.Cut(inner, "|")
			return first
		default:
			return "x"
		}
	})
}

// stubs puts fake sandcast, curl, jq, tar and openshell executables in a
// directory. The sandcast stub exits with $SANDCAST_STUB_RC, or first sends
// $SANDCAST_STUB_SIGNAL to the shell that runs it, as an interrupt would;
// none reads standard input.
func stubs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"sandcast":  "#!/bin/sh\nif [ -n \"$SANDCAST_STUB_SIGNAL\" ]; then kill -\"$SANDCAST_STUB_SIGNAL\" \"$PPID\"; sleep 1; fi\nexit \"${SANDCAST_STUB_RC:-0}\"\n",
		"curl":      "#!/bin/sh\nexit 0\n",
		"jq":        "#!/bin/sh\nexit 0\n",
		"tar":       "#!/bin/sh\nexit 0\n",
		"openshell": "#!/bin/sh\necho 'openshell 0.1.3'\nexit 0\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755))
	}
	return dir
}

// TestSnippets runs every sh snippet of the skill bundle under sh, bash and
// zsh with the sandcast stub exiting 0 and 2, and with an interrupt while
// sandcast runs (FR-022, SC-009): the snippet ends with sandcast's exit
// status (128+signal after an interrupt), and leaves no temporary file
// behind on any of these paths. A missing shell fails the test.
func TestSnippets(t *testing.T) {
	shells := map[string]string{}
	for _, sh := range []string{"sh", "bash", "zsh"} {
		p, err := exec.LookPath(sh)
		require.NoError(t, err, "%s is required to test the skill snippets", sh)
		shells[sh] = p
	}
	snippets := shellSnippets(t)
	require.NotEmpty(t, snippets)
	bin := stubs(t)
	for _, sn := range snippets {
		script := fillPlaceholders(sn.text)
		usesSandcast := sandcastLineRe.MatchString(script)
		for _, shName := range []string{"sh", "bash", "zsh"} {
			for _, rc := range []int{0, 2, 130} {
				// Only a snippet with a temporary file has an interrupt path
				// to test; a plain command may be exec'd by the shell, so the
				// stub's parent would be this test.
				if rc == 130 && (!usesSandcast || !strings.Contains(script, "mktemp")) {
					continue
				}
				name := sn.skill + ":" + itoa(sn.line) + "/" + shName + "/rc" + itoa(rc)
				t.Run(name, func(t *testing.T) {
					tmp := t.TempDir()
					project := t.TempDir()
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, shells[shName], "-c", script)
					cmd.Dir = project
					cmd.Env = []string{
						"PATH=" + bin + ":/usr/bin:/bin",
						"HOME=" + project,
						"TMPDIR=" + tmp,
						"SANDCAST_STUB_RC=" + itoa(rc),
					}
					if rc == 130 {
						// The stub itself exits 0, so 130 can only come from
						// the interrupt path.
						cmd.Env[len(cmd.Env)-1] = "SANDCAST_STUB_RC=0"
						cmd.Env = append(cmd.Env, "SANDCAST_STUB_SIGNAL=INT")
					}
					out, err := cmd.CombinedOutput()
					require.NoError(t, ctx.Err(), "snippet timed out: %s", out)
					code := 0
					if ee, ok := err.(*exec.ExitError); ok {
						code = ee.ExitCode()
					} else {
						require.NoError(t, err)
					}
					want := 0
					if usesSandcast {
						want = rc
					}
					assert.Equal(t, want, code, "exit status of\n%s\noutput:\n%s", script, out)
					assert.NotContains(t, string(out), "read-only variable", "the snippet assigns a special parameter")
					left, err := os.ReadDir(tmp)
					require.NoError(t, err)
					assert.Empty(t, left, "temporary files left behind by\n%s", script)
				})
			}
		}
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
