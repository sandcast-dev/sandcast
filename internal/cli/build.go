package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/gateway"
	"github.com/rhuss/sandcast/internal/imageref"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/verify"
)

type buildReport struct {
	Image string `json:"image"`
	// ImageRef is the fully qualified reference a gateway pulls (R-12).
	ImageRef string `json:"image_ref,omitempty"`
	// SandboxCreate is the command that starts a sandbox from the image
	// (OpenShell target).
	SandboxCreate string `json:"sandbox_create,omitempty"`
	// Pushed reports whether the image was pushed; Digest is the registry
	// digest when it was (FR-011).
	Pushed         bool     `json:"pushed"`
	Digest         string   `json:"digest,omitempty"`
	StageImage     string   `json:"stage_image,omitempty"`
	Containerfile  string   `json:"containerfile"`
	BuildLog       string   `json:"build_log"`
	Profiles       string   `json:"profiles,omitempty"`
	Flagged        []string `json:"flagged"`
	flagReasons    map[string]string
	AllowedDomains string             `json:"allowed_domains,omitempty"`
	Verify         *verify.Report     `json:"verify,omitempty"`
	Resolutions    []build.Resolution `json:"resolutions"`
	// Notices repeats the plan notices, followed by runtime notices
	// (FR-002, R-15).
	Notices []string `json:"notices"`
	Error   string   `json:"error,omitempty"`
}

func (a *App) buildCommand() *cobra.Command {
	var target, platform string
	var doVerify, push, noCacheLayers, keepStage bool
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Plan, assemble the Containerfile, build in two passes and write adapted profiles",
		Long: `build runs the plan, assembles .sandcast/build/<target>/Containerfile from
it and the stored fragments, builds the image with the container engine in
two passes (pass 1 under a per-run <image>:sandcast-stage-<id> tag, pass 2 adds the adapted
profile set and the io.sandcast.* labels), and writes profiles/ with
binaries resolved inside the image.

A failed build exits 1, leaves the Containerfile and build.log in place and
is never retried. --verify runs every component check afterwards.`,
		Args: args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := a.pipelineEnv()
			if err != nil {
				return err
			}
			planned, err := a.planned(cmd.Context(), env, target, platform, false)
			if err != nil {
				return err
			}
			if problems := planned.Plan.Problems(); len(problems) > 0 {
				for _, pr := range problems {
					a.logf("%s", pr)
				}
				return usageErr("the plan cannot be built (%d problem(s)); see above", len(problems))
			}
			for _, u := range planned.Plan.Unresolved {
				a.logf("skipping optional unresolved component %s: %s", u.Key, u.Reason)
			}
			runtimeNotices := pipeline.RuntimeNotices(cmd.Context(), env, planned)
			if w := a.gatewayWarning(cmd, env, planned); w != "" {
				runtimeNotices = append(runtimeNotices, w)
			}
			// Shown before the build as well, so an emulation or gateway
			// pitfall is visible before a long build starts; the report
			// repeats them.
			for _, n := range runtimeNotices {
				a.logf("notice: %s", n)
			}
			opts := pipeline.BuildOptions{Verify: doVerify, Push: push, NoCacheLayers: noCacheLayers, KeepStage: keepStage}
			if a.opts.verbose {
				opts.Progress = a.Stderr
			}
			a.logf("building %s (log: %s)", env.Manifest.Target(planned.Plan.Target).Ref(), env.Layout.RelToRoot(planned.Dir+"/"+build.BuildLogName))
			built, err := pipeline.Build(cmd.Context(), env, planned, opts)
			report := buildReportOf(env, planned, built, runtimeNotices)
			if err != nil {
				report.Error = err.Error()
				// Only a finished build has an image to start a sandbox from.
				report.SandboxCreate = ""
			}
			if a.opts.json {
				if jerr := writeJSON(a.Stdout, report); jerr != nil {
					return jerr
				}
			} else {
				printBuildReport(a, report)
				if errors.Is(err, build.ErrBuildFailed) && !a.opts.verbose && built != nil && built.Build != nil {
					a.printLogTail(built.Build.BuildLog, buildLogTail)
				}
			}
			switch {
			case err == nil:
				return nil
			case errors.Is(err, verify.ErrChecksFailed):
				return silentErr{code: ExitFailure}
			case errors.As(err, new(*pipeline.UsageError)):
				return pipelineErr(err)
			default:
				return withCode(ExitFailure, err)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "openshell or container (default: openshell when the manifest declares it, else container)")
	f.StringVar(&platform, "platform", "", "linux/amd64 or linux/arm64 (overrides the manifest, default: host)")
	f.BoolVar(&doVerify, "verify", false, "run every component check in the built image afterwards")
	f.BoolVar(&push, "push", false, "push the final image")
	f.BoolVar(&noCacheLayers, "no-cache-layers", false, "build without the engine layer cache")
	f.BoolVar(&keepStage, "keep-stage", false, "keep the pass-1 image as <image>:sandcast-stage")
	return cmd
}

// buildLogTail is how many build.log lines a failed build prints.
const buildLogTail = 25

