package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/probe"
	"github.com/rhuss/sandcast/internal/redact"
)

// FileName is the verify report in build/<target>/.
const FileName = "verify.yaml"

// ErrChecksFailed means at least one component check failed; the CLI exits 1
// after reporting every result (FR-030).
var ErrChecksFailed = errors.New("component checks failed")

// Report is build/<target>/verify.yaml (data-model "Verify result").
type Report struct {
	Image   ImageRef `yaml:"image" json:"image"`
	Results []Result `yaml:"results" json:"results"`
	Passed  int      `yaml:"passed" json:"passed"`
	Failed  int      `yaml:"failed" json:"failed"`
}

// ImageRef identifies the verified image.
type ImageRef struct {
	Reference string `yaml:"reference" json:"reference"`
	Digest    string `yaml:"digest" json:"digest"`
}

// Result is the outcome of one component check.
type Result struct {
	ComponentKey string `yaml:"component_key" json:"component_key"`
	OK           bool   `yaml:"ok" json:"ok"`
	Status       string `yaml:"status" json:"status"`
	Version      string `yaml:"version,omitempty" json:"version,omitempty"`
	Output       string `yaml:"output" json:"output"`
}

// Requests turns the plan's components into check requests, sorted by key.
func Requests(p *plan.Plan) []probe.Request {
	reqs := make([]probe.Request, 0, len(p.Components))
	for _, c := range p.Components {
		if c.Check.Command == "" {
			continue
		}
		bin := ""
		if len(c.Bins) > 0 {
			bin = c.Bins[0]
		}
		reqs = append(reqs, probe.Request{
			Key:            c.Key,
			Command:        c.Check.Command,
			Bin:            bin,
			Selector:       c.Check.Version,
			ExpectExit:     c.Check.ExpectExit,
			StdoutContains: c.Check.StdoutContains,
			Hash:           checkHash(c.Check),
		})
	}
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Key < reqs[j].Key })
	return reqs
}

// checkHash identifies a check the same way the probe cache does: the
// command and its expected outcome.
func checkHash(c plan.Check) string {
	h := sha256.New()
	for _, s := range []string{c.Command, strconv.Itoa(c.ExpectExit), c.Version, c.StdoutContains} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Run executes every component check of the plan inside image as the
// image's final user, under the container engine only (no gateway). It
// returns the full report; err wraps ErrChecksFailed when any check failed.
func Run(ctx context.Context, eng engine.Engine, image, platform string, p *plan.Plan) (*Report, error) {
	reqs := Requests(p)
	checks, err := probe.RunChecks(ctx, eng, image, platform, reqs)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", image, err)
	}
	report := &Report{Image: ImageRef{Reference: image}, Results: []Result{}}
	if id, err := eng.ImageID(ctx, image); err == nil {
		if !strings.HasPrefix(id, "sha256:") {
			id = "sha256:" + id
		}
		report.Image.Digest = id
	}
	var failed []string
	for _, req := range reqs {
		c := checks[req.Key]
		r := Result{
			ComponentKey: req.Key,
			OK:           c.Status == probe.StatusPresent,
			Status:       string(c.Status),
			Version:      c.Version,
			Output:       c.Output,
		}
		if r.Status == "" {
			r.Status = string(probe.StatusMissing)
		}
		report.Results = append(report.Results, r)
		if r.OK {
			report.Passed++
		} else {
			report.Failed++
			failed = append(failed, req.Key)
		}
	}
	if len(failed) > 0 {
		return report, fmt.Errorf("%w: %s", ErrChecksFailed, strings.Join(failed, ", "))
	}
	return report, nil
}

// Write stores the report as YAML.
func (r *Report) Write(path string) error {
	data, err := yaml.MarshalWithOptions(r,
		yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return fmt.Errorf("encode verify report: %w", err)
	}
	// Check output comes from the image; a credential in it is not written.
	if hits := redact.Scan(string(data)); len(hits) > 0 {
		return fmt.Errorf("%s not written: check output contains a credential-shaped value (%s at line %d)", filepath.Base(path), hits[0].Kind, hits[0].Line)
	}
	return fsutil.AtomicWriteFile(path, data, 0o644)
}
