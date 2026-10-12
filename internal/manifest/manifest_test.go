package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadValid(t *testing.T) {
	m, err := Load("testdata/valid.yaml")
	require.NoError(t, err)
	assert.Equal(t, "demo", m.Name)
	assert.Equal(t, []string{"go", "starship", "fzf"}, m.ComponentKeys())
	assert.True(t, m.Components[2].Optional)
	assert.Equal(t, "linux/arm64", m.Platform)
	assert.Equal(t, []string{TargetOpenShell, TargetContainer}, m.TargetNames())
	assert.Equal(t, "localhost/demo:latest", m.Target(TargetOpenShell).Ref())
	assert.Equal(t, "localhost/demo-plain:v1", m.Target(TargetContainer).Ref())
	assert.Equal(t, DefaultAllowedDomainsFile, m.Target(TargetContainer).AllowedDomains())
	require.NotNil(t, m.Harness("claude-code"))
	assert.Equal(t, "${SANDCAST_SECRET_1}", m.Harness("claude-code").MCPServers[0].Env["API_KEY"])
	assert.Contains(t, m.Shell.RC, "compinit")
}

func TestUnknownFieldRejectedWithLine(t *testing.T) {
	_, err := Load("testdata/unknown-field.yaml")
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	require.Len(t, ve.Diagnostics, 1)
	assert.Equal(t, 5, ve.Diagnostics[0].Line)
	assert.Contains(t, ve.Diagnostics[0].Message, "colour")
}

func TestNewerSchemaVersionRefused(t *testing.T) {
	_, err := Load("testdata/newer.yaml")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnsupportedVersion))
	assert.Contains(t, err.Error(), "schema version 2")
	assert.Contains(t, err.Error(), "supports version 1")
}

func TestValidationRules(t *testing.T) {
	_, err := Load("testdata/invalid.yaml")
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	byPath := map[string]Diagnostic{}
	for _, d := range ve.Diagnostics {
		byPath[d.Path] = d
	}
	for path, want := range map[string]string{
		"$.name":              "kebab-case",
		"$.engine":            "not podman or docker",
		"$.platform":          "not linux/amd64",
		"$.components[1].key": "duplicate component key",
		"$.components[2].key": "not a valid component key",
		"$.harnesses[0].name": "unknown harness",
		"$.targets":           "at least one target",
	} {
		d, ok := byPath[path]
		if assert.True(t, ok, "missing diagnostic for %s in %v", path, ve.Diagnostics) {
			assert.Contains(t, d.Message, want)
		}
	}
	assert.Equal(t, 2, byPath["$.name"].Line)
	assert.Equal(t, 8, byPath["$.components[1].key"].Line)
}

func TestCheckVersion(t *testing.T) {
	require.NoError(t, CheckVersion(1))
	for _, v := range []int{0, 2, -1} {
		err := CheckVersion(v)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnsupportedVersion)
	}
}

func TestMCPServerRules(t *testing.T) {
	m := Skeleton("demo", "x", TargetOpenShell)
	m.Harnesses = []HarnessRef{{Name: "claude-code", MCPServers: []MCPServer{
		{Name: "a"},
		{Name: "b", Command: []string{"x"}, URL: "https://x"},
	}, Plugins: []Extension{{Name: "p"}}}}
	diags := m.Validate()
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		msgs = append(msgs, d.Path+" "+d.Message)
	}
	joined := strings.Join(msgs, "\n")
	assert.Contains(t, joined, "mcp_servers[0] needs a command or a url")
	assert.Contains(t, joined, "mcp_servers[1] has both")
	assert.Contains(t, joined, "plugins[0] name and source are required")
}

func TestAllowedDomainsFileMustBePlainName(t *testing.T) {
	for _, name := range []string{"allowed-domains.txt", "domains.list", "a..b", "build.log.txt"} {
		m := Skeleton("demo", "x", TargetContainer)
		m.Targets.Container.AllowedDomainsFile = name
		assert.Empty(t, m.Validate(), name)
	}
	for _, name := range []string{
		"../../../.git/config", "../x", "sub/x", "/etc/passwd", `..\x`, `sub\x`,
		".", "..", "x\x00y", "x\ny", " ", "x/",
	} {
		m := Skeleton("demo", "x", TargetContainer)
		m.Targets.Container.AllowedDomainsFile = name
		diags := m.Validate()
		if assert.Len(t, diags, 1, "%q", name) {
			assert.Equal(t, "$.targets.container.allowed_domains_file", diags[0].Path)
			assert.Contains(t, diags[0].Message, "plain file name")
		}
	}
}

// The allowed-domains file is renamed into the build directory mid-build;
// over .lock it would defeat the build lock, over build.log it would hide
// the pass-2 log from the credential scan.
func TestAllowedDomainsFileRefusesBuildDirectoryNames(t *testing.T) {
	for _, name := range []string{
		".lock", "build.log", "plan.yaml", "policy.yaml", "verify.yaml", "Containerfile",
		"Containerfile.final", "profiles", "mise", "shell", "session", "harness",
		"BUILD.LOG", "containerfile",
	} {
		m := Skeleton("demo", "x", TargetContainer)
		m.Targets.Container.AllowedDomainsFile = name
		diags := m.Validate()
		if assert.Len(t, diags, 1, "%q", name) {
			assert.Equal(t, "$.targets.container.allowed_domains_file", diags[0].Path)
			assert.Contains(t, diags[0].Message, "sandcast", name)
		}
		assert.Error(t, CheckAllowedDomainsFile(name), name)
	}
	for _, name := range []string{".domains", "..domains", ".plan.yaml.tmp-1", ".gitignore"} {
		m := Skeleton("demo", "x", TargetContainer)
		m.Targets.Container.AllowedDomainsFile = name
		diags := m.Validate()
		if assert.Len(t, diags, 1, "%q", name) {
			assert.Contains(t, diags[0].Message, "must not start with a dot")
		}
	}
	assert.NoError(t, CheckAllowedDomainsFile(DefaultAllowedDomainsFile))
}

