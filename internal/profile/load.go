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

// profileObjectPaths are the schema-declared object positions where OpenShell
// v0.1.3 rejects a null value ("null is not an object") before typed decoding.
// A '*' selects each sequence element or mapping value. Only positions sandcast
// models with a typed schema are listed; the pass-through blocks (refresh,
// token_grant, and the experimental files feature) are left to the gateway,
// which re-validates on import.
var profileObjectPaths = []string{
	"annotations",
	"discovery",
	"credentials.*",
	"endpoints.*",
	"binaries.*",
	"endpoints.*.mcp",
	"endpoints.*.graphql_persisted_queries",
	"endpoints.*.graphql_persisted_queries.*",
	"endpoints.*.rules.*",
	"endpoints.*.rules.*.allow.query",
	"endpoints.*.rules.*.allow.params",
	"endpoints.*.deny_rules.*",
	"endpoints.*.deny_rules.*.query",
	"endpoints.*.deny_rules.*.params",
}

// Parse decodes one provider profile document. source names the document in
// error messages. Unknown fields, duplicate keys (both via yaml.Strict), null
// at a schema-declared object path, and missing required fields are errors.
func Parse(data []byte, source string) (*Profile, error) {
	if err := rejectNullObjects(data); err != nil {
		return nil, fmt.Errorf("parse provider profile %s: %w", source, err)
	}
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

// rejectNullObjects mirrors reject_null_objects in
// crates/openshell-policy-schema/src/yaml.rs: it rejects a null document and a
// null value at any schema-declared object path (profileObjectPaths). It runs
// before typed decoding so a null object fails the same way it does upstream
// rather than silently decoding to a zero value. A syntax error is left for the
// typed decode to report with its richer message.
func rejectNullObjects(data []byte) error {
	var root any
	if err := yaml.UnmarshalWithOptions(data, &root, yaml.UseOrderedMap()); err != nil {
		return nil
	}
	if err := rejectNullAt(root, "", ""); err != nil {
		return err
	}
	for _, p := range profileObjectPaths {
		if err := rejectNullAt(root, p, ""); err != nil {
			return err
		}
	}
	return nil
}

// rejectNullAt mirrors reject_null_at_path. remaining is the unconsumed object
// path; location is the dotted/bracketed position reached so far, for messages.
func rejectNullAt(value any, remaining, location string) error {
	if remaining == "" {
		if value == nil {
			loc := location
			if loc == "" {
				loc = "."
			} else {
				loc = boundProfilePath(loc)
			}
			return fmt.Errorf("%s: null is not an object", loc)
		}
		return nil
	}
	field, rest, _ := strings.Cut(remaining, ".")
	if field == "*" {
		switch v := value.(type) {
		case yaml.MapSlice:
			for _, item := range v {
				if err := rejectNullAt(item.Value, rest, fmt.Sprintf("%s[%q]", location, fmt.Sprint(item.Key))); err != nil {
					return err
				}
			}
		case map[string]any:
			for _, k := range sortedKeys(v) {
				if err := rejectNullAt(v[k], rest, fmt.Sprintf("%s[%q]", location, k)); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range v {
				if err := rejectNullAt(child, rest, fmt.Sprintf("%s[%d]", location, i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	child, ok := lookupField(value, field)
	if !ok {
		return nil
	}
	loc := field
	if location != "" {
		loc = location + "." + field
	}
	return rejectNullAt(child, rest, loc)
}

// boundProfilePath caps a path for an error message so a long or odd mapping
// key cannot produce an unbounded diagnostic.
func boundProfilePath(loc string) string {
	const max = 128
	if len(loc) <= max {
		return loc
	}
	return strings.ToValidUTF8(loc[:max], "") + "..."
}
