package probe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const factsOutput = `{"type":"os","id":"debian","id_like":"","name":"Debian GNU/Linux","version":"13"}
{"type":"pkgmgr","name":"apt-get"}
{"type":"user","name":"sandbox","uid":1000,"gid":1000,"group":"sandbox","home":"/home/sandbox","shell":"/bin/bash"}
{"type":"shells","list":["/bin/sh","/bin/bash","/usr/bin/bash","/bin/sh"]}
`

func TestParse(t *testing.T) {
	debian := &Facts{
		OS:             OS{ID: "debian", Name: "Debian GNU/Linux", Version: "13"},
		PackageManager: "apt-get",
		User:           User{Name: "sandbox", UID: 1000, GID: 1000, Group: "sandbox", Home: "/home/sandbox", Shell: "/bin/bash"},
		Shells:         []string{"/bin/sh", "/bin/bash", "/usr/bin/bash"},
	}
	tests := []struct {
		name   string
		out    string
		facts  *Facts
		checks []rawCheck
		err    string
	}{
		{
			name: "full",
			out: factsOutput +
				`{"type":"check","key":"go","exit":0,"path":"/usr/local/go/bin/go","out":"go version go1.25.1 linux/arm64"}` + "\n" +
				`{"type":"check","key":"node","exit":127,"path":"","out":"sh: 1: node: not found"}` + "\n",
			facts: debian,
			checks: []rawCheck{
				{Key: "go", Path: "/usr/local/go/bin/go", Out: "go version go1.25.1 linux/arm64"},
				{Key: "node", Exit: 127, Out: "sh: 1: node: not found"},
			},
		},
		{
			name:   "partial without facts",
			out:    `{"type":"check","key":"go","exit":0,"path":"","out":"a\tb\u0001\"c\"\\d\nnext"}`,
			checks: []rawCheck{{Key: "go", Out: "a\tb\x01\"c\"\\d\nnext"}},
		},
		{
			name:  "facts without checks",
			out:   factsOutput,
			facts: debian,
		},
		{
			name: "home rc facts and check paths",
			out: factsOutput + `{"type":"homerc","resets_path":[".bashrc",".bashrc"]}` + "\n" +
				`{"type":"check","key":"python","exit":0,"path":"/sandbox/.uv/bin/python3","paths":["/sandbox/.uv/bin/python3","/usr/bin/python3"],"out":"Python 3.14.3"}` + "\n" +
				`{"type":"check","key":"go","exit":0,"path":"","paths":[],"out":""}` + "\n",
			facts: func() *Facts { f := *debian; f.HomeRCResetsPath = []string{".bashrc"}; return &f }(),
			checks: []rawCheck{
				{Key: "python", Path: "/sandbox/.uv/bin/python3", Paths: []string{"/sandbox/.uv/bin/python3", "/usr/bin/python3"}, Out: "Python 3.14.3"},
				{Key: "go"},
			},
		},
		{
			name:  "path facts",
			out:   factsOutput + `{"type":"path","dirs":["/opt/x/bin","/usr/bin","/opt/x/bin",""]}` + "\n",
			facts: func() *Facts { f := *debian; f.Path = []string{"/opt/x/bin", "/usr/bin"}; return &f }(),
		},
		{
			name: "env facts",
			out: factsOutput + `{"type":"env","vars":[{"name":"VIRTUAL_ENV","value":"/sandbox/.venv"},` +
				`{"name":"UV_PYTHON_INSTALL_DIR","value":"/sandbox/.uv/python"},{"name":"VIRTUAL_ENV","value":"/other"},` +
				`{"name":"","value":"/x"},{"name":"EMPTY","value":""}]}` + "\n",
			facts: func() *Facts {
				f := *debian
				f.Env = []EnvVar{{Name: "UV_PYTHON_INSTALL_DIR", Value: "/sandbox/.uv/python"}, {Name: "VIRTUAL_ENV", Value: "/sandbox/.venv"}}
				return &f
			}(),
		},
		{
			name:  "empty env facts",
			out:   factsOutput + `{"type":"env","vars":[]}` + "\n",
			facts: debian,
		},
		{
			name: "binary hits",
			out: `{"type":"check","key":"python","exit":0,"path":"/usr/local/bin/python","paths":["/usr/local/bin/python"],` +
				`"bins":[{"name":"python","paths":["/usr/local/bin/python"],"real":["/sandbox/.uv/python/bin/python3.14"]},` +
				`{"name":"pip","paths":["/usr/bin/pip","/opt/pip"],"real":["/usr/bin/pip"]},{"name":"uv","paths":[],"real":[]},{"name":"","paths":["/x"]}],"out":""}`,
			checks: []rawCheck{{
				Key: "python", Path: "/usr/local/bin/python", Paths: []string{"/usr/local/bin/python"},
				Bins: []BinHits{
					{Name: "python", Paths: []string{"/usr/local/bin/python"}, Real: []string{"/sandbox/.uv/python/bin/python3.14"}},
					{Name: "pip", Paths: []string{"/usr/bin/pip", "/opt/pip"}},
					{Name: "uv"},
				},
			}},
		},
		{
			name: "noise and unknown types are ignored",
			out: "WARNING: platform mismatch\r\n\n" + factsOutput +
				`{"type":"future","x":1}` + "\n" + `{"type":"check","key":"go","exit":0,"path":"","out":""}` + "\r\n",
			facts:  debian,
			checks: []rawCheck{{Key: "go"}},
		},
		{
			name: "missing package manager means none",
			out: `{"type":"os","id":"","id_like":"","name":"","version":""}
{"type":"pkgmgr","name":""}
{"type":"user","name":"","uid":-1,"gid":-1,"home":"/","shell":""}
{"type":"shells","list":["/bin/sh"]}`,
			facts: &Facts{PackageManager: PackageManagerNone, User: User{UID: -1, GID: -1, Home: "/"}, Shells: []string{"/bin/sh"}},
		},
		{name: "empty output", out: "", err: "no probe lines"},
		{name: "only noise", out: "hello\nworld\n", err: "no probe lines"},
		{name: "malformed line", out: factsOutput + `{"type":"check","key":"go","exit":"x"}`, err: "line 5 (check)"},
		{name: "broken json", out: `{"type":"os",`, err: "line 1"},
		{name: "incomplete facts", out: `{"type":"pkgmgr","name":"apk"}` + "\n" + `{"type":"shells","list":[]}`, err: "no os, user line"},
		{name: "duplicate facts line", out: factsOutput + `{"type":"pkgmgr","name":"apk"}`, err: "duplicate pkgmgr line"},
		{
			name: "duplicate check",
			out:  `{"type":"check","key":"go","exit":0,"path":"","out":""}` + "\n" + `{"type":"check","key":"go","exit":0,"path":"","out":""}`,
			err:  "duplicate check line for go",
		},
		{name: "check without key", out: `{"type":"check","exit":0}`, err: "without key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts, checks, err := Parse([]byte(tt.out))
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.facts, facts)
			assert.Equal(t, tt.checks, checks)
		})
	}
}
