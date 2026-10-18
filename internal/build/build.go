package build

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/profile"
	"github.com/rhuss/sandcast/internal/redact"
	"github.com/rhuss/sandcast/internal/verify"
	"github.com/rhuss/sandcast/internal/version"
)

// Build directory file names and image tags.
const (
	ContainerfileName      = "Containerfile"
	FinalContainerfileName = "Containerfile.final"
	BuildLogName           = "build.log"
	ProfilesDirName        = "profiles"
	// LockFileName is the build directory lock (see LockDir). It is empty,
	// never copied into an image, and git-ignored with the build directory.
	LockFileName = ".lock"
	// StageTag names a pass-1 image kept with KeepStage. While a build runs,
	// its stage carries a per-invocation tag, StageTag plus a random suffix.
	StageTag = "sandcast-stage"
	DebugTag = "sandcast-debug"
)

// Image labels written in pass 2 (contracts/adapted-profiles.schema.yaml).
const (
	LabelVersion       = "io.sandcast.version"
	LabelManifestName  = "io.sandcast.manifest-name"
	LabelProfilesPath  = "io.sandcast.profiles-path"
	LabelProfilesIndex = "io.sandcast.profiles-index"
)

// ErrBuildFailed marks a failed engine build; the CLI exits 1 and never
// retries (FR-027).
var ErrBuildFailed = errors.New("image build failed")

// ErrBuildRunning means another sandcast process holds the build directory.
var ErrBuildRunning = errors.New("another sandcast build is running")

// Options configure one build.
type Options struct {
	// Dir is build/<target>; plan outputs (plan.yaml, mise/, policy.yaml)
	// are already there.
	Dir string
	// Plan is the plan the image is built from.
	Plan *plan.Plan
	// Engine builds and runs images.
	Engine engine.Engine
	// Image is the image name without tag; Tag defaults to latest.
	Image, Tag string
	// ManifestName is recorded in a label and sanitized into the image
	// label of the adapted profile set.
	ManifestName string
	// Profiles is the resolved profile set (all tiers).
	Profiles profile.Set
	// NoCacheLayers disables the engine layer cache.
	NoCacheLayers bool
	// KeepStage keeps the pass-1 image under <image>:sandcast-stage.
	KeepStage bool
	// Push pushes the final image.
	Push bool
	// Progress receives the engine output while building (may be nil).
	Progress io.Writer
	// AfterResolve runs after the profile set is written, e.g. to write the
	// container target's allowed-domains file.
	AfterResolve func(set *profile.AdaptedSet) error
}

// Result describes a finished build.
type Result struct {
	Image         string                 `json:"image"`
	StageImage    string                 `json:"stage_image,omitempty"`
	Containerfile string                 `json:"containerfile"`
	BuildLog      string                 `json:"build_log"`
	Resolutions   []Resolution           `json:"resolutions"`
	Profiles      *profile.AdaptedSet    `json:"-"`
	Flagged       []profile.IndexProfile `json:"flagged"`
	// Pushed is true once the push of Image succeeded.
	Pushed bool `json:"pushed"`
	// Digest is the registry digest of the pushed manifest, when the engine
	// reported one.
	Digest string `json:"digest,omitempty"`
}

// Ref returns name:tag.
func (o Options) Ref() string {
	tag := tagOrLatest(o.Tag)
	return o.Image + ":" + tag
}

