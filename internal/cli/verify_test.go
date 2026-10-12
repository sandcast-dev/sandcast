package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rhuss/sandcast/internal/verify"
)

func TestPrintVerifyMarksSessionChecks(t *testing.T) {
	var out bytes.Buffer
	printVerify(&App{Stdout: &out}, &verify.Report{
		Image: verify.ImageRef{Reference: "localhost/demo:latest"},
		Results: []verify.Result{
			{ComponentKey: "starship", OK: true, Status: "present", Version: "1.26.0", Output: "starship 1.26.0"},
			{ComponentKey: "starship", Mode: verify.ModeSession, OK: false, Status: "missing", Output: "zsh:1: command not found: starship"},
		},
		Passed: 1, Failed: 1,
	})
	assert.Contains(t, out.String(), "starship (session)")
	assert.Contains(t, out.String(), "FAIL (missing)")
}
