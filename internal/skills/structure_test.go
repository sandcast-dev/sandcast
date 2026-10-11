package skills

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/redact"
)

// mandatoryPhrases are, per skill directory, the steps of
// contracts/skill-bundle.md and the brainstorm 072 lessons the SKILL.md must
// spell out. Every skill directory needs an entry.
var mandatoryPhrases = map[string][]string{
	"capture": {
		"sandcast skills check",
		"Any other failure: show the output and stop.",
		"sandcast harness discover",
		"sandcast components match",
		"sandcast manifest set-shell",
		"sandcast manifest validate",
		"sandcast lock",
		"${SANDCAST_SECRET_N}",
		"autoload -Uz compinit && compinit -i -D",
		"never use `-C`",
		`[[ "$TERM" != "dumb" ]]`,
		"if fzf --zsh &>/dev/null; then",
		"completion zsh",
		"sandcast harness list --json",
		`tmp="$(mktemp)"`,
		"shell.components",
		// Skills and marketplaces outside the project are copied into it,
		// filtered, and committed only after the user confirms the file list.
		"to `.claude/skills/<name>` in the project, and never overwrite an existing directory",
		"leave out every file that holds a credential",
		"Record `source: .claude/skills/<name>`",
		"show the user the list of copied files (paths only, never their contents)",
		"ask them to confirm that none of them holds a credential",
		// Which variable supplies a redacted value at runtime.
		"under the environment variable named in `env_var`",
		"the `env` key when the whole env value was the secret, `SANDCAST_SECRET_N` for a secret inside a longer value",
		// sandcast lock without a usable mise.
		"Exit 3 means the host has no `mise` or one older than the minimum release named in the error message.",
	},
	"build": {
		"sandcast skills check",
		"Any other failure: show the output and stop.",
		"sandcast plan --json",
		"sandcast fragments write",
		"sandcast build --verify",
		"sandcast learnings add",
		"--outcome unfixed",
		"--outcome fixed",
		"--max-attempts",
		"sandcast promote",
		"If `sandcast promote` exits 2",
		"sandcast profiles import",
		"USER root",
		"final_user.name",
		"final_user.home",
		"`RUN`, `USER`, `ENV` and `WORKDIR`",
		"RUN <<'EOF'",
		"RUN bash -euxo pipefail -c",
		"image_facts.shells",
		`tmp="$(mktemp)"`,
		"installer_kind: script",
		"image_facts.package_manager",
		"curl -fsSLI",
		"sha256sum -c",
		"/sandbox",
		// Long-running commands: a harness timeout is neither a failure nor
		// an attempt, and the per-target build lock is never removed by hand.
		"## Long-running commands",
		"A harness timeout is not a failure.",
		"Do not count it as a build attempt",
		"is not an attempt (see \"Long-running commands\")",
		"Never delete `.sandcast/build/<target>/.lock`.",
		// The stall rule signals only the recorded build process, never a
		// pattern match, and keys the threshold off the pass-1 marker.
		"pid=$!",
		"### sandcast pass 1",
		"Never discover a process by pattern",
		`kill -INT "$pid"`,
		// A verify check that ran out of time.
		"A `status` of `timeout` means the check did not finish within its time limit",
	},
}

// forbiddenPhrases are phrases that must NOT appear in a skill's SKILL.md,
// usually the exact wording of a rule that was fixed. The mandatory-phrase
// test only checks that the new wording is present; without this, a partial
// revert that left the old instruction beside the new one would still pass.
var forbiddenPhrases = map[string][]string{
	"build": {
		// The stall rule must signal the recorded pid, never find the build
		// process by pattern. This is the exact old command; the current file
		// mentions pgrep only inside the prohibition ("do not use pgrep -f").
		"pgrep -f 'sandcast build'",
	},
}

// skillFiles returns the SKILL.md text of every embedded skill by directory.
func skillFiles(t *testing.T) map[string]string {
	t.Helper()
	paths, err := fs.Glob(sandcast.Skills, "*/SKILL.md")
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no skills/*/SKILL.md embedded")
	out := map[string]string{}
	for _, p := range paths {
		data, err := fs.ReadFile(sandcast.Skills, p)
		require.NoError(t, err)
		out[path.Dir(p)] = string(data)
	}
	return out
}

// bundleFiles returns the text of every file in the embedded skill bundle by
// path.
func bundleFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(sandcast.Skills, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(sandcast.Skills, p)
		if err != nil {
			return err
		}
		out[p] = string(data)
		return nil
	})
	require.NoError(t, err)
	return out
}

func frontmatter(t *testing.T, dir, text string) map[string]any {
	t.Helper()
	rest, ok := strings.CutPrefix(text, "---\n")
	require.True(t, ok, "%s: SKILL.md must start with a --- line", dir)
	head, _, ok := strings.Cut(rest, "\n---\n")
	require.True(t, ok, "%s: frontmatter is not closed by a --- line", dir)
	var fm map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(head), &fm), dir)
	return fm
}

func TestStructureFrontmatter(t *testing.T) {
	data, err := fs.ReadFile(sandcast.Skills, "VERSION")
	require.NoError(t, err)
	version := strings.TrimSpace(string(data))
	require.NotEmpty(t, version)

	files := skillFiles(t)
	for dir := range mandatoryPhrases {
		assert.Contains(t, files, dir, "skills/%s/SKILL.md is missing", dir)
	}
	for dir, text := range files {
		fm := frontmatter(t, dir, text)
		for _, key := range []string{"name", "description", "sandcast_version"} {
			assert.Contains(t, fm, key, "%s: frontmatter key %s", dir, key)
		}
		assert.Equal(t, "sandcast-"+dir, fm["name"], "%s: name", dir)
		assert.Equal(t, version, fmt.Sprint(fm["sandcast_version"]), "%s: sandcast_version must equal skills/VERSION", dir)
		desc, _ := fm["description"].(string)
		assert.NotEmpty(t, strings.TrimSpace(desc), "%s: description", dir)
		assert.NotContains(t, desc, "\n", "%s: description must be one line", dir)
		assert.Contains(t, mandatoryPhrases, dir, "%s: declare the skill's mandatory phrases in this test", dir)
	}
}

func TestStructureMandatoryPhrases(t *testing.T) {
	files := skillFiles(t)
	for dir, phrases := range mandatoryPhrases {
		text, ok := files[dir]
		if !assert.True(t, ok, "skills/%s/SKILL.md is missing", dir) {
			continue
		}
		for _, phrase := range phrases {
			assert.Contains(t, text, phrase, "skills/%s/SKILL.md", dir)
		}
	}
}

func TestStructureForbiddenPhrases(t *testing.T) {
	files := skillFiles(t)
	for dir, phrases := range forbiddenPhrases {
		text, ok := files[dir]
		if !assert.True(t, ok, "skills/%s/SKILL.md is missing", dir) {
			continue
		}
		for _, phrase := range phrases {
			assert.NotContains(t, text, phrase, "skills/%s/SKILL.md must not contain the superseded phrase %q", dir, phrase)
		}
	}
}

func TestStructureNoCredentials(t *testing.T) {
	for p, text := range bundleFiles(t) {
		assert.Empty(t, redact.Scan(text), "skills/%s contains a credential shape", p)
	}
}

func TestStructureNoDashes(t *testing.T) {
	for p, text := range bundleFiles(t) {
		for _, dash := range []string{"\u2014", "\u2013"} {
			if i := strings.Index(text, dash); i >= 0 {
				t.Errorf("skills/%s:%d contains an em-dash or en-dash", p, strings.Count(text[:i], "\n")+1)
			}
		}
	}
}
