package manifest

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/version"
)

// ErrUnsupportedVersion marks a manifest whose schema version this binary
// does not read (FR-009).
var ErrUnsupportedVersion = errors.New("unsupported manifest schema version")

// CheckVersion refuses any schema version other than the one this binary
// supports, naming both.
func CheckVersion(v int) error {
	switch {
	case v == version.ManifestSchemaVersion:
		return nil
	case v == 0:
		return fmt.Errorf("%w: schema_version is missing; this sandcast supports version %d",
			ErrUnsupportedVersion, version.ManifestSchemaVersion)
	case v > version.ManifestSchemaVersion:
		return fmt.Errorf("%w: manifest uses schema version %d, this sandcast supports version %d; update sandcast",
			ErrUnsupportedVersion, v, version.ManifestSchemaVersion)
	default:
		return fmt.Errorf("%w: manifest uses schema version %d, this sandcast supports version %d",
			ErrUnsupportedVersion, v, version.ManifestSchemaVersion)
	}
}

// checkVersionField reads only schema_version, so that a newer manifest with
// fields this binary does not know fails with the version message rather
// than an unknown-field error.
func checkVersionField(data []byte, source string) error {
	var probe struct {
		SchemaVersion int `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil // the strict decode reports the syntax error with its line
	}
	if probe.SchemaVersion > version.ManifestSchemaVersion {
		return fmt.Errorf("%s: %w", source, CheckVersion(probe.SchemaVersion))
	}
	return nil
}
