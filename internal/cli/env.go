package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/gateway"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/pipeline"
	"github.com/rhuss/sandcast/internal/registry"
)

// GatewayRunner runs the openshell CLI (gateway.Runner).
type GatewayRunner = gateway.Runner

type execGateway = gateway.ExecRunner

func (a *App) registry() (*registry.Registry, error) {
	return pipeline.Registry()
}

func (a *App) cacheDir() (string, error) {
	return fsutil.CacheDir()
}

// pipelineEnv loads the manifest and selects the engine.
func (a *App) pipelineEnv() (pipeline.Env, error) {
	m, l, err := a.loadManifest()
	if err != nil {
		return pipeline.Env{}, err
	}
	eng, sel, err := a.selectEngine(m.Engine)
	if err != nil {
		return pipeline.Env{}, err
	}
	cache, err := a.cacheDir()
	if err != nil {
		return pipeline.Env{}, err
	}
	henv, err := a.harnessEnv()
	if err != nil {
		return pipeline.Env{}, err
	}
	return pipeline.Env{
		Home:         henv.Home,
		Layout:       l,
		Manifest:     m,
		Engine:       eng,
		EngineSel:    sel,
		HostPlatform: a.HostPlatform(),
		CacheDir:     cache,
		Logf:         a.logf,
	}, nil
}

// pipelineErr maps pipeline errors to exit codes.
func pipelineErr(err error) error {
	if err == nil {
		return nil
	}
	var ue *pipeline.UsageError
	if errors.As(err, &ue) {
		return withCode(ExitUsage, err)
	}
	return err
}

// referenceDiagnostics runs the pipeline's reference validation and adds
// the manifest line of each diagnostic.
func (a *App) referenceDiagnostics(l fsutil.Layout, m *manifest.Manifest, data []byte) ([]manifest.Diagnostic, error) {
	cache, err := a.cacheDir()
	if err != nil {
		return nil, err
	}
	diags, err := pipeline.ValidateReferences(l, cache, m)
	if err != nil {
		return nil, err
	}
	for i := range diags {
		if diags[i].Line == 0 {
			diags[i].Line = yamlLine(data, diags[i].Path)
		}
	}
	return diags, nil
}

// planned runs the plan pipeline for a command.
func (a *App) planned(ctx context.Context, env pipeline.Env, target, platform string, noCache bool) (*pipeline.Planned, error) {
	planned, err := pipeline.Plan(ctx, env, pipeline.PlanOptions{Target: target, PlatformFlag: platform, NoCache: noCache})
	if err != nil {
		return nil, pipelineErr(err)
	}
	return planned, nil
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }
