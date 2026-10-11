package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/harness"
	"github.com/rhuss/sandcast/internal/skills"
)

func (a *App) skillsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Install and check the sandcast skill bundle in an agent harness",
	}
	var harnessName, scope string
	var force bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the skill bundle where the harness discovers it",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ad, err := harness.Get(harnessName)
			if err != nil {
				return usageErr("%v", err)
			}
			if scope != string(harness.ScopeUser) && scope != string(harness.ScopeProject) {
				return usageErr("--scope must be user or project, got %q", scope)
			}
			env, err := a.harnessEnv()
			if err != nil {
				return err
			}
			b, err := skills.Embedded()
			if err != nil {
				return err
			}
			res, err := skills.Install(b, ad, harness.Scope(scope), env, force)
			if err != nil {
				return err
			}
			if a.opts.json {
				return writeJSON(a.Stdout, res)
			}
			for _, r := range res {
				line := fmt.Sprintf("%s %s (%s) at %s", r.Action, r.Skill, r.Version, r.Path)
				if r.Reason != "" {
					line += ": " + r.Reason
				}
				_, _ = fmt.Fprintln(a.Stdout, line)
			}
			_, _ = fmt.Fprintln(a.Stdout, "invoke them as /sandcast-capture and /sandcast-build")
			return nil
		},
	}
	install.Flags().StringVar(&harnessName, "harness", "claude-code", "harness to install into")
	install.Flags().StringVar(&scope, "scope", string(harness.ScopeUser), "user (all projects) or project (this project)")
	install.Flags().BoolVar(&force, "force", false, "overwrite installed skills even when they are the same or newer")

	var checkHarness string
	check := &cobra.Command{
		Use:   "check",
		Short: "Compare the installed skills with the bundle in this binary (exit 3 on mismatch)",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ad, err := harness.Get(checkHarness)
			if err != nil {
				return usageErr("%v", err)
			}
			env, err := a.harnessEnv()
			if err != nil {
				return err
			}
			b, err := skills.Embedded()
			if err != nil {
				return err
			}
			rep, err := skills.Check(b, ad, env)
			if err != nil {
				return err
			}
			if a.opts.json {
				if err := writeJSON(a.Stdout, rep); err != nil {
					return err
				}
			} else if rep.OK {
				_, _ = fmt.Fprintf(a.Stdout, "skills match the sandcast binary (bundle %s)\n", b.Version)
			}
			if !rep.OK {
				a.logf("%s", rep.Message())
				return silentErr{code: ExitTool}
			}
			return nil
		},
	}
	check.Flags().StringVar(&checkHarness, "harness", "claude-code", "harness whose installed skills are checked")
	cmd.AddCommand(install, check)
	return cmd
}
