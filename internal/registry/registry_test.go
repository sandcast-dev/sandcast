package registry

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validYAML = `schema_version: 1
targets:
  openshell:
    - name: base
      ref: example.com/base:1
      default: true
      notes: the base
    - name: alt
      ref: example.com/alt:1
  container:
    - name: fedora
      ref: example.com/fedora:42
      default: true
`

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr string
		check   func(t *testing.T, r *Registry)
	}{
		{
			name: "valid",
			data: validYAML,
			check: func(t *testing.T, r *Registry) {
				assert.Equal(t, []Entry{
					{Name: "base", Ref: "example.com/base:1", Default: true, Notes: "the base"},
					{Name: "alt", Ref: "example.com/alt:1"},
				}, r.Targets[TargetOpenShell])
				assert.Len(t, r.Targets[TargetContainer], 1)
			},
		},
		{
			name:  "empty file",
			data:  "",
			check: func(t *testing.T, r *Registry) { assert.Empty(t, r.Targets) },
		},
		{
			name:  "comments only",
			data:  "# nothing yet\n",
			check: func(t *testing.T, r *Registry) { assert.Empty(t, r.Targets) },
		},
		{
			name: "no default is fine for a user file",
			data: "schema_version: 1\ntargets:\n  container:\n    - name: mine\n      ref: example.com/mine:1\n",
			check: func(t *testing.T, r *Registry) {
				assert.Equal(t, []Entry{{Name: "mine", Ref: "example.com/mine:1"}}, r.Targets[TargetContainer])
			},
		},
		{
			name:    "unknown top-level field",
			data:    "schema_version: 1\nimages: {}\n",
			wantErr: "parse base image registry",
		},
		{
			name:    "unknown entry field",
			data:    "schema_version: 1\ntargets:\n  container:\n    - name: a\n      ref: r\n      is_default: true\n",
			wantErr: "parse base image registry",
		},
		{
			name:    "newer schema",
			data:    "schema_version: 2\ntargets: {}\n",
			wantErr: "schema_version 2 is not supported, this sandcast reads schema_version 1",
		},
		{
			name:    "missing schema version",
			data:    "targets:\n  container:\n    - name: a\n      ref: r\n",
			wantErr: "schema_version 0 is not supported",
		},
		{
			name:    "unknown target",
			data:    "schema_version: 1\ntargets:\n  ssh:\n    - name: a\n      ref: r\n",
			wantErr: `unknown target "ssh", want one of container, openshell`,
		},
		{
			name:    "duplicate name",
			data:    "schema_version: 1\ntargets:\n  container:\n    - name: a\n      ref: r1\n    - name: a\n      ref: r2\n",
			wantErr: `target container: duplicate entry name "a"`,
		},
		{
			name:    "empty ref",
			data:    "schema_version: 1\ntargets:\n  container:\n    - name: a\n      ref: \"\"\n",
			wantErr: "target container, entry 1 (a): ref is empty",
		},
		{
			name:    "empty name",
			data:    "schema_version: 1\ntargets:\n  container:\n    - ref: r\n",
			wantErr: "target container, entry 1: name is empty",
		},
		{
			name:    "two defaults",
			data:    "schema_version: 1\ntargets:\n  container:\n    - name: a\n      ref: r1\n      default: true\n    - name: b\n      ref: r2\n      default: true\n",
			wantErr: "target container: 2 entries are marked default (a, b), want exactly one",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Parse([]byte(tt.data), "test.yaml")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Contains(t, err.Error(), "test.yaml")
				return
			}
			require.NoError(t, err)
			tt.check(t, r)
		})
	}
}

func TestLoadShippedRepositoryFile(t *testing.T) {
	r, err := LoadShipped(os.DirFS("../.."))
	require.NoError(t, err)
	require.NoError(t, r.Validate())

	def, err := r.Default(TargetOpenShell)
	require.NoError(t, err)
	assert.Equal(t, "nvidia-community-base", def.Name)
	assert.Equal(t, "ghcr.io/nvidia/openshell-community/sandboxes/base:latest", def.Ref)

	def, err = r.Default(TargetContainer)
	require.NoError(t, err)
	assert.Equal(t, "fedora-42", def.Name)
	assert.Equal(t, "registry.fedoraproject.org/fedora:42", def.Ref)

	names := map[string][]string{}
	for _, target := range KnownTargets {
		for _, e := range r.List(target) {
			names[target] = append(names[target], e.Name)
			assert.NotEmpty(t, e.Notes, "%s/%s has notes", target, e.Name)
		}
	}
	assert.Equal(t, map[string][]string{
		TargetOpenShell: {"nvidia-community-base", "nvidia-ubuntu"},
		TargetContainer: {"fedora-42", "ubuntu-24.04"},
	}, names)

	e, ok, _, err := r.Resolve("nvidia-ubuntu", TargetOpenShell)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "nvcr.io/nvidia/base/ubuntu:24.04", e.Ref)
}

