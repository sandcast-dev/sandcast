package version

// Version is the sandcast binary version. Release builds set it through
// -ldflags "-X github.com/rhuss/sandcast/internal/version.Version=<v>".
var Version = "dev"

// Pinned upstream releases. Changing a pin requires a CHANGELOG entry
// (constitution III).
const (
	// OpenShellPin is the OpenShell release the vendored profiles, the lint
	// rules and the default policy come from.
	OpenShellPin = "v0.1.3"
	// OpenShellCommit is the commit of OpenShellPin.
	OpenShellCommit = "e1f3c82caa3ed3b65de22889ae7ef32a774878ef"
	// MisePin is the mise release installed inside images.
	MisePin = "v2026.10.4"
	// MiseReleaseURL is the download location of the pinned mise binaries.
	MiseReleaseURL = "https://github.com/jdx/mise/releases/download/" + MisePin
	// MiseMinHost is the oldest host mise that writes lockfile v3 with
	// per-platform checksums.
	MiseMinHost = "2026.9.16"
	// ComponentRegistryPin is the default release tag of the remote
	// component registry.
	ComponentRegistryPin = "v0.1.0"
	// ComponentRegistryURL is the raw base URL of the remote component
	// registry; the tag is appended as a path element.
	ComponentRegistryURL = "https://raw.githubusercontent.com/rhuss/sandcast-components"
)

// MiseSHA256 holds the sha256 of the static (musl) mise release binary per
// mise platform for MisePin, from the release's SHASUMS256.txt. Images verify
// the downloaded binary against it, so no unpinned installer script runs.
var MiseSHA256 = map[string]string{
	"linux-x64":   "46ecfd7d391a22d442a650c4a7d717b3d35a01d6c3b8f37808f71abbcaa855ab",
	"linux-arm64": "7f92c8a883a489b31ef09862e9e198cc1b80ab32a67efebc9ae75146b7ea0e4c",
}

// MiseAsset returns the release asset name of the static mise binary for a
// mise platform.
func MiseAsset(platform string) string { return "mise-" + MisePin + "-" + platform + "-musl" }

// Schema versions this binary reads and writes.
const (
	ManifestSchemaVersion = 1
	PlanSchemaVersion     = 1
)
