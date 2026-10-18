package imageref

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	digest := "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		in, want, registry string
		noNamespace        bool
		localhost          bool
	}{
		{in: "ubuntu", want: "docker.io/library/ubuntu:latest", registry: "docker.io", noNamespace: true},
		{in: "ubuntu:24.04", want: "docker.io/library/ubuntu:24.04", registry: "docker.io", noNamespace: true},
		{in: "docker.io/openshell-demo", want: "docker.io/library/openshell-demo:latest", registry: "docker.io", noNamespace: true},
		{in: "docker.io/library/ubuntu", want: "docker.io/library/ubuntu:latest", registry: "docker.io"},
		{in: "docker.io/rhuss/openshell-demo", want: "docker.io/rhuss/openshell-demo:latest", registry: "docker.io"},
		{in: "rhuss/openshell-demo:v1", want: "docker.io/rhuss/openshell-demo:v1", registry: "docker.io"},
		{in: "index.docker.io/rhuss/demo", want: "docker.io/rhuss/demo:latest", registry: "docker.io"},
		{in: "localhost/demo", want: "localhost/demo:latest", registry: "localhost", localhost: true},
		{in: "localhost:5000/demo/pushdemo:1", want: "localhost:5000/demo/pushdemo:1", registry: "localhost:5000", localhost: true},
		{in: "quay.io/example/team-sandbox:2026.10", want: "quay.io/example/team-sandbox:2026.10", registry: "quay.io"},
		{in: "ghcr.io/nvidia/openshell-community/sandboxes/base:latest", want: "ghcr.io/nvidia/openshell-community/sandboxes/base:latest", registry: "ghcr.io"},
		{in: "registry.example.com:8443/a/b@" + digest, want: "registry.example.com:8443/a/b@" + digest, registry: "registry.example.com:8443"},
		{in: "quay.io/a/b:1@" + digest, want: "quay.io/a/b:1@" + digest, registry: "quay.io"},
		{in: "ubuntu@" + digest, want: "docker.io/library/ubuntu@" + digest, registry: "docker.io", noNamespace: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			r, err := Normalize(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, r.String())
			assert.Equal(t, tt.registry, r.Registry)
			assert.Equal(t, tt.noNamespace, r.DockerHubNoNamespace())
			assert.Equal(t, tt.localhost, r.IsLocalhost())
		})
	}
}

func TestNormalizeErrors(t *testing.T) {
	for _, in := range []string{"", "Upper/Case", "a b", "demo:bad tag", "demo@sha256:xyz", "demo:"} {
		_, err := Normalize(in)
		assert.Error(t, err, in)
	}
}