// Run assembles the Containerfile, builds pass 1 under a per-invocation
// stage tag, resolves binary paths, writes the adapted profile set, builds
// pass 2 from that stage with the profile set and the labels under the
// final tag (R-09), and runs the redaction pass. A failure leaves the
// Containerfile and build.log in Dir, keeps a completed stage image as
// <image>:sandcast-debug, and is never retried. The caller holds the build
// lock of Dir (LockTarget) for the whole call; Run does not lock.
func Run(ctx context.Context, o Options) (*Result, error) {
	p := o.Plan
	res := &Result{
		Image:         o.Ref(),
		Containerfile: filepath.Join(o.Dir, ContainerfileName),
		BuildLog:      filepath.Join(o.Dir, BuildLogName),
	}
	text, err := Assemble(p)
	if err != nil {
		return nil, err
	}
	if err := writeContext(o.Dir, p, text); err != nil {
		return nil, err
	}
	// Scan the build context before anything reaches an image (SC-009);
	// outputs of an earlier run are scanned after this build.
	if err := RedactPass(o.Dir, LockFileName, BuildLogName, ProfilesDirName, verify.FileName, FinalContainerfileName); err != nil {
		return res, err
	}
	// build.log is truncated in place, so a symlink there is refused rather
	// than followed: the engine output must never land outside Dir.
	logFile, err := openNoFollow(res.BuildLog, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create build log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	var log io.Writer = logFile
	if o.Progress != nil {
		log = io.MultiWriter(logFile, o.Progress)
	}

	// Pass 2 builds FROM this tag; a tag of its own keeps a concurrent build
	// of the same image from replacing the stage in between.
	stage, err := stageRef(o.Image)
	if err != nil {
		return res, err
	}
	_, _ = fmt.Fprintf(log, "### sandcast pass 1: %s\n", stage)
	if err := o.Engine.Build(ctx, engine.BuildSpec{
		ContextDir: o.Dir, Containerfile: res.Containerfile, Tags: []string{stage},
		Platform: p.Platform.Value, NoCache: o.NoCacheLayers, Log: log,
	}); err != nil {
		return res, fmt.Errorf("%w (pass 1): %w; see %s", ErrBuildFailed, err, res.BuildLog)
	}

	fail := func(err error) (*Result, error) {
		// Keep the stage for debugging even when the build was interrupted.
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		if terr := o.Engine.Tag(cleanup, stage, o.Image+":"+DebugTag); terr != nil {
			// Report the stage tag that still exists rather than a debug
			// tag that was never created.
			res.StageImage = stage
			return res, err
		}
		_ = o.Engine.RemoveImage(cleanup, stage)
		res.StageImage = o.Image + ":" + DebugTag
		return res, err
	}

	resolutions, err := Resolve(ctx, o.Engine, stage, p.Platform.Value, p)
	if err != nil {
		return fail(err)
	}
	res.Resolutions = resolutions
	stageID, err := o.Engine.ImageID(ctx, stage)
	if err != nil {
		return fail(err)
	}
	profiles, binaries, err := withMCPProfiles(p, o.Profiles, resolutions)
	if err != nil {
		return fail(err)
	}
	writable, err := WritableBinaries(p, binaries)
	if err != nil {
		return fail(err)
	}
	set, err := profile.Adapt(profiles, binaries, profile.AdaptOptions{
		ImageLabel: profile.ImageLabel(o.ManifestName),
		Image: profile.ImageRef{
			Name: o.Image, Tag: tagOrLatest(o.Tag), Digest: normalizeID(stageID), Platform: p.Platform.Value,
		},
	})
	if err != nil {
		return fail(err)
	}
	flagWritable(set, writable)
	profilesDir := filepath.Join(o.Dir, ProfilesDirName)
	if err := os.RemoveAll(profilesDir); err != nil {
		return fail(err)
	}
	if err := set.WriteDir(profilesDir); err != nil {
		return fail(err)
	}
	res.Profiles = set
	for _, ip := range set.Index.Profiles {
		if ip.Flagged {
			res.Flagged = append(res.Flagged, ip)
		}
	}
	if o.AfterResolve != nil {
		if err := o.AfterResolve(set); err != nil {
			return fail(err)
		}
	}
	// The adapted profiles and the AfterResolve outputs go into the image in
	// pass 2; check them first. build.log is checked after the build.
	// verify.yaml never enters the image and is checked when written.
	if err := RedactPass(o.Dir, LockFileName, BuildLogName, verify.FileName); err != nil {
		return fail(err)
	}
	labelJSON, err := set.LabelJSON()
	if err != nil {
		return fail(err)
	}
	final := fmt.Sprintf("# Containerfile.final generated by sandcast %s: pass 2 adds the adapted profile set.\nFROM %s\nCOPY %s/ %s/\n",
		version.Version, stage, ProfilesDirName, ProfilesPath)
	finalPath := filepath.Join(o.Dir, FinalContainerfileName)
	if err := fsutil.AtomicWriteFile(finalPath, []byte(final), 0o644); err != nil {
		return fail(err)
	}
	_, _ = fmt.Fprintf(log, "### sandcast pass 2: %s\n", res.Image)
	if err := o.Engine.Build(ctx, engine.BuildSpec{
		ContextDir: o.Dir, Containerfile: finalPath, Tags: []string{res.Image},
		Platform: p.Platform.Value, Log: log,
		Labels: map[string]string{
			LabelVersion:       version.Version,
			LabelManifestName:  o.ManifestName,
			LabelProfilesPath:  ProfilesPath,
			LabelProfilesIndex: labelJSON,
		},
	}); err != nil {
		return fail(fmt.Errorf("%w (pass 2): %w; see %s", ErrBuildFailed, err, res.BuildLog))
	}
	if o.KeepStage {
		res.StageImage = stage
		kept := o.Image + ":" + StageTag
		if err := o.Engine.Tag(ctx, stage, kept); err != nil {
			_, _ = fmt.Fprintf(log, "warning: could not tag %s as %s: %v\n", stage, kept, err)
		} else {
			res.StageImage = kept
		}
	}
	if res.StageImage != stage {
		if err := o.Engine.RemoveImage(ctx, stage); err != nil {
			_, _ = fmt.Fprintf(log, "warning: could not remove %s: %v\n", stage, err)
		}
	}
	// A debug image from an earlier failed build is stale now.
	if ok, _ := o.Engine.ImageExists(ctx, o.Image+":"+DebugTag); ok {
		_ = o.Engine.RemoveImage(ctx, o.Image+":"+DebugTag)
	}
	if err := logFile.Sync(); err != nil {
		return res, err
	}
	if err := RedactPass(o.Dir, LockFileName, verify.FileName); err != nil {
		// Never leave an image that failed the scan under the release tag.
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		if rerr := o.Engine.RemoveImage(cleanup, res.Image); rerr != nil {
			err = errors.Join(err, fmt.Errorf("could not remove %s, which failed the credential scan; remove it before pushing: %w", res.Image, rerr))
		} else {
			err = fmt.Errorf("%w\n%s was removed because it failed the credential scan", err, res.Image)
		}
		if res.StageImage != "" {
			// A kept stage image holds the same content.
			if rerr := o.Engine.RemoveImage(cleanup, res.StageImage); rerr != nil {
				err = errors.Join(err, fmt.Errorf("could not remove the kept stage %s; remove it by hand: %w", res.StageImage, rerr))
			} else {
				res.StageImage = ""
			}
		}
		return res, err
	}
	if o.Push {
		// The push output joins build.log after the credential scan, so it
		// passes through the same replacement first.
		var out bytes.Buffer
		digest, err := o.Engine.Push(ctx, res.Image, &out)
		text, _ := redact.Replace(out.String())
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		_, _ = fmt.Fprintf(log, "### sandcast push: %s\n%s", res.Image, text)
		_ = logFile.Sync()
		if err != nil {
			msg, _ := redact.Replace(err.Error())
			return res, fmt.Errorf("push %s: %s", res.Image, msg)
		}
		res.Pushed, res.Digest = true, digest
	}
	return res, nil
}

// LockDir takes an exclusive lock on dir/.lock without waiting, so that two
// sandcast processes never write one build directory or build one target
// at the same time. It fails with ErrBuildRunning while another process
// holds the lock. The returned function releases it; so does the end of the
// process. The file stays: removing it would let a later process lock a
// new file while an earlier one still holds the old. A symlink at .lock is
// refused, so the lock never creates a file outside dir. Most callers want
// LockTarget, which names the target in the error.
func LockDir(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("build lock: %w", err)
	}
	f, err := openNoFollow(filepath.Join(dir, LockFileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("build lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBuildRunning
		}
		return nil, fmt.Errorf("build lock %s: %w", f.Name(), err)
	}
	return func() { _ = f.Close() }, nil
}

// LockTarget takes the build lock of dir (LockDir) for target. While another
// process holds it, the error wraps ErrBuildRunning and names the target and
// the directory. Every sandcast command that writes a build directory locks
// it through this function.
func LockTarget(dir, target string) (func(), error) {
	unlock, err := LockDir(dir)
	if errors.Is(err, ErrBuildRunning) {
		return nil, fmt.Errorf("%w for target %s in %s; wait for it to finish", ErrBuildRunning, target, dir)
	}
	return unlock, err
}

// openNoFollow opens path like os.OpenFile but fails when its last element
// is a symlink, dangling or not (O_NOFOLLOW, on linux and darwin). Only the
// two files sandcast opens in place inside a build directory use it, .lock
// and build.log; every other output goes through fsutil.AtomicWriteFile,
// whose rename replaces a symlink instead of following it.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
	if err == nil {
		return f, nil
	}
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; sandcast never follows a link there, remove it", path)
	}
	return nil, err
}

