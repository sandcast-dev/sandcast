# Changelog

All notable changes to sandcast are recorded here.
Changing a pin below requires an entry in this file.

## Unreleased

First release of the standalone sandcast tool (feature 001-sandcast-standalone-build).

### Core build (User Story 1)

- `sandcast init`, `manifest validate`, `base-images list`, `components list|show|refresh`, `lock`, `probe`, `plan`, `build`, `verify`, `profiles lint|export|import`, `version`.
- Base image probe cached by image digest and platform, with partial probes for new or changed components and `--no-cache`.
- Component tiers: shipped (25 components), remote registry with checksum-verified refresh, project components and promoted recipes.
- mise as the installer: generated `mise.toml`, `sandcast lock` through the host mise, locked installs into `/opt/mise` with shims on `PATH`.
- Deterministic build plan (`plan.yaml`), Containerfile assembled from fixed sections, two-pass build with the adapted profile set copied into the image and `io.sandcast.*` labels.
- OpenShell provider profiles: the 17 upstream examples vendored at v0.1.3, an overlay for npm, crates.io, the Go module proxy, GitLab and Claude Code subscription (OAuth) login, built-in lint mirroring v0.1.3, adaptation of `binaries` to resolved paths, and import through the `openshell` CLI at workspace or platform scope.
- Image-embedded OpenShell policy from the pinned default plus component needs; no network entries.
- Redaction of credential shapes in manifests, fragments and every build output.

### Capture and harnesses (User Stories 2 and 4)

- Skill bundle (`/sandcast-capture`, `/sandcast-build`) with `sandcast skills install|check` for Claude Code.
- `sandcast components match` for project, host, harness and always proposals; `sandcast manifest set-shell`.
- Claude Code adapter: discovery of MCP servers, plugins and skills with secrets replaced by placeholders, a `70-harness` section with the configuration in `/opt/sandcast/claude-code`, and generated `mcp-<name>` profiles for MCP endpoints no agent profile covers.

### Repair, promotion and container target (User Stories 3 and 5)

- `sandcast fragments list|write`, `sandcast learnings add`, `sandcast promote`; stored fragments are reused by non-interactive builds and reported as stale once a recipe covers them.
- `--target container`: no OpenShell policy or sandbox user conventions, and an `allowed-domains.txt` from the referenced profiles.

### Runtime home model and shell initialization (feature 003)

