package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Engine names.
const (
	Podman = "podman"
	Docker = "docker"
)

// ErrNoDigest is returned by Digest when an image has no registry digest,
// for example because it was built locally and never pushed.
var ErrNoDigest = errors.New("image has no registry digest")

// ErrRunTimeout marks a container run that RunWithin stopped at its limit.
var ErrRunTimeout = errors.New("container did not finish in time")

// HelperTimeout bounds the short helper containers besides the probe:
// binary resolution and the profile export.
const HelperTimeout = 5 * time.Minute

// RunWithin runs spec like eng.Run but stops the container after limit. A
// run that hits the limit fails with ErrRunTimeout and still returns the
// output it produced; a cancelled ctx is reported as is.
func RunWithin(ctx context.Context, eng Engine, spec RunSpec, limit time.Duration) ([]byte, []byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	stdout, stderr, err := eng.Run(runCtx, spec)
	if err != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%w: stopped after %s (%v)", ErrRunTimeout, limit, err)
	}
	return stdout, stderr, err
}

// RunSpec describes one throwaway container run.
type RunSpec struct {
	// Image is the image reference or ID to run.
	Image string
	// Platform is the target platform, for example linux/arm64. Empty means
	// the engine default.
	Platform string
	// Entrypoint overrides the image entrypoint. Defaults to /bin/sh.
	Entrypoint string
	// User runs the container as this user when set.
	User string
	// Script is passed to the entrypoint as "-c <script>".
	Script string
	// Env holds extra environment variables.
	Env map[string]string
}

// BuildSpec describes one image build.
type BuildSpec struct {
	// ContextDir is the build context directory.
	ContextDir string
	// Containerfile is the path to the image definition.
	Containerfile string
	// Tags are the image names applied to the result.
	Tags []string
	// Platform is the target platform.
	Platform string
	// Labels are added to the image.
	Labels map[string]string
	// NoCache disables the layer cache.
	NoCache bool
	// Log receives the combined build output. May be nil.
	Log io.Writer
}

// Engine is the container engine surface sandcast needs. Implementations
// use only the flag subset podman and docker share (R-02).
type Engine interface {
	// Name returns podman or docker.
	Name() string
	// Pull pulls ref for platform.
	Pull(ctx context.Context, ref, platform string) error
	// Digest returns the registry digest (sha256:...) of a pulled image.
	Digest(ctx context.Context, ref string) (string, error)
	// ImagePlatform returns os/arch of the local image ref, e.g. linux/arm64.
	ImagePlatform(ctx context.Context, ref string) (string, error)
	// ImageID returns the local image ID of ref.
	ImageID(ctx context.Context, ref string) (string, error)
	// Run starts a throwaway container and returns its output.
	Run(ctx context.Context, spec RunSpec) (stdout, stderr []byte, err error)
	// Build builds an image.
	Build(ctx context.Context, spec BuildSpec) error
	// Tag adds dst as a name for src.
	Tag(ctx context.Context, src, dst string) error
	// RemoveImage removes the image name ref.
	RemoveImage(ctx context.Context, ref string) error
	// ImageExists reports whether ref exists in local storage.
	ImageExists(ctx context.Context, ref string) (bool, error)
	// Labels returns the image labels of ref.
	Labels(ctx context.Context, ref string) (map[string]string, error)
	// Push pushes ref to its registry.
	Push(ctx context.Context, ref string) error
}

// WithStderr adds the trimmed stderr of a failed engine command to err,
// unless err already carries it (Exec includes stderr in its errors; other
// engines may not).
func WithStderr(err error, stderr []byte) error {
	detail := strings.TrimSpace(string(stderr))
	if err == nil || detail == "" || strings.Contains(err.Error(), detail) {
		return err
	}
	return fmt.Errorf("%w: %s", err, detail)
}
