package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/gateway"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/profile"
)

func (a *App) profilesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "Lint, export and import OpenShell provider profiles",
	}
	cmd.AddCommand(a.profilesLintCommand(), a.profilesExportCommand(), a.profilesImportCommand())
	return cmd
}

// adaptedDir returns build/<target>/profiles for the requested target and
// the project layout.
func (a *App) adaptedDir(target string) (string, fsutil.Layout, error) {
	m, l, err := a.loadManifest()
	if err != nil {
		return "", fsutil.Layout{}, err
	}
	t, err := pipeline.TargetOf(m, target)
	if err != nil {
		return "", fsutil.Layout{}, pipelineErr(err)
	}
	return filepath.Join(l.BuildDir(t), build.ProfilesDirName), l, nil
}

func (a *App) profilesLintCommand() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "lint [<file|dir>]",
		Short: "Lint profiles with the built-in OpenShell v0.1.3 rules (default: the adapted set)",
		Args:  args(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, argv []string) error {
			path := ""
			if len(argv) == 1 {
				path = argv[0]
			} else {
				dir, l, err := a.adaptedDir(target)
				if err != nil {
					return err
				}
				// The default set lives in the build directory; a symlink
				// there would make lint read profile files outside it.
				if err := checkBuildDir(l, filepath.Dir(dir)); err != nil {
					return err
				}
				path = dir
			}
			files, err := profileFiles(path)
			if err != nil {
				return withCode(ExitUsage, err)
			}
			var entries []profile.Entry
			diags := []profile.Diagnostic{}
			for _, f := range files {
				p, err := profile.LoadFile(f)
				if err != nil {
					diags = append(diags, profile.Diagnostic{Source: f, Message: err.Error()})
					continue
				}
				entries = append(entries, profile.Entry{Profile: p, Tier: profile.TierProject, SourceFile: f})
			}
			diags = append(diags, profile.LintSet(entries)...)
			if a.opts.json {
				if err := writeJSON(a.Stdout, map[string]any{"files": len(files), "diagnostics": diags}); err != nil {
					return err
				}
			} else {
				for _, d := range diags {
					_, _ = fmt.Fprintf(a.Stdout, "%s: %s: %s: %s\n", d.Source, orDash(d.ProfileID), orDash(d.Field), d.Message)
				}
				_, _ = fmt.Fprintf(a.Stdout, "%d profile file(s), %d diagnostic(s)\n", len(files), len(diags))
			}
			if len(diags) > 0 {
				return silentErr{code: ExitUsage}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "target whose adapted set is linted when no path is given")
	return cmd
}

// profileFiles lists the profile files of a file or directory (index.yaml
// excluded).
func profileFiles(path string) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s does not exist; run \"sandcast build\" first or pass a path", path)
		}
		return nil, err
	}
	if !fi.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == pipeline.IndexFile || (filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml") {
			continue
		}
		// A symlinked profile file would make lint read and echo a file
		// outside the directory.
		if e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symlink; sandcast lints profile files in place and never follows a link there, remove it", filepath.Join(path, name))
		}
		out = append(out, filepath.Join(path, name))
	}
	sort.Strings(out)
	return out, nil
}

func (a *App) profilesExportCommand() *cobra.Command {
	var target, image string
	var suffix bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Re-write the adapted profile set from an existing image without rebuilding",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := a.pipelineEnv()
			if err != nil {
				return err
			}
			t, err := pipeline.TargetOf(env.Manifest, target)
			if err != nil {
				return pipelineErr(err)
			}
			if image == "" {
				image = env.Manifest.Target(t).Ref()
			}
			platform := ""
			if p, err := plan.Load(filepath.Join(env.Layout.BuildDir(t), plan.PlanFile)); err == nil {
				platform = p.Platform.Value
			}
			dir := filepath.Join(env.Layout.BuildDir(t), build.ProfilesDirName)
			if err := checkBuildDir(env.Layout, filepath.Dir(dir)); err != nil {
				return err
			}
			set, err := pipeline.ExportFromImage(cmd.Context(), env.Engine, image, platform, dir, suffix)
			if err != nil {
				return err
			}
			if a.opts.json {
				return writeJSON(a.Stdout, set.Index)
			}
			for _, ip := range set.Index.Profiles {
				_, _ = fmt.Fprintf(a.Stdout, "%s -> %s (%d binaries)\n", ip.UpstreamID, ip.AdaptedID, len(ip.Binaries))
			}
			_, _ = fmt.Fprintf(a.Stdout, "wrote %s\n", env.Layout.RelToRoot(dir))
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "openshell or container (default: openshell when the manifest declares it, else container)")
	cmd.Flags().StringVar(&image, "image", "", "image to read the profiles from (default: the target's image and tag)")
	cmd.Flags().BoolVar(&suffix, "suffix", false, "suffix ids with the image label")
	return cmd
}

