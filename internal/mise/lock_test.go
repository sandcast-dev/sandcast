package mise

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, name string) *Lockfile {
	t.Helper()
	l, err := ReadLock(filepath.Join("testdata", name))
	require.NoError(t, err)
	return l
}

func TestReadLockFixture(t *testing.T) {
	l := readFixture(t, "mise.lock")
	assert.Equal(t, 3, l.FormatVersion)
	require.Len(t, l.Tools, 3)

	jq := l.Tools["jq"]
	require.Len(t, jq, 1)
	assert.Equal(t, "1.8.2", jq[0].Version)
	assert.Equal(t, "aqua:jqlang/jq", jq[0].Backend)
	assert.Equal(t, []string{"latest"}, jq[0].Specifiers)
	assert.Equal(t, PlatformAsset{
		Checksum:   "sha256:b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f",
		URL:        "https://github.com/jqlang/jq/releases/download/jq-1.8.2/jq-linux-amd64",
		URLAPI:     "https://api.github.com/repos/jqlang/jq/releases/assets/453012752",
		Provenance: "github-attestations",
	}, jq[0].Platforms["linux-x64"])

	claude := l.Tools["aqua:anthropics/claude-code"]
	require.Len(t, claude, 1)
	assert.Len(t, claude[0].Platforms, 2)
	assert.Equal(t, "core:go", l.Tools["go"][0].Backend)
	assert.Equal(t, []string{"1.25"}, l.Tools["go"][0].Specifiers)
}

