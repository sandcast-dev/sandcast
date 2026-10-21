package probe

import (
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/shell"
	"github.com/rhuss/sandcast/internal/testutil"
)

// tricky holds values that break naive quoting or JSON escaping.
var tricky = []string{
	"",
	"plain",
	"it's",
	`say "hi"`,
	`back\slash \n literal`,
	"$(echo injected) `echo injected` $HOME",
	"a | b; c && d > /dev/null",
	"tab\there",
	"two\nlines\n",
	"cr\rhere",
	"ctrl \x01\x02\x1b[0m del\x7f",
	"unicode héllo ✓",
	"'''",
	`\'\"`,
}

// shells returns the POSIX shells available on the host as argv prefixes.
// The probe script must run under each of them.
func shells(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, sh := range []string{"/bin/sh", "/bin/dash", "/usr/bin/dash"} {
		if _, err := os.Stat(sh); err == nil {
			out[sh] = []string{sh}
		}
	}
	if p, err := exec.LookPath("bash"); err == nil {
		out["bash"] = []string{p, "--posix"}
	}
	if p, err := exec.LookPath("busybox"); err == nil {
		out["busybox"] = []string{p, "sh"}
	}
	require.NotEmpty(t, out, "no POSIX shell on the host")
	return out
}

func runShell(t *testing.T, argv []string, script string, env ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], append(argv[1:], "-c", script)...)
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "stderr: %s", stderr.String())
	return string(out)
}

func TestShellQuote(t *testing.T) {
	for _, v := range tricky {
		if strings.ContainsRune(v, 0) {
			continue
		}
		q := shell.Quote(v)
		assert.True(t, strings.HasPrefix(q, "'") && strings.HasSuffix(q, "'"), q)
		for name, sh := range shells(t) {
			got := runShell(t, sh, "printf '%s' "+q)
			assert.Equal(t, v, got, "shell %s, quoted %s", name, q)
		}
	}
	assert.Equal(t, `'it'\''s'`, shell.Quote("it's"))
}

func TestJsafe(t *testing.T) {
	prelude := "_max=" + strconv.Itoa(MaxOutput) + "\n" + scriptHelpers
	for name, sh := range shells(t) {
		for _, v := range tricky {
			got := runShell(t, sh, prelude+`printf '%s' "$(_jsafe "$V")"`, "V="+v)
			var decoded string
			require.NoError(t, json.Unmarshal([]byte(`"`+got+`"`), &decoded), "shell %s, value %q, escaped %q", name, v, got)
			assert.Equal(t, v, decoded, "shell %s", name)
			assert.NotContains(t, got, "\n", "shell %s", name)
			assert.NotContains(t, got, "\t", "shell %s", name)
		}
	}
}

