package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generic decodes a YAML document into plain maps, slices and scalars, so two
// documents can be compared independent of style, key order and comments.
func generic(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	require.NoError(t, yaml.Unmarshal(data, &v))
	return v
}

func TestRoundTripUpstreamProfiles(t *testing.T) {
	// These files carry no explicit zero values, so load then marshal must
	// reproduce the original document exactly, comments aside.
	for _, name := range []string{
		"pypi.yaml", "claude-code.yaml", "github.yaml",
		"aws.yaml", "aws-s3.yaml", "copilot.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(upstreamDir, name)
			orig, err := os.ReadFile(path)
			require.NoError(t, err)
			p, err := LoadFile(path)
			require.NoError(t, err)
			out, err := Marshal(p)
			require.NoError(t, err)
			assert.Equal(t, generic(t, orig), generic(t, out))
		})
	}
}

func TestMarshalIsStable(t *testing.T) {
	upstream, overlay := loadShipped(t)
	for _, e := range append(upstream, overlay...) {
		t.Run(e.SourceFile, func(t *testing.T) {
			first, err := Marshal(e.Profile)
			require.NoError(t, err)
			again, err := Parse(first, e.SourceFile)
			require.NoError(t, err)
			second, err := Marshal(again)
			require.NoError(t, err)
			assert.Equal(t, string(first), string(second))
			assert.Empty(t, Lint(again, e.SourceFile), "a marshalled profile still lints clean")
		})
	}
}

func TestMarshalFollowsUpstreamLayout(t *testing.T) {
	p, err := LoadFile(filepath.Join(upstreamDir, "github.yaml"))
	require.NoError(t, err)
	out, err := Marshal(p)
	require.NoError(t, err)
	text := string(out)

	var order []int
	for _, key := range []string{"id:", "display_name:", "description:", "category:", "credentials:", "discovery:", "endpoints:", "binaries:"} {
		order = append(order, strings.Index(text, "\n"+key))
	}
	order[0] = strings.Index(text, "id:")
	for i := 1; i < len(order); i++ {
		assert.Less(t, order[i-1], order[i], "top-level keys keep the upstream file order")
	}
	assert.Contains(t, text, "env_vars: [GITHUB_TOKEN, GH_TOKEN]")
	assert.Contains(t, text, "- allow: {method: GET, path: \"**\"}")
	assert.Contains(t, text, "- allow: {method: POST, path: /**/git-upload-pack}")
}

func TestPassThroughBlocksKeepKeyOrder(t *testing.T) {
	p, err := LoadFile(filepath.Join(upstreamDir, "aws.yaml"))
	require.NoError(t, err)
	out, err := Marshal(p)
	require.NoError(t, err)
	text := string(out)
	// refresh keys and material entries keep the authored order, not
	// alphabetical order.
	assert.Less(t, strings.Index(text, "strategy: aws_sts_assume_role"), strings.Index(text, "additional_outputs:"))
	assert.Less(t, strings.Index(text, "additional_outputs:"), strings.Index(text, "material:"))
	assert.Less(t, strings.Index(text, "- name: role_arn"), strings.Index(text, "description: ARN of the IAM role to assume"))
}

func TestParseNormalizesBinaryObjects(t *testing.T) {
	p, err := Parse([]byte(`
id: example
display_name: Example
binaries:
  - /usr/bin/curl
  - path: /usr/bin/wget
  - {path: "/opt/tools/**"}
`), "example.yaml")
	require.NoError(t, err)
	assert.Equal(t, Binaries{"/usr/bin/curl", "/usr/bin/wget", "/opt/tools/**"}, p.Binaries)

	out, err := Marshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(out), "binaries:\n  - /usr/bin/curl\n  - /usr/bin/wget\n  - /opt/tools/**\n")
}