- The interactive environment now survives the OpenShell workspace mount over the home. Everything is baked outside the home and programs are pointed at it through environment variables: zsh sets `ZDOTDIR=/opt/sandcast/zdotdir` in the global zshenv, written to both `/etc/zsh/zshenv` (Debian and Ubuntu) and `/etc/zshenv` (other builds), and as image `ENV`, so it holds when a runtime clears the environment; bash uses a baked `/opt/sandcast/shell-login` launcher that runs `bash --noprofile --rcfile /opt/sandcast/bashrc` (without `--norc`, which would make bash ignore the rcfile) and is the login shell and `CMD`, also for a user kept from the base image (`usermod -s`, with a passwd fallback); any other login shell gets a baked `/opt/sandcast/sh-login` launcher that points `$ENV` at `/etc/profile.d/sandcast.sh` and execs a non-login `/bin/sh`. Both launchers drop `-l` and `--login` from the forwarded options (`-ilc` becomes `-ic`), so a caller that asks for a login shell (`su -l`, an editor's environment probe) cannot make the shell read a workspace `.profile` or `.bash_logout`, and the bash launcher keeps its rcfile. The bash launcher exports the pid guard the bashrc hook recognizes only when its bash will be interactive; a `shell-login -c 'cmd'` run removes it, so an interactive bash that the command execs with the same pid still goes through the hook. Because OpenShell starts `/bin/bash` itself after clearing the environment (`-i` for terminals, `-lc` for `sandbox exec`, `-l` for its default agent), OpenShell images also carry two hooks: `/etc/profile.d/zz-sandcast-shell.sh` replaces a login bash before it reads a home profile or logout file (keeping the command, arguments, options such as `-e`, `-u`, `-x` and `-o pipefail`, and the exit status; a script is passed after `--` and a renamed bash still reads its input) and redirects `HOME` for other POSIX login shells, and a block prepended to the system bashrc hands an interactive bash to the curated shell before it reads `~/.bashrc`. Both export the relocation variables again. The build bakes the absolute path of the image's bash into the launcher, so an explicit `/bin/bash` handed to it by a hook never resolves bash through a `PATH` that the curated rc extended with the workspace `$HOME/bin`. When the curated shell cannot be executed, an interactive session prints one `sandcast:` line naming the target and the `/opt/sandcast` policy grant and continues in `bash --noprofile --norc` instead of ending with status 126. The image policy grants `/opt/sandcast` read-only. bash builds without a system bashrc (Fedora, RHEL) cannot be hooked for interactive non-login shells; the build guide lists this limitation.
- A startup file in the mounted workspace home (a committed `.zshrc`, `.bashrc` or `.profile`) is never auto-sourced, closing a code-execution vector for untrusted projects.
- New manifest `dotfiles` field: capture proposes a bounded host-home allowlist (git, starship, tmux, readline) and accepts explicit paths. Each dotfile is redacted and relocated outside the home to a location its tool reads natively (`/etc/gitconfig`, `/etc/tmux.conf`, `/etc/xdg/...`) or one named by a tool-specific variable (`STARSHIP_CONFIG`, `INPUTRC`; the baked inputrc includes `/etc/inputrc`). `XDG_CONFIG_HOME` is never set image-wide, so tools such as `gh` can still write their configuration. Colliding entries dedup deterministically (explicit wins) with a notice, except git's two files: `~/.config/git/config` and `~/.gitconfig` are merged into `/etc/gitconfig` in git's order, so both take effect and `~/.gitconfig` values win. Sources are bounded so a committed manifest cannot pull host secrets into an image: from the home only allowlist entries and files under `~/.config`, otherwise only files that resolve inside the project; credential stores (`.ssh`, `.aws`, `.gnupg`, `.kube`, `.docker`, `.config/gh`), `.netrc`, history files and credential-looking names are refused before and after following symlinks; only regular files up to 256 KiB are read. Capture proposes an allowlist entry only within the same bounds (the whole path is resolved, so a symlinked `~/.config` that leaves the home is caught), and `sandcast components match` reports a host home it cannot determine instead of proposing nothing silently. An entry outside the bounds, missing or unreadable, or whose relocation target would carry shell metacharacters, is skipped with a notice rather than baked.
- The plan reclassifies a base-image tool found only under the overmount home as missing and re-provisions it to a persistent location, reporting every relocation and re-provision as a notice. The probe cache is unchanged. The plan fails when anything it bakes or declares would lie under the overmount home: shell files, dotfile targets, component and harness directories, and component, harness and fragment policy paths.
- The overmount home is a property of the base image: a registry entry may set `overmount_home` (openshell entries only, default `/sandbox`), and the plan records the effective value as `base_image.overmount_home`, which the `WORKDIR`, the workspace directory, the re-provisioning and the guard all use.

### Pins

- OpenShell v0.1.3 (commit e1f3c82c): vendored example profiles, profile lint rules, default sandbox policy, `openshell` CLI for the import step, gateway image `ghcr.io/nvidia/openshell/gateway:0.1.3` for `make test-gateway`.
- mise v2026.10.4 inside images; host `mise` 2026.9.16 or newer for `sandcast lock`.
- Remote component registry v0.1.0.
- Manifest schema version 1, plan schema version 1, skill bundle 0.1.3 (0.1.3 describes the bounded dotfile sources and the shell hook paths).

### OpenShell pin refresh v0.1.2 to v0.1.3

- Re-vendored the upstream provider profiles from OpenShell v0.1.2 (commit 6648bd0c) to v0.1.3 (commit e1f3c82c). The 16 existing profiles are byte for byte identical; `oci-genai` is new, for 17 in total.
- Default sandbox policy moved from `policy/default-v0.1.2.yaml` to `policy/default-v0.1.3.yaml`; `restrictive_default_policy()` content is unchanged between the two releases.
- Ported the v0.1.3 stable profile validation into `internal/profile`: credential names and token_grant endpoint and audience-override selectors must be free of control characters; a token_grant endpoint may not use protocol `sql` or `tls: skip`; `2026-07-28` joins the supported MCP protocol revisions (the default revision stays `2025-11-25`); and a null value at a schema-declared object path is rejected before typed decoding.
- Relaxed the `sandcast profiles import` version check: it now accepts any `openshell` CLI release on the pinned minor line (any `0.1.x` for the v0.1.3 pin, pre-release suffixes included) instead of an exact match, so a future patch release needs no code change.
- Deferred to a follow-up: the experimental `ProviderFileProfile` (`files`) feature and the token_grant audience-override to endpoint binding-conflict checks, both of which upstream marks as subject to change.
