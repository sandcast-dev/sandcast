package build

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/version"
)

// Assemble renders the Containerfile from the plan in the fixed section
// order of R-14. The plan must have no conflicts and no blocking unresolved
// components.
func Assemble(p *plan.Plan) (string, error) {
	if problems := p.Problems(); len(problems) > 0 {
		return "", fmt.Errorf("the plan cannot be built:\n  %s", strings.Join(problems, "\n  "))
	}
	var sections []string
	add := func(s string, err error) error {
		if err != nil {
			return err
		}
		if s != "" {
			sections = append(sections, s)
		}
		return nil
	}

	if err := add(execTemplate(headerTemplate, map[string]any{
		"Version":  version.Version,
		"Target":   p.Target,
		"Platform": p.Platform.Value,
		"From":     fromReference(p.BaseImage.Reference, p.BaseImage.Digest),
	})); err != nil {
		return "", err
	}
	if err := add(uniqueFragment(p, plan.SectionPackages), nil); err != nil {
		return "", err
	}
	if err := add(userSection(p)); err != nil {
		return "", err
	}
	if err := add(uniqueFragment(p, plan.SectionMise), nil); err != nil {
		return "", err
	}
	for _, prefix := range []string{plan.SectionRecipes + "/", plan.SectionFragments + "/"} {
		for _, c := range p.Components {
			if strings.HasPrefix(c.FragmentName, prefix) && c.Action != plan.ActionSkip {
				sections = append(sections, c.Fragment)
			}
		}
	}
	if err := add(shellSection(p)); err != nil {
		return "", err
	}
	if err := add(dotfilesSection(p)); err != nil {
		return "", err
	}
	for _, h := range p.Harnesses {
		sections = append(sections, h.Fragment)
	}
	if p.Target == manifest.TargetOpenShell {
		if err := add(execTemplate(policyTemplate, map[string]any{"Path": PolicyPath})); err != nil {
			return "", err
		}
	}
	if err := add(execTemplate(footerTemplate, map[string]any{
		"WorkDir": workDir(p),
		"User":    p.FinalUser.Name,
		"Cmd":     loginShell(p).cmd(),
	})); err != nil {
		return "", err
	}
	for i, s := range sections {
		sections[i] = strings.TrimRight(s, "\n") + "\n"
	}
	return strings.Join(sections, "\n"), nil
}

// fromReference pins ref to digest, replacing a digest ref may already
// carry (a digest-pinned base image is a valid manifest input).
func fromReference(ref, digest string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	return ref + "@" + digest
}

// usesMise reports whether the plan installs anything through the 30-mise
// layer.
func usesMise(p *plan.Plan) bool {
	return uniqueFragment(p, plan.SectionMise) != ""
}

// NeedsSessionEnv reports whether the image carries the session environment
// script (spec 002 R-04): whenever the image uses mise tools, installed by
// this build or kept from a sandcast-built base (baseProvidesMise), because
// a login shell whose system profile resets PATH needs the shims back on
// either target, and for the OpenShell target whenever the script has
// something to export (plan.SessionEnvExports: the components', harnesses'
// and relocation variables; the image PATH value the plan writes is not one
// of its exports) or the base image has PATH directories beyond the
// OpenShell default PATH outside the overmount home, which an OpenShell
// session would otherwise lose.
func NeedsSessionEnv(p *plan.Plan) bool {
	if usesMise(p) || baseProvidesMise(p) {
		return true
	}
	if p.Target != manifest.TargetOpenShell {
		return false
	}
	return len(plan.SessionEnvExports(p)) > 0 || len(plan.SessionPathDirsFor(p)) > 0
}

// baseProvidesMise reports whether the base image already carries mise
// tools the plan keeps: a mise component the delta skips because the base
// provides it at the locked version, or the shims on the base image's PATH.
// That is a base image sandcast built; its session script is then written
// again by this build (the cleanup would remove it otherwise), so its login
// shells keep the MISE_* settings and the shims.
func baseProvidesMise(p *plan.Plan) bool {
	if slices.Contains(p.ImageFacts.Path, plan.MiseShimsDir) {
		return true
	}
	for _, c := range p.Components {
		if c.InstallerKind == component.KindMise && c.Action == plan.ActionSkip {
			return true
		}
	}
	return false
}

// CuratedRC is the curated rc file content: CuratedRCPrefix, then the
// manifest's rc (spec 002 R-06).
func CuratedRC(p *plan.Plan) string {
	return CuratedRCPrefix + p.Shell.RC
}

// ShellContextFile is the build-context path of the curated configuration.
func ShellContextFile(program string) string {
	if program == "zsh" {
		return path.Join(ShellContextDir, ZshRCFile)
	}
	return path.Join(ShellContextDir, BashRCFile)
}

func uniqueFragment(p *plan.Plan, name string) string {
	for _, c := range p.Components {
		if c.FragmentName == name && c.Action != plan.ActionSkip && c.Fragment != "" {
			return c.Fragment
		}
	}
	return ""
}

// workDir is the OpenShell workspace directory for the OpenShell target, the
// path the workspace volume is mounted over (plan.OvermountPrefix, /sandbox
// on the shipped base images, R-10), and the final user's home for the
// container target, which applies no sandbox conventions (FR-040).
func workDir(p *plan.Plan) string {
	if ws := plan.OvermountPrefix(p); ws != "" {
		return ws
	}
	if p.FinalUser.Home != "" {
		return p.FinalUser.Home
	}
	return "/"
}

func userSection(p *plan.Plan) (string, error) {
	var userDirs, rootDirs []string
	// The OpenShell workspace directory is always created for the final
	// user; a component or harness directory equal to it adds nothing.
	sandboxDir := plan.OvermountPrefix(p)
	for _, d := range p.Image.Dirs {
		if sandboxDir != "" && d.Path == sandboxDir {
			continue
		}
		if d.Owner == "root" {
			rootDirs = append(rootDirs, d.Path)
		} else {
			userDirs = append(userDirs, d.Path)
		}
	}
	return execTemplate(userTemplate, map[string]any{
		"User":       p.FinalUser,
		"LoginShell": loginShell(p).Path,
		"WorkDir":    sandboxDir,
		"UserDirs":   userDirs,
		"RootDirs":   rootDirs,
		"Env":        p.Image.Env,
	})
}
