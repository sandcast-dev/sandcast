package capture

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
)

// DotfileProposal is one host dotfile capture found and offers to bake.
type DotfileProposal struct {
	// Path is the "~/"-prefixed source path written into the manifest.
	Path string `json:"path"`
}

// ProposeDotfiles scans the host home for the allowlist entries
// (manifest.DotfileAllowlist) that exist as a regular file and are safe to
// copy, returning one manifest path per found entry and a notice for each
// entry skipped. It applies the same bounds the pipeline reads the entries
// with (spec 003 R6): the whole path is resolved, so a symlinked parent such
// as ~/.config pointing out of the home is caught as well as a symlinked
// leaf; the resolved file must lie inside the resolved home, must not be a
// credential or history file, and must be a regular file of at most
// manifest.MaxDotfileSize bytes. A proposal is therefore never skipped later
// by the plan. It never fails: a missing entry is skipped silently, anything
// else with a notice, rather than aborting capture (spec 003 Edge Cases).
func ProposeDotfiles(home string) (proposals []DotfileProposal, skipped []string) {
	if home == "" {
		return nil, nil
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, []string{fmt.Sprintf("the home directory %s cannot be resolved (%v); dotfiles were not scanned", home, err)}
	}
	for _, rel := range manifest.DotfileAllowlist {
		if why := proposeDotfile(home, realHome, rel); why != "" {
			if why != notPresent {
				skipped = append(skipped, fmt.Sprintf("~/%s %s; skipped", rel, why))
			}
			continue
		}
		proposals = append(proposals, DotfileProposal{Path: "~/" + rel})
	}
	return proposals, skipped
}

// notPresent marks an allowlist entry that does not exist, the common case,
// which is skipped without a notice.
const notPresent = "is not present"

// proposeDotfile checks one allowlist entry and returns why it is not
// proposed, or "" when it is.
func proposeDotfile(home, realHome, rel string) string {
	src := filepath.Join(home, filepath.FromSlash(rel))
	if _, err := os.Lstat(src); err != nil {
		return notPresent
	}
	if manifest.IsCredentialName(rel) {
		return "looks like a credential file"
	}
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Sprintf("cannot be resolved (%v)", err)
	}
	if !fsutil.Within(realHome, real) {
		return fmt.Sprintf("resolves to %s, outside the home", real)
	}
	if r, err := filepath.Rel(realHome, real); err == nil {
		if why := manifest.DotfileDenied(filepath.ToSlash(r)); why != "" {
			return fmt.Sprintf("resolves to %s, which is not baked because %s", real, why)
		}
	}
	fi, err := os.Stat(real)
	switch {
	case err != nil:
		return fmt.Sprintf("cannot be read (%v)", err)
	case !fi.Mode().IsRegular():
		return "is not a regular file"
	case fi.Size() > manifest.MaxDotfileSize:
		return fmt.Sprintf("is larger than %d KiB", manifest.MaxDotfileSize>>10)
	}
	return ""
}
