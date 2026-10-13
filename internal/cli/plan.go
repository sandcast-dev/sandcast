package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/plan"
)

func (a *App) planCommand() *cobra.Command {
	var target, platform, out string
	var noCache bool
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Probe the base image and write the build plan",
		Long: `plan probes the base image (cached by digest), resolves components and
profiles from their tiers, checks the lockfile for the target platform and
writes .sandcast/build/<target>/plan.yaml with mise/ and policy.yaml.

The plan is deterministic: the same manifest, lockfile, definitions, base
image digest and platform give byte-identical output. Conflicts or lockfile
gaps exit 2 without writing anything.`,
		Args: args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := a.pipelineEnv()
			if err != nil {
				return err
			}
			planned, err := a.planned(cmd.Context(), env, target, platform, noCache)
			if err != nil {
				return err
			}
			p := planned.Plan
			if out != "" {
				abs, err := filepath.Abs(out)
				if err != nil {
					return err
				}
				planned.Dir = abs
			}
			conflicts := len(p.Conflicts) > 0
			if !conflicts {
				if err := pipeline.WritePlan(env, planned); err != nil {
					return pipelineErr(err)
				}
			}
			if a.opts.json {
				if err := writeJSON(a.Stdout, p); err != nil {
					return err
				}
			} else {
				printPlanSummary(a, p, env.Layout.RelToRoot(filepath.Join(planned.Dir, plan.PlanFile)), !conflicts)
			}
			// Engine host facts never enter the plan file (FR-016).
			for _, n := range pipeline.RuntimeNotices(cmd.Context(), env, planned) {
				a.logf("notice: %s", n)
			}
			if conflicts {
				for _, c := range p.Conflicts {
					a.logf("%s", plan.ConflictProblem(c))
				}
				return silentErr{code: ExitUsage}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "openshell or container (default: openshell when the manifest declares it, else container)")
	f.StringVar(&platform, "platform", "", "linux/amd64 or linux/arm64 (overrides the manifest, default: host)")
	f.BoolVar(&noCache, "no-cache", false, "discard cached probe results for the base image digest and probe again")
	f.StringVar(&out, "out", "", "write the plan outputs here instead of .sandcast/build/<target>")
	return cmd
}

func printPlanSummary(a *App, p *plan.Plan, path string, written bool) {
	registry := "-"
	if p.BaseImage.RegistryEntry != nil {
		registry = *p.BaseImage.RegistryEntry
	}
	user := p.FinalUser.Name
	if p.FinalUser.Created {
		user += " (created)"
	}
	_ = keyValue(a.Stdout,
		"target", p.Target,
		"platform", p.Platform.Value+" (from "+p.Platform.Source+")",
		"engine", p.Engine.Name+" ("+p.Engine.Source+")",
		"base image", p.BaseImage.Reference+" ["+registry+"]",
		"digest", p.BaseImage.Digest,
		"package manager", p.ImageFacts.PackageManager,
		"final user", user,
	)
	_, _ = fmt.Fprintln(a.Stdout)
	printDelta(a, p.ProbeDelta)
	if len(p.Profiles) > 0 {
		_, _ = fmt.Fprintln(a.Stdout)
		tb := newTable(a.Stdout, "PROFILE", "TIER", "SOURCE")
		for _, pr := range p.Profiles {
			tb.row(pr.ID, pr.Tier, pr.SourceFile)
		}
		_ = tb.flush()
	}
	if len(p.Unresolved) > 0 {
		_, _ = fmt.Fprintln(a.Stdout)
		for _, u := range p.Unresolved {
			kind := "unresolved"
			if u.Optional {
				kind = "unresolved (optional)"
			}
			_, _ = fmt.Fprintf(a.Stdout, "%s: %s: %s\n", kind, u.Key, u.Reason)
		}
	}
	for _, n := range p.Notices {
		_, _ = fmt.Fprintf(a.Stdout, "notice: %s\n", n)
	}
	if written {
		_, _ = fmt.Fprintf(a.Stdout, "\nwrote %s\n", path)
	}
}
