package build

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestWriteListSupersede(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteFragment(dir, "mytool", "USER root\nRUN set -eux; echo hi\nUSER sandbox\n", testNow)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "# sandcast-fragment: key=mytool written=2026-10-08 status=active\nUSER root\nRUN set -eux; echo hi\nUSER sandbox\n", string(data))

	list, err := ListFragments(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "mytool", list[0].Key)
	assert.Equal(t, FragmentActive, list[0].Status)
	assert.Equal(t, "USER root\nRUN set -eux; echo hi\nUSER sandbox\n", list[0].Body)

	require.NoError(t, MarkSuperseded(dir, "mytool"))
	list, err = ListFragments(dir)
	require.NoError(t, err)
	assert.Equal(t, FragmentSuperseded, list[0].Status)
	assert.Equal(t, "2026-10-08", list[0].Written)

	// Rewriting replaces the header instead of nesting it.
	_, err = WriteFragment(dir, "mytool", string(data), testNow)
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1, countLines(string(data), "# sandcast-fragment:"))
}

func TestListFragmentsMissingDir(t *testing.T) {
	list, err := ListFragments(filepath.Join(t.TempDir(), "nope"))
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestValidateFragment(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"ok", "USER root\nRUN apt-get install -y x \\\n  && true\nUSER sandbox\n", ""},
		{"comment in continuation", "USER root\nRUN apt-get update \\\n  # install the tool\n  && apt-get install -y x\nUSER sandbox\n", ""},
		{"add refused", "ADD https://example.com/x /usr/local/bin/x\n", "not a Containerfile instruction a fragment may use"},
		{"shell refused", "SHELL [\"/bin/bash\", \"-c\"]\nRUN true\n", "not a Containerfile instruction a fragment may use"},
		{"heredoc", "USER root\nRUN <<'EOF'\nset -eux\nFROM is fine inside a heredoc\nEOF\nUSER sandbox\n", ""},
		{"from", "FROM ubuntu\nRUN true\n", "FROM is not allowed"},
		{"ends root", "USER root\nRUN true\n", "ends as USER root"},
		{"ends uid 0", "USER 0:0\nRUN true\n", "ends as USER root"},
		{"empty", "# only a comment\n", "no instructions"},
		{"junk", "echo hello\n", "not a Containerfile instruction"},
		{"open heredoc", "RUN <<EOF\necho\n", "not terminated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFragment(tt.body)
			if tt.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestWriteFragmentRejects(t *testing.T) {
	_, err := WriteFragment(t.TempDir(), "Bad Key", "RUN true\n", testNow)
	require.Error(t, err)
	_, err = WriteFragment(t.TempDir(), "ok", "FROM x\n", testNow)
	require.Error(t, err)
}

func TestParseFragmentHeaderMismatch(t *testing.T) {
	_, err := ParseFragment("a", "a.containerfile", []byte("# sandcast-fragment: key=b written=x status=active\nRUN true\n"))
	require.Error(t, err)
	_, err = ParseFragment("a", "a.containerfile", []byte("# sandcast-fragment: key=a written=x status=weird\nRUN true\n"))
	require.Error(t, err)
	f, err := ParseFragment("a", "a.containerfile", []byte("RUN true\n"))
	require.NoError(t, err)
	assert.Equal(t, FragmentActive, f.Status, "a fragment without header is active")
}

func countLines(s, prefix string) int {
	n := 0
	for _, l := range splitLines(s) {
		if len(l) >= len(prefix) && l[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range s {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