func TestSaveRoundTrip(t *testing.T) {
	m, err := Load("testdata/valid.yaml")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	require.NoError(t, Save(path, m))
	back, err := Load(path)
	require.NoError(t, err)
	assert.True(t, Equal(m, back))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "rc: |")
}

func TestSaveRefusesInvalid(t *testing.T) {
	m := Skeleton("Bad Name", "x", TargetOpenShell)
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	require.Error(t, Save(path, m))
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestSkeleton(t *testing.T) {
	m := Skeleton("demo", "nvidia-community-base", TargetContainer)
	assert.Empty(t, m.Validate())
	assert.Equal(t, "localhost/demo", m.Targets.Container.Image)
	assert.Nil(t, m.Targets.OpenShell)
	assert.Equal(t, TargetContainer, m.DefaultTarget())
}

func TestSaveRefusesSecrets(t *testing.T) {
	m := Skeleton("demo", "x", TargetOpenShell)
	token := "ghp_" + strings.Repeat("Q", 36)
	m.Harnesses = []HarnessRef{{Name: "claude-code", MCPServers: []MCPServer{{Name: "gh", URL: "https://x.example.com/mcp", Env: map[string]string{"GITHUB_TOKEN": token}}}}}
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	err := Save(path, m)
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, err.Error(), "GITHUB_TOKEN")
	assert.NotContains(t, err.Error(), token)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "nothing is written")

	m.Harnesses[0].MCPServers[0].Env["GITHUB_TOKEN"] = "${SANDCAST_SECRET_1}"
	require.NoError(t, Save(path, m))
}

// A credential pasted into a field that Validate quotes (name, engine,
// platform, a component key, a harness name, the shell program, the
// allowed-domains file) must never reach the diagnostic message. The value is
// replaced with a redaction marker. R3-2.
func TestValidateRedactsCredentialShapedValues(t *testing.T) {
	token := "ghp_" + strings.Repeat("Q", 36)
	cases := []struct {
		name  string
		path  string
		setup func(*Manifest)
	}{
		{"name", "$.name", func(m *Manifest) { m.Name = token }},
		{"engine", "$.engine", func(m *Manifest) { m.Engine = token }},
		{"platform", "$.platform", func(m *Manifest) { m.Platform = token }},
		{"component key", "$.components[0].key", func(m *Manifest) {
			m.Components = []ComponentRef{{Key: token}}
		}},
		{"harness", "$.harnesses[0].name", func(m *Manifest) {
			m.Harnesses = []HarnessRef{{Name: token}}
		}},
		{"shell program", "$.shell.program", func(m *Manifest) {
			m.Shell = &Shell{Program: token}
		}},
		{"allowed_domains_file", "$.targets.container.allowed_domains_file", func(m *Manifest) {
			m.Targets = Targets{Container: &Target{Image: "localhost/demo", AllowedDomainsFile: token + "/evil"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Skeleton("demo", "base", TargetOpenShell)
			tc.setup(m)
			diags := m.Validate()
			byPath := map[string]Diagnostic{}
			for _, d := range diags {
				byPath[d.Path] = d
				assert.NotContains(t, d.Message, token, "token leaked in %s", d.Path)
			}
			d, ok := byPath[tc.path]
			require.True(t, ok, "missing diagnostic for %s in %v", tc.path, diags)
			assert.NotContains(t, d.Message, token)
			assert.Contains(t, d.Message, redactedValue)
		})
	}
}

// CheckAllowedDomainsFile is called directly from the pipeline; its error must
// not echo a credential-shaped name either. R3-2.
func TestCheckAllowedDomainsFileRedactsCredential(t *testing.T) {
	token := "glpat-" + strings.Repeat("a1", 12)
	err := CheckAllowedDomainsFile(token + "/evil")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), token)
	assert.Contains(t, err.Error(), redactedValue)
}

// Save marshals the manifest and scans it for credential shapes before the
// value-quoting validation runs, so a token in a field is refused with a safe
// message and nothing is written. R3-2.
func TestSaveDoesNotEchoCredentialValue(t *testing.T) {
	token := "ghp_" + strings.Repeat("Z", 36)
	m := Skeleton("demo", "base", TargetOpenShell)
	m.Name = token
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	err := Save(path, m)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), token)
	assert.ErrorContains(t, err, "credential-shaped")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "nothing is written")
}

// A manifest that fails to decode but carries a token on a source line (an
// unknown field name, or a value the parser would quote) is refused with a
// credential error, not a decode error that echoes the token. R3-2.
func TestParseRefusesCredentialBeforeDecodeError(t *testing.T) {
	t.Run("token as unknown field", func(t *testing.T) {
		token := "ghp_" + strings.Repeat("Q", 36)
		data := []byte("schema_version: 1\nname: demo\nbase_image: base\n" +
			token + ": oops\ntargets:\n  openshell:\n    image: localhost/demo\n")
		_, err := Parse(data, "manifest.yaml")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), token)
		assert.ErrorContains(t, err, "credential-shaped")
	})
	t.Run("token on a malformed line", func(t *testing.T) {
		token := "sk-ant-api03-" + strings.Repeat("A", 40)
		// The unterminated quoted string makes the document fail to decode.
		data := []byte("schema_version: 1\nname: \"" + token + "\n")
		_, err := Parse(data, "manifest.yaml")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), token)
		assert.ErrorContains(t, err, "credential-shaped")
	})
}
