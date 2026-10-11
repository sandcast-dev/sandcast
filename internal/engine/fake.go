package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"sort"
	"sync"
)

// Call is one recorded Fake operation.
type Call struct {
	// Op is the method name in lower case (pull, digest, run, build, ...).
	Op string
	// Args are the main arguments (reference, platform, tags).
	Args []string
	// Run is set for run calls.
	Run *RunSpec
	// Build is set for build calls.
	Build *BuildSpec
}

// Fake is a scripted Engine for tests. Zero values give sensible defaults:
// every pull succeeds, digests come from Digests, runs call RunFunc.
type Fake struct {
	mu sync.Mutex

	// EngineName is returned by Name; defaults to podman.
	EngineName string
	// Digests maps a reference to its digest.
	Digests map[string]string
	// PullErr fails Pull for the listed references.
	PullErr map[string]error
	// RunFunc answers Run calls.
	RunFunc func(spec RunSpec) (stdout, stderr []byte, err error)
	// BuildFunc answers Build calls; it may write to spec.Log.
	BuildFunc func(spec BuildSpec) error
	// Images is the set of existing image names.
	Images map[string]bool
	// Platforms maps a reference to its os/arch; defaults to the last pulled
	// platform or linux/arm64.
	Platforms map[string]string
	// ImageLabels maps a reference to its labels.
	ImageLabels map[string]map[string]string

	calls []Call
}

// Calls returns a copy of the recorded calls.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// CountOp returns how many calls of op were recorded.
func (f *Fake) CountOp(op string) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Op == op {
			n++
		}
	}
	return n
}

func (f *Fake) record(c Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *Fake) images() map[string]bool {
	if f.Images == nil {
		f.Images = map[string]bool{}
	}
	return f.Images
}

// Name implements Engine.
func (f *Fake) Name() string {
	if f.EngineName == "" {
		return Podman
	}
	return f.EngineName
}

// Every method records its call and then, like exec.CommandContext, fails
// with the context error when the context is already cancelled, so tests see
// the same behavior as the real engine after an interrupt.

// Pull implements Engine.
func (f *Fake) Pull(ctx context.Context, ref, platform string) error {
	f.record(Call{Op: "pull", Args: []string{ref, platform}})
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.PullErr[ref]; err != nil {
		return err
	}
	f.mu.Lock()
	f.images()[ref] = true
	if platform != "" {
		if f.Platforms == nil {
			f.Platforms = map[string]string{}
		}
		f.Platforms[ref] = platform
	}
	f.mu.Unlock()
	return nil
}

// ImagePlatform implements Engine.
func (f *Fake) ImagePlatform(ctx context.Context, ref string) (string, error) {
	f.record(Call{Op: "platform", Args: []string{ref}})
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images()[ref] {
		return "", fmt.Errorf("fake engine: no image %s", ref)
	}
	if p := f.Platforms[ref]; p != "" {
		return p, nil
	}
	return "linux/arm64", nil
}

// Digest implements Engine.
func (f *Fake) Digest(ctx context.Context, ref string) (string, error) {
	f.record(Call{Op: "digest", Args: []string{ref}})
	if err := ctx.Err(); err != nil {
		return "", err
	}
	d, ok := f.Digests[ref]
	if !ok || d == "" {
		return "", ErrNoDigest
	}
	return d, nil
}

// ImageID implements Engine.
func (f *Fake) ImageID(ctx context.Context, ref string) (string, error) {
	f.record(Call{Op: "imageid", Args: []string{ref}})
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(ref))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Run implements Engine.
func (f *Fake) Run(ctx context.Context, spec RunSpec) ([]byte, []byte, error) {
	s := spec
	f.record(Call{Op: "run", Args: []string{spec.Image}, Run: &s})
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if f.RunFunc == nil {
		return nil, nil, fmt.Errorf("fake engine: unexpected run of %s", spec.Image)
	}
	return f.RunFunc(spec)
}

// Build implements Engine.
func (f *Fake) Build(ctx context.Context, spec BuildSpec) error {
	s := spec
	f.record(Call{Op: "build", Args: append([]string(nil), spec.Tags...), Build: &s})
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.BuildFunc != nil {
		if err := f.BuildFunc(spec); err != nil {
			return err
		}
	} else if spec.Log != nil {
		_, _ = io.WriteString(spec.Log, "fake build ok\n")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range spec.Tags {
		f.images()[t] = true
		if len(spec.Labels) > 0 {
			if f.ImageLabels == nil {
				f.ImageLabels = map[string]map[string]string{}
			}
			f.ImageLabels[t] = spec.Labels
		}
	}
	return nil
}

// Tag implements Engine.
func (f *Fake) Tag(ctx context.Context, src, dst string) error {
	f.record(Call{Op: "tag", Args: []string{src, dst}})
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images()[dst] = true
	return nil
}

// RemoveImage implements Engine.
func (f *Fake) RemoveImage(ctx context.Context, ref string) error {
	f.record(Call{Op: "rmi", Args: []string{ref}})
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.images(), ref)
	return nil
}

// ImageExists implements Engine.
func (f *Fake) ImageExists(ctx context.Context, ref string) (bool, error) {
	f.record(Call{Op: "exists", Args: []string{ref}})
	if err := ctx.Err(); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images()[ref], nil
}

// Labels implements Engine.
func (f *Fake) Labels(ctx context.Context, ref string) (map[string]string, error) {
	f.record(Call{Op: "labels", Args: []string{ref}})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	maps.Copy(out, f.ImageLabels[ref])
	return out, nil
}

// Push implements Engine.
func (f *Fake) Push(ctx context.Context, ref string) error {
	f.record(Call{Op: "push", Args: []string{ref}})
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// ImageNames returns the sorted image names the fake knows.
func (f *Fake) ImageNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.images()))
	for k := range f.images() {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ Engine = (*Fake)(nil)
var _ Engine = (*Exec)(nil)
