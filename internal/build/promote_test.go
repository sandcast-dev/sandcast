package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/component"
)

func TestPromote(t *testing.T) {
	root := t.TempDir()
	frag := filepath.Join(root, "fragments")
	recipes := filepath.Join(root, "recipes")
	_, err := WriteFragment(frag, "mytool", `USER root
ENV MYTOOL_HOME=/opt/mytool
RUN <<'EOF'
set -eux
curl -fsSL https://example.com/mytool-1.2.3.tar.gz -o /tmp/m.tgz
echo "abc123  /tmp/m.tgz" | sha256sum -c
tar -xzf /tmp/m.tgz -C /usr/local/bin mytool
EOF
RUN chmod +x /usr/local/bin/mytool \
 && mytool --version
USER sandbox
`, testNow)
	require.NoError(t, err)

	existing := &component.Component{Key: "mytool", Name: "My Tool", Check: component.Check{Command: "mytool --version"}, Profiles: []string{"github"}}
	p, err := BuildRecipe(frag, recipes, "mytool", PromoteOptions{Name: "My Tool", Existing: existing, FinalUser: "sandbox"})
	require.NoError(t, err)
	assert.True(t, p.Recipe.Installer.Script.AsRoot)
	assert.Contains(t, p.Recipe.Installer.Script.Run, "tar -xzf /tmp/m.tgz")
	assert.Contains(t, p.Recipe.Installer.Script.Run, "chmod +x /usr/local/bin/mytool \\\n && mytool --version")
	assert.Equal(t, "mytool --version", p.Recipe.Check.Command)
	assert.Equal(t, []string{"github"}, p.Recipe.Profiles)
	assert.Equal(t, "/opt/mytool", p.Recipe.Image.Env["MYTOOL_HOME"])
	assert.Equal(t, "fragments/mytool.containerfile", p.Recipe.PromotedFrom)
	assert.Equal(t, []string{"mytool"}, p.Recipe.Installer.Script.Bins)

	require.NoError(t, Promote(frag, p))
	c, err := component.LoadDir(os.DirFS(recipes), component.TierProject, ".sandcast/recipes")
	require.NoError(t, err)
	require.Len(t, c, 1)
	assert.Equal(t, "mytool", c[0].Key)
	list, err := ListFragments(frag)
	require.NoError(t, err)
	assert.Equal(t, FragmentSuperseded, list[0].Status)
}

// TestPromotedScriptRuns executes a promoted recipe script with the host sh
// to make sure the conversion keeps it valid shell.
func TestPromotedScriptRuns(t *testing.T) {
	root := t.TempDir()
	frag := filepath.Join(root, "fragments")
	target := filepath.Join(root, "out")
	_, err := WriteFragment(frag, "multi", "USER root\nRUN printf 'a\\n' > "+target+" \\\n && printf 'b\\n' >> "+target+"\nUSER sandbox\n", testNow)
	require.NoError(t, err)
	p, err := BuildRecipe(frag, root, "multi", PromoteOptions{FinalUser: "sandbox"})
	require.NoError(t, err)
	out, err := exec.Command("sh", "-c", "set -eux\n"+p.Recipe.Installer.Script.Run).CombinedOutput()
	require.NoError(t, err, string(out))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", string(data))
}

