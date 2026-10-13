package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rhuss/sandcast/internal/build"
	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/gateway"
	"github.com/rhuss/sandcast/internal/imageref"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/profile"
)

// IndexFile is the adapted set index name, owned by the profile package.
const IndexFile = profile.IndexFile

// renameFn is os.Rename, indirected so tests can force the swap and its
// rollback in replaceAdapted to fail.
var renameFn = os.Rename

// refuseSymlinkEntry fails when path is a symlink, dangling or not. The adapted
// profile files and the index are read in place, so a committed link such as
// build/<target>/profiles/x.yaml pointing at a host secret would otherwise be
// followed and its contents read. A missing entry is left to the reader.
func refuseSymlinkEntry(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; sandcast reads an adapted profile set in place and never follows a link there, remove it", path)
	}
	return nil
}

// LoadAdapted reads an adapted profile set directory back into the inputs
// of profile.Adapt: the profiles keyed by upstream id, their binaries, and
// the index.
func LoadAdapted(dir string) (profile.Set, map[string][]string, *profile.Index, error) {
	// The directory itself must be real: a committed symlink at
	// build/<target>/profiles would otherwise make every read below follow
	// the link out of the build directory. The cli commands also check the
	// build directory for symlinked entries first, so this is defense in
	// depth for any other caller.
	if err := refuseSymlinkEntry(dir); err != nil {
		return nil, nil, nil, err
	}
	if err := refuseSymlinkEntry(filepath.Join(dir, IndexFile)); err != nil {
		return nil, nil, nil, err
	}
	idx, err := profile.ReadIndex(filepath.Join(dir, IndexFile))
	if err != nil {
		return nil, nil, nil, err
	}
	set := profile.Set{}
	binaries := map[string][]string{}
	for _, ip := range idx.Profiles {
		file := filepath.Join(dir, filepath.Base(ip.File))
		if err := refuseSymlinkEntry(file); err != nil {
			return nil, nil, nil, err
		}
		p, err := profile.LoadFile(file)
		if err != nil {
			return nil, nil, nil, err
		}
		p.ID = ip.UpstreamID
		set[ip.UpstreamID] = profile.Entry{Profile: p, Tier: ip.SourceTier, SourceFile: ip.SourceFile}
		binaries[ip.UpstreamID] = append([]string{}, ip.Binaries...)
	}
	return set, binaries, idx, nil
}

// Readapt adapts the set in dir again with ids suffixed or not (FR-022a),
// keeping binaries, the image reference and the flags the build set (for
// example for binaries under writable paths). It writes nothing: callers
// write the result into a fresh directory or swap it in with
// replaceAdapted, so a reader of a build directory never sees a partial set.
func Readapt(dir string, suffix bool, scope string) (*profile.AdaptedSet, error) {
	set, binaries, idx, err := LoadAdapted(dir)
	if err != nil {
		return nil, err
	}
	adapted, err := profile.Adapt(set, binaries, profile.AdaptOptions{
		Suffix: suffix, ImageLabel: idx.ImageLabel, ScopeHint: scope, Image: idx.Image,
	})
	if err != nil {
		return nil, err
	}
	keepFlags(adapted, idx)
	return adapted, nil
}

// lockAdapted takes the build lock of the build directory that holds the
// adapted set dir (build/<target>/profiles), so the set never changes while
// a build copies it into an image. It fails like a build does while another
// process holds the lock. The callers in internal/cli check the build
// directory for symlinked entries first.
func lockAdapted(dir string) (func(), error) {
	buildDir := filepath.Dir(dir)
	return build.LockTarget(buildDir, filepath.Base(buildDir))
}