// printLogTail prints the last n lines of the log at path to stderr, so a
// failed build shows the engine's error without opening the file.
func (a *App) printLogTail(path string, n int) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	a.logf("last %d line(s) of %s:", len(lines), filepath.Base(path))
	for _, l := range lines {
		a.logf("  %s", l)
	}
}

// buildReportOf assembles the report: plan notices first, then the runtime
// notices (engine host and gateway), then the docker-hub notice of a push.
func buildReportOf(env pipeline.Env, planned *pipeline.Planned, built *pipeline.Built, runtime []string) buildReport {
	r := buildReport{Flagged: []string{}, Resolutions: []build.Resolution{}, Notices: []string{}}
	if planned != nil && planned.Plan != nil {
		r.Notices = append(r.Notices, planned.Plan.Notices...)
	}
	r.Notices = append(r.Notices, runtime...)
	if built == nil || built.Build == nil {
		return r
	}
	b := built.Build
	r.Image = b.Image
	r.Pushed, r.Digest = b.Pushed, b.Digest
	if ref, err := imageref.Normalize(b.Image); err == nil {
		r.ImageRef = ref.String()
		if planned != nil && planned.Plan != nil && planned.Plan.Target == manifest.TargetOpenShell {
			r.SandboxCreate = "openshell sandbox create --from " + r.ImageRef
		}
		if b.Pushed && ref.Registry == imageref.DockerHub {
			r.Notices = append(r.Notices, plan.Notice(plan.NoticeDockerHub, plan.NoticeSubjectImage,
				"anonymous pulls from cluster nodes are rate limited; give the gateway's sandbox service account a pull secret for docker.io or push to a registry the cluster already authenticates to"))
		}
	}
	r.StageImage = b.StageImage
	r.Containerfile = env.Layout.RelToRoot(b.Containerfile)
	r.BuildLog = env.Layout.RelToRoot(b.BuildLog)
	if b.Profiles != nil {
		r.Profiles = env.Layout.RelToRoot(strings.TrimSuffix(b.Containerfile, build.ContainerfileName) + build.ProfilesDirName)
	}
	r.flagReasons = map[string]string{}
	for _, f := range b.Flagged {
		r.Flagged = append(r.Flagged, f.AdaptedID)
		r.flagReasons[f.AdaptedID] = f.FlagReason
	}
	if b.Resolutions != nil {
		r.Resolutions = b.Resolutions
	}
	if built.AllowedDomains != "" {
		r.AllowedDomains = env.Layout.RelToRoot(built.AllowedDomains)
	}
	r.Verify = built.Verify
	return r
}

func printBuildReport(a *App, r buildReport) {
	pushed := "no"
	if r.Pushed {
		pushed = "yes"
	}
	if r.Error != "" {
		_, _ = fmt.Fprintf(a.Stdout, "build failed: %s\n", r.Error)
		if r.ImageRef != "" {
			_, _ = fmt.Fprintf(a.Stdout, "image ref: %s\npushed: %s\n", r.ImageRef, pushed)
		}
		_, _ = fmt.Fprintf(a.Stdout, "containerfile: %s\nbuild log: %s\n", orDash(r.Containerfile), orDash(r.BuildLog))
		if r.StageImage != "" {
			_, _ = fmt.Fprintf(a.Stdout, "stage image kept as %s\n", r.StageImage)
		}
	} else {
		_ = keyValue(a.Stdout,
			"image", r.Image,
			"image ref", orDash(r.ImageRef),
			"pushed", pushed,
			"digest", orDash(r.Digest),
			"containerfile", r.Containerfile,
			"build log", r.BuildLog,
			"profiles", orDash(r.Profiles),
		)
		if r.SandboxCreate != "" {
			_, _ = fmt.Fprintf(a.Stdout, "sandbox create: %s\n", r.SandboxCreate)
		}
		if r.AllowedDomains != "" {
			_, _ = fmt.Fprintf(a.Stdout, "allowed domains: %s\n", r.AllowedDomains)
		}
		for _, id := range r.Flagged {
			_, _ = fmt.Fprintf(a.Stdout, "flagged: profile %s: %s\n", id, r.flagReasons[id])
		}
	}
	for _, n := range r.Notices {
		_, _ = fmt.Fprintf(a.Stdout, "notice: %s\n", n)
	}
	if r.Verify != nil {
		_, _ = fmt.Fprintln(a.Stdout)
		printVerify(a, r.Verify)
	}
}

// gatewayWarning checks the active gateway for an OpenShell build when the
// openshell CLI is present (FR-014). It never fails the build.
func (a *App) gatewayWarning(cmd *cobra.Command, env pipeline.Env, planned *pipeline.Planned) string {
	if planned == nil || planned.Plan == nil || planned.Plan.Target != manifest.TargetOpenShell || a.Gateway == nil {
		return ""
	}
	if _, err := a.LookPath("openshell"); err != nil {
		return ""
	}
	t := env.Manifest.Target(manifest.TargetOpenShell)
	if t == nil {
		return ""
	}
	return pipeline.GatewayWarning(cmd.Context(), &gateway.Client{Runner: a.Gateway}, t.Ref())
}
