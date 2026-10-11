package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// UpdateEnv is the environment variable that makes Golden rewrite golden
// files instead of comparing against them.
const UpdateEnv = "UPDATE_GOLDEN"

// Golden compares got with the content of the golden file at path. When
// UPDATE_GOLDEN=1 is set, it writes got to path instead and passes.
func Golden(t testing.TB, path string, got []byte) {
	t.Helper()
	if os.Getenv(UpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden: create dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("golden: write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden: read %s: %v (run with %s=1 to create it)", path, err, UpdateEnv)
	}
	if string(want) != string(got) {
		t.Errorf("golden mismatch for %s (run with %s=1 to update)\n--- want\n%s\n--- got\n%s",
			path, UpdateEnv, want, got)
	}
}
