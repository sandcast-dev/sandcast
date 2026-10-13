package plan

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/mise"
	"github.com/rhuss/sandcast/internal/version"
)

// Paths of the mise layout inside the image (R-04).
const (
	MiseDataDir   = "/opt/mise"
	MiseConfigDir = mise.ImageConfigDir
	MiseBinary    = "/usr/local/bin/mise"
	// MiseRuntimeDir holds mise's cache and state. It lives under /tmp, which
	// the sandbox policy keeps writable, because /opt/mise is read-only at
	// runtime.
	MiseRuntimeDir = "/tmp/sandcast-mise"
	MiseConfigFile = MiseConfigDir + "/mise.toml"
	MiseLockFile   = MiseConfigDir + "/mise.lock"
)

var packageTemplates = map[string]*template.Template{
	"apt-get": template.Must(template.New("apt-get").Parse(`# {{.Section}}: system packages via apt-get
USER root
RUN mkdir -p /var/lib/apt/lists/partial \
 && apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
{{.Lines}} \
 && rm -rf /var/lib/apt/lists/*
`)),
	"dnf": template.Must(template.New("dnf").Parse(`# {{.Section}}: system packages via dnf
USER root
RUN dnf install -y --setopt=install_weak_deps=False \
{{.Lines}} \
 && dnf clean all
`)),
	"yum": template.Must(template.New("yum").Parse(`# {{.Section}}: system packages via yum
USER root
RUN yum install -y \
{{.Lines}} \
 && yum clean all
`)),
	"apk": template.Must(template.New("apk").Parse(`# {{.Section}}: system packages via apk
USER root
RUN apk add --no-cache \
{{.Lines}}
`)),
}

var miseTemplate = template.Must(template.New("mise").Parse(`# {{.Section}}: tools installed by mise {{.Pin}} in locked mode
USER root
ENV {{range .Env}}{{.Name}}={{.Value}} \
    {{end}}PATH={{.DataDir}}/shims:$PATH
COPY mise/mise.toml mise/mise.lock {{.ConfigDir}}/
RUN chmod 0644 {{.ConfigFile}} {{.LockFile}} \
 && for attempt in 1 2 3; do \
      if curl -fsSL --connect-timeout 20 --max-time 300 -o {{.Binary}} {{.URL}}; then break; fi; \
      if [ "$attempt" = 3 ]; then echo "sandcast: could not download mise {{.Pin}} after 3 attempts: {{.URL}}" >&2; exit 1; fi; \
      sleep 5; \
    done \
 && echo "{{.SHA256}}  {{.Binary}}" | sha256sum -c - \
 && chmod 0755 {{.Binary}} \
 && install -d -o {{.User}} -g {{.Group}} {{.DataDir}}
USER {{.User}}
RUN (mise install --locked --yes || mise install --locked --yes) \
 && mise reshim \
 && rm -rf {{.RuntimeDir}}
`))

var scriptTemplate = template.Must(template.New("script").Parse(`# {{.Section}}: {{.Key}} ({{.Source}})
USER {{.User}}
RUN <<'{{.EOF}}'
set -eux
{{.Run}}
{{.EOF}}
`))

// PackageFragment renders the aggregated 10-system-packages layer for the
// probed package manager. Packages are sorted and deduplicated.
func PackageFragment(packageManager string, packages []string) (string, error) {
	tmpl, ok := packageTemplates[packageManager]
	if !ok {
		return "", fmt.Errorf("no package template for %q", packageManager)
	}
	pkgs := slices.Clone(packages)
	slices.Sort(pkgs)
	pkgs = slices.Compact(pkgs)
	if len(pkgs) == 0 {
		return "", nil
	}
	// Component validation checks the names too; this guard keeps the RUN
	// line intact whatever produced the list.
	for _, p := range pkgs {
		if !component.ValidPackageName(p) {
			return "", fmt.Errorf("%q is not a valid package name", p)
		}
	}
	lines := make([]string, len(pkgs))
	for i, p := range pkgs {
		lines[i] = "      " + p
	}
	return render(tmpl, map[string]any{"Section": SectionPackages, "Lines": strings.Join(lines, " \\\n")})
}

// MiseFragment renders the aggregated 30-mise layer (R-04) for a mise
// platform. The pinned static mise binary is downloaded and verified against
// its sha256. Downloads are retried (the binary up to three times, the
// locked install once) because a transient network error must not fail the
// build; the build itself is never retried (FR-027).
func MiseFragment(user FinalUser, pin, misePlatform string) (string, error) {
	sum, ok := version.MiseSHA256[misePlatform]
	if !ok || pin != version.MisePin {
		return "", fmt.Errorf("no pinned mise %s checksum for %s", pin, misePlatform)
	}
	return render(miseTemplate, map[string]any{
		"URL":        version.MiseReleaseURL + "/" + version.MiseAsset(misePlatform),
		"SHA256":     sum,
		"Section":    SectionMise,
		"Pin":        pin,
		"DataDir":    MiseDataDir,
		"RuntimeDir": MiseRuntimeDir,
		"ConfigDir":  MiseConfigDir,
		"ConfigFile": MiseConfigFile,
		"LockFile":   MiseLockFile,
		"Binary":     MiseBinary,
		"User":       user.Name,
		"Group":      user.Group,
		"Env":        MiseEnv,
	})
}

// ScriptFragment renders one 40-recipes layer for a script component. The
// script runs as root when as_root is set, else as the final user.
func ScriptFragment(c *component.Component, user FinalUser) (string, error) {
	if c.Installer.Script == nil {
		return "", fmt.Errorf("component %s has no script installer", c.Key)
	}
	// The run text sits in a quoted heredoc. A line equal to the delimiter
	// would end it early and turn the following lines into Containerfile
	// instructions; a line break in the key or source would do the same
	// from the provenance comment.
	if component.EndsHeredoc(c.Installer.Script.Run) {
		return "", fmt.Errorf("component %s: installer.script.run must not contain a line equal to the heredoc delimiter %s", c.Key, component.HeredocDelimiter)
	}
	if component.HasControl(c.Key) || component.HasControl(c.SourceFile) {
		return "", fmt.Errorf("component %q (%q): key and source must not contain a control character", c.Key, c.SourceFile)
	}
	runAs := user.Name
	if c.Installer.Script.AsRoot {
		runAs = "root"
	}
	return render(scriptTemplate, map[string]any{
		"Section": SectionRecipes,
		"Key":     c.Key,
		"Source":  c.SourceFile,
		"User":    runAs,
		"Run":     strings.TrimRight(c.Installer.Script.Run, "\n"),
		"EOF":     component.HeredocDelimiter,
	})
}

// StoredFragmentText prepares a stored fragment for the 50-fragments
// section: a provenance comment and USER root, so every fragment starts as
// root like every other section, then the fragment body verbatim.
func StoredFragmentText(key, source, body string) string {
	body = strings.TrimRight(body, "\n")
	return fmt.Sprintf("# %s: %s (%s)\nUSER root\n%s\n", SectionFragments, key, source, body)
}

func render(t *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s fragment: %w", t.Name(), err)
	}
	return buf.String(), nil
}