// ExportFromImage copies the adapted profile set out of image (from
// /usr/share/sandcast/profiles) into dir without rebuilding. With suffix the
// ids are re-suffixed with the image label. It holds the build lock and
// replaces dir as a whole, so a failure leaves the previous set.
func ExportFromImage(ctx context.Context, eng engine.Engine, image, platform, dir string, suffix bool) (*profile.AdaptedSet, error) {
	unlock, err := lockAdapted(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	script := fmt.Sprintf(`for f in %s/*.yaml; do [ -f "$f" ] || continue; printf '=== %%s\n' "$(basename "$f")"; cat "$f"; printf '\n'; done`, build.ProfilesPath)
	stdout, stderr, err := engine.RunWithin(ctx, eng, engine.RunSpec{Image: image, Platform: platform, Entrypoint: "/bin/sh", Script: script}, engine.HelperTimeout)
	if err != nil {
		return nil, fmt.Errorf("read profiles from %s: %w", image, engine.WithStderr(err, stderr))
	}
	files := splitFiles(string(stdout))
	if _, ok := files[IndexFile]; !ok {
		return nil, fmt.Errorf("%s has no %s/%s; was it built by sandcast?", image, build.ProfilesPath, IndexFile)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if !profileFileName(name) {
			return nil, fmt.Errorf("%s lists %q in %s; each profile file must be a plain file name ending in .yaml; nothing written", image, name, build.ProfilesPath)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	raw, err := os.MkdirTemp("", "sandcast-export-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(raw) }()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(raw, name), []byte(files[name]), 0o644); err != nil {
			return nil, err
		}
	}
	// A profile set read from an arbitrary image is checked before anything
	// is written into the build directory.
	if err := build.RedactPass(raw); err != nil {
		return nil, fmt.Errorf("profiles in image %s (%s), nothing written: %w", image, build.ProfilesPath, err)
	}
	idx, err := profile.ReadIndex(filepath.Join(raw, IndexFile))
	if err != nil {
		return nil, err
	}
	set, err := Readapt(raw, suffix, idx.ScopeHint)
	if err != nil {
		return nil, err
	}
	if err := replaceAdapted(dir, set); err != nil {
		return nil, err
	}
	return set, nil
}

// profileFileName reports whether name, read from an image, is a plain file
// name ending in .yaml, as the export script lists them.
func profileFileName(name string) bool {
	return manifest.IsPlainFileName(name) && strings.HasSuffix(name, ".yaml") && name != ".yaml"
}

// keepFlags copies the flags of idx onto the profiles of set that Adapt did
// not flag itself.
func keepFlags(set *profile.AdaptedSet, idx *profile.Index) {
	reasons := map[string]string{}
	for _, ip := range idx.Profiles {
		if ip.Flagged {
			reasons[ip.UpstreamID] = ip.FlagReason
		}
	}
	for i := range set.Profiles {
		ap := &set.Profiles[i]
		if r, ok := reasons[ap.UpstreamID]; ok && !ap.Flagged {
			ap.Flagged, ap.FlagReason = true, r
		}
	}
	for i := range set.Index.Profiles {
		ip := &set.Index.Profiles[i]
		if r, ok := reasons[ip.UpstreamID]; ok && !ip.Flagged {
			ip.Flagged, ip.FlagReason = true, r
		}
	}
}

func splitFiles(out string) map[string]string {
	files := map[string]string{}
	var name string
	var buf strings.Builder
	flush := func() {
		if name != "" {
			files[name] = strings.TrimRight(buf.String(), "\n") + "\n"
		}
		buf.Reset()
	}
	for _, line := range strings.SplitAfter(out, "\n") {
		if strings.HasPrefix(line, "=== ") {
			flush()
			name = strings.TrimSpace(strings.TrimPrefix(line, "=== "))
			continue
		}
		buf.WriteString(line)
	}
	flush()
	return files
}

// ImportOptions control the gateway import.
type ImportOptions struct {
	Scope     string
	Workspace string
	Suffix    bool
	Update    bool
	DryRun    bool
}

// ImportReport lists what the import did per adapted id.
type ImportReport struct {
	Scope     string   `json:"scope"`
	Imported  []string `json:"imported"`
	Updated   []string `json:"updated"`
	Conflicts []string `json:"conflicts"`
	DryRun    bool     `json:"dry_run"`
	Mapping   []IDMap  `json:"mapping"`
	// Index is the index.yaml that now records the imported ids and scope;
	// empty when the import changed nothing (dry run, conflicts, error).
	Index string `json:"index,omitempty"`
	// Warnings are gateway notices, for example a localhost/ image with a
	// remote active gateway (FR-014). They never fail the import.
	Warnings []string `json:"warnings"`
	// Error is set when the import stopped part way; the lists above then
	// hold what the gateway already accepted.
	Error string `json:"error,omitempty"`
}

// IDMap maps an upstream id to the adapted id that was imported.
type IDMap struct {
	UpstreamID string `json:"upstream_id"`
	AdaptedID  string `json:"adapted_id"`
}

// Import imports the adapted set in dir into the gateway through the
// openshell CLI. Existing ids are never overwritten unless Update is set;
// they are reported as conflicts (FR-023). After an import that placed the
// whole set at the gateway, dir holds the set as imported: its index.yaml
// records each upstream id with the imported id and the scope (FR-022a).
// The image label keeps the ids of the build. Import fails while a build
// holds the build directory.
func Import(ctx context.Context, gw *gateway.Client, dir string, o ImportOptions) (*ImportReport, error) {
	if !o.DryRun {
		unlock, err := lockAdapted(dir)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	if err := gw.CheckVersion(ctx); err != nil {
		return nil, err
	}
	warnings := []string{}
	if idx, err := profile.ReadIndex(filepath.Join(dir, IndexFile)); err == nil && idx.Image.Name != "" {
		image := idx.Image.Name
		if idx.Image.Tag != "" {
			image += ":" + idx.Image.Tag
		}
		if w := GatewayWarning(ctx, gw, image); w != "" {
			warnings = append(warnings, w)
		}
	}
	if o.Scope == "" {
		o.Scope = gateway.ScopeWorkspace
	}
	suffix := o.Suffix || o.Scope == gateway.ScopePlatform
	staging, err := os.MkdirTemp("", "sandcast-import-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	set, err := Readapt(dir, suffix, o.Scope)
	if err != nil {
		return nil, err
	}
	if err := set.WriteDir(staging); err != nil {
		return nil, err
	}
	// Nothing with a credential is sent to a shared gateway, whatever
	// happened to the files after the build.
	if err := build.RedactPass(staging); err != nil {
		return nil, fmt.Errorf("adapted profile set %s (files named by their import ids), nothing imported: %w", dir, err)
	}
	if err := os.Remove(filepath.Join(staging, IndexFile)); err != nil {
		return nil, err
	}
	existing, err := gw.ListProfiles(ctx, o.Scope, o.Workspace)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range existing {
		have[e.ID] = true
	}
	rep := &ImportReport{Scope: o.Scope, DryRun: o.DryRun, Imported: []string{}, Updated: []string{}, Conflicts: []string{}, Mapping: []IDMap{}, Warnings: warnings}
	var fresh []profile.AdaptedProfile
	for _, ap := range set.Profiles {
		rep.Mapping = append(rep.Mapping, IDMap{UpstreamID: ap.UpstreamID, AdaptedID: ap.AdaptedID})
		if !have[ap.AdaptedID] {
			fresh = append(fresh, ap)
			continue
		}
		if !o.Update {
			rep.Conflicts = append(rep.Conflicts, ap.AdaptedID)
			continue
		}
		if !o.DryRun {
			if err := updateProfile(ctx, gw, staging, ap, o); err != nil {
				rep.Error = err.Error()
				return rep, err
			}
		}
		rep.Updated = append(rep.Updated, ap.AdaptedID)
	}
	for _, ap := range fresh {
		if !o.DryRun {
			if err := gw.ImportFile(ctx, filepath.Join(staging, filepath.Base(ap.File)), o.Scope, o.Workspace); err != nil {
				rep.Error = err.Error()
				return rep, err
			}
		}
		rep.Imported = append(rep.Imported, ap.AdaptedID)
	}
	if o.DryRun || len(rep.Conflicts) > 0 {
		return rep, nil
	}
	if err := replaceAdapted(dir, set); err != nil {
		err = fmt.Errorf("profiles imported, but %s still lists the ids of the build: %w", filepath.Join(dir, IndexFile), err)
		rep.Error = err.Error()
		return rep, err
	}
	rep.Index = filepath.Join(dir, IndexFile)
	return rep, nil
}

// replaceAdapted swaps dir for a fresh copy of set. The copy is written in
// a temporary directory next to dir first, so a failure leaves either the
// old or the new set. A missing dir is created.
func replaceAdapted(dir string, set *profile.AdaptedSet) error {
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".profiles-new-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmp)
		}
	}()
	staged := filepath.Join(tmp, filepath.Base(dir))
	if err := set.WriteDir(staged); err != nil {
		return err
	}
	old := filepath.Join(tmp, "previous")
	if err := renameFn(dir, old); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return renameFn(staged, dir)
	}
	if err := renameFn(staged, dir); err != nil {
		if rerr := renameFn(old, dir); rerr != nil {
			keep = true
			return fmt.Errorf("%w; the previous set is at %s", err, old)
		}
		return err
	}
	return nil
}

// updateProfile replaces an existing gateway profile: it reads the current
// resource_version through export and passes it with the new file.
func updateProfile(ctx context.Context, gw *gateway.Client, staging string, ap profile.AdaptedProfile, o ImportOptions) error {
	current, err := gw.Export(ctx, ap.AdaptedID, o.Scope, o.Workspace)
	if err != nil {
		return err
	}
	remote, err := profile.Parse(current, "gateway:"+ap.AdaptedID)
	if err != nil {
		return fmt.Errorf("parse exported profile %s: %w", ap.AdaptedID, err)
	}
	local := *ap.Profile
	local.ResourceVersion = remote.ResourceVersion
	data, err := profile.Marshal(&local)
	if err != nil {
		return err
	}
	file := filepath.Join(staging, "update-"+filepath.Base(ap.File))
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return err
	}
	return gw.Update(ctx, ap.AdaptedID, file, o.Scope, o.Workspace)
}

// GatewayLookupTimeout bounds the best-effort active gateway lookup.
var GatewayLookupTimeout = 5 * time.Second

// GatewayWarning returns a gateway notice when the active gateway is remote
// and image is a localhost/ reference, which only the build engine's own
// storage can resolve (FR-014). The lookup is best effort: a missing CLI, a
// failed lookup or an unknown gateway type yield "", and nothing fails.
func GatewayWarning(ctx context.Context, gw *gateway.Client, image string) string {
	ref, err := imageref.Normalize(image)
	if err != nil || !ref.IsLocalhost() {
		return ""
	}
	// A hanging openshell CLI must not block a build or an import.
	lookupCtx, cancel := context.WithTimeout(ctx, GatewayLookupTimeout)
	defer cancel()
	active, err := gw.ActiveGateway(lookupCtx)
	if err != nil || !active.Remote() {
		return ""
	}
	return plan.Notice(plan.NoticeGateway, plan.NoticeSubjectImage, fmt.Sprintf(
		"the active gateway %s is remote and cannot pull %s, because a localhost reference names this machine (its engine storage or a registry on it); set targets.openshell.image to a registry reference the cluster can reach and targets.openshell.push to true",
		active.Name, ref.String()))
}
