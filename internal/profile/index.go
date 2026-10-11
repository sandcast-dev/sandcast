package profile

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// IndexSchemaVersion is the schema version of the adapted profile set index.
const IndexSchemaVersion = 1

// IndexFile is the file name of the index in an adapted profile directory.
const IndexFile = "index.yaml"

// Scope hints recorded in the index (FR-022a).
const (
	ScopeWorkspace = "workspace"
	ScopePlatform  = "platform"
)

// Index is the adapted profile set index, build/<target>/profiles/index.yaml
// (contracts/adapted-profiles.schema.yaml). Field names and order follow the
// contract.
type Index struct {
	SchemaVersion int            `yaml:"schema_version" json:"schema_version"`
	Image         ImageRef       `yaml:"image" json:"image"`
	ImageLabel    string         `yaml:"image_label" json:"image_label"`
	ScopeHint     string         `yaml:"scope_hint" json:"scope_hint"`
	Profiles      []IndexProfile `yaml:"profiles" json:"profiles"`
}

// ImageRef identifies the built image the profiles were adapted to.
type ImageRef struct {
	Name     string `yaml:"name" json:"name"`
	Tag      string `yaml:"tag" json:"tag"`
	Digest   string `yaml:"digest" json:"digest"`
	Platform string `yaml:"platform" json:"platform"`
}

// IndexProfile maps one upstream profile id to its adapted profile.
// Binaries is always written, as an empty list when nothing resolved.
type IndexProfile struct {
	UpstreamID string   `yaml:"upstream_id" json:"upstream_id"`
	AdaptedID  string   `yaml:"adapted_id" json:"adapted_id"`
	File       string   `yaml:"file" json:"file"`
	SourceTier Tier     `yaml:"source_tier" json:"source_tier"`
	SourceFile string   `yaml:"source_file" json:"source_file"`
	Binaries   []string `yaml:"binaries" json:"binaries"`
	Flagged    bool     `yaml:"flagged" json:"flagged"`
	FlagReason string   `yaml:"flag_reason,omitempty" json:"flag_reason,omitempty"`
}

// marshalIndex encodes the index with the fixed encoder options.
func marshalIndex(idx *Index) ([]byte, error) {
	out, err := yaml.MarshalWithOptions(idx, encodeOptions()...)
	if err != nil {
		return nil, fmt.Errorf("marshal profile index: %w", err)
	}
	return out, nil
}

// ReadIndex reads an adapted profile set index. Unknown fields and an
// unsupported schema version are errors.
func ReadIndex(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile index: %w", err)
	}
	var idx Index
	if err := yaml.UnmarshalWithOptions(data, &idx, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse profile index %s: %s", path, decodeError(err))
	}
	if idx.SchemaVersion != IndexSchemaVersion {
		return nil, fmt.Errorf("profile index %s: unsupported schema_version %d (want %d)",
			path, idx.SchemaVersion, IndexSchemaVersion)
	}
	return &idx, nil
}
