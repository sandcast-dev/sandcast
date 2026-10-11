package engine

import (
	"errors"
	"fmt"
	"os/exec"
)

// Selection sources recorded in the plan.
const (
	SourceFlag     = "flag"
	SourceManifest = "manifest"
	SourceDetected = "detected"
	SourceFallback = "fallback"
)

// ErrNoEngine is returned when neither podman nor docker is available.
var ErrNoEngine = errors.New("no container engine found: install podman (preferred) or docker")

// ErrUnsupportedEngine marks an engine name other than podman or docker.
var ErrUnsupportedEngine = errors.New("unsupported engine")

// Selection is the chosen engine and why it was chosen.
type Selection struct {
	// Name is podman or docker.
	Name string
	// Source is flag, manifest, detected or fallback.
	Source string
	// Notice is printed to stderr when non-empty (docker fallback).
	Notice string
}

// LookPath finds an executable; tests replace it.
type LookPath func(file string) (string, error)

// Detect selects the engine: the command-line flag, then the manifest
// field, then podman on PATH, then docker with a notice (FR-005).
func Detect(flag, manifest string, look LookPath) (Selection, error) {
	if look == nil {
		look = exec.LookPath
	}
	for _, c := range []struct{ value, source string }{{flag, SourceFlag}, {manifest, SourceManifest}} {
		if c.value == "" {
			continue
		}
		if c.value != Podman && c.value != Docker {
			return Selection{}, fmt.Errorf("%w %q (%s): use podman or docker", ErrUnsupportedEngine, c.value, c.source)
		}
		if _, err := look(c.value); err != nil {
			return Selection{}, fmt.Errorf("engine %s selected by %s is not on PATH: %w", c.value, c.source, err)
		}
		return Selection{Name: c.value, Source: c.source}, nil
	}
	if _, err := look(Podman); err == nil {
		return Selection{Name: Podman, Source: SourceDetected}, nil
	}
	if _, err := look(Docker); err == nil {
		return Selection{
			Name:   Docker,
			Source: SourceFallback,
			Notice: "podman not found on PATH; using docker",
		}, nil
	}
	return Selection{}, ErrNoEngine
}

func isExit(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
