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
