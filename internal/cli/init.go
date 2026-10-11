package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/profile"
)

func (a *App) initCommand() *cobra.Command {
	var name, baseImage, target string
	var force bool
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
			m := manifest.Skeleton(name, baseImage, target)
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
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "manifest name, kebab-case (default: the directory name)")
	f.StringVar(&baseImage, "base-image", "", "registry entry name or image reference (default: the registry default for the target)")
	f.StringVar(&target, "target", manifest.TargetOpenShell, "target: openshell or container")
	f.BoolVar(&force, "force", false, "overwrite an existing manifest")
	return cmd
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
