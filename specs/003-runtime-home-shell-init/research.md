# Research: Runtime home model and shell initialization

Phase 0 decisions. Each entry: Decision, Rationale, Alternatives considered.

## R1. Relocation mechanism per shell

**Decision**:
- **zsh**: set `ZDOTDIR=/opt/sandcast/zdotdir` in `/etc/zshenv`. zsh reads `/etc/zshenv` first and unconditionally, before any file in `$HOME`, and `/etc/zshenv` is outside the overmounted home. The curated zsh configuration moves from today's `/etc/zsh/zshrc.d/sandcast.zsh` into the `ZDOTDIR` tree (`/opt/sandcast/zdotdir/.zshrc`, plus `.zshenv`/`.zprofile` as needed). Because `ZDOTDIR` is set, zsh never reads `$HOME/.zshrc`, so the workspace rc vector is closed natively for zsh.
- **bash**: interactive shells are launched against a baked rcfile with `bash --noprofile --norc --rcfile /opt/sandcast/bashrc` through a small baked login launcher at `/opt/sandcast/shell-login`. `--noprofile --norc` guarantees bash reads neither `/etc/profile`-chained home files nor any `~/.bashrc`/`~/.bash_profile`/`~/.profile` in the workspace. The launcher is the login shell for the created user and the image `CMD`.
- **other login shells (sh, etc.)**: the minimal safe default (R2).

**Rationale**: zsh has a native, airtight relocation hook (`ZDOTDIR`); bash has none, so the only deterministic way to prevent it from reading workspace startup files is to control how the interactive shell is launched and pass `--noprofile --norc` with an explicit baked rcfile. Both keep all real configuration outside the home.

**Alternatives considered**: relying on `/etc/profile`/`/etc/bash.bashrc` alone (rejected: bash login still reads the first of `~/.bash_profile`/`~/.bash_login`/`~/.profile`, which the workspace can supply); `BASH_ENV` (rejected: applies to non-interactive shells only); deleting home rc files at build (rejected: the home is the mounted workspace at runtime, so build-time deletion has no effect).

## R2. Minimal safe default shell (FR-011)

**Decision**: when the user's login shell is neither zsh nor bash, the image does not fail. It sets the login shell to `/bin/sh`, provides the curated environment (PATH, prompt, the relocation variables) through `/etc/profile.d/sandcast.sh` (already read by a POSIX `sh` login), and capture records a notice that the shell was not curated. No per-user home files are written.

**Rationale**: `sh` login reading `/etc/profile.d` is universal and needs no home files, so it survives the overmount without extra machinery.

**Alternatives considered**: refusing to build (rejected: too strict); forcing zsh (rejected: changes the user's environment without consent).

## R3. Where relocation variables are set

**Decision**: set the relocation variables (`ZDOTDIR`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME` as needed, and per-tool variables such as `GIT_CONFIG_GLOBAL`) as image `ENV` in the Containerfile, emitted deterministically by the plan, not only in shell rc files.

**Rationale**: image `ENV` is inherited by every process OpenShell starts in the sandbox, including non-shell tools and processes not launched through an interactive shell. Setting them only in shell rc would miss those. This matches the existing `CLAUDE_CONFIG_DIR` precedent in `internal/harness/claudecode/scaffold.go`.

**Alternatives considered**: `/etc/environment` (rejected: read by PAM/login, not guaranteed for every container process); shell rc only (rejected: misses non-shell processes).

## R4. Bounded dotfile allowlist (FR-005)

**Decision**: v1 host-proposed allowlist, matched in the host home: `.gitconfig` and `.config/git/config` (git); `.config/starship.toml` (the prompt tool sandcast already supports); `.tmux.conf`; `.inputrc`. The shell configuration is already handled by the existing curation and is not duplicated here. Anything outside this set is added through explicit manifest paths (FR-006).

**Rationale**: a small, well-understood set covers the common environment with low risk; each maps to a tool with a clear relocation variable or a system path. Keeping it bounded avoids dragging in large or sensitive trees.

**Alternatives considered**: scanning all dotfiles (rejected: unbounded, credential risk, noise); shell-only (rejected: the spec scope includes general dotfiles).

## R5. Relocation targets per dotfile

**Decision**:
- git: bake to `/etc/gitconfig` (git reads it as system config natively) or set `GIT_CONFIG_GLOBAL` to a baked path; prefer `/etc/gitconfig`.
- starship: `STARSHIP_CONFIG=/opt/sandcast/xdg/starship.toml` (or `XDG_CONFIG_HOME`).
- tmux, inputrc and other XDG/home-relative configs: place under `/opt/sandcast/xdg` with `XDG_CONFIG_HOME=/opt/sandcast/xdg`, and for non-XDG tools set the tool's own path variable where one exists; otherwise document as a known limitation (candidate for the runtime-seeding escape hatch, which is not implemented here).

**Rationale**: prefer a system path a tool reads natively; otherwise a per-tool variable; otherwise XDG. Never a home-relative path.

**Alternatives considered**: home symlinks (rejected: hidden by the overmount).

## R6. Symlink / outside-project dotfile rule (Edge Cases)

**Decision**: reuse the capture skill-copy safety rules. Resolve symlinks; if a dotfile resolves outside the user's home (or into a disallowed location), skip it with a notice rather than copying. Never copy credential-named files (`.env`, `*.pem`, `*.key`, names containing token/secret/credential); run the copied set through `internal/redact`; show the copied file list for confirmation in the capture flow. A listed path that does not exist is reported and skipped, not fatal.

**Rationale**: consistency with the established, reviewed skill-copy behavior; keeps the credential guarantee.

**Alternatives considered**: following symlinks anywhere (rejected: escapes the project/home and risks copying secrets).

## R7. Reclassifying `/sandbox`-only probe hits (FR-009)

**Decision**: the probe continues to record each component's binary `Path` (it already does, in `CheckResult.Path`). In the plan delta, a component whose probe status is `present` but whose `Path` resolves under the overmount home prefix (a property of the base image, `/sandbox` today) is reclassified as not present, so the plan schedules it for installation to a persistent location (for example `/opt/mise`). The probe cache stores the raw result unchanged, so caching stays correct and deterministic; the reclassification is a pure function of the cached result plus the known overmount prefix, applied at plan time.

**Rationale**: keeps probe caching valid (no cache key change), keeps the decision deterministic, and localizes the new logic to the plan delta where install decisions already live.

**Alternatives considered**: changing the probe to treat `/sandbox` as absent (rejected: couples the probe to a runtime-mount fact and would invalidate caches); ignoring it (rejected: the tool vanishes at runtime).

## R8. Verifying survival against the base image

**Decision**: add an integration check (podman) that builds an image with the relocation in place, runs it with a volume mounted over the home, and asserts: zsh and bash load the baked configuration, a planted workspace `~/.zshrc`/`~/.bashrc` does not execute, and a baked git/tmux config is in effect. This verifies `ZDOTDIR`/`XDG`/launcher survival end to end rather than by assertion only.

**Rationale**: the overmount behavior is the whole point; a real mounted-over run is the only honest proof (constitution-aligned, matches the existing integration-test posture).

**Alternatives considered**: unit tests only (rejected: cannot observe the mount interaction).

## Open items deferred to implementation

- Non-XDG, home-hardcoding tools with no path variable: documented as a known limitation and the motivating case for the (unimplemented) runtime-seeding escape hatch.
- Exact `/etc/zshenv` interaction with a base image that already ships one: the build appends rather than overwrites, verified by R8.