func TestPromoteDefaultsAndRefusals(t *testing.T) {
	root := t.TempDir()
	frag := filepath.Join(root, "fragments")
	_, err := WriteFragment(frag, "plain", "USER root\nRUN install -m 0755 /tmp/x /usr/local/bin/plain\nUSER sandbox\n", testNow)
	require.NoError(t, err)
	p, err := BuildRecipe(frag, root, "plain", PromoteOptions{Bins: []string{"plain", "plainctl"}, FinalUser: "sandbox"})
	require.NoError(t, err)
	assert.Equal(t, "command -v plain && command -v plainctl", p.Recipe.Check.Command, "the recipe checks every binary, like the fragment did")
	assert.Equal(t, "plain", p.Recipe.Name)

	_, err = WriteFragment(frag, "copying", "COPY x /usr/local/bin/x\nRUN true\n", testNow)
	require.NoError(t, err)
	_, err = BuildRecipe(frag, root, "copying", PromoteOptions{FinalUser: "sandbox"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "COPY")

	_, err = WriteFragment(frag, "mixed", "USER root\nRUN true\nUSER sandbox\nRUN true\n", testNow)
	require.NoError(t, err)
	_, err = BuildRecipe(frag, root, "mixed", PromoteOptions{FinalUser: "sandbox"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both as root and as the final user")

	_, err = BuildRecipe(frag, root, "absent", PromoteOptions{})
	require.Error(t, err)
}

func TestPromoteShellHeredocKeepsCommand(t *testing.T) {
	root := t.TempDir()
	frag := filepath.Join(root, "fragments")
	target := filepath.Join(root, "conf")
	_, err := WriteFragment(frag, "conf", "USER root\nRUN cat <<'EOF' > "+target+"\nkey = value\nEOF\nUSER sandbox\n", testNow)
	require.NoError(t, err)
	p, err := BuildRecipe(frag, root, "conf", PromoteOptions{FinalUser: "sandbox"})
	require.NoError(t, err)
	out, err := exec.Command("sh", "-c", "set -eu\n"+p.Recipe.Installer.Script.Run).CombinedOutput()
	require.NoError(t, err, string(out))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "key = value\n", string(data))
}

func TestPromoteContinuationWithComments(t *testing.T) {
	root := t.TempDir()
	frag := filepath.Join(root, "fragments")
	target := filepath.Join(root, "out")
	body := "USER root\nRUN printf 'a\\n' > " + target + " \\\n  # second step\n\n  && printf 'b\\n' >> " + target + "\nUSER sandbox\n"
	_, err := WriteFragment(frag, "commented", body, testNow)
	require.NoError(t, err)
	p, err := BuildRecipe(frag, root, "commented", PromoteOptions{FinalUser: "sandbox"})
	require.NoError(t, err)
	out, err := exec.Command("sh", "-c", "set -eu\n"+p.Recipe.Installer.Script.Run).CombinedOutput()
	require.NoError(t, err, string(out))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", string(data))
}

// TestPromoteGrantsOptKey covers the skill's /opt/<key> location: the
// image policy must let the sandbox read the unpacked tree after promotion,
// with or without an existing definition.
func TestPromoteGrantsOptKey(t *testing.T) {
	root := t.TempDir()
	frag := filepath.Join(root, "fragments")
	_, err := WriteFragment(frag, "tree", "USER root\nRUN mkdir -p /opt/tree && ln -s /opt/tree/bin/tree /usr/local/bin/tree\nUSER sandbox\n", testNow)
	require.NoError(t, err)

	p, err := BuildRecipe(frag, root, "tree", PromoteOptions{FinalUser: "sandbox"})
	require.NoError(t, err)
	require.NotNil(t, p.Recipe.Image)
	assert.Equal(t, []string{"/opt/tree"}, p.Recipe.Image.ReadOnly)
	assert.Contains(t, string(p.Data), "/opt/tree")

	// An existing definition without /opt/<key> keeps its needs and gains it.
	existing := &component.Component{Key: "tree", Image: &component.ImageNeeds{ReadOnly: []string{"/etc/tree"}}}
	p, err = BuildRecipe(frag, root, "tree", PromoteOptions{FinalUser: "sandbox", Existing: existing})
	require.NoError(t, err)
	assert.Equal(t, []string{"/etc/tree", "/opt/tree"}, p.Recipe.Image.ReadOnly)
	assert.Equal(t, []string{"/etc/tree"}, existing.Image.ReadOnly, "the existing definition is not changed")

	// A definition that already grants /opt/<key> read-write is left alone.
	existing = &component.Component{Key: "tree", Image: &component.ImageNeeds{ReadWrite: []string{"/opt/tree"}}}
	p, err = BuildRecipe(frag, root, "tree", PromoteOptions{FinalUser: "sandbox", Existing: existing})
	require.NoError(t, err)
	assert.Empty(t, p.Recipe.Image.ReadOnly)
	assert.Equal(t, []string{"/opt/tree"}, p.Recipe.Image.ReadWrite)
}

func TestParseEnv(t *testing.T) {
	got, err := parseEnv(`A=1 B="two words" C='it''s' D=x\ y`)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"A": "1", "B": "two words", "C": "its", "D": "x y"}, got)
	got, err = parseEnv("LEGACY value with spaces")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"LEGACY": "value with spaces"}, got)
	_, err = parseEnv("PATH=/opt/tool/bin:$PATH")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references a variable")
	_, err = parseEnv(`X="open`)
	require.Error(t, err)
}

func TestPromoteRefusesBareUserAndVariableEnv(t *testing.T) {
	root := t.TempDir()
	frag := filepath.Join(root, "fragments")
	require.NoError(t, os.MkdirAll(frag, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(frag, "bare.containerfile"), []byte("USER\nRUN true\n"), 0o644))
	_, err := BuildRecipe(frag, root, "bare", PromoteOptions{FinalUser: "sandbox"})
	require.Error(t, err, "a bare USER is an error, not a panic")

	_, err = WriteFragment(frag, "pathy", "USER root\nENV PATH=/opt/tool/bin:$PATH\nRUN true\nUSER sandbox\n", testNow)
	require.NoError(t, err)
	_, err = BuildRecipe(frag, root, "pathy", PromoteOptions{FinalUser: "sandbox"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references a variable")
}