func TestQueryMatcherForms(t *testing.T) {
	p, err := Parse([]byte(`
id: example
display_name: Example
endpoints:
  - host: api.example.com
    port: 443
    protocol: rest
    rules:
      - allow:
          method: GET
          path: /search
          query:
            q: "*"
            sort: { any: [asc, desc] }
            page: { glob: "[0-9]*" }
`), "example.yaml")
	require.NoError(t, err)
	q := p.Endpoints[0].Rules[0].Allow.Query
	assert.Equal(t, QueryMatcher{Glob: "*"}, q["q"])
	assert.Equal(t, QueryMatcher{Any: []string{"asc", "desc"}}, q["sort"])
	assert.Equal(t, QueryMatcher{Glob: "[0-9]*"}, q["page"])

	out, err := Marshal(p)
	require.NoError(t, err)
	again, err := Parse(out, "again.yaml")
	require.NoError(t, err)
	assert.Equal(t, p, again)
}

func TestParseErrors(t *testing.T) {
	tests := map[string]struct {
		doc  string
		want string
	}{
		"unknown top-level field": {"id: x\ndisplay_name: X\nhomepage: y\n", `unknown field "homepage"`},
		"unknown credential field": {
			"id: x\ndisplay_name: X\ncredentials:\n  - name: a\n    secret_value: y\n", `unknown field "secret_value"`},
		"unknown rule field": {
			"id: x\ndisplay_name: X\nendpoints:\n  - host: a.example.com\n    port: 443\n    rules:\n      - deny: {method: GET}\n",
			`unknown field "deny"`},
		"binary with removed harness field": {
			"id: x\ndisplay_name: X\nbinaries:\n  - {path: /usr/bin/a, harness: claude}\n",
			"unsupported provider profile binary fields: harness; binaries accept only 'path'"},
		"binary mapping without path": {"id: x\ndisplay_name: X\nbinaries:\n  - {}\n", `missing field "path"`},
		"binary number":               {"id: x\ndisplay_name: X\nbinaries: [42]\n", "binaries[0]: binary entry must be a path string"},
		"matcher number": {
			"id: x\ndisplay_name: X\nendpoints:\n  - host: a.example.com\n    port: 443\n    rules:\n      - allow: {method: GET, query: {q: 1}}\n",
			"matcher must be a string or a mapping"},
		// A negative port cannot fit the unsigned port field. goccy reports a
		// type error by source location, not by field name, so we assert on
		// the offending value being echoed rather than on goccy's exact
		// "cannot unmarshal ... uint32" phrasing, which changes across releases.
		"negative port rejected": {"id: x\ndisplay_name: X\nendpoints:\n  - host: a.example.com\n    port: -1\n", "-1"},
		"missing id":             {"display_name: X\n", `missing field "id"`},
		"missing display name":   {"id: x\n", `missing field "display_name"`},
		"credential name":        {"id: x\ndisplay_name: X\ncredentials:\n  - env_vars: [A]\n", `credentials[0]: missing field "name"`},
		"endpoint host":          {"id: x\ndisplay_name: X\nendpoints:\n  - port: 443\n", `endpoints[0]: missing field "host"`},
		"duplicate key":          {"id: x\nid: y\ndisplay_name: X\n", "already defined"},
		"not a mapping":          {"- id: x\n", "parse provider profile bad.yaml"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc), "bad.yaml")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "bad.yaml")
		})
	}
}

func TestLoadFileMissing(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "absent.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read provider profile")
}

func TestLoadDir(t *testing.T) {
	fsys := fstest.MapFS{
		"b.yml":         {Data: []byte("id: b\ndisplay_name: B\n")},
		"a.yaml":        {Data: []byte("id: a\ndisplay_name: A\n")},
		"README.md":     {Data: []byte("# not a profile\n")},
		"SOURCE.md":     {Data: []byte("pins\n")},
		"sub/c.yaml":    {Data: []byte("id: c\ndisplay_name: C\n")},
		"notes.yaml.bk": {Data: []byte("junk")},
	}
	entries, err := LoadDir(fsys, TierOverlay)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "a", entries[0].Profile.ID)
	assert.Equal(t, "profiles/overlay/a.yaml", entries[0].SourceFile)
	assert.Equal(t, TierOverlay, entries[0].Tier)
	assert.Equal(t, "profiles/overlay/b.yml", entries[1].SourceFile)

	_, err = LoadDir(fstest.MapFS{"bad.yaml": {Data: []byte("id: x\n")}}, TierProject)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".sandcast/profiles/bad.yaml")
}
