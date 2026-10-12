package pipeline

import "testing"

func TestResolvePush(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name   string
		flag   *bool
		target bool
		want   bool
	}{
		{"flag unset, target off", nil, false, false},
		{"flag unset, target on", nil, true, true},
		{"--push, target off", &yes, false, true},
		{"--push=false, target on", &no, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePush(tt.flag, tt.target); got != tt.want {
				t.Errorf("resolvePush = %v, want %v", got, tt.want)
			}
		})
	}
}
