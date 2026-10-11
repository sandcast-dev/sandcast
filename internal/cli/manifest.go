package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/redact"
)

func (a *App) manifestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Validate and edit .sandcast/manifest.yaml",
	}
	cmd.AddCommand(a.manifestValidateCommand(), a.manifestSetShellCommand())
	return cmd
}

type validateReport struct {
	Valid       bool                  `json:"valid"`
	Manifest    string                `json:"manifest"`
	Diagnostics []manifest.Diagnostic `json:"diagnostics"`
}

func (a *App) manifestValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the manifest schema, version, component and profile references",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := a.layout()
			if err != nil {
				return err
			}
			path := l.ManifestPath()
			report := validateReport{Manifest: l.RelToRoot(path), Diagnostics: []manifest.Diagnostic{}}
			data, err := os.ReadFile(path)
			if err != nil {
				return withCode(ExitUsage, fmt.Errorf("read manifest: %w", err))
			}
			m, err := manifest.Parse(data, report.Manifest)
			var ve *manifest.ValidationError
			switch {
			case errors.As(err, &ve):
				report.Diagnostics = append(report.Diagnostics, ve.Diagnostics...)
			case err != nil:
				report.Diagnostics = append(report.Diagnostics, manifest.Diagnostic{Path: "$", Message: err.Error()})
			default:
				refs, err := a.referenceDiagnostics(l, m, data)
				if err != nil {
					return err
				}
				report.Diagnostics = append(report.Diagnostics, refs...)
			}
			report.Diagnostics = append(report.Diagnostics, secretDiagnostics(data)...)
			report.Valid = len(report.Diagnostics) == 0
			if a.opts.json {
				if err := writeJSON(a.Stdout, report); err != nil {
					return err
				}
			} else {
				printDiagnostics(a.Stdout, report)
			}
			if !report.Valid {
				return silentErr{code: ExitUsage}
			}
			return nil
		},
	}
}

func printDiagnostics(w io.Writer, r validateReport) {
	if r.Valid {
		_, _ = fmt.Fprintf(w, "%s: valid\n", r.Manifest)
		return
	}
	for _, d := range r.Diagnostics {
		if d.Line > 0 {
			_, _ = fmt.Fprintf(w, "%s:%d: %s: %s\n", r.Manifest, d.Line, d.Path, d.Message)
		} else {
			_, _ = fmt.Fprintf(w, "%s: %s: %s\n", r.Manifest, d.Path, d.Message)
		}
	}
}

// secretDiagnostics reports credential shapes in the manifest (FR-008).
func secretDiagnostics(data []byte) []manifest.Diagnostic {
	hits, err := redact.ScanYAML(data)
	if err != nil {
		return nil
	}
	out := make([]manifest.Diagnostic, 0, len(hits))
	for _, h := range hits {
		out = append(out, manifest.Diagnostic{
			Path:    "$." + h.Path,
			Line:    h.Line,
			Message: fmt.Sprintf("looks like a credential (%s); replace it with a ${SANDCAST_SECRET_N} placeholder and supply it as a provider credential at runtime", h.Kind),
		})
	}
	return out
}

func (a *App) manifestSetShellCommand() *cobra.Command {
	var program, file string
	cmd := &cobra.Command{
		Use:   "set-shell",
		Short: "Write a curated shell configuration from stdin or a file into the manifest",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if program != "zsh" && program != "bash" {
				return usageErr("--program must be zsh or bash, got %q", program)
			}
			m, l, err := a.loadManifest()
			if err != nil {
				return err
			}
			var data []byte
			if file != "" && file != "-" {
				data, err = os.ReadFile(file)
			} else {
				data, err = io.ReadAll(a.Stdin)
			}
			if err != nil {
				return fmt.Errorf("read shell configuration: %w", err)
			}
			rc := strings.TrimRight(string(data), "\n") + "\n"
			if hits := redact.Scan(rc); len(hits) > 0 {
				return usageErr("the shell configuration contains %d credential-shaped value(s) (first: %s at line %d); remove them before writing the manifest", len(hits), hits[0].Kind, hits[0].Line)
			}
			if m.Shell == nil {
				m.Shell = &manifest.Shell{}
			}
			m.Shell.Program = program
			m.Shell.RC = rc
			if err := manifest.Save(l.ManifestPath(), m); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.Stdout, "wrote %s shell configuration (%d lines) to %s\n",
				program, strings.Count(rc, "\n"), l.RelToRoot(l.ManifestPath()))
			return nil
		},
	}
	cmd.Flags().StringVar(&program, "program", "zsh", "shell program: zsh or bash")
	cmd.Flags().StringVar(&file, "file", "", "read the configuration from this file instead of stdin")
	return cmd
}

// yamlLine returns the line of a YAML path in data, or 0.
func yamlLine(data []byte, path string) int {
	p, err := yaml.PathString(path)
	if err != nil {
		return 0
	}
	node, err := p.ReadNode(strings.NewReader(string(data)))
	if err != nil || node == nil || node.GetToken() == nil {
		return 0
	}
	return node.GetToken().Position.Line
}
