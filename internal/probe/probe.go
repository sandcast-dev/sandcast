package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rhuss/sandcast/internal/engine"
)

// maxStderr is the number of trailing stderr bytes quoted in a probe error.
const maxStderr = 2048

// Options control Probe.
type Options struct {
	// NoCache removes the cache entries of the image digest and platform
	// before probing, so that the facts and every check run again.
	NoCache bool
	// Cache is the probe cache; nil means DefaultCache.
	Cache *Cache
	// Now returns the probe time; nil means time.Now.
	Now func() time.Time
	// Offline skips the pull and uses the local image.
	Offline bool
}

// Probe pulls ref for platform, reads its digest and returns the image facts
// and the result of every request (FR-011, FR-012). Facts and checks come
// from the cache when present; a container runs only for what is missing:
// the facts and all checks on a new image, only the missing checks when the
// facts are cached, and nothing at all when everything is cached (SC-004).
// An empty platform means the platform of the pulled image. When the pull
// fails but the image exists locally for the platform, the local image is
// used and a notice says so.
func Probe(ctx context.Context, eng engine.Engine, ref, platform string, reqs []Request, opts Options) (*Result, error) {
	if ref == "" {
		return nil, errors.New("probe: empty image reference")
	}
	reqs, err := normalize(reqs)
	if err != nil {
		return nil, err
	}
	cache := opts.Cache
	if cache == nil {
		if cache, err = DefaultCache(); err != nil {
			return nil, err
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	res := &Result{Checks: map[string]CheckResult{}, Probed: []string{}, Notices: []string{}}

	platform, notice, err := ensureImage(ctx, eng, ref, platform, opts.Offline)
	if err != nil {
		return nil, err
	}
	if notice != "" {
		res.Notices = append(res.Notices, notice)
	}
	digest, err := eng.Digest(ctx, ref)
	if err == nil && digest == "" {
		err = engine.ErrNoDigest
	}
	if err != nil {
		return nil, fmt.Errorf("probe %s: read image digest: %w", ref, err)
	}
	if _, err := entryName(digest, platform); err != nil {
		return nil, fmt.Errorf("probe %s: %w", ref, err)
	}
	if opts.NoCache {
		if err := cache.Invalidate(digest, platform); err != nil {
			return nil, fmt.Errorf("probe %s: %w", ref, err)
		}
	}

	facts, cached := cache.LookupFacts(digest, platform)
	res.FactsFromCache = cached
	var todo []Request
	for _, r := range reqs {
		if c, ok := cache.LookupCheck(digest, platform, r.Key, r.Hash); ok {
			res.Checks[r.Key] = *c
		} else {
			todo = append(todo, r)
		}
	}

	var storeErr error
	if !cached || len(todo) > 0 {
		res.ContainerStarted = true
		probed, checks, _, err := runProbe(ctx, eng, ref, platform, todo, runOptions{facts: !cached, requireAll: true})
		if err != nil {
			return nil, err
		}
		if !cached {
			facts = probed
			facts.Reference, facts.Digest, facts.Platform = ref, digest, platform
			facts.ProbedAt = now().UTC().Format(time.RFC3339)
			storeErr = cache.StoreFacts(digest, platform, *facts)
		}
		for _, r := range todo {
			c := evaluateRaw(r, checks[r.Key])
			res.Checks[r.Key] = c
			res.Probed = append(res.Probed, r.Key)
			// A stalled network may be transient; the next plan checks again.
			if c.Status == StatusTimeout {
				res.Notices = append(res.Notices, fmt.Sprintf("check of %s did not finish within %s; treated as missing; it runs again on the next plan", r.Key, CheckTimeout))
				continue
			}
			if err := cache.StoreCheck(digest, platform, c); err != nil && storeErr == nil {
				storeErr = err
			}
		}
	}
	if storeErr != nil {
		res.Notices = append(res.Notices, fmt.Sprintf("probe cache not updated, the next plan probes %s again: %v", ref, storeErr))
	}

	res.Facts = *facts
	res.Facts.Reference, res.Facts.Digest, res.Facts.Platform = ref, digest, platform
	return res, nil
}

// noResult is the output reported by RunChecks for a request without a
// check line in the container output.
const noResult = "no result"

// RunChecks runs the checks of reqs in one container of image with the
// image's default user and evaluates them. It neither pulls nor uses the
// cache and prints no facts, so it suits a freshly built image that has no
// registry digest. A check that did not finish within CheckTimeout is
// reported with StatusTimeout. When the container itself hits its deadline,
// the first check without a line (checks run in key order) is the one that
// was running and is reported as timed out; the checks after it never ran
// and are reported as timed out too, with an output that starts with "not
// run:", so nothing repairs a component that was never checked. Without a
// deadline, a request without a check line is reported as missing with
// output "no result". Both cases have exit -1. No container starts when
// reqs is empty.
func RunChecks(ctx context.Context, eng engine.Engine, image, platform string, reqs []Request) (map[string]CheckResult, error) {
	if image == "" {
		return nil, errors.New("run checks: empty image reference")
	}
	reqs, err := normalize(reqs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]CheckResult, len(reqs))
	if len(reqs) == 0 {
		return out, nil
	}
	_, checks, limit, err := runProbe(ctx, eng, image, platform, reqs, runOptions{})
	if err != nil {
		return nil, err
	}
	// An interrupted run says nothing about the checks it did not reach.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	running := ""
	for _, r := range reqs {
		raw, ok := checks[r.Key]
		switch {
		case ok:
			out[r.Key] = evaluateRaw(r, raw)
		case limit > 0 && running == "":
			out[r.Key] = timedOut(r, -1, "", "", fmt.Sprintf("timeout: the check did not finish; the container was stopped after %s", limit))
			running = r.Key
		case limit > 0:
			out[r.Key] = timedOut(r, -1, "", "", fmt.Sprintf("not run: the container was stopped after %s while %s was running", limit, running))
		default:
			out[r.Key] = CheckResult{ComponentKey: r.Key, CheckHash: r.Hash, Status: StatusMissing, Output: noResult, Exit: -1}
		}
	}
	return out, nil
}

// normalize validates reqs and returns a copy sorted by key, so that the
// generated script is deterministic.
func normalize(reqs []Request) ([]Request, error) {
	out := slices.Clone(reqs)
	slices.SortFunc(out, func(a, b Request) int { return strings.Compare(a.Key, b.Key) })
	for i, r := range out {
		switch {
		case !keyPattern.MatchString(r.Key):
			return nil, fmt.Errorf("probe: invalid component key %q", r.Key)
		case i > 0 && out[i-1].Key == r.Key:
			return nil, fmt.Errorf("probe: component %s requested twice", r.Key)
		case strings.TrimSpace(r.Command) == "":
			return nil, fmt.Errorf("probe: component %s has no check command", r.Key)
		case strings.ContainsRune(r.Command, 0) || strings.ContainsRune(r.Bin, 0):
			return nil, fmt.Errorf("probe: check of component %s contains a NUL byte", r.Key)
		case !hashPattern.MatchString(r.Hash):
			return nil, fmt.Errorf("probe: component %s has an invalid check hash %q", r.Key, r.Hash)
		}
	}
	return out, nil
}

// ensureImage makes ref available locally and returns the platform to probe
// with an optional notice. An empty platform resolves to the image's own.
func ensureImage(ctx context.Context, eng engine.Engine, ref, platform string, offline bool) (string, string, error) {
	var pullErr error
	if !offline {
		if pullErr = eng.Pull(ctx, ref, platform); pullErr == nil {
			if platform != "" {
				return platform, "", nil
			}
			local, err := eng.ImagePlatform(ctx, ref)
			if err != nil {
				return "", "", fmt.Errorf("probe %s: read image platform: %w", ref, err)
			}
			return local, "", nil
		}
	}
	fail := func(reason string) error {
		if offline {
			return fmt.Errorf("probe %s: the host is offline and %s", ref, reason)
		}
		return fmt.Errorf("pull %s: %w (%s)", ref, pullErr, reason)
	}
	exists, err := eng.ImageExists(ctx, ref)
	if err != nil {
		return "", "", fail(fmt.Sprintf("the local image cannot be inspected: %v", err))
	}
	if !exists {
		return "", "", fail("the image is not available locally")
	}
	local, err := eng.ImagePlatform(ctx, ref)
	if err != nil {
		return "", "", fail(fmt.Sprintf("the platform of the local image is unknown: %v", err))
	}
	if platform != "" && !samePlatform(local, platform) {
		return "", "", fail(fmt.Sprintf("the local image is %s, not %s", local, platform))
	}
	if platform == "" {
		platform = local
	}
	if offline {
		return platform, fmt.Sprintf("offline: using the local image of %s (%s)", ref, platform), nil
	}
	return platform, fmt.Sprintf("pull of %s failed, using the local image (%s): %v", ref, platform, pullErr), nil
}

// samePlatform compares the os and architecture of two platforms and
// ignores a variant, because engines report the local image without one.
func samePlatform(a, b string) bool {
	osArch := func(p string) string {
		parts := strings.SplitN(p, "/", 3)
		return strings.Join(parts[:min(len(parts), 2)], "/")
	}
	return osArch(a) == osArch(b)
}

// runOptions control runProbe.
type runOptions struct {
	// facts adds the facts sections; their lines are then required.
	facts bool
	// requireAll makes a request without a check line an error.
	requireAll bool
}

// containerOverhead is the time a probe container may need besides its
// checks: start-up (slow under emulation), the facts sections and removal.
const containerOverhead = 2 * time.Minute

// checkGrace is added to CheckTimeout per check for the work around it.
const checkGrace = 10 * time.Second

// runDeadline bounds one probe container with n checks. It is the backstop
// for images without timeout(1) and for checks that ignore SIGTERM. Tests
// replace it.
var runDeadline = func(n int) time.Duration {
	return containerOverhead + time.Duration(n)*(CheckTimeout+checkGrace)
}

// runProbe runs the probe script for reqs in one container of ref and
// returns the parsed facts and checks by key. A non-zero container exit is
// accepted when the output is complete. When the container is stopped at
// its deadline, limit is that deadline: without requireAll the checks that
// finished are returned, with requireAll the run fails with
// engine.ErrRunTimeout naming the check that did not finish.
func runProbe(ctx context.Context, eng engine.Engine, ref, platform string, reqs []Request, o runOptions) (facts *Facts, checks map[string]rawCheck, limit time.Duration, err error) {
	deadline := runDeadline(len(reqs))
	stdout, stderr, runErr := engine.RunWithin(ctx, eng, engine.RunSpec{
		Image:      ref,
		Platform:   platform,
		Entrypoint: "/bin/sh",
		Script:     Script(reqs, ScriptOptions{Facts: o.facts}),
	}, deadline)
	expired := errors.Is(runErr, engine.ErrRunTimeout)
	if expired {
		limit = deadline
		// A stopped container may end in the middle of a line.
		stdout = stdout[:bytes.LastIndexByte(stdout, '\n')+1]
	}
	facts, raw, err := Parse(stdout)
	if expired && !o.requireAll && errors.Is(err, errNoProbeLines) {
		// No check finished before the deadline.
		err = nil
	}
	if err == nil && o.facts && facts == nil {
		err = errors.New("probe output has no image facts")
	}
	checks = make(map[string]rawCheck, len(raw))
	for _, c := range raw {
		checks[c.Key] = c
	}
	for _, r := range reqs {
		if _, ok := checks[r.Key]; !ok && o.requireAll && err == nil {
			if expired {
				err = fmt.Errorf("the check of %s did not finish", r.Key)
			} else {
				err = fmt.Errorf("probe output has no check line for %s", r.Key)
			}
		}
	}
	if err == nil {
		return facts, checks, limit, nil
	}
	if runErr != nil {
		err = fmt.Errorf("run probe container for %s: %w (%v)", ref, runErr, err)
	} else {
		err = fmt.Errorf("probe %s: %w", ref, err)
	}
	// Exec already carries the engine's stderr in runErr; do not repeat it.
	full := strings.TrimSpace(string(stderr))
	if detail := tail(full, maxStderr); detail != "" && (runErr == nil || !strings.Contains(runErr.Error(), full)) {
		err = fmt.Errorf("%w; stderr: %s", err, detail)
	}
	return nil, nil, 0, err
}

// tail returns the last n bytes of s, marked as cut when shortened.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
