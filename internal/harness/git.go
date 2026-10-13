package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Git runs git commands in a directory on the host (R-18). Discovery uses
// it only to read local repository state; it never fetches.
type Git interface {
	// Run runs git with args in dir and returns the trimmed standard
	// output.
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// ExecGit runs the git executable through os/exec.
type ExecGit struct {
	// Bin is the git binary; defaults to git.
	Bin string
}

// gitArgs builds the argument list: -C dir selects the repository, and the
// fixed settings keep the output stable and free of pagers and colors.
// core.fsmonitor is off because a repository's own configuration could
// otherwise make git status run a command of its choice.
func gitArgs(dir string, args []string) []string {
	return append([]string{"-C", dir, "-c", "core.quotepath=off", "-c", "color.ui=false", "-c", "core.fsmonitor=false", "--no-pager"}, args...)
}

// gitEnv keeps git from prompting for credentials or reading a terminal.
func gitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
}

// Run implements Git.
func (g ExecGit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, gitArgs(dir, args)...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
