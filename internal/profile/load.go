package profile

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/goccy/go-yaml"
)

// decodeOptions are the fixed decoder options for profiles: unknown fields
// are rejected, and pass-through blocks keep their key order.
func decodeOptions() []yaml.DecodeOption {
	return []yaml.DecodeOption{yaml.Strict(), yaml.UseOrderedMap()}
}

// encodeOptions are the fixed encoder options for profiles and the index, so
// that output is byte for byte deterministic.
func encodeOptions() []yaml.EncodeOption {
	return []yaml.EncodeOption{
		yaml.Indent(2),
		yaml.IndentSequence(true),
		yaml.UseLiteralStyleIfMultiline(true),
	}
}

// Parse decodes one provider profile document. source names the document in
// error messages. Unknown fields and missing required fields are errors.
func Parse(data []byte, source string) (*Profile, error) {
	var p Profile
	if err := yaml.UnmarshalWithOptions(data, &p, decodeOptions()...); err != nil {
		return nil, fmt.Errorf("parse provider profile %s: %s", source, decodeError(err))
	}
	var req requiredFields
	if err := yaml.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parse provider profile %s: %s", source, decodeError(err))
	}
	if err := req.missing(); err != nil {
		return nil, fmt.Errorf("parse provider profile %s: %w", source, err)
	}
	return &p, nil
}

// decodeError renders a YAML decode error without any source snippet. The
// default rendering quotes the offending source lines, so a symlinked profile
// file pointing at a secret would print that secret in the error. The line and
// column are kept; the file content is not.
func decodeError(err error) string {
	return yaml.FormatError(err, false, false)
}

// LoadFile reads and parses the provider profile at path.
func LoadFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider profile: %w", err)
	}
	return Parse(data, path)
}

// LoadDir parses every *.yaml and *.yml file at the top level of fsys, in
// file name order. Each entry records the tier and a display path built from
// the tier prefix (see Tier.Prefix). Other files and directories are ignored.
func LoadDir(fsys fs.FS, tier Tier) ([]Entry, error) {
	files, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read %s profiles: %w", tier, err)
	}
	var entries []Entry
	for _, f := range files {
		if f.IsDir() || !isYAML(f.Name()) {
			continue
		}
		data, err := fs.ReadFile(fsys, f.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s profiles: %w", tier, err)
		}
		source := path.Join(tier.Prefix(), f.Name())
		p, err := Parse(data, source)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Profile: p, Tier: tier, SourceFile: source})
	}
	return entries, nil
}

// Marshal encodes a profile with the fixed encoder options. Comments of the
// source document are not preserved.
func Marshal(p *Profile) ([]byte, error) {
	out, err := yaml.MarshalWithOptions(p, encodeOptions()...)
	if err != nil {
		return nil, fmt.Errorf("marshal provider profile %s: %w", p.ID, err)
	}
	return out, nil
}

// isYAML reports whether name has a YAML file extension.
func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}
