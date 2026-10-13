package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
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

// gitRepoEnv are the variables that would point git at another repository
// than -C dir, or inject configuration; gitEnv drops them.
var gitRepoEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
}

// gitEnv keeps git from prompting for credentials or reading a terminal,
// and from following repository-location variables of the caller's shell
// (a git hook or a bare dotfiles setup) instead of -C dir.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(gitRepoEnv, name) || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
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
