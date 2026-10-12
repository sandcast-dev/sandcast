package pipeline

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/profile"
	"github.com/rhuss/sandcast/internal/verify"
)

// BuildOptions control the build step.
type BuildOptions struct {
	Verify bool
	// Push overrides the target's push field when set (nil: use the
	// manifest), so --push=false skips the push of a target with push: true.
	Push          *bool
	NoCacheLayers bool
	KeepStage     bool
	// Progress receives the engine output (nil: only build.log).
	Progress io.Writer
}

// Built is the outcome of Build.
type Built struct {
	Build  *build.Result
	Verify *verify.Report
	// AllowedDomains is the container target's allowed-domains file.
	AllowedDomains string
}

// Build writes the plan outputs, builds the image in two passes and, when
// asked, verifies it. A failed verify still returns the report.
func Build(ctx context.Context, env Env, planned *Planned, opts BuildOptions) (*Built, error) {
	p := planned.Plan
	t := env.Manifest.Target(p.Target)
	out := &Built{}
	if p.Target == manifest.TargetContainer {
		path, err := allowedDomainsPath(planned.Dir, t.AllowedDomains())
		if err != nil {
			return nil, err
		}
		out.AllowedDomains = path
	}
	// One lock covers the plan outputs, both passes and verify.
	unlock, err := lockBuildDir(env, planned)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := writePlan(env, planned); err != nil {
		return nil, err
	}
	var after func(set *profile.AdaptedSet) error
	if out.AllowedDomains != "" {
		after = func(set *profile.AdaptedSet) error {
			return WriteAllowedDomains(out.AllowedDomains, set)
		}
	}
	res, err := build.Run(ctx, build.Options{
		Dir:           planned.Dir,
		Plan:          p,
		Engine:        env.Engine,
		Image:         t.Image,
		Tag:           t.TagOrDefault(),
		ManifestName:  env.Manifest.Name,
		Profiles:      planned.Profiles,
		NoCacheLayers: opts.NoCacheLayers,
		KeepStage:     opts.KeepStage,
		Push:          resolvePush(opts.Push, t.Push),
		Progress:      opts.Progress,
		AfterResolve:  after,
	})
	out.Build = res
	if err != nil {
		return out, err
	}
	if opts.Verify {
		report, err := Verify(ctx, env, planned.Dir, res.Image, p.Platform.Value, planned)
		out.Verify = report
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// resolvePush returns the command-line choice when one was given, else the
// target's push field.
func resolvePush(flag *bool, target bool) bool {
	if flag != nil {
		return *flag
	}
	return target
}

// Verify runs the plan's checks inside image and writes verify.yaml.
func Verify(ctx context.Context, env Env, dir, image, platform string, planned *Planned) (*verify.Report, error) {
	if err := env.Layout.CheckNoSymlinks(dir); err != nil {
		return nil, &UsageError{Err: err}
	}
	report, err := verify.Run(ctx, env.Engine, image, platform, planned.Plan)
	if report != nil {
		if werr := report.Write(filepath.Join(dir, verify.FileName)); werr != nil {
			return report, werr
		}
	}
	return report, err
}

// allowedDomainsPath joins the allowed-domains file name to the build
// directory. The manifest validation already checks the name
// (manifest.CheckAllowedDomainsFile); this check repeats it so a manifest
// that skipped validation still cannot make the build write outside dir or
// rename the domain list over the lock or the log while the build runs.
func allowedDomainsPath(dir, name string) (string, error) {
	if err := manifest.CheckAllowedDomainsFile(name); err != nil {
		return "", usage("targets.container.allowed_domains_file %v", err)
	}
	path := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel != name {
		return "", usage("targets.container.allowed_domains_file %q leaves the build directory %s", name, dir)
	}
	return path, nil
}

// WriteAllowedDomains writes the sorted unique endpoint hosts of the adapted
// profiles, one per line (FR-040). A host that is not a DNS name, wildcard
// pattern or IP address is refused, so no profile can add lines or other
// content to the file.
func WriteAllowedDomains(path string, set *profile.AdaptedSet) error {
	profiles := make([]*profile.Profile, 0, len(set.Profiles))
	for _, ap := range set.Profiles {
		profiles = append(profiles, ap.Profile)
	}
	hosts := profile.Domains(profiles)
	for _, h := range hosts {
		if !profile.ValidHost(h) {
			return fmt.Errorf("write allowed domains: endpoint host %q is not a valid host name, wildcard pattern or IP address", h)
		}
	}
	content := ""
	if len(hosts) > 0 {
		content = strings.Join(hosts, "\n") + "\n"
	}
	if err := fsutil.AtomicWriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write allowed domains: %w", err)
	}
	return nil
}
