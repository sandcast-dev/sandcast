# Brainstorm: Runtime home model and shell initialization

**Date:** 2026-10-14
**Status:** active

## Problem Framing

In an OpenShell sandbox the final user's home is `/sandbox`, and the Kubernetes driver mounts the workspace volume over that path at runtime.
The home is therefore shadowed (anything the image baked into it is hidden behind the mount), writable, and tied to the workspace rather than to the image.
This breaks the environment sandcast is supposed to reproduce in three ways:

1. Dotfiles and tool configuration baked into the home vanish behind the mount, so the sandbox does not have the user's environment.
2. The home is writable and holds a possibly untrusted project, so any rc file in it that the shell auto-sources is a code-execution vector (the earlier `compinit`/`.zcompdump` hardening closed only one instance of this class).
3. A base-image tool found only under `/sandbox` passes the probe but disappears at runtime once the workspace is mounted, so the sandbox is missing a tool sandcast reported as present.

sandcast already solves the shell-config case correctly: the curated shell file lives under `/etc` (`/etc/zsh/zshrc.d/sandcast.zsh`, `/etc/profile.d/sandcast.sh`) and is sourced from the system rc, which survives the mask.
This feature generalizes that proven pattern into a complete, deterministic model for the user's wider environment, and closes the writable-home execution vector.

Seeded from three idea-inbox entries: `dotfiles-in-masked-home`, `workspace-writable-rc-exec`, and `sandbox-only-tools-vanish-at-runtime`.

## Approaches Considered

### A: Env relocation (chosen primary)
Keep `HOME=/sandbox`, but point every program at baked configuration that lives outside the home.
zsh reads from `ZDOTDIR` (set in `/etc/zshenv`, which zsh reads first and which is not under the home) pointing at a baked tree such as `/opt/sandcast/zdotdir`.
XDG-aware apps read from `XDG_CONFIG_HOME` and per-tool variables (`GIT_CONFIG_GLOBAL=/etc/gitconfig`, and similar) pointing at baked paths.
bash uses the `/etc` system files.
- Pros: extends what sandcast already does for shell config; deterministic; read-only; no writes into the workspace; survives the overmount by construction.
- Cons: bash has no `ZDOTDIR` equivalent, so suppressing an auto-sourced `~/.bashrc` from the writable home needs a specific mechanism (open question); relies on the relocation variables reaching every relevant process.

### B: Relocate HOME
Set `HOME` to a baked path (for example `/opt/sandcast/home`) so ordinary `~/.x` dotfiles resolve to a non-masked location.
- Pros: simplest mental model; standard dotfile layout works unchanged.
- Cons: fights the OpenShell driver, which sets `HOME=/sandbox`; changes home-versus-workspace semantics that tools and the driver may depend on; unverified whether the driver overrides it. Rejected as primary.

### C: Runtime seeding (chosen escape hatch)
An init hook copies or symlinks baked dotfiles into the mounted home at shell start.
- Pros: works for tools that hardcode `~/` with no relocation variable.
- Cons: writes into the workspace (pollutes the project tree), needs the home writable, runs every session, and is non-deterministic. Kept only as an explicit, documented escape hatch for the few tools that leave no other option, never the primary path.

## Decision

Build the runtime home model on **env relocation (A) as the primary mechanism, with runtime seeding (C) as a documented escape hatch**. Approach B (relocating `HOME`) is rejected.

Scope is the three facets of the runtime-home problem; persisting runtime state or credentials across sandbox recreations is explicitly deferred to a later feature.

Policy decisions reached:
- **Untrusted home:** the baked configuration is authoritative and the shell never auto-sources rc files from the workspace home. This closes the execution vector. A user who wants project-local shell config captures it into the image explicitly rather than having the workspace sourced.
- **Dotfile source:** capture proposes a curated allowlist of known dotfiles it finds in the host home (confirmed through the existing multi-select flow) and the manifest also accepts explicit extra paths. Every baked dotfile passes the existing credential redaction, so no secret enters the image (constitution IV).
- **`/sandbox` handling:** sandcast bakes all configuration to the relocated prefix by construction (never under `/sandbox`), and treats a probe hit found only under `/sandbox` as missing so the component is re-provisioned to a persistent location. It prints a notice naming each relocation or re-provision.

## Key Requirements

- The sandbox's interactive environment is defined entirely by image-baked configuration outside the home, is deterministic, and survives the workspace overmount.
- Workspace-home rc files (`~/.zshrc`, `~/.bashrc`, `~/.zshenv`, and the like) are never auto-sourced by the baked init.
- zsh is driven by `ZDOTDIR` set in `/etc/zshenv`; bash by the `/etc` system files; app configuration by `XDG_CONFIG_HOME` and per-tool environment variables, all pointing at baked `/opt` or `/etc` paths.
- Capture proposes a curated dotfile allowlist from the host home and accepts explicit manifest paths; all baked dotfiles pass credential redaction.
- Nothing sandcast bakes lands under `/sandbox`; a base-image tool found only under `/sandbox` is treated as missing and re-provisioned to a persistent path.
- sandcast prints a notice for each path it relocates or component it re-provisions because of the overmount.
- Runtime seeding is available only as an explicit, documented escape hatch.
- Tests and documentation ship with the feature (constitution I); the build stays deterministic (constitution II); no credentials enter the image (constitution IV).

## Open Questions

- How does the baked init guarantee bash does not auto-source a `~/.bashrc` from the writable home, given bash has no `ZDOTDIR` equivalent? Candidate mechanisms (not sourcing, `BASH_ENV`, a guarded system bashrc) need a research spike.
- Confirm end to end against the OpenShell base image that `ZDOTDIR` and the XDG and per-tool variables survive the overmount and reach an interactive shell the way OpenShell starts it (it may not start a login shell).
- Where are the relocation variables set so every relevant process sees them deterministically: image `ENV`, `/etc/environment`, or the system rc?
- What is the exact curated dotfile allowlist (git, tmux, starship, editor basics, and which others)?
- How does this unify with the existing shell-curation flow so the two do not duplicate or conflict?
- How does "treat a `/sandbox`-only probed tool as missing" interact with the probe cache and the existing plan delta logic?
