# Quickstart: validating runtime home and shell initialization

Runnable scenarios that prove the feature end to end. Each maps to a user story. Use `make` targets; podman is the runtime.

## Prerequisites

- `make build` produces `./sandcast`.
- A base image from the registry (the probe and build use it).
- podman available; for the mounted-over-home scenarios, an OpenShell v0.1.3 gateway for `make test-gateway`, or a direct `podman run` with a volume mounted over the home path.

## Scenario 1 — curated shell survives the overmount (US1, SC-001)

1. Capture and build an image for a project whose capture curated a zsh (and bash) configuration.
2. Start the image with a volume mounted over the home path (`/sandbox`), as OpenShell does.
3. Open an interactive zsh shell; confirm the curated prompt, aliases, and completions are active.
4. Repeat for bash.

Expected: the curated environment is active in both shells even though the home is mounted over. `ZDOTDIR` points at `/opt/sandcast/zdotdir`; the bash launcher uses the baked rcfile.

## Scenario 2 — the writable workspace home is never executed (US2, SC-002)

1. In the mounted workspace, place `~/.zshrc` and `~/.bashrc` that each create a sentinel file (for example `touch /tmp/pwned-zsh`).
2. Start the sandbox and open an interactive zsh shell, then a bash shell.
3. Check for the sentinels.

Expected: no sentinel exists; the planted rc files did not run. The baked configuration is what loaded.

## Scenario 3 — dotfiles survive via relocation (US3, SC-003)

1. On the host, have a `.gitconfig` (with a recognizable `user.email`) and a `.tmux.conf`.
2. Capture; confirm the proposed dotfiles in the multi-select; build.
3. Start the sandbox (home mounted over) and run `git config --get user.email` and start tmux.

Expected: git returns the captured value (read from `/etc/gitconfig` or `GIT_CONFIG_GLOBAL`), tmux uses the captured config. A `.gitconfig` containing a token-shaped value is redacted, not baked (US3-AS3).

## Scenario 4 — nothing vanishes under the overmount (US4, SC-004, SC-006)

1. Use (or simulate) a base image whose only copy of a required tool lives under the home path.
2. Run `sandcast plan` and read the notices.
3. Build and start the sandbox with the home mounted over.

Expected: the plan reports a `reprovisioned` notice for that tool and installs it to a persistent location; at runtime the tool is present. The plan also reports `relocated` notices for any baked config that would have fallen under the home. No sandcast-managed file resolves under `/sandbox`.

## Scenario 5 — determinism (SC-005)

1. Build the image twice from the same manifest, lockfile, base digest, and platform.
2. Compare the generated Containerfile sections and the plan notices.

Expected: identical shell-initialization and dotfile-placement configuration and identical notices.

## Automated coverage

- Unit and golden: `make test` (shell section, plan ENV, dotfile selection, probe reclassification, notices).
- Integration (podman, mounted-over home): `make test-integration` includes the overmount survival check from research R8.
