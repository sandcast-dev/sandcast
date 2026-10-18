package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/redact"
)

func (a *App) fragmentsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fragments",
		Short: "List and store Containerfile fragments for unresolved components",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "Show stored fragments and their status",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := a.layout()
			if err != nil {
				return err
			}
			frags, err := build.ListFragments(l.FragmentsDir())
			if err != nil {
				return err
			}
			if frags == nil {
				frags = []*build.Fragment{}
			}
			for _, f := range frags {
				f.Path = l.RelToRoot(f.Path)
			}
			if a.opts.json {
				return writeJSON(a.Stdout, frags)
			}
			tb := newTable(a.Stdout, "KEY", "STATUS", "WRITTEN", "PATH")
			for _, f := range frags {
				tb.row(f.Key, f.Status, orDash(f.Written), f.Path)
			}
			return tb.flush()
		},
	}
	var file string
	write := &cobra.Command{
		Use:   "write <key>",
		Short: "Store a fragment from stdin or a file, validated as a Containerfile snippet",
		Args:  args(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, argv []string) error {
			l, err := a.layout()
			if err != nil {
				return err
			}
			body, err := readInput(a.Stdin, file)
			if err != nil {
				return err
			}
			if hits := redact.Scan(body); len(hits) > 0 {
				return usageErr("the fragment contains %d credential-shaped value(s) (first: %s at line %d); fragments are committed and must not carry credentials", len(hits), hits[0].Kind, hits[0].Line)
			}
			path, err := build.WriteFragment(l.FragmentsDir(), argv[0], body, time.Now())
			if err != nil {
				return withCode(ExitUsage, err)
			}
			_, _ = fmt.Fprintf(a.Stdout, "stored %s\n", l.RelToRoot(path))
			return nil
		},
	}
	write.Flags().StringVar(&file, "file", "", "read the fragment from this file instead of stdin")
	cmd.AddCommand(list, write)
	return cmd
}

func (a *App) promoteCommand() *cobra.Command {
	var name string
	var bins []string
	var yes bool
	cmd := &cobra.Command{
		Use:   "promote <key>",
		Short: "Convert a working fragment into a project-local recipe and mark the fragment superseded",
		Args:  args(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, argv []string) error {
			key := argv[0]
			l, err := a.layout()
			if err != nil {
				return err
			}
			set, _, err := a.componentSet()
			if err != nil {
				return err
			}
			existing, _ := set.Get(key)
			if len(bins) == 0 && existing == nil {
				// The manifest entry of a component no tier defines may
				// declare its binaries (FR-018); a missing or unreadable
				// manifest leaves the key as the default.
				if m, err := manifest.Load(l.ManifestPath()); err == nil {
					for _, ref := range m.Components {
						if ref.Key == key {
							bins = slices.Clone(ref.Bins)
						}
					}
				}
			}
			finalUser := ""
			if planned, err := lastPlanUser(l); err == nil {
				finalUser = planned
			}
			p, err := build.BuildRecipe(l.FragmentsDir(), l.RecipesDir(), key, build.PromoteOptions{
				Name: name, Bins: bins, Existing: existing, FinalUser: finalUser,
			})
			if err != nil {
				return withCode(ExitUsage, err)
			}
			if !yes {
				_, _ = fmt.Fprintf(a.Stdout, "%s\n", p.Data)
				_, _ = fmt.Fprintf(a.Stdout, "write %s and mark the fragment superseded? [y/N] ", l.RelToRoot(p.Path))
				answer, err := a.readLine(cmd.Context())
				if err != nil {
					return err
				}
				if reply := strings.ToLower(strings.TrimSpace(answer)); reply != "y" && reply != "yes" {
					_, _ = fmt.Fprintln(a.Stdout, "not promoted")
					return silentErr{code: ExitFailure}
				}
			}
			if err := build.Promote(l.FragmentsDir(), p); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.Stdout, "promoted %s to %s; the fragment is marked superseded\n", key, l.RelToRoot(p.Path))
			removed, err := dropDeclaredBins(l.ManifestPath(), key)
			if err != nil {
				return fmt.Errorf("promoted %s, but removing bins from its manifest entry failed (remove them by hand, the recipe now defines the binaries): %w", key, err)
			}
			if removed {
				_, _ = fmt.Fprintf(a.Stdout, "removed bins from the manifest entry %s; the recipe now defines its binaries\n", key)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "display name of the recipe (default: the existing name or the key)")
	f.StringSliceVar(&bins, "bins", nil, "binaries the recipe installs (default: the existing definition's, else the manifest entry's bins, else the key)")
	f.BoolVar(&yes, "yes", false, "promote without asking")
	return cmd
}

// dropDeclaredBins removes the declared bins of the manifest entry key
// after promotion: the project recipe now defines the component, and a
// declared list on a tier-defined component fails validation (FR-018, one
// source of truth). The manifest goes through the regular save path, which
// re-encodes it. A missing manifest or an entry without bins is left alone.
func dropDeclaredBins(path, key string) (bool, error) {
	m, err := manifest.Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	removed := false
	for i := range m.Components {
		if m.Components[i].Key == key && len(m.Components[i].Bins) > 0 {
			m.Components[i].Bins = nil
			removed = true
		}
	}
	if !removed {
		return false, nil
	}
	return true, manifest.Save(path, m)
}

func (a *App) learningsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "learnings",
		Short: "Record build failures and fixes (used by the build skill)",
	}
	var e build.Learning
	add := &cobra.Command{
		Use:   "add",
		Short: "Append a structured learnings entry to .sandcast/learnings.yaml",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := a.layout()
			if err != nil {
				return err
			}
			if e.Date == "" {
				e.Date = time.Now().UTC().Format("2006-01-02")
			}
			if err := build.AppendLearning(l.LearningsPath(), e); err != nil {
				return withCode(ExitUsage, err)
			}
			_, _ = fmt.Fprintf(a.Stdout, "recorded attempt %d for %s (%s) in %s\n", e.Attempt, e.Component, e.Outcome, l.RelToRoot(l.LearningsPath()))
			return nil
		},
	}
	f := add.Flags()
	f.StringVar(&e.Component, "component", "", "component key")
	f.StringVar(&e.Failure, "failure", "", "what failed (summary of the build log)")
	f.StringVar(&e.Change, "change", "", "what changes for the next attempt")
	f.IntVar(&e.Attempt, "attempt", 1, "attempt number")
	f.StringVar(&e.Outcome, "outcome", build.OutcomeUnfixed, "fixed or unfixed")
	f.StringVar(&e.Date, "date", "", "date YYYY-MM-DD (default: today)")
	cmd.AddCommand(add)
	return cmd
}

// lastPlanUser returns the final user of the last plan of the manifest's
// default target, so promotion can tell root steps from user steps.
func lastPlanUser(l fsutil.Layout) (string, error) {
	m, err := manifest.Load(l.ManifestPath())
	if err != nil {
		return "", err
	}
	p, err := plan.Load(filepath.Join(l.BuildDir(m.DefaultTarget()), plan.PlanFile))
	if err != nil {
		return "", err
	}
	return p.FinalUser.Name, nil
}

func readInput(stdin io.Reader, file string) (string, error) {
	var data []byte
	var err error
	if file != "" && file != "-" {
		data, err = os.ReadFile(file)
	} else {
		data, err = io.ReadAll(stdin)
	}
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return string(data), nil
}
