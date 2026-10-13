package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/harness"
)

type harnessRow struct {
	Name      string `json:"name"`
	Component string `json:"component"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

// harnessEnv describes the host for adapters.
func (a *App) harnessEnv() (harness.Env, error) {
	home := a.Getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return harness.Env{}, err
		}
		home = h
	}
	l, err := a.layout()
	if err != nil {
		return harness.Env{}, err
	}
	env := harness.Env{Home: home, Project: l.Root, LookPath: a.LookPath}
	// Without git on the host, local marketplaces are skipped as before.
	if bin, err := a.LookPath("git"); err == nil {
		env.Git = harness.ExecGit{Bin: bin}
	}
	return env, nil
}

func (a *App) harnessCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Inspect agent harnesses and their local extensions",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "Show known adapters and whether each harness is installed locally",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := a.harnessEnv()
			if err != nil {
				return err
			}
			rows := []harnessRow{}
			for _, n := range harness.Names() {
				ad, _ := harness.Get(n)
				ok, path := ad.Installed(env)
				rows = append(rows, harnessRow{Name: n, Component: ad.ComponentKey(), Installed: ok, Path: path})
			}
			if a.opts.json {
				return writeJSON(a.Stdout, rows)
			}
			tb := newTable(a.Stdout, "HARNESS", "COMPONENT", "INSTALLED", "PATH")
			for _, r := range rows {
				inst := "no"
				if r.Installed {
					inst = "yes"
				}
				tb.row(r.Name, r.Component, inst, orDash(r.Path))
			}
			return tb.flush()
		},
	}
	discover := &cobra.Command{
		Use:   "discover <name>",
		Short: "Run one adapter's discovery (secrets redacted) and print MCP servers, plugins and skills",
		Args:  args(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, argv []string) error {
			ad, err := harness.Get(argv[0])
			if err != nil {
				return usageErr("%v", err)
			}
			env, err := a.harnessEnv()
			if err != nil {
				return err
			}
			d, err := ad.Discover(cmd.Context(), env)
			if err != nil {
				return err
			}
			if a.opts.json {
				return writeJSON(a.Stdout, d)
			}
			_, _ = fmt.Fprintf(a.Stdout, "harness %s\n", d.Harness)
			for _, s := range d.MCPServers {
				target := s.URL
				if target == "" {
					target = fmt.Sprint(s.Command)
				}
				_, _ = fmt.Fprintf(a.Stdout, "mcp server: %s  %s  (%s)\n", s.Name, target, s.Origin)
			}
			for _, p := range d.Plugins {
				_, _ = fmt.Fprintf(a.Stdout, "plugin: %s  %s%s\n", p.Name, p.Source, extensionDetail(p))
			}
			for _, s := range d.Skills {
				_, _ = fmt.Fprintf(a.Stdout, "skill: %s  %s%s\n", s.Name, s.Source, extensionDetail(s))
			}
			for _, r := range d.Redacted {
				_, _ = fmt.Fprintf(a.Stdout, "redacted: %s (%s) -> %s; supply it as a provider credential at runtime\n", r.Path, r.Kind, r.Placeholder)
			}
			for _, n := range d.Notes {
				_, _ = fmt.Fprintf(a.Stdout, "note: %s\n", n)
			}
			return nil
		},
	}
	cmd.AddCommand(list, discover)
	return cmd
}

// extensionDetail is the console suffix of a discovered plugin or skill: the
// portability reason, and the git state of a marketplace proposed through
// its remote.
func extensionDetail(e harness.Extension) string {
	var parts []string
	if !e.Portable && e.Reason != "" {
		parts = append(parts, "not portable: "+e.Reason)
	}
	if r := e.Remote; r != nil {
		unpushed := "unpushed commits unknown (no remote-tracking ref)"
		if r.UnpushedCommits != nil {
			unpushed = fmt.Sprintf("%d unpushed commit(s)", *r.UnpushedCommits)
		}
		parts = append(parts, fmt.Sprintf("from git remote %s, %s, uncommitted changes: %t, marketplace name: %s",
			r.URL, unpushed, r.UncommittedChanges, r.NameMatch))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  (" + strings.Join(parts, "; ") + ")"
}
