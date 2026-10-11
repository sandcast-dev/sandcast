package cli

import (
	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast"
	"github.com/rhuss/sandcast/internal/version"
)

type versionInfo struct {
	Version               string `json:"version"`
	OpenShell             string `json:"openshell"`
	OpenShellCommit       string `json:"openshell_commit"`
	Mise                  string `json:"mise"`
	ComponentRegistry     string `json:"component_registry"`
	SkillBundle           string `json:"skill_bundle"`
	ManifestSchemaVersion int    `json:"manifest_schema_version"`
	PlanSchemaVersion     int    `json:"plan_schema_version"`
}

func currentVersion() versionInfo {
	return versionInfo{
		Version:               version.Version,
		OpenShell:             version.OpenShellPin,
		OpenShellCommit:       version.OpenShellCommit,
		Mise:                  version.MisePin,
		ComponentRegistry:     version.ComponentRegistryPin,
		SkillBundle:           sandcast.SkillsVersion(),
		ManifestSchemaVersion: version.ManifestSchemaVersion,
		PlanSchemaVersion:     version.PlanSchemaVersion,
	}
}

func (a *App) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the binary version and the pinned upstream releases",
		Args:  args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := currentVersion()
			if a.opts.json {
				return writeJSON(a.Stdout, v)
			}
			return keyValue(a.Stdout,
				"sandcast", v.Version,
				"openshell", v.OpenShell+" ("+v.OpenShellCommit[:8]+")",
				"mise", v.Mise,
				"component registry", v.ComponentRegistry,
				"skill bundle", v.SkillBundle,
				"manifest schema", itoa(v.ManifestSchemaVersion),
				"plan schema", itoa(v.PlanSchemaVersion),
			)
		},
	}
}