// stageRef returns <image>:sandcast-stage-<random hex>.
func stageRef(image string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("stage tag: %w", err)
	}
	return image + ":" + StageTag + "-" + hex.EncodeToString(b[:]), nil
}

// writableReason starts the flag reason of a profile with binaries that the
// sandbox can replace.
const writableReason = "binaries under paths the sandbox can write (policy read_write or owned by the final user), so any process placed there gets this profile's network access: "

// flagWritable flags every profile of set with entries in writable (see
// WritableBinaries). A profile already flagged keeps its reason.
func flagWritable(set *profile.AdaptedSet, writable map[string][]string) {
	reasons := map[string]string{}
	for i := range set.Profiles {
		ap := &set.Profiles[i]
		if w := writable[ap.UpstreamID]; len(w) > 0 && !ap.Flagged {
			ap.Flagged, ap.FlagReason = true, writableReason+strings.Join(w, ", ")
			reasons[ap.UpstreamID] = ap.FlagReason
		}
	}
	for i := range set.Index.Profiles {
		ip := &set.Index.Profiles[i]
		if r, ok := reasons[ip.UpstreamID]; ok {
			ip.Flagged, ip.FlagReason = true, r
		}
	}
}

// withMCPProfiles adds a generated profile for every harness MCP server
// whose endpoint no referenced profile covers, bound to the agent's resolved
// binaries (FR-039).
func withMCPProfiles(p *plan.Plan, all profile.Set, res []Resolution) (profile.Set, map[string][]string, error) {
	binaries := ProfileBinaries(p, res)
	if len(p.Harnesses) == 0 {
		return all, binaries, nil
	}
	out := profile.Set{}
	for id, e := range all {
		out[id] = e
	}
	for _, h := range p.Harnesses {
		// Only profiles bound to the agent cover its MCP endpoints: a host
		// that another component's profile lists (api.github.com for gh) is
		// still denied to the agent's binary.
		referenced := profile.Set{}
		if c := p.Component(h.Component); c != nil {
			for _, id := range c.Profiles {
				if e, ok := all[id]; ok {
					referenced[id] = e
				}
			}
		}
		var agent []string
		for _, r := range res {
			if r.Component == h.Component && r.Path != "" {
				agent = append(agent, r.Path)
			}
		}
		generated, _, err := profile.MCPProfiles(h.MCPServers, referenced, agent)
		if err != nil {
			return nil, nil, fmt.Errorf("harness %s: %w", h.Name, err)
		}
		for _, g := range generated {
			out[g.ID] = profile.Entry{Profile: g, Tier: profile.TierProject, SourceFile: "generated from harness " + h.Name + " mcp_servers"}
			binaries[g.ID] = append([]string{}, g.Binaries...)
		}
	}
	return out, binaries, nil
}

