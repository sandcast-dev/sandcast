package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/mise"
	"github.com/rhuss/sandcast/internal/pipeline"
)

type lockReport struct {
	Lockfile  string           `json:"lockfile"`
	Platforms []string         `json:"platforms"`
	Locked    []string         `json:"locked"`
	Failed    []mise.ToolError `json:"failed"`
	Changed   bool             `json:"changed"`
}

func (a *App) lockCommand() *cobra.Command {
	var platforms []string
	var bump bool
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Generate or update .sandcast/mise.lock for the target platform(s)",
		Long: `lock resolves every mise tool the manifest needs (for all declared targets)
with the host mise and writes .sandcast/mise.lock with exact versions and
per-platform checksums. Builds install in locked mode from this file.`,
		Args: args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, l, err := a.loadManifest()
			if err != nil {
				return err
			}
			if len(platforms) == 0 {
				sel, err := pipeline.SelectPlatform("", m.Platform, a.HostPlatform())
				if err != nil {
					return pipelineErr(err)
				}
				platforms = []string{sel.Value}
			}
			var misePlatforms []string
			for _, p := range platforms {
				mp, err := mise.Platform(p)
				if err != nil {
					return usageErr("--platform %s: %v", p, err)
				}
				misePlatforms = append(misePlatforms, mp)
			}
			slices.Sort(misePlatforms)
			misePlatforms = slices.Compact(misePlatforms)

			set, notices, err := a.componentSet()
			if err != nil {
				return err
			}
			for _, n := range notices {
				a.logf("notice: %s", n)
			}
			tools := pipeline.LockTools(m, set)
			if len(tools) == 0 {
				_, _ = fmt.Fprintln(a.Stdout, "no mise tools in the manifest; nothing to lock")
				return nil
			}
			var opts []mise.LockOption
			if bump {
				opts = append(opts, mise.WithBump())
			}
			res, err := mise.Lock(cmd.Context(), a.MiseRunner, tools, misePlatforms, l.LockPath(), opts...)
			if err != nil {
				if errors.Is(err, mise.ErrMiseMissing) || errors.Is(err, mise.ErrMiseTooOld) {
					return toolErr(err)
				}
				return err
			}
			report := lockReport{
				Lockfile: l.RelToRoot(l.LockPath()), Platforms: misePlatforms,
				Locked: res.Locked, Failed: res.Failed, Changed: res.Changed,
			}
			if report.Failed == nil {
				report.Failed = []mise.ToolError{}
			}
			if a.opts.json {
				if err := writeJSON(a.Stdout, report); err != nil {
					return err
				}
			} else {
				state := "unchanged"
				if res.Changed {
					state = "written"
				}
				_, _ = fmt.Fprintf(a.Stdout, "%s %s for %v: %d tool(s) locked\n", report.Lockfile, state, misePlatforms, len(res.Locked))
				for _, f := range res.Failed {
					_, _ = fmt.Fprintf(a.Stdout, "failed: %s\n", f.Error())
				}
			}
			if len(res.Failed) > 0 {
				return silentErr{code: ExitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&platforms, "platform", nil, "target platform(s) linux/amd64 or linux/arm64 (repeatable; default: manifest platform, else host)")
	cmd.Flags().BoolVar(&bump, "bump", false, "re-resolve fuzzy versions (latest, 1.25) to the newest matching releases")
	return cmd
}
