package policy

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/testutil"
)

// upstreamDefaults loads the real pinned default policy.
func upstreamDefaults(t *testing.T) *Defaults {
	t.Helper()
	d, err := LoadDefaults(os.DirFS("../../policy"))
	require.NoError(t, err)
	return d
}

func parseDoc(t *testing.T, data []byte) document {
	t.Helper()
	var doc document
	require.NoError(t, yaml.UnmarshalWithOptions(data, &doc, yaml.Strict()))
	return doc
}

func TestLoadDefaultsRealFile(t *testing.T) {
	d := upstreamDefaults(t)
	// restrictive_default_policy() at v0.1.3, crates/openshell-policy/src/lib.rs:972-995.
	assert.Equal(t, &Defaults{
		IncludeWorkdir:        true,
		ReadOnly:              []string{"/bin", "/usr", "/lib", "/proc", "/dev/urandom", "/etc", "/var/log"},
		ReadWrite:             []string{"/tmp", "/dev/null"},
		LandlockCompatibility: "best_effort",
	}, d)
}

func TestLoadDefaultsErrors(t *testing.T) {
	const valid = "version: 1\nfilesystem_policy:\n  include_workdir: true\n  read_only: [/usr]\n  read_write: [/tmp]\nlandlock:\n  compatibility: best_effort\nnetwork_policies: {}\n"
	tests := []struct {
		name    string
		files   fstest.MapFS
		wantErr string
	}{
		{"missing file", fstest.MapFS{}, "read default policy"},
		{"malformed yaml", fstest.MapFS{DefaultsFile: {Data: []byte("version: [1\n")}}, "parse policy"},
		{"unknown field", fstest.MapFS{DefaultsFile: {Data: []byte(valid + "future: true\n")}}, "parse policy"},
		{"no filesystem", fstest.MapFS{DefaultsFile: {Data: []byte("version: 1\nlandlock:\n  compatibility: best_effort\n")}}, "filesystem_policy is missing"},
		{"no landlock", fstest.MapFS{DefaultsFile: {Data: []byte("version: 1\nfilesystem_policy:\n  read_only: [/usr]\n")}}, "landlock is missing"},
		{"process present", fstest.MapFS{DefaultsFile: {Data: []byte(valid + "process:\n  run_as_user: sandbox\n")}}, "process must be absent"},
		{"network rule", fstest.MapFS{DefaultsFile: {Data: []byte(strings.Replace(valid, "network_policies: {}", "network_policies:\n  x: {}", 1))}}, "network_policies must be empty"},
		{"relative path", fstest.MapFS{DefaultsFile: {Data: []byte(strings.Replace(valid, "[/usr]", "[usr]", 1))}}, "path must be absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadDefaults(tt.files)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func goldenInput() Input {
	return Input{
		ReadOnly:   []string{"/opt/tools/", "/usr", "/opt//shared/./lib", "/opt/tools"},
		ReadWrite:  []string{"/opt/mise", "/home/sandbox", "/opt/mise/", "/var/log"},
		RunAsUser:  "sandbox",
		RunAsGroup: "sandbox",
	}
}

func TestRenderGolden(t *testing.T) {
	got, err := Render(upstreamDefaults(t), goldenInput())
	require.NoError(t, err)
	testutil.Golden(t, "testdata/policy.yaml.golden", got)
}

func TestRenderDeterministic(t *testing.T) {
	d := upstreamDefaults(t)
	first, err := Render(d, goldenInput())
	require.NoError(t, err)

	in := goldenInput()
	in.ReadOnly = []string{"/opt/tools", "/opt/shared/lib", "/usr", "/opt/tools/"}
	in.ReadWrite = []string{"/var/log", "/opt/mise", "/home/sandbox"}
	second, err := Render(d, in)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
	assert.Equal(t, []string{"/bin", "/usr", "/lib", "/proc", "/dev/urandom", "/etc", "/var/log"}, d.ReadOnly,
		"Render must not modify the defaults")
}

func TestRenderMerge(t *testing.T) {
	d := &Defaults{
		IncludeWorkdir:        true,
		ReadOnly:              []string{"/usr", "/etc", "/var/log"},
		ReadWrite:             []string{"/tmp"},
		LandlockCompatibility: "best_effort",
	}
	tests := []struct {
		name          string
		in            Input
		wantReadOnly  []string
		wantReadWrite []string
	}{
		{
			name:          "defaults only",
			in:            Input{RunAsUser: "sandbox", RunAsGroup: "sandbox"},
			wantReadOnly:  []string{"/etc", "/usr", "/var/log"},
			wantReadWrite: []string{"/tmp"},
		},
		{
			name:          "sorted and deduplicated",
			in:            Input{ReadOnly: []string{"/opt/b", "/opt/a", "/opt/b", "/usr"}, RunAsUser: "sandbox", RunAsGroup: "sandbox"},
			wantReadOnly:  []string{"/etc", "/opt/a", "/opt/b", "/usr", "/var/log"},
			wantReadWrite: []string{"/tmp"},
		},
		{
			name:          "lexically normalized",
			in:            Input{ReadWrite: []string{"/opt/mise/", "/opt//mise", "/opt/./mise"}, RunAsUser: "sandbox", RunAsGroup: "sandbox"},
			wantReadOnly:  []string{"/etc", "/usr", "/var/log"},
			wantReadWrite: []string{"/opt/mise", "/tmp"},
		},
		{
			name:          "read-write wins over read-only",
			in:            Input{ReadOnly: []string{"/opt/mise"}, ReadWrite: []string{"/opt/mise", "/var/log/"}, RunAsUser: "sandbox", RunAsGroup: "sandbox"},
			wantReadOnly:  []string{"/etc", "/usr"},
			wantReadWrite: []string{"/opt/mise", "/tmp", "/var/log"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Render(d, tt.in)
			require.NoError(t, err)
			doc := parseDoc(t, out)
			require.NotNil(t, doc.FilesystemPolicy)
			assert.True(t, doc.FilesystemPolicy.IncludeWorkdir)
			assert.Equal(t, tt.wantReadOnly, doc.FilesystemPolicy.ReadOnly)
			assert.Equal(t, tt.wantReadWrite, doc.FilesystemPolicy.ReadWrite)
			assert.Equal(t, &landlockPolicy{Compatibility: "best_effort"}, doc.Landlock)
			assert.Equal(t, &processPolicy{RunAsUser: "sandbox", RunAsGroup: "sandbox"}, doc.Process)
			assert.Empty(t, doc.NetworkPolicies)
			assert.Contains(t, string(out), "\nnetwork_policies: {}\n")
		})
	}
}

func TestRenderNumericIdentity(t *testing.T) {
	out, err := Render(upstreamDefaults(t), Input{RunAsUser: "1000", RunAsGroup: "1000"})
	require.NoError(t, err)
	// Upstream reads the identities as strings, so numbers must stay quoted.
	assert.Contains(t, string(out), "run_as_user: \"1000\"\n")
	assert.Contains(t, string(out), "run_as_group: \"1000\"\n")
	assert.Equal(t, &processPolicy{RunAsUser: "1000", RunAsGroup: "1000"}, parseDoc(t, out).Process)
}

func TestRenderLimits(t *testing.T) {
	sandbox := func(in Input) Input {
		in.RunAsUser, in.RunAsGroup = "sandbox", "sandbox"
		return in
	}
	// The pinned defaults hold 9 paths; fill up to and beyond the upstream maximum.
	paths := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("/data/p%03d", i)
		}
		return out
	}
	tests := []struct {
		name    string
		in      Input
		wantErr string
	}{
		{"at path limit", sandbox(Input{ReadOnly: paths(MaxFilesystemPaths - 9)}), ""},
		{"too many paths", sandbox(Input{ReadOnly: paths(MaxFilesystemPaths - 8)}), "too many filesystem paths (257 > 256)"},
		{"duplicates do not count", sandbox(Input{ReadOnly: append(paths(MaxFilesystemPaths-9), paths(10)...)}), ""},
		{"relative read-only", sandbox(Input{ReadOnly: []string{"opt/tools"}}), "path must be absolute (start with '/'): opt/tools"},
		{"relative read-write", sandbox(Input{ReadWrite: []string{"./cache"}}), "path must be absolute (start with '/'): ./cache"},
		{"empty path", sandbox(Input{ReadOnly: []string{""}}), "path must be absolute"},
		{"traversal", sandbox(Input{ReadWrite: []string{"/opt/../etc"}}), "path contains '..' traversal component: /opt/../etc"},
		{"trailing traversal", sandbox(Input{ReadOnly: []string{"/opt/.."}}), "traversal component"},
		{"root read-write", sandbox(Input{ReadWrite: []string{"/"}}), "read-write path is overly broad: /"},
		{"root read-write repeated slashes", sandbox(Input{ReadWrite: []string{"///"}}), "read-write path is overly broad: /"},
		{"root read-only allowed", sandbox(Input{ReadOnly: []string{"/"}}), ""},
		{"path too long", sandbox(Input{ReadOnly: []string{"/" + strings.Repeat("a", MaxPathLength)}}), "path exceeds maximum length (4097 > 4096)"},
		{"path at length limit", sandbox(Input{ReadOnly: []string{"/" + strings.Repeat("a", MaxPathLength-1)}}), ""},
		{"user missing", Input{RunAsGroup: "sandbox"}, "run_as_user is required"},
		{"group missing", Input{RunAsUser: "sandbox"}, "run_as_group is required"},
		{"root user", Input{RunAsUser: "root", RunAsGroup: "sandbox"}, "run_as_user must be 'sandbox' or a numeric UID/GID in range [1, 4294967294], got 'root'"},
		{"uid zero", Input{RunAsUser: "0", RunAsGroup: "sandbox"}, "got '0'"},
		{"gid sentinel", Input{RunAsUser: "sandbox", RunAsGroup: "4294967295"}, "run_as_group must be"},
		{"named user", Input{RunAsUser: "node", RunAsGroup: "node"}, "got 'node'"},
		{"boundary ids", Input{RunAsUser: "1", RunAsGroup: "4294967294"}, ""},
	}
	d := upstreamDefaults(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Render(d, tt.in)
			if tt.wantErr == "" {
				require.NoError(t, err)
				require.NoError(t, Validate(out))
				return
			}
			require.Error(t, err)
			assert.Nil(t, out)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestRenderReportsAllViolations(t *testing.T) {
	_, err := Render(upstreamDefaults(t), Input{
		ReadOnly:  []string{"rel", "/a/../b"},
		ReadWrite: []string{"/"},
		RunAsUser: "root",
	})
	require.Error(t, err)
	for _, want := range []string{"must be absolute", "traversal", "overly broad", "run_as_user must be", "run_as_group is required"} {
		assert.ErrorContains(t, err, want)
	}
}

func TestRenderNilDefaults(t *testing.T) {
	_, err := Render(nil, Input{RunAsUser: "sandbox", RunAsGroup: "sandbox"})
	assert.ErrorContains(t, err, "defaults are nil")
}

func TestValidate(t *testing.T) {
	const fsSection = "filesystem_policy:\n  include_workdir: true\n  read_only: [/usr]\n  read_write: [/tmp]\n"
	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"full document", "version: 1\n" + fsSection + "landlock:\n  compatibility: best_effort\nprocess:\n  run_as_user: sandbox\n  run_as_group: \"1000\"\nnetwork_policies: {}\n", ""},
		{"minimal document", "version: 1\n", ""},
		{"hard requirement", "version: 1\nlandlock:\n  compatibility: hard_requirement\n", ""},
		{"omitted identities", "version: 1\nprocess: {}\n", ""},
		{"empty", "", "unsupported policy version 0"},
		{"version 2", "version: 2\n", "unsupported policy version 2; expected version 1"},
		{"unknown top-level field", "version: 1\nfuture: true\n", "parse policy"},
		{"unknown filesystem field", "version: 1\nfilesystem_policy:\n  future: true\n", "parse policy"},
		{"unknown process field", "version: 1\nprocess:\n  future: true\n", "parse policy"},
		{"duplicate key", "version: 1\nversion: 1\n", "parse policy"},
		{"bad landlock", "version: 1\nlandlock:\n  compatibility: strict\n", "invalid landlock.compatibility 'strict'; accepted: best_effort, hard_requirement"},
		{"relative path", "version: 1\nfilesystem_policy:\n  read_only: [usr]\n", "path must be absolute (start with '/'): usr"},
		{"traversal", "version: 1\nfilesystem_policy:\n  read_write: [/tmp/../etc]\n", "path contains '..' traversal component: /tmp/../etc"},
		{"root read-write", "version: 1\nfilesystem_policy:\n  read_write: [/]\n", "read-write path is overly broad: /"},
		{"root user", "version: 1\nprocess:\n  run_as_user: root\n", "run_as_user must be 'sandbox'"},
		{"nobody group", "version: 1\nprocess:\n  run_as_group: nobody\n", "run_as_group must be 'sandbox'"},
		{"network rule", "version: 1\nnetwork_policies:\n  github:\n    endpoints:\n      - host: api.github.com\n        port: 443\n", "network_policies must be empty"},
		{"too many paths", "version: 1\nfilesystem_policy:\n  read_only:\n" + strings.Repeat("    - /usr\n", MaxFilesystemPaths+1), "too many filesystem paths (257 > 256)"},
		{"path too long", "version: 1\nfilesystem_policy:\n  read_only: [/" + strings.Repeat("a", MaxPathLength) + "]\n", "path exceeds maximum length (4097 > 4096): /aaaa"},
		{"document too large", "version: 1\n#" + strings.Repeat("x", MaxDocumentBytes), "exceeds the 4194304-byte input limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate([]byte(tt.doc))
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateAcceptsDefaultsFile(t *testing.T) {
	data, err := os.ReadFile("../../policy/" + DefaultsFile)
	require.NoError(t, err)
	require.NoError(t, Validate(data))
}