func TestLoadShippedErrors(t *testing.T) {
	_, err := LoadShipped(fstest.MapFS{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read shipped base image registry")

	noDefault := "schema_version: 1\ntargets:\n  container:\n    - name: a\n      ref: r\n"
	_, err = LoadShipped(fstest.MapFS{FileName: {Data: []byte(noDefault)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base-images.yaml (shipped): target container: no entry is marked default")
}

func TestLoadUser(t *testing.T) {
	dir := t.TempDir()

	r, err := LoadUser(filepath.Join(dir, "missing.yaml"))
	require.NoError(t, err)
	assert.Empty(t, r.Targets)

	path := filepath.Join(dir, FileName)
	require.NoError(t, os.WriteFile(path, []byte(validYAML), 0o644))
	r, err = LoadUser(path)
	require.NoError(t, err)
	assert.Len(t, r.Targets[TargetOpenShell], 2)

	require.NoError(t, os.WriteFile(path, []byte("schema_version: 1\nbogus: true\n"), 0o644))
	_, err = LoadUser(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)

	_, err = LoadUser(dir)
	require.Error(t, err, "a directory is not a registry file")
}

func TestUserPath(t *testing.T) {
	t.Setenv("SANDCAST_CONFIG_DIR", "/cfg")
	got, err := UserPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/cfg", FileName), got)
}

func shippedFixture(t *testing.T) *Registry {
	t.Helper()
	r, err := Parse([]byte(validYAML), "shipped")
	require.NoError(t, err)
	require.NoError(t, r.Validate())
	return r
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name    string
		user    map[string][]Entry
		want    map[string][]Entry
		wantErr string
	}{
		{
			name: "no user entries",
			user: nil,
			want: shippedFixtureTargets(),
		},
		{
			name: "user overrides ref and keeps the shipped default",
			user: map[string][]Entry{TargetOpenShell: {{Name: "base", Ref: "mirror.example.com/base:1"}}},
			want: map[string][]Entry{
				TargetOpenShell: {
					{Name: "base", Ref: "mirror.example.com/base:1", Default: true},
					{Name: "alt", Ref: "example.com/alt:1"},
				},
				TargetContainer: shippedFixtureTargets()[TargetContainer],
			},
		},
		{
			name: "user default replaces the shipped default",
			user: map[string][]Entry{TargetContainer: {{Name: "mine", Ref: "example.com/mine:1", Default: true, Notes: "own"}}},
			want: map[string][]Entry{
				TargetOpenShell: shippedFixtureTargets()[TargetOpenShell],
				TargetContainer: {
					{Name: "fedora", Ref: "example.com/fedora:42"},
					{Name: "mine", Ref: "example.com/mine:1", Default: true, Notes: "own"},
				},
			},
		},
		{
			name: "user marks a shipped entry as default",
			user: map[string][]Entry{TargetOpenShell: {{Name: "alt", Ref: "example.com/alt:2", Default: true}}},
			want: map[string][]Entry{
				TargetOpenShell: {
					{Name: "base", Ref: "example.com/base:1", Notes: "the base"},
					{Name: "alt", Ref: "example.com/alt:2", Default: true},
				},
				TargetContainer: shippedFixtureTargets()[TargetContainer],
			},
		},
		{
			name:    "user adds two defaults",
			user:    map[string][]Entry{TargetContainer: {{Name: "a", Ref: "r", Default: true}, {Name: "b", Ref: "r", Default: true}}},
			wantErr: "target container: 2 entries are marked default (a, b)",
		},
		{
			name:    "user adds an unknown target",
			user:    map[string][]Entry{"ssh": {{Name: "a", Ref: "r", Default: true}}},
			wantErr: `unknown target "ssh"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shipped := shippedFixture(t)
			got, err := Merge(shipped, &Registry{Targets: tt.user})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "merged base image registry")
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Targets)
			assert.Equal(t, shippedFixtureTargets(), shipped.Targets, "inputs are not modified")
		})
	}
}

func TestMergeUserOnlyTargetNeedsDefault(t *testing.T) {
	shipped := &Registry{Targets: map[string][]Entry{TargetOpenShell: {{Name: "base", Ref: "r", Default: true}}}}
	_, err := Merge(shipped, &Registry{Targets: map[string][]Entry{TargetContainer: {{Name: "a", Ref: "r"}}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target container: no entry is marked default")

	got, err := Merge(shipped, nil)
	require.NoError(t, err)
	assert.Equal(t, shipped.Targets, got.Targets)
}

func shippedFixtureTargets() map[string][]Entry {
	return map[string][]Entry{
		TargetOpenShell: {
			{Name: "base", Ref: "example.com/base:1", Default: true, Notes: "the base"},
			{Name: "alt", Ref: "example.com/alt:1"},
		},
		TargetContainer: {
			{Name: "fedora", Ref: "example.com/fedora:42", Default: true},
		},
	}
}

func TestValidate(t *testing.T) {
	r := &Registry{Targets: map[string][]Entry{
		TargetContainer: {{Name: "a", Ref: ""}, {Name: "a", Ref: "r"}},
		TargetOpenShell: {},
	}}
	err := r.Validate()
	require.Error(t, err)
	assert.Equal(t, "target container, entry 1 (a): ref is empty\n"+
		"target container: duplicate entry name \"a\"\n"+
		"target container: no entry is marked default, want exactly one\n"+
		"target openshell: no entry is marked default, want exactly one", err.Error())

	assert.NoError(t, (&Registry{Targets: map[string][]Entry{}}).Validate())
}

func TestDefaultAndList(t *testing.T) {
	r := &Registry{Targets: map[string][]Entry{
		TargetContainer: {
			{Name: "zeta", Ref: "z"},
			{Name: "mid", Ref: "m", Default: true},
			{Name: "alpha", Ref: "a"},
		},
	}}
	def, err := r.Default(TargetContainer)
	require.NoError(t, err)
	assert.Equal(t, "mid", def.Name)

	_, err = r.Default(TargetOpenShell)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no default entry for target openshell")

	_, err = r.Default("ssh")
	require.Error(t, err)

	var names []string
	for _, e := range r.List(TargetContainer) {
		names = append(names, e.Name)
	}
	assert.Equal(t, []string{"mid", "alpha", "zeta"}, names)
	assert.Equal(t, "zeta", r.Targets[TargetContainer][0].Name, "List does not reorder the registry")
	assert.Empty(t, r.List(TargetOpenShell))
}

func TestResolve(t *testing.T) {
	r := shippedFixture(t)
	tests := []struct {
		name       string
		input      string
		target     string
		want       Entry
		inRegistry bool
		notice     string
		wantErr    string
	}{
		{
			name:       "empty input selects the default",
			input:      "",
			target:     TargetOpenShell,
			want:       Entry{Name: "base", Ref: "example.com/base:1", Default: true, Notes: "the base"},
			inRegistry: true,
		},
		{
			name:       "name",
			input:      "alt",
			target:     TargetOpenShell,
			want:       Entry{Name: "alt", Ref: "example.com/alt:1"},
			inRegistry: true,
		},
		{
			name:       "reference",
			input:      " example.com/fedora:42 ",
			target:     TargetContainer,
			want:       Entry{Name: "fedora", Ref: "example.com/fedora:42", Default: true},
			inRegistry: true,
		},
		{
			name:       "name of another target is not a match",
			input:      "alt",
			target:     TargetContainer,
			want:       Entry{Ref: "alt"},
			inRegistry: false,
			notice:     `base image "alt" is not in the registry; no registry notes apply`,
		},
		{
			name:       "reference outside the registry",
			input:      "quay.io/example/custom:1.0",
			target:     TargetContainer,
			want:       Entry{Ref: "quay.io/example/custom:1.0"},
			inRegistry: false,
			notice:     `base image "quay.io/example/custom:1.0" is not in the registry; no registry notes apply`,
		},
		{
			name:    "unknown target",
			input:   "alt",
			target:  "ssh",
			wantErr: `unknown target "ssh"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, in, notice, err := r.Resolve(tt.input, tt.target)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.inRegistry, in)
			assert.Equal(t, tt.notice, notice)
		})
	}

	empty := &Registry{Targets: map[string][]Entry{}}
	_, _, _, err := empty.Resolve("", TargetContainer)
	require.Error(t, err, "no default to fall back to")
}