func TestScriptSections(t *testing.T) {
	reqs := []Request{{Key: "go", Bins: []string{"go"}, Command: "go version"}, {Key: "node", Bins: []string{"node"}, Command: "node --version"}}
	tests := []struct {
		name    string
		facts   bool
		present []string
		absent  []string
	}{
		{
			name:    "full",
			facts:   true,
			present: []string{"os-release", `"type":"pkgmgr"`, `"type":"user"`, `"type":"shells"`, "_check 'go' 'go' 'go version'\n_check 'node'"},
		},
		{
			name:    "partial",
			present: []string{"_check 'go' 'go' 'go version'", "_check 'node' 'node' 'node --version'"},
			absent:  []string{"os-release", `"type":"pkgmgr"`, `"type":"user"`, `"type":"shells"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Script(reqs, ScriptOptions{Facts: tt.facts})
			for _, p := range tt.present {
				assert.Contains(t, s, p)
			}
			for _, a := range tt.absent {
				assert.NotContains(t, s, a)
			}
			assert.True(t, strings.HasSuffix(s, "exit 0\n"))
			assert.Equal(t, s, Script(reqs, ScriptOptions{Facts: tt.facts}), "deterministic")
		})
	}
}

func TestScriptRunsChecks(t *testing.T) {
	long := strings.Repeat("abcdefgh", 600)
	reqs := []Request{
		{Key: "exits-first", Command: "echo bye; exit 5"},
		{Key: "plain", Bins: []string{"sh"}, Command: "echo hello 1.2.3"},
		{Key: "quotes", Command: `printf '%s\n' "it's \"quoted\""`},
		{Key: "subst", Command: `echo "$(echo nested)" | tr a-z A-Z`},
		{Key: "stderr", Command: "echo failing >&2; exit 3"},
		{Key: "multiline", Command: `printf 'line1\nline2\n'`},
		{Key: "control", Command: `printf 'a\tb\001c\\d'`},
		{Key: "long", Command: `i=0; while [ $i -lt 600 ]; do printf abcdefgh; i=$((i+1)); done`},
		{Key: "no-bin", Bins: []string{"sandcast-no-such-binary"}, Command: "true"},
		{Key: "stdin", Command: "cat"},
		{Key: "isolated", Command: `echo "[$_max][$_sysroot][$1]"`},
	}
	want := map[string]rawCheck{
		"exits-first": {Exit: 5, Out: "bye"},
		"plain":       {Exit: 0, Out: "hello 1.2.3"},
		"quotes":      {Exit: 0, Out: `it's "quoted"`},
		"subst":       {Exit: 0, Out: "NESTED"},
		"stderr":      {Exit: 3, Out: "failing"},
		"multiline":   {Exit: 0, Out: "line1\nline2"},
		"control":     {Exit: 0, Out: "a\tb\x01c\\d"},
		"long":        {Exit: 0, Out: long[:MaxOutput]},
		"no-bin":      {Exit: 0},
		"stdin":       {Exit: 0},
		"isolated":    {Exit: 0, Out: "[][][]"},
	}
	script := Script(reqs, ScriptOptions{})
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			facts, checks, err := Parse([]byte(runShell(t, sh, script)))
			require.NoError(t, err)
			assert.Nil(t, facts)
			require.Len(t, checks, len(reqs))
			for i, c := range checks {
				assert.Equal(t, reqs[i].Key, c.Key, "check order")
				w := want[c.Key]
				assert.Equal(t, w.Exit, c.Exit, c.Key)
				assert.Equal(t, w.Out, c.Out, c.Key)
				if c.Key == "plain" {
					assert.NotEmpty(t, c.Path, "path of sh")
				} else {
					assert.Empty(t, c.Path, c.Key)
				}
			}
		})
	}
}

func TestScriptFactsOnHost(t *testing.T) {
	script := Script([]Request{{Key: "sh", Bins: []string{"sh"}, Command: "echo ok"}}, ScriptOptions{Facts: true})
	cur, err := user.Current()
	require.NoError(t, err)
	group, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	require.NoError(t, err)
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			facts, checks, err := Parse([]byte(runShell(t, sh, script)))
			require.NoError(t, err)
			require.NotNil(t, facts)
			require.Len(t, checks, 1)
			assert.Contains(t, []string{"apt-get", "dnf", "yum", "apk", PackageManagerNone}, facts.PackageManager)
			assert.Equal(t, os.Getuid(), facts.User.UID)
			assert.Equal(t, os.Getgid(), facts.User.GID)
			assert.Equal(t, cur.Username, facts.User.Name)
			assert.Equal(t, group.Name, facts.User.Group)
			assert.Equal(t, os.Getenv("HOME"), facts.User.Home)
			assert.Contains(t, facts.Shells, "/bin/sh")
		})
	}
}

func TestScriptFactsFromFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "etc", "os-release"), strings.Join([]string{
		"# comment",
		`NAME="Fedora Linux"`,
		"ID=fedora",
		`ID_LIKE='rhel centos'`,
		"PRETTY_NAME=\"Fedora \\\"quoted\\\"\"",
		"VERSION_ID=42",
	}, "\n")) // no trailing newline on purpose
	writeFile(t, filepath.Join(root, "etc", "shells"), "# shells\n\n/bin/sh\n/sandcast/no/such/shell\n/bin/sh\n")
	script := strings.Replace(Script(nil, ScriptOptions{Facts: true}), "_sysroot=\n", "_sysroot="+shell.Quote(root)+"\n", 1)
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			facts, checks, err := Parse([]byte(runShell(t, sh, script)))
			require.NoError(t, err)
			require.NotNil(t, facts)
			assert.Empty(t, checks)
			assert.Equal(t, OS{ID: "fedora", IDLike: "rhel centos", Name: "Fedora Linux", Version: "42"}, facts.OS)
			assert.Equal(t, []string{"/bin/sh"}, facts.Shells)
		})
	}
}

