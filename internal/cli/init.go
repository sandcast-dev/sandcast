package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/imageref"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/profile"
)

func (a *App) initCommand() *cobra.Command {
	var name, baseImage, target, image, platform string
	var force, push bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create .sandcast/ with a manifest skeleton and .gitignore",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := a.initLayout()
			if err != nil {
				return err
			}
			if target != manifest.TargetOpenShell && target != manifest.TargetContainer {
				return usageErr("--target must be openshell or container, got %q", target)
			}
			if name == "" {
				name = profile.ImageLabel(filepath.Base(l.Root))
			}
			reg, err := a.registry()
			if err != nil {
				return err
			}
			if baseImage == "" {
				def, err := reg.Default(target)
				if err != nil {
					return err
				}
				baseImage = def.Name
			}
			if fsutil.Exists(l.ManifestPath()) && !force {
				return usageErr("%s already exists (use --force to overwrite)", l.RelToRoot(l.ManifestPath()))
			}
			opts := manifest.SkeletonOptions{Platform: platform, Push: push}
			if image != "" {
				if opts.Image, opts.Tag, err = splitImageFlag(image); err != nil {
					return usageErr("--image: %v", err)
				}
			}
			if platform != "" {
				if _, err := pipeline.SelectPlatform(platform, "", ""); err != nil {
					return usageErr("--platform %q is not supported: use linux/amd64 or linux/arm64", platform)
				}
			}
			m := manifest.Skeleton(name, baseImage, target, opts)
			if diags := m.Validate(); len(diags) > 0 {
				return &manifest.ValidationError{Source: "init", Diagnostics: diags}
			}
			if err := os.MkdirAll(l.SandcastDir(), 0o755); err != nil {
				return err
			}
			if err := manifest.Save(l.ManifestPath(), m); err != nil {
				return err
			}
			if err := l.EnsureGitignore(); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.Stdout, "created %s (base image %s, target %s)\n", l.RelToRoot(l.ManifestPath()), baseImage, target)
			_, _ = fmt.Fprintf(a.Stdout, "created %s\n", l.RelToRoot(filepath.Join(l.SandcastDir(), ".gitignore")))
			if image == "" && target == manifest.TargetOpenShell {
				_, _ = fmt.Fprintf(a.Stdout, "note: targets.openshell.image is %s; this works only with a local OpenShell gateway that shares this engine's image storage. "+
					"For a remote gateway, rerun with --image <registry>/%s --platform <os/arch> --push.\n", m.Targets.OpenShell.Image, name)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "manifest name, kebab-case (default: the directory name)")
	f.StringVar(&baseImage, "base-image", "", "registry entry name or image reference (default: the registry default for the target)")
	f.StringVar(&target, "target", manifest.TargetOpenShell, "target: openshell or container")
	f.BoolVar(&force, "force", false, "overwrite an existing manifest")
	f.StringVar(&image, "image", "", "target image name[:tag] (default: localhost/<name>, which only a local gateway sharing this engine's storage can start)")
	f.StringVar(&platform, "platform", "", "linux/amd64 or linux/arm64, written to the manifest (default: the host at plan time)")
	f.BoolVar(&push, "push", false, "push the image after every build (targets.<target>.push)")
	return cmd
}

// splitImageFlag splits name[:tag] into the image name and the tag. A
// digest is refused: the manifest pins tags, the build records digests.
func splitImageFlag(v string) (string, string, error) {
	if strings.Contains(v, "@") {
		return "", "", fmt.Errorf("%q: give name[:tag], not a digest", v)
	}
	if _, err := imageref.Normalize(v); err != nil {
		return "", "", err
	}
	name, tag := v, ""
	if i := strings.LastIndex(v, ":"); i >= 0 && !strings.Contains(v[i+1:], "/") {
		name, tag = v[:i], v[i+1:]
	}
	return name, tag, nil
}

// initLayout uses --project as is, else the current directory (init never
// searches ancestors, so it does not write into a parent project).
func (a *App) initLayout() (fsutil.Layout, error) {
	if a.opts.project != "" {
		return fsutil.NewLayout(a.opts.project)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fsutil.Layout{}, err
	}
	return fsutil.Layout{Root: cwd}, nil
}