func (a *App) profilesImportCommand() *cobra.Command {
	var target, gatewayName string
	var o pipeline.ImportOptions
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import the adapted profile set into an OpenShell gateway through the openshell CLI",
		Long: `import checks that the openshell CLI is ` + gateway.RequiredVersion() + `, lists the profiles at the chosen
scope, imports every adapted profile whose id does not exist yet, and reports
existing ids instead of overwriting them. Use --update to replace existing
profiles, --suffix for image-specific ids, or another --scope. The gateway
is the one --gateway names, else whatever the openshell CLI selects from
OPENSHELL_GATEWAY, OPENSHELL_GATEWAY_ENDPOINT or its active gateway.`,
		Args: args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.Scope != gateway.ScopeWorkspace && o.Scope != gateway.ScopePlatform {
				return usageErr("--scope must be workspace or platform, got %q", o.Scope)
			}
			dir, l, err := a.adaptedDir(target)
			if err != nil {
				return err
			}
			// Import replaces the set in the build directory; a symlink there
			// would redirect the replace onto another directory.
			if err := checkBuildDir(l, filepath.Dir(dir)); err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(dir, pipeline.IndexFile)); err != nil {
				return usageErr("no adapted profile set at %s; run \"sandcast build\" first", dir)
			}
			gw := &gateway.Client{Runner: a.Gateway}
			if gatewayName != "" {
				gw.Extra = []string{"--gateway", gatewayName}
			}
			rep, err := pipeline.Import(cmd.Context(), gw, dir, o)
			if err != nil {
				if errors.Is(err, gateway.ErrMissing) || errors.Is(err, gateway.ErrVersion) {
					return toolErr(err)
				}
				if rep == nil {
					return err
				}
			}
			if a.opts.json {
				if jerr := writeJSON(a.Stdout, rep); jerr != nil {
					return jerr
				}
			} else {
				printImportReport(a, rep)
			}
			if err != nil {
				return err
			}
			if len(rep.Conflicts) > 0 {
				a.logf("%d profile id(s) already exist at %s scope: %s", len(rep.Conflicts), rep.Scope, strings.Join(rep.Conflicts, ", "))
				a.logf("choose one: --update to replace them, --suffix for image-specific ids, or --scope %s", otherScope(rep.Scope))
				return silentErr{code: ExitFailure}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "openshell or container (default: openshell when the manifest declares it, else container)")
	f.StringVar(&o.Scope, "scope", gateway.ScopeWorkspace, "gateway scope: workspace or platform (platform implies --suffix)")
	f.StringVar(&o.Workspace, "workspace", "", "workspace name for workspace scope (default: the CLI's current workspace)")
	f.BoolVar(&o.Suffix, "suffix", false, "suffix ids with the image label")
	f.BoolVar(&o.Update, "update", false, "replace profiles whose id already exists")
	f.BoolVar(&o.DryRun, "dry-run", false, "list what would be imported or updated without changing the gateway")
	f.StringVar(&gatewayName, "gateway", "", "openshell gateway name, passed to the openshell CLI as --gateway")
	return cmd
}

func printImportReport(a *App, rep *pipeline.ImportReport) {
	for _, w := range rep.Warnings {
		_, _ = fmt.Fprintf(a.Stdout, "warning: %s\n", w)
	}
	verb := ""
	if rep.DryRun {
		verb = "would be "
	}
	for _, id := range rep.Imported {
		_, _ = fmt.Fprintf(a.Stdout, "%simported: %s (%s scope)\n", verb, id, rep.Scope)
	}
	for _, id := range rep.Updated {
		_, _ = fmt.Fprintf(a.Stdout, "%supdated: %s\n", verb, id)
	}
	for _, id := range rep.Conflicts {
		_, _ = fmt.Fprintf(a.Stdout, "exists: %s (left unchanged)\n", id)
	}
}

func otherScope(s string) string {
	if s == gateway.ScopePlatform {
		return gateway.ScopeWorkspace
	}
	return gateway.ScopePlatform
}
