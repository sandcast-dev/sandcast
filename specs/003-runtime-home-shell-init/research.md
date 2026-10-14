# Research: Runtime home model and shell initialization

Phase 0 decisions. Each entry: Decision, Rationale, Alternatives considered.

## R1. Relocation mechanism per shell

**Decision** (as implemented; corrected after the code review of PR #15, see below):
- **zsh**: set `ZDOTDIR=/opt/sandcast/zdotdir` in the global zshenv, written to both `/etc/zsh/zshenv` and `/etc/zshenv`, and as image `ENV`. zsh reads its global zshenv first and unconditionally, before any file in `$HOME`, but its location is a build option: Debian and Ubuntu read `/etc/zsh/zshenv` and never `/etc/zshenv`. The curated zsh configuration moves from `/etc/zsh/zshrc.d/sandcast.zsh` into the `ZDOTDIR` tree (`/opt/sandcast/zdotdir/.zshrc`). Because `ZDOTDIR` is set, zsh never reads `$HOME/.zshrc`.
- **bash**: a baked launcher at `/opt/sandcast/shell-login` runs `bash --noprofile --rcfile /opt/sandcast/bashrc "$@"`. `--noprofile` keeps `~/.bash_profile`, `~/.bash_login` and `~/.profile` out, and `--rcfile` replaces `~/.bashrc`. The launcher is the final user's passwd login shell (also for a user kept from the base image) and the image `CMD`.
- **shells OpenShell starts directly**: OpenShell v0.1.x clears the environment and runs `/bin/bash` itself (`-i`, `-lc`, `-c`, `-l`); it never uses the `CMD`, the passwd shell or the image `ENV`. OpenShell images therefore carry two hooks that run before bash reads a home file: a block prepended to the system bashrc (`/etc/bash.bashrc`) that hands an interactive non-login bash to the curated shell, and `/etc/profile.d/zz-sandcast-shell.sh`, which replaces a login bash with the curated shell (interactive) or with a bash that reads no startup file (a command, keeping its arguments and exit status), and redirects `HOME` for other POSIX login shells. Both export the relocation variables again.
- **other login shells (sh, etc.)**: the minimal safe default (R2).

**Rationale**: zsh has a native relocation hook (`ZDOTDIR`); bash has none, so sandcast controls how bash starts (the launcher) and hooks the two system files bash reads before any home file (for the shells a runtime starts directly). All real configuration stays outside the home.

**Correction after review**: the original decision was `bash --noprofile --norc --rcfile ...` and `ZDOTDIR` in `/etc/zshenv` only. Both were wrong: bash ignores `--rcfile` whenever `--norc` is given, so the curated configuration never loaded, and Ubuntu's zsh never reads `/etc/zshenv`, so a cleared environment left `ZDOTDIR` unset. The original decision also assumed the runtime starts the `CMD` or the passwd shell, which OpenShell does not; the hooks were added for that.

**Alternatives considered**: relying on `/etc/profile`/`/etc/bash.bashrc` alone (rejected: bash login still reads the first of `~/.bash_profile`/`~/.bash_login`/`~/.profile`, which the workspace can supply); `BASH_ENV` (rejected: applies to non-interactive shells only); deleting home rc files at build (rejected: the home is the mounted workspace at runtime, so build-time deletion has no effect).

## R2. Minimal safe default shell (FR-011)

**Decision** (as implemented): when the user's login shell is neither zsh nor bash, the image does not fail. The login shell and `CMD` are a baked launcher, `/opt/sandcast/sh-login`, that points `$ENV` at the minimal `/etc/profile.d/sandcast.sh` and execs a non-login `/bin/sh`, dropping `-l` and `--login` from its options. Capture records a notice that the shell was not curated. No per-user home files are written.

**Rationale**: a login `sh` reads `$HOME/.profile` after `/etc/profile`, which is the workspace file FR-002 forbids, so the shell must never become a login shell. The original decision (login shell `/bin/sh`, relying on `/etc/profile.d`) was corrected for that reason; the option filter was added after review, because a caller passing `-l` reopened the same file.

**Alternatives considered**: refusing to build (rejected: too strict); forcing zsh (rejected: changes the user's environment without consent).

## R3. Where relocation variables are set

**Decision** (as implemented): set the relocation variables (`ZDOTDIR` and tool-specific variables such as `STARSHIP_CONFIG` and `INPUTRC`) as image `ENV` in the Containerfile, emitted deterministically by the plan, and export them again in the global zshenv and the shell hooks. A relocation variable wins over a component or harness value of the same name. `XDG_CONFIG_HOME` is never set.

**Rationale**: image `ENV` reaches processes that the image starts (the `CMD`, `podman run`). OpenShell clears the environment before it starts a shell, so the shell-side exports are what reach its sessions. The original decision also set `XDG_CONFIG_HOME` (and considered `XDG_DATA_HOME` and `GIT_CONFIG_GLOBAL`); review showed that an image-wide `XDG_CONFIG_HOME` pointing at a root-owned tree breaks every tool that writes its own configuration there (`gh`, `helm`), so generic files go to `/etc/xdg` (in the default `XDG_CONFIG_DIRS`) instead.

**Alternatives considered**: `/etc/environment` (rejected: read by PAM/login, not guaranteed for every container process); shell rc only (rejected: misses non-shell processes).

## R4. Bounded dotfile allowlist (FR-005)

**Decision**: v1 host-proposed allowlist, matched in the host home: `.gitconfig` and `.config/git/config` (git); `.config/starship.toml` (the prompt tool sandcast already supports); `.tmux.conf`; `.inputrc`. The shell configuration is already handled by the existing curation and is not duplicated here. Anything outside this set is added through explicit manifest paths (FR-006).

**Rationale**: a small, well-understood set covers the common environment with low risk; each maps to a tool with a clear relocation variable or a system path. Keeping it bounded avoids dragging in large or sensitive trees.

**Alternatives considered**: scanning all dotfiles (rejected: unbounded, credential risk, noise); shell-only (rejected: the spec scope includes general dotfiles).

## R5. Relocation targets per dotfile

**Decision** (as implemented, after review):
- git: bake to `/etc/gitconfig`, which git reads as system config natively. When both `~/.config/git/config` and `~/.gitconfig` are selected they are merged there in git's order (XDG first, `~/.gitconfig` last), because git reads both.
- starship: `/etc/xdg/starship.toml` with `STARSHIP_CONFIG`.
- tmux: `/etc/tmux.conf`, read natively. readline: `/opt/sandcast/inputrc` with `INPUTRC`, starting with `$include /etc/inputrc`.
- other configs: under `/etc/xdg`, keeping a `~/.config` subpath. Tools that hardcode a home path remain a known limitation (candidate for the runtime-seeding escape hatch, which is not implemented here).

The original decision placed these under `/opt/sandcast/xdg` with `XDG_CONFIG_HOME`; see R3 for why that was dropped.

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
- Exact global zshenv interaction with a base image that already ships one: the build appends to `/etc/zsh/zshenv` and `/etc/zshenv` rather than overwriting, verified by R8.
