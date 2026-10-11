package build

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/redact"
)

// Learning outcomes.
const (
	OutcomeFixed   = "fixed"
	OutcomeUnfixed = "unfixed"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Learning is one entry of .sandcast/learnings.yaml (data-model "Learnings
// entry"): a build failure and the change made for it.
type Learning struct {
	Attempt   int    `yaml:"attempt" json:"attempt"`
	Component string `yaml:"component" json:"component"`
	Failure   string `yaml:"failure" json:"failure"`
	Change    string `yaml:"change" json:"change"`
	Outcome   string `yaml:"outcome" json:"outcome"`
	Date      string `yaml:"date" json:"date"`
}

// Validate checks one entry.
func (l Learning) Validate() error {
	var problems []string
	if l.Attempt < 1 {
		problems = append(problems, "attempt must be 1 or more")
	}
	if !fragmentKeyRe.MatchString(l.Component) {
		problems = append(problems, fmt.Sprintf("component %q is not a component key", l.Component))
	}
	if strings.TrimSpace(l.Failure) == "" {
		problems = append(problems, "failure is required")
	}
	if strings.TrimSpace(l.Change) == "" {
		problems = append(problems, "change is required")
	}
	if l.Outcome != OutcomeFixed && l.Outcome != OutcomeUnfixed {
		problems = append(problems, fmt.Sprintf("outcome %q must be fixed or unfixed", l.Outcome))
	}
	if !dateRe.MatchString(l.Date) {
		problems = append(problems, fmt.Sprintf("date %q must be YYYY-MM-DD", l.Date))
	}
	if hits := redact.Scan(l.Failure + "\n" + l.Change); len(hits) > 0 {
		problems = append(problems, "failure or change contains a credential-shaped value; remove it")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// LoadLearnings reads and validates the learnings file; a missing file is
// an empty list.
func LoadLearnings(path string) ([]Learning, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Learning
	if err := yaml.UnmarshalWithOptions(data, &list, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %s", path, yaml.FormatError(err, false, false))
	}
	for i, l := range list {
		if err := l.Validate(); err != nil {
			return nil, fmt.Errorf("%s: entry %d: %w", path, i+1, err)
		}
	}
	return list, nil
}

// AppendLearning validates entry and appends it to the learnings file.
func AppendLearning(path string, entry Learning) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	list, err := LoadLearnings(path)
	if err != nil {
		return err
	}
	list = append(list, entry)
	data, err := yaml.MarshalWithOptions(list,
		yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return fmt.Errorf("encode learnings: %w", err)
	}
	header := "# sandcast learnings: build failures and the changes made for them (written by the build skill).\n"
	return fsutil.AtomicWriteFile(path, append([]byte(header), data...), 0o644)
}
