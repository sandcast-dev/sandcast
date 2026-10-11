package mise

import (
	"fmt"
	"runtime"
)

// Supported build targets and the mise platform names they map to (R-04).
var targetToPlatform = map[string]string{
	"linux/amd64": "linux-x64",
	"linux/arm64": "linux-arm64",
}

// Platform maps a build target ("linux/amd64", "linux/arm64") to the mise
// lockfile platform name ("linux-x64", "linux-arm64").
func Platform(target string) (string, error) {
	p, ok := targetToPlatform[target]
	if !ok {
		return "", fmt.Errorf("unsupported target platform %q: want linux/amd64 or linux/arm64", target)
	}
	return p, nil
}

// Target maps a mise platform name back to its build target, the inverse of
// Platform.
func Target(platform string) (string, error) {
	for t, p := range targetToPlatform {
		if p == platform {
			return t, nil
		}
	}
	return "", fmt.Errorf("unsupported mise platform %q: want linux-x64 or linux-arm64", platform)
}

// HostTarget returns the build target matching the host architecture,
// linux/<GOARCH>. Images are always Linux, whatever the host system is.
func HostTarget() string {
	return "linux/" + runtime.GOARCH
}
