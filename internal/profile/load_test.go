package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// secretLine is a credential-shaped value placed where a decode error would, by
// default, echo the offending source line back into the message.
const secretLine = "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestParseDoesNotEchoSourceLine(t *testing.T) {
	// port expects an integer; a string there is a type error whose default
	// rendering quotes the source line, which here carries the secret.
	doc := "id: x\ndisplay_name: X\nendpoints:\n  - host: a.example.com\n    port: " + secretLine + "\n"

	_, err := Parse([]byte(doc), "bad.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad.yaml", "the file name stays for context")
	assert.NotContains(t, err.Error(), secretLine, "the source line must not leak into the error")

	// Confirm the test is meaningful: the raw decoder error, propagated before
	// the fix, does carry the source line with the secret.
	var p Profile
	raw := yaml.UnmarshalWithOptions([]byte(doc), &p, decodeOptions()...)
	require.Error(t, raw)
	require.Contains(t, raw.Error(), secretLine)
}

func TestReadIndexDoesNotEchoSourceLine(t *testing.T) {
	// schema_version expects an integer; a string there is a type error.
	doc := "schema_version: " + secretLine + "\nimage:\n  name: x\n"
	path := filepath.Join(t.TempDir(), IndexFile)
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

	_, err := ReadIndex(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path, "the file name stays for context")
	assert.NotContains(t, err.Error(), secretLine, "the source line must not leak into the error")

	// Confirm the test is meaningful: the raw decoder error, decoded the same
	// way ReadIndex decodes, does carry the source line with the secret.
	var idx Index
	raw := yaml.UnmarshalWithOptions([]byte(doc), &idx, yaml.Strict())
	require.Error(t, raw)
	require.Contains(t, raw.Error(), secretLine)
}
