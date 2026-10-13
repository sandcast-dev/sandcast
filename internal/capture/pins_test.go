package capture

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyPin(t *testing.T) {
	for v, want := range map[string]string{
		"1.95.0": PinExact, "3.14.5": PinExact, "v20.11.1": PinExact, "1.26.7": PinExact, "1.2.3-rc.1": PinExact,
		"stable": PinChannel, "nightly": PinChannel, "lts/*": PinChannel, "lts/iron": PinChannel, "system": PinChannel, "latest": PinChannel,
		">=3.12": PinRange, "^20": PinRange, "~1.2": PinRange, "1.26": PinRange, "20": PinRange, "3.12.*": PinRange,
	} {
		assert.Equal(t, want, classifyPin(v), v)
	}
}

func writeProject(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	listed, err := projectFiles(root)
	require.NoError(t, err)
	return root, listed
}

func TestReadPins(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  toolPins
		notes []string
	}{
		{
			name:  "go.mod go and toolchain",
			files: map[string]string{"go.mod": "module example.com/x\n\ngo 1.26.7 // comment\n\ntoolchain go1.26.8\n"},
			want:  toolPins{"go": {{Value: "1.26.8", File: "go.mod", Kind: PinExact}, {Value: "1.26.7", File: "go.mod", Kind: PinExact}}},
		},
		{
			name: "go.work highest across modules first",
			files: map[string]string{
				"go.work":     "go 1.25.0\n\nuse (\n\t./a\n\t\"./b\"\n)\n",
				"a/go.mod":    "module a\n\ngo 1.26.2\n",
				"b/go.mod":    "module b\n\ngo 1.24\n",
				"c/go.mod":    "module c\n\ngo 1.27.1\n",
				"go.work.sum": "",
			},
			want: toolPins{"go": {
				{Value: "1.27.1", File: "c/go.mod", Kind: PinExact},
				{Value: "1.26.2", File: "a/go.mod", Kind: PinExact},
				{Value: "1.25.0", File: "go.work", Kind: PinExact},
				{Value: "1.24", File: "b/go.mod", Kind: PinRange},
			}},
		},
		{
			name:  "go.mod without go directive",
			files: map[string]string{"go.mod": "module x\n"},
			want:  toolPins{},
			notes: []string{"go.mod: no go directive"},
		},
		{
			name: "rust toolchain files",
			files: map[string]string{
				"rust-toolchain.toml": "[toolchain]\nchannel = \"1.95.0\"\ncomponents = [\"clippy\"]\n",
				"sub/rust-toolchain":  "stable\n",
			},
			want: toolPins{"rust": {{Value: "1.95.0", File: "rust-toolchain.toml", Kind: PinExact}, {Value: "stable", File: "sub/rust-toolchain", Kind: PinChannel}}},
		},
		{
			name:  "rust toolchain invalid toml",
			files: map[string]string{"rust-toolchain.toml": "[toolchain\nchannel = 1.95"},
			want:  toolPins{},
			notes: []string{"rust-toolchain.toml: invalid TOML"},
		},
		{
			name: "python files",
			files: map[string]string{
				".python-version": "# pinned\n3.14.5\n",
				"pyproject.toml":  "[project]\nname = \"x\"\nrequires-python = \">=3.12\"\n",
			},
			want: toolPins{"python": {{Value: "3.14.5", File: ".python-version", Kind: PinExact}, {Value: ">=3.12", File: "pyproject.toml", Kind: PinRange}}},
		},
		{
			name:  "invalid pyproject",
			files: map[string]string{"pyproject.toml": "[project\nrequires-python = "},
			want:  toolPins{},
			notes: []string{"pyproject.toml: invalid TOML"},
		},
		{
			name: "node files",
			files: map[string]string{
				".node-version": "v22.11.0\n",
				".nvmrc":        "lts/*\n",
				"package.json":  `{"name":"x","engines":{"node":"^20"}}`,
			},
			want: toolPins{"node": {
				{Value: "22.11.0", File: ".node-version", Kind: PinExact},
				{Value: "lts/*", File: ".nvmrc", Kind: PinChannel},
				{Value: "^20", File: "package.json", Kind: PinRange},
			}},
		},
		{
			name:  "invalid package.json",
			files: map[string]string{"package.json": `{"engines": `},
			want:  toolPins{},
			notes: []string{"package.json: invalid JSON"},
		},
		{
			name: "tool-versions and mise.toml",
			files: map[string]string{
				".tool-versions": "nodejs 20.11.1 # comment\ngolang 1.26.7\nterraform 1.9.0\n\n",
				"mise.toml":      "[tools]\npython = \"3.13\"\nrust = [\"1.95.0\", \"nightly\"]\ngh = { version = \"2.102.0\" }\n",
			},
			want: toolPins{
				"node":      {{Value: "20.11.1", File: ".tool-versions", Kind: PinExact}},
				"go":        {{Value: "1.26.7", File: ".tool-versions", Kind: PinExact}},
				"terraform": {{Value: "1.9.0", File: ".tool-versions", Kind: PinExact}},
				"gh":        {{Value: "2.102.0", File: "mise.toml", Kind: PinExact}},
				"python":    {{Value: "3.13", File: "mise.toml", Kind: PinRange}},
				"rust":      {{Value: "1.95.0", File: "mise.toml", Kind: PinExact}, {Value: "nightly", File: "mise.toml", Kind: PinChannel}},
			},
		},
		{
			name:  "invalid mise.toml",
			files: map[string]string{"mise.toml": "[tools\n"},
			want:  toolPins{},
			notes: []string{"mise.toml: invalid TOML"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, files := writeProject(t, tt.files)
			got, notes := readPins(root, files)
			assert.Equal(t, tt.want, got)
			require.Len(t, notes, len(tt.notes))
			for i, n := range tt.notes {
				assert.Contains(t, notes[i], n)
			}
		})
	}
}

// TestMatchNotesUnparsablePinFiles checks the spec edge case: a pin file
// that cannot be parsed adds a note and match still succeeds.
func TestMatchNotesUnparsablePinFiles(t *testing.T) {
	root, _ := writeProject(t, map[string]string{"package.json": `{"engines": `, "go.mod": "module x\n"})
	res, err := Match(context.Background(), root, shipped(t), Options{})
	require.NoError(t, err)
	require.Len(t, res.Notes, 2)
	assert.Contains(t, res.Notes[0], "go.mod: no go directive")
	assert.Contains(t, res.Notes[1], "package.json: invalid JSON")
	assert.Contains(t, byKey(res), "node", "the file is still evidence")
}

// TestMatchNotesUnreadablePinFile: a pin file that exists but cannot be read
// yields a note naming it, and match still succeeds.
func TestMatchNotesUnreadablePinFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	root, _ := writeProject(t, map[string]string{".python-version": "3.14.5\n"})
	file := filepath.Join(root, ".python-version")
	require.NoError(t, os.Chmod(file, 0))
	t.Cleanup(func() { _ = os.Chmod(file, 0o644) })
	res, err := Match(context.Background(), root, shipped(t), Options{})
	require.NoError(t, err)
	assert.Contains(t, strings.Join(res.Notes, "\n"), ".python-version: cannot be read: permission denied")
}