// writeContext writes the Containerfile, the curated shell configuration,
// the session environment script and the harness files into the build
// context.
func writeContext(dir string, p *plan.Plan, containerfile string) error {
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, ContainerfileName), []byte(containerfile), 0o644); err != nil {
		return err
	}
	shellDir := filepath.Join(dir, ShellContextDir)
	if err := os.RemoveAll(shellDir); err != nil {
		return err
	}
	if p.Shell.RCPath != "" {
		file := filepath.Join(dir, filepath.FromSlash(ShellContextFile(p.Shell.Program)))
		if err := fsutil.AtomicWriteFile(file, []byte(CuratedRC(p)), 0o644); err != nil {
			return err
		}
	}
	if NeedsSessionEnv(p) {
		file := filepath.Join(dir, filepath.FromSlash(SessionEnvContextFile))
		if err := fsutil.AtomicWriteFile(file, []byte(plan.SessionEnvScriptFor(p)), 0o644); err != nil {
			return err
		}
	}
	dotfilesDir := filepath.Join(dir, DotfileContextDir)
	if err := os.RemoveAll(dotfilesDir); err != nil {
		return err
	}
	for _, d := range p.Dotfiles {
		file := filepath.Join(dir, filepath.FromSlash(DotfileContextFile(d.RelocatedTo)))
		if err := fsutil.AtomicWriteFile(file, []byte(d.Content), 0o644); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, HarnessContextDir)); err != nil {
		return err
	}
	for _, h := range p.Harnesses {
		for _, f := range h.Files {
			if err := stageFile(dir, f); err != nil {
				return fmt.Errorf("harness %s: %w", h.Name, err)
			}
		}
	}
	return nil
}