func TestScriptFactsUsrLibOSRelease(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "usr", "lib", "os-release"), "ID=alpine\nVERSION_ID=3.22.1\nNAME=\"Alpine Linux\"\n")
	script := strings.Replace(Script(nil, ScriptOptions{Facts: true}), "_sysroot=\n", "_sysroot="+shell.Quote(root)+"\n", 1)
	for name, sh := range shells(t) {
		facts, _, err := Parse([]byte(runShell(t, sh, script)))
		require.NoError(t, err, name)
		assert.Equal(t, OS{ID: "alpine", Name: "Alpine Linux", Version: "3.22.1"}, facts.OS, name)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// TestScriptGolden pins the rendered probe script, so changes to the script
// are reviewed together with a FormatVersion bump.
func TestScriptGolden(t *testing.T) {
	s := Script([]Request{{Key: "go", Bins: []string{"go"}, Command: "go version"}}, ScriptOptions{Facts: true})
	testutil.Golden(t, "testdata/script.golden", []byte(s))
}

// TestScriptRecordsAllPaths checks that every PATH hit of the requested
// binary is recorded in PATH order, without duplicates, and that
// directories without the binary, non-executable files and relative PATH
// entries are ignored (R-02).
func TestScriptRecordsAllPaths(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "home", "bin")
	second := filepath.Join(root, "usr", "bin")
	noexec := filepath.Join(root, "noexec")
	empty := filepath.Join(root, "empty")
	for _, d := range []string{first, second, noexec, empty} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	for _, d := range []string{first, second} {
		require.NoError(t, os.WriteFile(filepath.Join(d, "sandcast-tool"), []byte("#!/bin/sh\necho 1.2.3\n"), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(noexec, "sandcast-tool"), []byte("x"), 0o644))
	path := strings.Join([]string{first, empty, noexec, "relative", second, first, "/usr/bin", "/bin"}, ":")
	script := Script([]Request{
		{Key: "tool", Bins: []string{"sandcast-tool"}, Command: "sandcast-tool"},
		{Key: "none", Bins: []string{"sandcast-no-such-binary"}, Command: "true"},
	}, ScriptOptions{})
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			_, checks, err := Parse([]byte(runShell(t, sh, script, "PATH="+path)))
			require.NoError(t, err)
			require.Len(t, checks, 2)
			assert.Equal(t, filepath.Join(first, "sandcast-tool"), checks[0].Path)
			assert.Equal(t, []string{filepath.Join(first, "sandcast-tool"), filepath.Join(second, "sandcast-tool")}, checks[0].Paths)
			assert.Nil(t, checks[1].Paths)
		})
	}
}

// TestScriptRecordsEveryBinary checks that each requested binary gets its own
// hits with their resolved targets, that a symlink into another directory
// resolves to its target, and that path and paths stay those of the first
// binary (T060).
func TestScriptRecordsEveryBinary(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	bin := filepath.Join(root, "usr", "bin")
	hidden := filepath.Join(root, "sandbox", ".local", "bin")
	for _, d := range []string{bin, hidden} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, "sandcast-main"), []byte("#!/bin/sh\necho 1.2.3\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hidden, "sandcast-helper"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(hidden, "sandcast-helper"), filepath.Join(bin, "sandcast-helper")))
	script := Script([]Request{
		{Key: "tool", Bins: []string{"sandcast-main", "sandcast-helper", "sandcast-no-such-binary"}, Command: "sandcast-main"},
	}, ScriptOptions{})
	for name, sh := range shells(t) {
		t.Run(name, func(t *testing.T) {
			_, checks, err := Parse([]byte(runShell(t, sh, script, "PATH="+bin+":/usr/bin:/bin")))
			require.NoError(t, err)
			require.Len(t, checks, 1)
			c := checks[0]
			assert.Equal(t, filepath.Join(bin, "sandcast-main"), c.Path)
			assert.Equal(t, []string{filepath.Join(bin, "sandcast-main")}, c.Paths)
			require.Len(t, c.Bins, 3)
			assert.Equal(t, "sandcast-main", c.Bins[0].Name)
			assert.Equal(t, []string{filepath.Join(bin, "sandcast-main")}, c.Bins[0].Paths)
			assert.Equal(t, BinHits{
				Name:  "sandcast-helper",
				Paths: []string{filepath.Join(bin, "sandcast-helper")},
				Real:  []string{filepath.Join(hidden, "sandcast-helper")},
			}, c.Bins[1])
			assert.Equal(t, BinHits{Name: "sandcast-no-such-binary"}, c.Bins[2])
		})
	}
}

