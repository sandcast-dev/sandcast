# sandcast reference project

This directory is the capture fixture for success criterion SC-003.
It combines a Go module, a Python project managed by uv, and a zsh configuration that uses starship, fzf and lsd.

## Expected capture result

Running `/sandcast-capture` here, on a host with `starship`, `fzf` and `lsd` installed and Claude Code selected as the harness, proposes:

| Component | Evidence |
|-----------|----------|
| `go` | `go.mod` |
| `python` | `pyproject.toml` |
| `uv` | `uv.lock` |
| `starship` | host tool, referenced by `.zshrc` |
| `fzf` | host tool, referenced by `.zshrc` |
| `lsd` | host tool, referenced by `.zshrc` |
| `zsh` | shell program of the curated configuration |
| `claude-code` | harness selection |

The curated zsh configuration in the manifest:

- drops the Homebrew `PATH` entry and the antidote plugin manager,
- adds `autoload -Uz compinit && compinit -i -D` because `zstyle ':completion:*'` remains,
- guards `starship init` with `[[ "$TERM" != "dumb" ]]`,
- replaces `source <(fzf --zsh)` with the version-aware block,
- keeps the `lsd` aliases and the history options.

`sandcast manifest validate` passes on the result, and `sandcast build --verify` succeeds with at most one repair iteration.
The manifest recorded from a real capture lives in `expected-manifest.yaml` once quickstart scenario 4 has been run.
