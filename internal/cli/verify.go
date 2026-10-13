package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/verify"
)

func (a *App) verifyCommand() *cobra.Command {
	var target, image string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Run every component check inside the built image",
		Long: `verify runs each component's check as the image's final user under the
container engine (no gateway needed), writes verify.yaml, reports every
result, and exits 1 if any check failed.

For the OpenShell target it always adds session checks: every component
binary must resolve in a login bash and an interactive bash started with
OpenShell's cleared environment (only HOME, USER, TERM and the default
PATH), and with shell.program zsh also in an interactive zsh, whose startup
must read the curated rc and write nothing to stderr.`,
		Args: args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := a.pipelineEnv()
			if err != nil {
				return err
			}
			t, err := pipeline.TargetOf(env.Manifest, target)
			if err != nil {
				return pipelineErr(err)
			}
			dir := env.Layout.BuildDir(t)
			planPath := filepath.Join(dir, plan.PlanFile)
			if err := checkBuildDir(env.Layout, dir); err != nil {
				return err
			}
			if _, err := os.Stat(planPath); errors.Is(err, os.ErrNotExist) {
				return usageErr("no plan for the %s target; run \"sandcast build --target %s\" first", t, t)
			}
			// A build rewrites the plan, the image and verify.yaml; verify
			// reads and writes them only while no build runs.
			unlock, err := build.LockTarget(dir, t)
			if err != nil {
				return err
			}
			defer unlock()
			p, err := plan.Load(planPath)
			if err != nil {
				return err
			}
			if image == "" {
				image = env.Manifest.Target(t).Ref()
			}
			report, err := pipeline.Verify(cmd.Context(), env, dir, image, p.Platform.Value, &pipeline.Planned{Plan: p, Dir: dir})
			if report != nil {
				if a.opts.json {
					if jerr := writeJSON(a.Stdout, report); jerr != nil {
						return jerr
					}
				} else {
					printVerify(a, report)
				}
			}
			if errors.Is(err, verify.ErrChecksFailed) {
				return silentErr{code: ExitFailure}
			}
			return pipelineErr(err)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "openshell or container (default: openshell when the manifest declares it, else container)")
	cmd.Flags().StringVar(&image, "image", "", "image to verify (default: the target's image and tag)")
	return cmd
}

// checkBuildDir refuses a build directory reached through a symlink or
// holding a symlink among the entries sandcast writes there (exit 2), before
// a command locks it and writes into it.
func checkBuildDir(l fsutil.Layout, dir string) error {
	if err := l.CheckNoSymlinks(dir); err != nil {
		return withCode(ExitUsage, err)
	}
	if err := l.CheckNoSymlinkEntries(dir, fsutil.BuildEntries()...); err != nil {
		return withCode(ExitUsage, err)
	}
	return nil
}

func printVerify(a *App, r *verify.Report) {
	var image, session []verify.Result
	for _, res := range r.Results {
		if res.IsSession() {
			session = append(session, res)
		} else {
			image = append(image, res)
		}
	}
	tb := newTable(a.Stdout, "COMPONENT", "RESULT", "VERSION", "OUTPUT")
	for _, res := range image {
		tb.row(res.ComponentKey, verifyResult(res), orDash(res.Version), firstLine(res.Output))
	}
	_ = tb.flush()
	if len(session) > 0 {
		_, _ = fmt.Fprintln(a.Stdout, "\nsession checks (cleared environment as in an OpenShell session):")
		tb = newTable(a.Stdout, "CHECK", "CONDITION", "RESULT", "OUTPUT")
		for _, res := range session {
			tb.row(res.ComponentKey, res.Condition, verifyResult(res), firstLine(res.Output))
		}
		_ = tb.flush()
	}
	_, _ = fmt.Fprintf(a.Stdout, "verify %s: %d passed, %d failed\n", r.Image.Reference, r.Passed, r.Failed)
}

func verifyResult(res verify.Result) string {
	if res.OK {
		return "ok"
	}
	return "FAIL (" + res.Status + ")"
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return orDash(s)
}
