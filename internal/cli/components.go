package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/version"
)

type componentRow struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Tier       string   `json:"tier"`
	Installer  string   `json:"installer"`
	SourceFile string   `json:"source_file"`
	Profiles   []string `json:"profiles"`
	Match      any      `json:"match,omitempty"`
}

func (a *App) componentsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "components",
		Short: "Inspect and refresh component definitions",
	}
	cmd.AddCommand(a.componentsListCommand(), a.componentsShowCommand(), a.componentsRefreshCommand(), a.componentsMatchCommand())
	return cmd
}

func (a *App) componentSet() (*component.Set, []string, error) {
	l, err := a.layout()
	if err != nil {
		return nil, nil, err
	}
	cache, err := a.cacheDir()
	if err != nil {
		return nil, nil, err
	}
	return pipeline.Components(l, cache)
}

func (a *App) componentsListCommand() *cobra.Command {
	var tier string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List components across tiers with the winning tier",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tier != "" && tier != string(component.TierShipped) && tier != string(component.TierRegistry) && tier != string(component.TierProject) {
				return usageErr("--tier must be shipped, registry or project, got %q", tier)
			}
			set, notices, err := a.componentSet()
			if err != nil {
				return err
			}
			for _, n := range notices {
				a.logf("notice: %s", n)
			}
			rows := []componentRow{}
			for _, c := range set.All() {
				if tier != "" && string(c.Tier) != tier {
					continue
				}
				r := componentRow{Key: c.Key, Name: c.Name, Tier: string(c.Tier), Installer: c.Kind(), SourceFile: c.SourceFile, Profiles: c.Profiles}
				if c.Match != nil {
					r.Match = c.Match
				}
				if r.Profiles == nil {
					r.Profiles = []string{}
				}
				rows = append(rows, r)
			}
			if a.opts.json {
				return writeJSON(a.Stdout, rows)
			}
			tb := newTable(a.Stdout, "KEY", "TIER", "INSTALLER", "PROFILES", "SOURCE")
			for _, r := range rows {
				tb.row(r.Key, r.Tier, r.Installer, joinOrDash(r.Profiles), r.SourceFile)
			}
			return tb.flush()
		},
	}
	cmd.Flags().StringVar(&tier, "tier", "", "only components whose winning tier is shipped, registry or project")
	return cmd
}

func (a *App) componentsShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <key>",
		Short: "Print the effective component definition",
		Args:  args(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, argv []string) error {
			set, _, err := a.componentSet()
			if err != nil {
				return err
			}
			c, ok := set.Get(argv[0])
			if !ok {
				return usageErr("component %q is not defined in any tier", argv[0])
			}
			if a.opts.json {
				return writeJSON(a.Stdout, struct {
					*component.Component
					Tier       string `json:"tier"`
					SourceFile string `json:"source_file"`
				}{c, string(c.Tier), c.SourceFile})
			}
			data, err := component.Marshal(c)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.Stdout, "# %s (%s tier)\n%s", c.SourceFile, c.Tier, data)
			return nil
		},
	}
}

func (a *App) componentsRefreshCommand() *cobra.Command {
	var ref string
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Fetch the remote component registry at the pinned or given tag and verify checksums",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cache, err := a.cacheDir()
			if err != nil {
				return err
			}
			if ref == "" {
				ref = version.ComponentRegistryPin
			}
			if !component.ValidTag(ref) {
				return usageErr("--ref %q is not a valid registry tag", ref)
			}
			base := version.ComponentRegistryURL
			if v := a.Getenv("SANDCAST_COMPONENT_REGISTRY_URL"); v != "" {
				base = v
			}
			report, err := component.Refresh(cmd.Context(), a.HTTPClient, base, ref, cache)
			if err != nil && !errors.Is(err, component.ErrIncomplete) {
				return err
			}
			if a.opts.json {
				if jerr := writeJSON(a.Stdout, report); jerr != nil {
					return jerr
				}
			} else {
				printRefreshReport(a, report)
			}
			switch {
			case err != nil:
				return err
			case len(report.Rejected) > 0:
				return silentErr{code: ExitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "registry release tag (default: the pinned "+version.ComponentRegistryPin+")")
	return cmd
}

func printRefreshReport(a *App, report *component.RefreshReport) {
	if report.Installed {
		_, _ = fmt.Fprintf(a.Stdout, "registry %s: %d accepted, %d rejected (cache %s)\n",
			report.Tag, len(report.Accepted), len(report.Rejected), report.Dir)
	}
	for _, r := range report.Rejected {
		_, _ = fmt.Fprintf(a.Stdout, "rejected: %s (%s)\n", r, report.Reasons[r])
	}
	if report.Installed && len(report.Kept) > 0 {
		_, _ = fmt.Fprintf(a.Stdout, "kept previous copies: %s\n", strings.Join(report.Kept, ", "))
	}
}
