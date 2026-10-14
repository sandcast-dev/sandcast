package plan

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/rhuss/sandcast/internal/shell"
	"github.com/rhuss/sandcast/internal/version"
)

var (
	platformRe = regexp.MustCompile(`^linux/(amd64|arm64)$`)
	digestRe   = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	// accountRe is a POSIX portable user or group name, or a numeric id.
	accountRe = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_.-]*|[0-9]+)$`)
	// homeRe is an absolute path without whitespace, control characters,
	// quotes or shell metacharacters.
	homeRe = regexp.MustCompile(`^/[A-Za-z0-9._/@+-]*$`)
)

// FinalUserProblems checks the final user values that the build renders
// unquoted into USER, WORKDIR, install -d and chown. They come from the base
// image's passwd, group and HOME, so a hostile image could otherwise inject
// Containerfile instructions or shell commands. Each problem names its field.
func FinalUserProblems(u FinalUser) []string {
	var problems []string
	if !accountRe.MatchString(u.Name) {
		problems = append(problems, fmt.Sprintf("final_user.name: %q is not a portable user name or numeric id", u.Name))
	}
	if !accountRe.MatchString(u.Group) {
		problems = append(problems, fmt.Sprintf("final_user.group: %q is not a portable group name or numeric id", u.Group))
	}
	if !homeRe.MatchString(u.Home) || path.Clean(u.Home) != u.Home {
		problems = append(problems, fmt.Sprintf("final_user.home: %q is not a clean absolute path of letters, digits and ._/@+-", u.Home))
	}
	return problems
}

// CheckFinalUser returns an error listing every final user value that
// FinalUserProblems refuses, or nil.
func CheckFinalUser(u FinalUser) error {
	if problems := FinalUserProblems(u); len(problems) > 0 {
		return fmt.Errorf("plan: the base image user cannot be used safely:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// Validate checks a decoded plan against contracts/plan.schema.yaml.
func Validate(p *Plan) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	oneOf := func(field, value string, allowed ...string) {
		if !slices.Contains(allowed, value) {
			add("%s: %q is not one of %s", field, value, strings.Join(allowed, ", "))
		}
	}
	if p.SchemaVersion != version.PlanSchemaVersion {
		add("schema_version: plan uses version %d, this sandcast reads version %d; run sandcast plan again",
			p.SchemaVersion, version.PlanSchemaVersion)
	}
	oneOf("target", p.Target, "openshell", "container")
	if !platformRe.MatchString(p.Platform.Value) {
		add("platform.value: %q is not linux/amd64 or linux/arm64", p.Platform.Value)
	}
	oneOf("platform.source", p.Platform.Source, "flag", "manifest", "host")
	oneOf("engine.name", p.Engine.Name, "podman", "docker")
	oneOf("engine.source", p.Engine.Source, "flag", "manifest", "detected", "fallback")
	if p.BaseImage.Reference == "" {
		add("base_image.reference: required")
	}
	if !digestRe.MatchString(p.BaseImage.Digest) {
		add("base_image.digest: %q is not a sha256 digest", p.BaseImage.Digest)
	}
	if h := p.BaseImage.OvermountHome; h != "" && !shell.SafeAbsPath(h) {
		add("base_image.overmount_home: %q is not an absolute path of safe segments", h)
	}
	oneOf("image_facts.package_manager", p.ImageFacts.PackageManager, "apt-get", "dnf", "yum", "apk", "none")
	if p.FinalUser.Name == "" {
		add("final_user.name: required")
	} else {
		problems = append(problems, FinalUserProblems(p.FinalUser)...)
	}
	for i, d := range p.ProbeDelta {
		oneOf(fmt.Sprintf("probe_delta[%d].status", i), d.Status, "present", "missing", "incompatible")
		oneOf(fmt.Sprintf("probe_delta[%d].action", i), d.Action, ActionSkip, ActionInstall, ActionShadow, ActionUnresolvable)
	}
	for i, c := range p.Components {
		oneOf(fmt.Sprintf("components[%d].tier", i), c.Tier, "shipped", "registry", "project")
		oneOf(fmt.Sprintf("components[%d].installer_kind", i), c.InstallerKind, "mise", "package", "script", KindFragment)
		oneOf(fmt.Sprintf("components[%d].action", i), c.Action, ActionSkip, ActionInstall, ActionShadow)
	}
	oneOf("mise.platform", p.Mise.Platform, "linux-x64", "linux-arm64")
	for i, pr := range p.Profiles {
		oneOf(fmt.Sprintf("profiles[%d].tier", i), pr.Tier, "upstream", "overlay", "project")
	}
	oneOf("shell.program", p.Shell.Program, "zsh", "bash", "none")
	if len(problems) > 0 {
		return fmt.Errorf("invalid plan:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
