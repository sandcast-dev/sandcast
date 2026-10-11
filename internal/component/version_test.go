package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"go version go1.25.1 linux/arm64":   "1.25.1",
		"starship 1.20.1\nbranch:master":    "1.20.1",
		"jq-1.7.1":                          "1.7.1",
		"Python 3.12.3":                     "3.12.3",
		"2.1.3 (Claude Code)":               "2.1.3",
		"zsh 5.9 (aarch64-unknown-linux)":   "5.9",
		"no version here":                   "",
		"fzf 0.56.3 (devel)\n":              "0.56.3",
		"ripgrep 14.1.1\n\nfeatures:+pcre2": "14.1.1",
	} {
		assert.Equal(t, want, ParseVersion(in), in)
	}
}

func TestSatisfies(t *testing.T) {
	tests := []struct {
		installed, selector string
		want                bool
	}{
		{"1.25.1", "", true},
		{"1.25.1", "latest", true},
		{"", "1.25", false},
		{"1.25.1", "1.25", true},
		{"1.25", "1.25", true},
		{"1.251.0", "1.25", false},
		{"1.24.9", "1.25", false},
		{"1.25.1", "1.25.1", true},
		{"1.25.1", ">=1.24", true},
		{"1.23.0", ">=1.24", false},
		{"1.24.0", ">1.24", false},
		{"1.24.0", "<1.25", true},
		{"1.25.0", "<=1.25", true},
		{"1.30.0", "^1.25", true},
		{"2.0.0", "^1.25", false},
		{"1.25.9", "~1.25.1", true},
		{"1.26.0", "~1.25.1", false},
		{"v1.25.1", "1.25", true},
		{"20.11.0", "lts", true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, Satisfies(tt.installed, tt.selector), "%s vs %s", tt.installed, tt.selector)
	}
}

func TestCompare(t *testing.T) {
	assert.Equal(t, 0, Compare("1.2", "1.2.0"))
	assert.Equal(t, -1, Compare("1.2", "1.10"))
	assert.Equal(t, 1, Compare("2.0.0-rc1", "1.9"))
}
