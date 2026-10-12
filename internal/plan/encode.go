package plan

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/fsutil"
)

// Output file names inside build/<target>/.
const (
	PlanFile     = "plan.yaml"
	MiseDir      = "mise"
	MiseTOMLFile = "mise.toml"
	MiseLockCopy = "mise.lock"
	PolicyFile   = "policy.yaml"
	ShellDir     = "shell"
)

// Encode renders the plan with fixed encoder settings: two-space indent,
// block style, literal blocks for multi-line strings, struct field order.
func Encode(p *Plan) ([]byte, error) {
	data, err := yaml.MarshalWithOptions(p,
		yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}
	return data, nil
}

// Load reads and validates build/<target>/plan.yaml.
func Load(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	return Decode(data, path)
}

// Decode parses and validates plan bytes.
func Decode(data []byte, source string) (*Plan, error) {
	var p Plan
	if err := yaml.UnmarshalWithOptions(data, &p, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %s", source, yaml.FormatError(err, false, false))
	}
	if err := Validate(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &p, nil
}

// WriteOutputs writes plan.yaml, mise/ (mise.toml and the lockfile copy)
// and policy.yaml into dir. lock is the content of
// .sandcast/mise.lock, nil when no mise tools are planned. Stale outputs from
// an earlier plan are removed.
func WriteOutputs(dir string, p *Plan, lock []byte) error {
	data, err := Encode(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	miseDir := filepath.Join(dir, MiseDir)
	if err := os.RemoveAll(miseDir); err != nil {
		return err
	}
	if p.Mise.TOML != "" {
		files := map[string][]byte{
			MiseTOMLFile: []byte(p.Mise.TOML),
			MiseLockCopy: lock,
		}
		for name, content := range files {
			if err := fsutil.AtomicWriteFile(filepath.Join(miseDir, name), content, 0o644); err != nil {
				return err
			}
		}
	}
	policyPath := filepath.Join(dir, PolicyFile)
	if p.Policy != "" {
		if err := fsutil.AtomicWriteFile(policyPath, []byte(p.Policy), 0o644); err != nil {
			return err
		}
	} else if err := os.Remove(policyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return fsutil.AtomicWriteFile(filepath.Join(dir, PlanFile), data, 0o644)
}