// TestScriptEnvFact checks the env fact: exported variables whose value is
// an absolute path or a colon list of absolute paths, without PATH, PWD,
// relative values, multi-line values and unexported variables (#18).
func TestScriptEnvFact(t *testing.T) {
	script := "SANDCAST_LOCAL=/not/exported\n" + Script(nil, ScriptOptions{Facts: true})
	env := []string{
		"SANDCAST_VENV=/sandbox/.venv",
		"SANDCAST_LIST=/sandbox/lib:/usr/lib::",
		"SANDCAST_REL=relative/path",
		"SANDCAST_MIXED=/usr/lib:relative",
		"SANDCAST_URL=https://example.com/x",
		"SANDCAST_MULTI=/a\nexport SANDCAST_FAKE=/b",
		"SANDCAST_QUOTE=/with \"quote\" and 'apostrophe'",
		"SANDCAST_EMPTY=",
	}
	for name, sh := range shells(t) {
		facts, _, err := Parse([]byte(runShell(t, sh, script, env...)))
		require.NoError(t, err, name)
		require.NotNil(t, facts, name)
		got := map[string]string{}
		for _, v := range facts.Env {
			got[v.Name] = v.Value
		}
		assert.Equal(t, "/sandbox/.venv", got["SANDCAST_VENV"], name)
		assert.Equal(t, "/sandbox/lib:/usr/lib::", got["SANDCAST_LIST"], name)
		assert.Equal(t, `/with "quote" and 'apostrophe'`, got["SANDCAST_QUOTE"], name)
		for _, absent := range []string{"PATH", "PWD", "OLDPWD", "SANDCAST_REL", "SANDCAST_MIXED", "SANDCAST_URL", "SANDCAST_MULTI", "SANDCAST_FAKE", "SANDCAST_EMPTY", "SANDCAST_LOCAL"} {
			assert.NotContains(t, got, absent, "%s: %s", name, absent)
		}
		assert.True(t, slices.IsSortedFunc(facts.Env, func(a, b EnvVar) int { return strings.Compare(a.Name, b.Name) }), name)
	}
}

// TestScriptPathFact checks the path fact: the directories of PATH in
// order, without empty or duplicate entries.
func TestScriptPathFact(t *testing.T) {
	script := Script(nil, ScriptOptions{Facts: true})
	for name, sh := range shells(t) {
		facts, _, err := Parse([]byte(runShell(t, sh, script, "PATH=/opt/x/bin::"+os.Getenv("PATH")+":/opt/x/bin")))
		require.NoError(t, err, name)
		require.NotNil(t, facts, name)
		require.NotEmpty(t, facts.Path, name)
		assert.Equal(t, "/opt/x/bin", facts.Path[0], name)
		assert.NotContains(t, facts.Path, "", name)
		assert.Equal(t, 1, strings.Count(strings.Join(facts.Path, ":")+":", "/opt/x/bin:"), name)
	}
}

// TestScriptHomeRCResetsPath checks the homerc fact: a PATH assignment
// without $PATH is a reset, one that extends $PATH is not, and missing
// files are skipped (R-05).
func TestScriptHomeRCResetsPath(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "reset in bashrc",
			files: map[string]string{".bashrc": "# rc\n  export PATH=/usr/local/bin:/usr/bin:/bin\nalias ll='ls -l'\n"},
			want:  []string{".bashrc"},
		},
		{
			name: "extensions are not resets",
			files: map[string]string{
				".bashrc":       `export PATH="$HOME/bin:$PATH"` + "\n" + `PATH=${PATH}:/opt/x` + "\n",
				".profile":      "# PATH=/only/a/comment\n",
				".bash_profile": "PATH=\"${PATH:+$PATH:}/opt/y\"\n",
			},
		},
		{
			name: "several files",
			files: map[string]string{
				".profile":      "PATH=/bin\n",
				".bash_profile": "export PATH=/usr/bin",
			},
			want: []string{".profile", ".bash_profile"},
		},
		{name: "no files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			home := "/sandbox"
			require.NoError(t, os.MkdirAll(filepath.Join(root, home), 0o755))
			for name, content := range tt.files {
				writeFile(t, filepath.Join(root, home, name), content)
			}
			script := strings.Replace(Script(nil, ScriptOptions{Facts: true}), "_sysroot=\n", "_sysroot="+shell.Quote(root)+"\n", 1)
			for name, sh := range shells(t) {
				facts, _, err := Parse([]byte(runShell(t, sh, script, "HOME="+home)))
				require.NoError(t, err, name)
				assert.Equal(t, tt.want, facts.HomeRCResetsPath, name)
			}
		})
	}
}