// HarnessContextDir holds harness files in the build context.
const HarnessContextDir = "harness"

// vcsDirs are version control directories that stageFile leaves out: their
// history is compressed, so the credential scan cannot see into it, and the
// agent does not need it at runtime.
var vcsDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, ".jj": true, ".bzr": true, "_darcs": true, "CVS": true}

// stageFile copies a host file or directory into the build context, without
// version control metadata. The target must stay inside the context
// directory.
func stageFile(dir string, f plan.ContextFile) error {
	target := filepath.Join(dir, filepath.FromSlash(f.Path))
	if !fsutil.Within(dir, target) {
		return fmt.Errorf("context path %q leaves the build directory", f.Path)
	}
	fi, err := os.Stat(f.Source)
	if err != nil {
		return fmt.Errorf("stage %s: %w", f.Source, err)
	}
	// Modes are kept (plus read for everyone), so scripts stay executable
	// after COPY.
	if !fi.IsDir() {
		data, err := os.ReadFile(f.Source)
		if err != nil {
			return err
		}
		return fsutil.AtomicWriteFile(target, data, fi.Mode().Perm()|0o444)
	}
	root, err := os.OpenRoot(f.Source)
	if err != nil {
		return fmt.Errorf("stage %s: %w", f.Source, err)
	}
	defer func() { _ = root.Close() }()
	src := root.FS()
	return fs.WalkDir(src, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if vcsDirs[d.Name()] && rel != "." {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(target, filepath.FromSlash(rel)), 0o755)
		}
		// Reading through the root follows symlinks that stay inside the
		// source directory and fails for any that leave it.
		info, err := fs.Stat(src, rel)
		if err != nil {
			return fmt.Errorf("stage %s: %s: %w", f.Source, rel, err)
		}
		if info.IsDir() {
			return fmt.Errorf("stage %s: %s is a symlink to a directory, which is not supported", f.Source, rel)
		}
		data, err := fs.ReadFile(src, rel)
		if err != nil {
			return fmt.Errorf("stage %s: %s: %w", f.Source, rel, err)
		}
		return fsutil.AtomicWriteFile(filepath.Join(target, filepath.FromSlash(rel)), data, info.Mode().Perm()|0o444)
	})
}

// cleanupTimeout bounds image cleanup after a failure or an interrupt.
const cleanupTimeout = 30 * time.Second

// cleanupContext survives the cancellation of ctx, so an interrupted build
// still tags its debug image and removes what it must, but never waits on a
// stuck engine for longer than cleanupTimeout.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

func tagOrLatest(t string) string {
	if t == "" {
		return "latest"
	}
	return t
}

// normalizeID turns an image ID into sha256:<hex>.
func normalizeID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "sha256:") {
		return id
	}
	return "sha256:" + id
}
