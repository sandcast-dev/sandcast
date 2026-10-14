package build

import (
	"fmt"
	"path"
	"strings"

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
