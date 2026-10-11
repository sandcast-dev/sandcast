package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLearnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "learnings.yaml")
	list, err := LoadLearnings(path)
	require.NoError(t, err)
	assert.Empty(t, list)

	first := Learning{Attempt: 1, Component: "mytool", Failure: "tar: mytool: Not found in archive", Change: "use the nested bin/ path", Outcome: OutcomeUnfixed, Date: "2026-10-08"}
	require.NoError(t, AppendLearning(path, first))
	second := first
	second.Attempt = 2
	second.Outcome = OutcomeFixed
	second.Failure = "multi\nline failure"
	require.NoError(t, AppendLearning(path, second))

	list, err = LoadLearnings(path)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, first, list[0])
	assert.Equal(t, "multi\nline failure", list[1].Failure)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "# sandcast learnings"))
}

func TestLearningValidation(t *testing.T) {
	good := Learning{Attempt: 1, Component: "x", Failure: "f", Change: "c", Outcome: OutcomeFixed, Date: "2026-10-08"}
	require.NoError(t, good.Validate())
	for name, mutate := range map[string]func(*Learning){
		"attempt": func(l *Learning) { l.Attempt = 0 },
		"key":     func(l *Learning) { l.Component = "Bad Key" },
		"failure": func(l *Learning) { l.Failure = " " },
		"change":  func(l *Learning) { l.Change = "" },
		"outcome": func(l *Learning) { l.Outcome = "maybe" },
		"date":    func(l *Learning) { l.Date = "yesterday" },
		"secret":  func(l *Learning) { l.Failure = "auth failed for ghp_" + strings.Repeat("a", 36) },
	} {
		l := good
		mutate(&l)
		assert.Error(t, l.Validate(), name)
	}
	path := filepath.Join(t.TempDir(), "learnings.yaml")
	require.NoError(t, os.WriteFile(path, []byte("- attempt: 1\n  component: x\n  colour: red\n"), 0o644))
	_, err := LoadLearnings(path)
	require.Error(t, err)
}
