package mise

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatform(t *testing.T) {
	tests := []struct {
		target  string
		want    string
		wantErr bool
	}{
		{target: "linux/amd64", want: "linux-x64"},
		{target: "linux/arm64", want: "linux-arm64"},
		{target: "linux/386", wantErr: true},
		{target: "darwin/arm64", wantErr: true},
		{target: "linux-x64", wantErr: true},
		{target: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got, err := Platform(tt.target)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported target platform")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)

			back, err := Target(got)
			require.NoError(t, err)
			assert.Equal(t, tt.target, back)
		})
	}
}

func TestTargetRejectsUnknownPlatform(t *testing.T) {
	_, err := Target("macos-arm64")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported mise platform")
}

func TestHostTarget(t *testing.T) {
	assert.Equal(t, "linux/"+runtime.GOARCH, HostTarget())
	if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
		_, err := Platform(HostTarget())
		assert.NoError(t, err)
	}
}
