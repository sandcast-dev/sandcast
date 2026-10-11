package engine

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lookFor(available ...string) LookPath {
	return func(file string) (string, error) {
		for _, a := range available {
			if a == file {
				return "/usr/bin/" + file, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name       string
		flag, man  string
		available  []string
		wantName   string
		wantSource string
		wantNotice bool
		wantErr    string
	}{
		{name: "flag wins", flag: Docker, man: Podman, available: []string{Podman, Docker}, wantName: Docker, wantSource: SourceFlag},
		{name: "manifest", man: Docker, available: []string{Podman, Docker}, wantName: Docker, wantSource: SourceManifest},
		{name: "podman detected", available: []string{Podman, Docker}, wantName: Podman, wantSource: SourceDetected},
		{name: "docker fallback", available: []string{Docker}, wantName: Docker, wantSource: SourceFallback, wantNotice: true},
		{name: "nothing", wantErr: "no container engine"},
		{name: "flag missing binary", flag: Podman, available: []string{Docker}, wantErr: "not on PATH"},
		{name: "bad value", flag: "nerdctl", available: []string{Podman}, wantErr: "unsupported engine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel, err := Detect(tt.flag, tt.man, lookFor(tt.available...))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, sel.Name)
			assert.Equal(t, tt.wantSource, sel.Source)
			assert.Equal(t, tt.wantNotice, sel.Notice != "")
		})
	}
}
