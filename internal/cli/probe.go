package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/probe"
)

type probeReport struct {
	Reference        string            `json:"reference"`
	Facts            probe.Facts       `json:"facts"`
	Delta            []plan.DeltaEntry `json:"delta"`
	ContainerStarted bool              `json:"container_started"`
	Probed           []string          `json:"probed"`
}

func (a *App) probeCommand() *cobra.Command {
	var platform, target string
	var noCache bool
	cmd := &cobra.Command{
		Use:   "probe [<ref>]",
		Short: "Probe a base image (default: the manifest's) and print facts plus the probe delta",
		Args:  args(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, argv []string) error {
			l, err := a.layout()
			if err != nil {
				return err
			}
			m, err := manifest.Load(l.ManifestPath())
			refOnly := errors.Is(err, os.ErrNotExist) && len(argv) == 1
			if err != nil && !refOnly {
				return withCode(ExitUsage, err)
			}
			manifestEngine := ""
			if m != nil {
				manifestEngine = m.Engine
			}
			eng, sel, err := a.selectEngine(manifestEngine)
			if err != nil {
				return err
			}
			cache, err := a.cacheDir()
			if err != nil {
				return err
			}
			env := pipeline.Env{Layout: l, Manifest: m, Engine: eng, EngineSel: sel, HostPlatform: a.HostPlatform(), CacheDir: cache, Logf: a.logf}
			ref := ""
			if len(argv) == 1 {
				ref = argv[0]
			}
			probed, err := pipeline.ProbeBase(cmd.Context(), env, pipeline.ProbeOptions{Target: target, PlatformFlag: platform, Reference: ref, NoCache: noCache})
			if err != nil {
				return pipelineErr(err)
			}
			for _, n := range probed.Notices {
				a.logf("notice: %s", n)
			}
			res := probed.Result
			ref = probed.Base.Reference
			delta, reasons := probed.Delta()
			for _, r := range reasons {
				a.logf("%s", r)
			}
			report := probeReport{Reference: ref, Facts: res.Facts, Delta: delta, ContainerStarted: res.ContainerStarted, Probed: res.Probed}
			if a.opts.json {
				return writeJSON(a.Stdout, report)
			}
			f := res.Facts
			if err := keyValue(a.Stdout,
				"reference", ref,
				"digest", f.Digest,
				"platform", f.Platform,
				"os", fmt.Sprintf("%s %s (%s)", f.OS.Name, f.OS.Version, f.OS.ID),
				"package manager", f.PackageManager,
				"user", fmt.Sprintf("%s (uid %d, gid %d, home %s)", f.User.Name, f.User.UID, f.User.GID, f.User.Home),
				"shells", joinOrDash(f.Shells),
			); err != nil {
				return err
			}
			if len(report.Delta) > 0 {
				_, _ = fmt.Fprintln(a.Stdout)
				printDelta(a, report.Delta)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&platform, "platform", "", "target platform linux/amd64 or linux/arm64 (default: manifest, else host)")
	f.StringVar(&target, "target", "", "target whose components are checked (default: openshell when the manifest declares it, else container)")
	f.BoolVar(&noCache, "no-cache", false, "discard cached results for this image digest and probe again")
	return cmd
}

func printDelta(a *App, delta []plan.DeltaEntry) {
	tb := newTable(a.Stdout, "COMPONENT", "STATUS", "INSTALLED", "REQUIRED", "ACTION")
	for _, d := range delta {
		tb.row(d.ComponentKey, d.Status, orDash(d.InstalledVersion), orDash(d.RequiredVersion), d.Action)
	}
	_ = tb.flush()
}
