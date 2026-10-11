// Package sandcast embeds the assets shipped inside the binary: component
// definitions, vendored and overlay profiles, the pinned default policy, the
// skill bundle and the base image registry.
package sandcast

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/rhuss/sandcast/internal/version"
)

//go:embed components
var componentsFS embed.FS

//go:embed profiles
var profilesFS embed.FS

//go:embed policy
var policyFS embed.FS

//go:embed skills
var skillsFS embed.FS

//go:embed base-images.yaml
var baseImagesFS embed.FS

// Embedded sub-filesystems, each rooted at its directory.
var (
	Components             = mustSub(componentsFS, "components")
	Profiles               = mustSub(profilesFS, "profiles")
	UpstreamProfiles       = mustSub(profilesFS, "profiles/upstream/"+version.OpenShellPin)
	OverlayProfiles        = mustSub(profilesFS, "profiles/overlay")
	Policy                 = mustSub(policyFS, "policy")
	Skills                 = mustSub(skillsFS, "skills")
	BaseImages       fs.FS = baseImagesFS
)

// Display prefixes recorded as source files for embedded tiers.
const (
	ComponentsPrefix       = "components"
	UpstreamProfilesPrefix = "profiles/upstream/" + version.OpenShellPin
	OverlayProfilesPrefix  = "profiles/overlay"
)

// SkillsVersion returns the skill bundle version from skills/VERSION.
func SkillsVersion() string {
	data, err := fs.ReadFile(Skills, "VERSION")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// fs.Sub only fails on an invalid path, which is a programming error
		// caught by embed_test.go.
		return emptyFS{}
	}
	return sub
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
