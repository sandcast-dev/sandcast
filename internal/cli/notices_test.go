package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/version"
)

// planJSON runs plan --json and decodes the plan.
func planJSON(t *testing.T, ta *testApp) *plan.Plan {
	t.Helper()
	require.Equal(t, 0, ta.run("plan", "--json"), ta.stderr.String())
	var p plan.Plan
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &p))
	return &p
}

// noticeWith returns the first notice that starts with prefix, or "".
func noticeWith(notices []string, prefix string) string {
	for _, n := range notices {
		if strings.HasPrefix(n, prefix) {
			return n
		}
	}
	return ""
}

func TestPlanNoticesProjectOverrides(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	shippedGo, err := os.ReadFile(filepath.Join("..", "..", "components", "go.yaml"))
	require.NoError(t, err)
	ta.write(t, ".sandcast/components/go.yaml", strings.Replace(string(shippedGo), "name: Go\n", "name: Go (project)\n", 1))
	upstreamGithub, err := os.ReadFile(filepath.Join("..", "..", "profiles", "upstream", version.OpenShellPin, "github.yaml"))
	require.NoError(t, err)
	ta.write(t, ".sandcast/profiles/github.yaml", string(upstreamGithub))

	p := planJSON(t, ta)
	comp := noticeWith(p.Notices, "component go: ")
	assert.Contains(t, comp, ".sandcast/components/go.yaml (project tier) overrides", "notices: %v", p.Notices)
	assert.Contains(t, comp, "shipped tier")
	prof := noticeWith(p.Notices, "profile github: ")
	assert.Contains(t, prof, ".sandcast/profiles/github.yaml overrides", "notices: %v", p.Notices)
	assert.Contains(t, prof, "github.yaml")

	for _, c := range p.Components {
		if c.Key == "go" {
			assert.Equal(t, ".sandcast/components/go.yaml", c.SourceFile)
			assert.Equal(t, "project", c.Tier)
		}
	}
	for _, pr := range p.Profiles {
		if pr.ID == "github" {
			assert.Equal(t, ".sandcast/profiles/github.yaml", pr.SourceFile)
			assert.Equal(t, "project", pr.Tier)
		}
	}
	assert.Empty(t, noticeWith(p.Notices, "profile gitlab: "), "profiles without a project copy have no notice")
}

func TestPlanPullFailureWithoutLocalImage(t *testing.T) {
	ta := newTestApp(t)
	ta.project(t)
	ta.eng.PullErr = map[string]error{communityBase: errors.New("dial tcp ghcr.io:443: i/o timeout")}

	code := ta.run("plan", "--json")
	assert.NotEqual(t, 0, code)
	stderr := ta.stderr.String()
	assert.Contains(t, stderr, "nvidia-community-base")
	assert.Contains(t, stderr, communityBase)
	assert.Contains(t, stderr, "dial tcp ghcr.io:443: i/o timeout")
	assert.Empty(t, ta.stdout.String(), "no plan document")
	_, err := os.Stat(filepath.Join(ta.dir, ".sandcast", "build", "openshell", "plan.yaml"))
	assert.True(t, os.IsNotExist(err), "no plan.yaml written")
	assert.Zero(t, ta.eng.CountOp("run"), "no probe container")
}

func TestPlanCustomBaseImageNotice(t *testing.T) {
	const custom = "quay.io/example/custom:1"
	ta := newTestApp(t)
	ta.project(t)
	ta.write(t, ".sandcast/manifest.yaml", strings.Replace(testManifest, "base_image: nvidia-community-base\n", "base_image: "+custom+"\n", 1))
	digest := "sha256:" + strings.Repeat("ab", 32)
	ta.eng.Digests[custom] = digest

	p := planJSON(t, ta)
	assert.Contains(t, p.Notices, `base image "`+custom+`" is not in the registry; no registry notes apply`)
	assert.Nil(t, p.BaseImage.RegistryEntry)
	assert.Equal(t, custom, p.BaseImage.Reference)
	assert.Equal(t, digest, p.BaseImage.Digest)
}