func TestReadLockMissing(t *testing.T) {
	_, err := ReadLock(filepath.Join(t.TempDir(), "mise.lock"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestParseLockErrors(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "empty file is format 0", data: "", wantErr: "unsupported mise lockfile format version 0"},
		{name: "newer format", data: "lockfile_version = 4\n", wantErr: "unsupported mise lockfile format version 4"},
		{name: "not toml", data: "lockfile_version = = 3", wantErr: "parse mise lockfile"},
		{
			name:    "version not a string",
			data:    "lockfile_version = 3\n[[tools.go]]\nversion = 1\n",
			wantErr: "tools.go[0]: version: want a string",
		},
		{
			name:    "specifiers not strings",
			data:    "lockfile_version = 3\n[[tools.go]]\nversion = \"1\"\nspecifiers = [1]\n",
			wantErr: "specifiers: want an array of strings",
		},
		{
			name:    "platform not a table",
			data:    "lockfile_version = 3\n[[tools.go]]\nversion = \"1\"\n\"platforms.linux-x64\" = \"x\"\n",
			wantErr: "platform linux-x64: want a table",
		},
		{
			name:    "checksum not a string",
			data:    "lockfile_version = 3\n[[tools.go]]\nversion = \"1\"\n[tools.go.\"platforms.linux-x64\"]\nchecksum = 1\n",
			wantErr: "platform linux-x64: checksum: want a string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLock([]byte(tt.data))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParseLockNestedPlatforms(t *testing.T) {
	data := `lockfile_version = 3

[[tools.go]]
version = "1.25.14"
backend = "core:go"

[tools.go.platforms.linux-x64]
checksum = "sha256:aa"
url = "https://example.invalid/go.tar.gz"
`
	l, err := ParseLock([]byte(data))
	require.NoError(t, err)
	assert.Equal(t, PlatformAsset{Checksum: "sha256:aa", URL: "https://example.invalid/go.tar.gz"},
		l.Tools["go"][0].Platforms["linux-x64"])
}

func TestLockVersion(t *testing.T) {
	l := readFixture(t, "mise.lock")
	assert.Equal(t, "1.25.14", l.Version("go"))
	assert.Equal(t, "2.1.292", l.Version("aqua:anthropics/claude-code"))
	assert.Equal(t, "", l.Version("uv"))
}

func TestCheckComplete(t *testing.T) {
	l := readFixture(t, "mise.lock")
	tools := []Tool{
		{Key: "jq", Tool: "jq"},
		{Key: "go", Tool: "go", Version: "1.25"},
		{Key: "claude-code", Tool: "aqua:anthropics/claude-code", Version: "latest"},
	}
	for _, p := range []string{"linux-x64", "linux-arm64"} {
		assert.Empty(t, l.Check(tools, p), p)
	}
}

func TestCheckFailures(t *testing.T) {
	full := readFixture(t, "mise.lock")
	partial := readFixture(t, "mise-partial.lock")
	tests := []struct {
		name     string
		lock     *Lockfile
		tools    []Tool
		platform string
		want     []ToolError
	}{
		{
			name:     "tool missing",
			lock:     full,
			tools:    []Tool{{Key: "uv", Tool: "uv"}},
			platform: "linux-x64",
			want: []ToolError{{
				Key: "uv", Tool: "uv", Platform: "linux-x64",
				Reason: `mise.lock has no entry for uv; run "sandcast lock --platform linux/amd64"`,
			}},
		},
		{
			name:     "selector changed",
			lock:     full,
			tools:    []Tool{{Key: "go", Tool: "go", Version: "1.26"}},
			platform: "linux-arm64",
			want: []ToolError{{
				Key: "go", Tool: "go", Platform: "linux-arm64",
				Reason: `mise.lock has no entry for go matching "1.26" (locked: 1.25.14); run "sandcast lock --platform linux/arm64"`,
			}},
		},
		{
			name:     "platform entry missing",
			lock:     partial,
			tools:    []Tool{{Key: "jq", Tool: "jq"}},
			platform: "linux-x64",
			want: []ToolError{{
				Key: "jq", Tool: "jq", Platform: "linux-x64",
				Reason: `mise.lock has no linux-x64 entry for jq; run "sandcast lock --platform linux/amd64"`,
			}},
		},
		{
			name:     "checksum missing",
			lock:     partial,
			tools:    []Tool{{Key: "go", Tool: "go", Version: "1.25"}},
			platform: "linux-x64",
			want: []ToolError{{
				Key: "go", Tool: "go", Platform: "linux-x64",
				Reason: `mise.lock entry for go on linux-x64 has no checksum; run "sandcast lock --platform linux/amd64"`,
			}},
		},
		{
			name:     "unresolved version without platforms, sorted by key",
			lock:     partial,
			tools:    []Tool{{Key: "uv", Tool: "uv", Version: "0.8"}, {Key: "jq", Tool: "jq"}},
			platform: "linux-arm64",
			want: []ToolError{{
				Key: "uv", Tool: "uv", Platform: "linux-arm64",
				Reason: `mise.lock has no linux-arm64 entry for uv; run "sandcast lock --platform linux/arm64"`,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.lock.Check(tt.tools, tt.platform))
		})
	}
}

func TestCheckSortsByKey(t *testing.T) {
	l := readFixture(t, "mise-partial.lock")
	errs := l.Check([]Tool{
		{Key: "zz-uv", Tool: "uv", Version: "0.8"},
		{Key: "aa-jq", Tool: "jq"},
		{Key: "mm-go", Tool: "go", Version: "1.25"},
	}, "linux-x64")
	require.Len(t, errs, 3)
	assert.Equal(t, []string{"aa-jq", "mm-go", "zz-uv"}, []string{errs[0].Key, errs[1].Key, errs[2].Key})
}

func TestCheckEntryWithoutSpecifiers(t *testing.T) {
	data := `lockfile_version = 3

[[tools.go]]
version = "1.25.14"

[tools.go."platforms.linux-x64"]
url = "https://example.invalid/go.tar.gz"
`
	l, err := ParseLock([]byte(data))
	require.NoError(t, err)
	errs := l.Check([]Tool{{Key: "go", Tool: "go", Version: "1.25"}}, "linux-x64")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Reason, "has no checksum;")
}

func TestToolErrorError(t *testing.T) {
	e := ToolError{Key: "claude-code", Tool: "aqua:anthropics/claude-code", Platform: "linux-x64", Reason: "boom"}
	assert.Equal(t, "component claude-code (mise tool aqua:anthropics/claude-code): boom", e.Error())
}

func TestCheckVersionOnlyBackend(t *testing.T) {
	l, err := ParseLock([]byte(`lockfile_version = 3

[[tools.rust]]
version = "1.95.0"
backend = "core:rust"
specifiers = ["latest"]

[[tools.jq]]
version = "1.8.2"
backend = "aqua:jqlang/jq"
specifiers = ["latest"]
`))
	if err != nil {
		t.Fatal(err)
	}
	errs := l.Check([]Tool{{Key: "rust", Tool: "rust"}, {Key: "jq", Tool: "jq"}}, "linux-arm64")
	if len(errs) != 1 || errs[0].Key != "jq" {
		t.Fatalf("want only jq to fail, got %v", errs)
	}
}
