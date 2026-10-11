package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/version"
)

// registryFiles serves a remote component registry: path -> body.
type registryFiles struct {
	mu    sync.Mutex
	files map[string]string
}

func (r *registryFiles) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	body, ok := r.files[req.URL.Path]
	r.mu.Unlock()
	if !ok {
		http.NotFound(w, req)
		return
	}
	_, _ = w.Write([]byte(body))
}

func (r *registryFiles) set(path, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[path] = body
}

func TestComponentsRefresh(t *testing.T) {
	tag := version.ComponentRegistryPin
	regtool := "key: regtool\nname: regtool\ninstaller: {mise: {tool: regtool}}\ncheck: {command: regtool --version}\n"
	jq := "key: jq\nname: jq (registry)\ninstaller: {mise: {tool: \"aqua:jqlang/jq\"}}\ncheck: {command: jq --version}\n"
	sum := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	srv := &registryFiles{files: map[string]string{
		"/" + tag + "/index.yaml": fmt.Sprintf("schema_version: 1\nrelease: %s\nfiles:\n  - {path: jq.yaml, sha256: %s}\n  - {path: regtool.yaml, sha256: %s}\n",
			tag, sum(jq), sum(regtool)),
		"/" + tag + "/jq.yaml":      jq,
		"/" + tag + "/regtool.yaml": regtool,
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ta := newTestApp(t)
	ta.project(t)
	home := t.TempDir()
	ta.HTTPClient = ts.Client()
	ta.Getenv = func(k string) string {
		switch k {
		case "SANDCAST_COMPONENT_REGISTRY_URL":
			return ts.URL
		case "HOME":
			return home
		}
		return ""
	}

	require.Equal(t, 0, ta.run("components", "refresh"), ta.stderr.String())
	assert.Contains(t, ta.stdout.String(), "registry "+tag+": 2 accepted, 0 rejected")
	tiers := componentTiers(t, ta)
	assert.Equal(t, componentRow{Key: "regtool", Name: "regtool", Tier: "registry", Installer: "mise", SourceFile: "registry/" + tag + "/regtool.yaml", Profiles: []string{}}, tiers["regtool"])
	assert.Equal(t, "registry", tiers["jq"].Tier, "the registry tier overrides the shipped jq")
	assert.Equal(t, "shipped", tiers["go"].Tier)

	// A tampered file is rejected, the previous copy stays in use, and the
	// next plan says so.
	srv.set("/"+tag+"/regtool.yaml", strings.Replace(regtool, "tool: regtool", "tool: evil", 1))
	assert.Equal(t, ExitFailure, ta.run("components", "refresh"))
	var rejectedRegtool, rejectedJq bool
	for _, line := range strings.Split(ta.stdout.String(), "\n") {
		if strings.Contains(line, "rejected") {
			rejectedRegtool = rejectedRegtool || strings.Contains(line, "regtool.yaml")
			rejectedJq = rejectedJq || strings.Contains(line, "jq.yaml")
		}
	}
	assert.True(t, rejectedRegtool, "a rejected line names regtool.yaml:\n%s", ta.stdout.String())
	assert.False(t, rejectedJq, "jq.yaml is not rejected:\n%s", ta.stdout.String())
	tiers = componentTiers(t, ta)
	assert.Equal(t, "registry", tiers["regtool"].Tier, "the previous copy is kept")

	// The JSON report exits 1 the same way and carries the reason.
	assert.Equal(t, ExitFailure, ta.run("components", "refresh", "--json"))
	var rep component.RefreshReport
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rep), ta.stdout.String())
	assert.Equal(t, []string{"regtool.yaml"}, rep.Rejected)
	assert.Equal(t, "checksum mismatch", rep.Reasons["regtool.yaml"])
	assert.True(t, rep.Installed)

	// A file that cannot be downloaded leaves the cache unchanged.
	srv.set("/"+tag+"/regtool.yaml", regtool)
	srv.mu.Lock()
	delete(srv.files, "/"+tag+"/jq.yaml")
	srv.mu.Unlock()
	assert.Equal(t, ExitFailure, ta.run("components", "refresh"))
	assert.Contains(t, ta.stdout.String(), "rejected: jq.yaml (download failed")
	assert.NotContains(t, ta.stdout.String(), "accepted", "nothing was installed")
	assert.Contains(t, ta.stderr.String(), "left unchanged")
	assert.Equal(t, "registry", componentTiers(t, ta)["jq"].Tier, "the cached jq stays in use")

	require.Equal(t, 0, ta.run("plan", "--json"), ta.stderr.String())
	var p plan.Plan
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &p))
	var notice string
	for _, n := range p.Notices {
		if strings.Contains(n, "regtool.yaml") {
			notice = n
		}
	}
	assert.Contains(t, notice, "rejected", "plan notices: %v", p.Notices)
}

func componentTiers(t *testing.T, ta *testApp) map[string]componentRow {
	t.Helper()
	require.Equal(t, 0, ta.run("components", "list", "--json"), ta.stderr.String())
	var rows []componentRow
	require.NoError(t, json.Unmarshal(ta.stdout.Bytes(), &rows))
	out := map[string]componentRow{}
	for _, r := range rows {
		out[r.Key] = r
	}
	return out
}
