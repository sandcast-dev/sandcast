package cli

import (
	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/registry"
)

type baseImageRow struct {
	Target  string `json:"target"`
	Name    string `json:"name"`
	Ref     string `json:"ref"`
	Default bool   `json:"default"`
	Notes   string `json:"notes,omitempty"`
}

func (a *App) baseImagesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "base-images",
		Short: "Show the base image registry",
	}
	var target string
	list := &cobra.Command{
		Use:   "list",
		Short: "List the merged base image registry (shipped plus ~/.config/sandcast/base-images.yaml)",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := a.registry()
			if err != nil {
				return err
			}
			targets := registry.KnownTargets
			if target != "" {
				targets = []string{target}
			}
			rows := []baseImageRow{}
			for _, t := range targets {
				for _, e := range reg.List(t) {
					rows = append(rows, baseImageRow{Target: t, Name: e.Name, Ref: e.Ref, Default: e.Default, Notes: e.Notes})
				}
			}
			if a.opts.json {
				return writeJSON(a.Stdout, rows)
			}
			tb := newTable(a.Stdout, "TARGET", "NAME", "DEFAULT", "REFERENCE")
			for _, r := range rows {
				def := ""
				if r.Default {
					def = "yes"
				}
				tb.row(r.Target, r.Name, def, r.Ref)
			}
			return tb.flush()
		},
	}
	list.Flags().StringVar(&target, "target", "", "only this target: openshell or container")
	cmd.AddCommand(list)
	return cmd
}
