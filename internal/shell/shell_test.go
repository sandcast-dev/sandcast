package shell

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuote(t *testing.T) {
	assert.Equal(t, `'it'\''s'`, Quote("it's"))
	assert.Equal(t, `''`, Quote(""))
	assert.Equal(t, "plain-value_1.0", QuoteIfNeeded("plain-value_1.0"))
	assert.Equal(t, `'a b'`, QuoteIfNeeded("a b"))
	assert.Equal(t, `'$HOME'`, QuoteIfNeeded("$HOME"))
}

// TestJSONHelpersBuiltinsOnly runs _jsafe with an empty PATH, so any use of
// an external command such as sed or tr fails the test.
func TestJSONHelpersBuiltinsOnly(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, v := range []string{"plain", `quo"te`, `back\slash`, "tab\there", "new\nline", "cr\rx", "bell\x07", "ünïcode", ""} {
		cmd := exec.Command(sh, "-c", JSONHelpers+`printf '"'; _jsafe "$1"; printf '"'`, "sh", v)
		cmd.Env = []string{"PATH="}
		out, err := cmd.Output()
		require.NoError(t, err, "value %q", v)
		var got string
		require.NoError(t, json.Unmarshal(out, &got), "output %q", out)
		assert.Equal(t, v, got)
	}
}
