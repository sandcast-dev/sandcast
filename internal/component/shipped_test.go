package component

import (
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/profile"
)

// shippedKeys are the components under components/ (T022).
var shippedKeys = []string{
	"bash", "bat", "ca-certificates", "claude-code", "codex", "curl", "fzf",
	"gh", "git", "go", "iproute2", "jq", "lsd", "nftables", "node", "opencode",
	"pnpm", "python", "ripgrep", "rust", "starship", "uv", "yq", "zoxide", "zsh",
}

// knownProfileIDs returns the ids of the embedded upstream and overlay
// profiles (T023).
func knownProfileIDs(t *testing.T) []string {
	t.Helper()
	set, _, err := profile.Resolve(sandcast.UpstreamProfiles, sandcast.OverlayProfiles, nil)
	require.NoError(t, err)
	return set.IDs()
}

// alwaysKeys are the components every image gets; openshellOnly are the ones
// limited to the OpenShell target (R-10).
var (
	alwaysKeys    = []string{"ca-certificates", "curl", "git", "iproute2", "nftables"}
	openshellOnly = []string{"iproute2", "nftables"}
)

func loadShipped(t *testing.T) []*Component {
	t.Helper()
	comps, err := LoadDir(sandcast.Components, TierShipped, sandcast.ComponentsPrefix)
	require.NoError(t, err)
	return comps
}

func TestShippedComponentKeys(t *testing.T) {
	comps := loadShipped(t)
	keys := make([]string, 0, len(comps))
	for _, c := range comps {
		keys = append(keys, c.Key)
	}
	assert.ElementsMatch(t, shippedKeys, keys)
}

func TestShippedComponentsValid(t *testing.T) {
	ids := knownProfileIDs(t)
	assert.Len(t, ids, 22, "17 upstream plus 5 overlay profiles")
	for _, c := range loadShipped(t) {
		t.Run(c.Key, func(t *testing.T) {
			assert.Equal(t, path.Join("components", c.Key+".yaml"), c.SourceFile, "file name must be <key>.yaml")
			assert.NotEmpty(t, strings.TrimSpace(c.Check.Command), "check.command")
			require.NotNil(t, c.Match, "match")
			assert.Equal(t, slices.Contains(alwaysKeys, c.Key), c.Match.Always, "match.always")
			if slices.Contains(openshellOnly, c.Key) {
				assert.Equal(t, []string{"openshell"}, c.Targets)
			} else {
				assert.Empty(t, c.Targets)
			}
			for _, p := range c.Profiles {
				assert.Contains(t, ids, p, "profile id must exist in profiles/upstream/v0.1.3 or profiles/overlay")
			}
			switch c.Kind() {
			case KindMise:
				assert.NotEmpty(t, c.Installer.Mise.Bins, "installer.mise.bins")
			case KindPackage:
				for _, pm := range PackageManagers {
					assert.NotEmpty(t, c.Installer.Package.Packages[pm], "packages for %s", pm)
				}
			default:
				t.Errorf("shipped components use mise or package installers, got %q", c.Kind())
			}
			// The Kubernetes driver mounts the workspace over /sandbox (R-10).
			if c.Image != nil {
				var paths []string
				paths = append(paths, c.Image.ReadOnly...)
				paths = append(paths, c.Image.ReadWrite...)
				paths = append(paths, c.Image.RuntimeGlobs...)
				for _, d := range c.Image.Dirs {
					paths = append(paths, d.Path)
				}
				for _, v := range c.Image.Env {
					paths = append(paths, v)
				}
				for _, p := range paths {
					assert.False(t, p == "/sandbox" || strings.HasPrefix(p, "/sandbox/"), "%s is under /sandbox", p)
				}
			}
		})
	}
}

// writableGlobExceptions are shipped components whose runtime globs lie
// under a directory they create owned by the final user, so the build flags
// their profiles by design.
var writableGlobExceptions = map[string]string{
	// rustup installs the toolchains into RUSTUP_HOME (/opt/rustup) during
	// the mise install, which runs as the final user, so that user owns the
	// toolchain binaries; the image policy lists /opt/rustup read-only.
	"rust": "/opt/rustup",
}

// underRoot reports whether glob, an absolute path or glob, can name root or
// a path below it (the rule of build.WritableBinaries).
func underRoot(glob, root string) bool {
	gs := strings.Split(strings.Trim(path.Clean(glob), "/"), "/")
	for i, seg := range strings.Split(strings.Trim(path.Clean(root), "/"), "/") {
		if i >= len(gs) {
			return false
		}
		if strings.Contains(gs[i], "**") {
			return true
		}
		if ok, err := path.Match(gs[i], seg); !ok && err == nil {
			return false
		}
	}
	return true
}

// TestShippedRuntimeGlobsNotWritable: a runtime glob grants the profile's
// network access to every binary it matches, so no shipped component may
// point one into a path the sandbox can write: its own read_write paths or
// the directories it creates owned by the final user.
func TestShippedRuntimeGlobsNotWritable(t *testing.T) {
	assert.True(t, underRoot("/opt/rustup/toolchains/*/bin/*", "/opt/rustup"))
	assert.True(t, underRoot("/opt/*/bin/x", "/opt/go"))
	assert.False(t, underRoot("/opt/gopher/bin/x", "/opt/go"))
	for _, c := range loadShipped(t) {
		if c.Image == nil {
			continue
		}
		t.Run(c.Key, func(t *testing.T) {
			roots := slices.Clone(c.Image.ReadWrite)
			for _, d := range c.Image.Dirs {
				if d.OwnerOrDefault() != "root" {
					roots = append(roots, d.Path)
				}
			}
			var writable []string
			for _, g := range c.Image.RuntimeGlobs {
				for _, root := range roots {
					if underRoot(g, root) {
						writable = append(writable, root)
					}
				}
			}
			if exc, ok := writableGlobExceptions[c.Key]; ok {
				assert.Equal(t, []string{exc}, slices.Compact(writable), "known exception, update writableGlobExceptions")
				return
			}
			assert.Empty(t, writable, "runtime globs of %s lie under writable paths", c.Key)
		})
	}
}

// TestShippedGoPinsToolchain: without GOTOOLCHAIN=local the go command
// downloads toolchains into the writable module cache, where a binary would
// get the go-proxy egress.
func TestShippedGoPinsToolchain(t *testing.T) {
	for _, c := range loadShipped(t) {
		if c.Key == "go" {
			require.NotNil(t, c.Image)
			assert.Equal(t, "local", c.Image.Env["GOTOOLCHAIN"])
			return
		}
	}
	t.Fatal("no shipped go component")
}

func TestShippedComponentsNoConflicts(t *testing.T) {
	assert.Empty(t, Conflicts(loadShipped(t)))
}

func TestShippedEmulationUnsafe(t *testing.T) {
	comps, err := LoadDir(sandcast.Components, TierShipped, sandcast.ComponentsPrefix)
	require.NoError(t, err)
	var unsafe []string
	for _, c := range comps {
		if c.EmulationUnsafe {
			unsafe = append(unsafe, c.Key)
		}
	}
	assert.Equal(t, []string{"rust"}, unsafe, "only rust is declared emulation unsafe")
}
