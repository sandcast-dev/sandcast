package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDomains(t *testing.T) {
	tests := map[string]struct {
		profiles []*Profile
		want     []string
	}{
		"none": {nil, nil},
		"sorted, unique, wildcards verbatim": {
			profiles: []*Profile{
				{Endpoints: []Endpoint{{Host: "pypi.org"}, {Host: "github.com"}, {Host: "*.s3.amazonaws.com"}}},
				nil,
				{Endpoints: []Endpoint{{Host: "github.com"}, {Host: ""}, {Host: "api.github.com"}}},
			},
			want: []string{"*.s3.amazonaws.com", "api.github.com", "github.com", "pypi.org"},
		},
		"profile without endpoints": {
			profiles: []*Profile{{ID: "aws"}},
			want:     nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, Domains(tc.profiles))
		})
	}
}
