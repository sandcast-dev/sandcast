package version

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPinsAreSemverLike(t *testing.T) {
	semver := regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)
	for name, pin := range map[string]string{
		"OpenShellPin":         OpenShellPin,
		"MisePin":              MisePin,
		"MiseMinHost":          MiseMinHost,
		"ComponentRegistryPin": ComponentRegistryPin,
	} {
		assert.NotEmpty(t, pin, name)
		assert.Regexp(t, semver, pin, name)
	}
}

func TestSchemaVersions(t *testing.T) {
	assert.Equal(t, 1, ManifestSchemaVersion)
	assert.Equal(t, 1, PlanSchemaVersion)
	assert.Len(t, OpenShellCommit, 40)
	assert.NotEmpty(t, Version)
}

func TestMiseChecksums(t *testing.T) {
	for _, platform := range []string{"linux-x64", "linux-arm64"} {
		assert.Regexp(t, `^[a-f0-9]{64}$`, MiseSHA256[platform], platform)
	}
	assert.Equal(t, "mise-v2026.10.4-linux-arm64-musl", MiseAsset("linux-arm64"))
}
