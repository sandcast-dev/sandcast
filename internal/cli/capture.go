package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/capture"
	"github.com/rhuss/sandcast/internal/harness"
)

func (a *App) componentsMatchCommand() *cobra.Command {
	var host, noHost bool
	var harnesses []string
	cmd := &cobra.Command{
		Use:   "match [<dir>]",
		Short: "Match component conditions against a project and the host; print proposals with evidence",
		Args:  args(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, argv []string) error {
			dir := "."
			if len(argv) == 1 {
				dir = argv[0]
			} else if a.opts.project != "" {
				dir = a.opts.project
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
				return usageErr("%s is not a directory", dir)
			}
			set, _, err := a.componentSet()
			if err != nil {
				return err
			}
			for _, h := range harnesses {
				if _, err := harness.Get(h); err != nil {
					return usageErr("%v", err)
				}
			}
			var home string
			var homeErr error
			if host && !noHost {
				home, homeErr = a.hostHome()
			}
			res, err := capture.Match(cmd.Context(), abs, set.All(), capture.Options{
				Host:      host && !noHost,
				Home:      home,
				Harnesses: harnesses,
				LookPath:  a.LookPath,
				Version:   a.HostVersion,
			})
			if err != nil {
				return err
			}
			if homeErr != nil {
				// Without a home no dotfile can be proposed; say so, so "none
				// found" and "not scanned" are told apart.
				res.DotfileNotices = append(res.DotfileNotices, fmt.Sprintf("the host home directory is unknown (%v); dotfiles were not scanned", homeErr))
			}
			if a.opts.json {
				return writeJSON(a.Stdout, res)
			}
			if len(res.Proposals) == 0 {
				_, _ = fmt.Fprintln(a.Stdout, "nothing detected")
			}
			tb := newTable(a.Stdout, "COMPONENT", "SOURCE", "VERSION", "PINNED", "EVIDENCE")
			for _, p := range res.Proposals {
				tb.row(p.Key, p.Source, orDash(p.DetectedVersion), orDash(p.PinnedSummary()), strings.Join(p.Evidence, "; "))
			}
			if err := tb.flush(); err != nil {
				return err
			}
			for _, n := range res.Notes {
				_, _ = fmt.Fprintf(a.Stdout, "note: %s\n", n)
			}
			names := make([]string, 0, len(res.Available))
			for _, av := range res.Available {
				names = append(names, av.Key)
			}
			_, _ = fmt.Fprintf(a.Stdout, "\navailable: %s\n", strings.Join(names, ", "))
			if len(res.Dotfiles) > 0 {
				paths := make([]string, 0, len(res.Dotfiles))
				for _, d := range res.Dotfiles {
					paths = append(paths, d.Path)
				}
				_, _ = fmt.Fprintf(a.Stdout, "dotfiles: %s\n", strings.Join(paths, ", "))
			}
			for _, n := range res.DotfileNotices {
				_, _ = fmt.Fprintf(a.Stdout, "dotfile skipped: %s\n", n)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&host, "host", true, "detect host tools")
	f.BoolVar(&noHost, "no-host", false, "do not detect host tools")
	f.StringSliceVar(&harnesses, "harness", nil, "selected harness(es), proposes their agent components")
	return cmd
}
