# Implementation Plan: Runtime home model and shell initialization

**Branch**: `003-runtime-home-shell-init` | **Date**: 2026-10-14 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/003-runtime-home-shell-init/spec.md`

## Summary

Make the sandbox's interactive environment survive the OpenShell home overmount by defining it entirely in image-baked locations outside the home and pointing programs at them through environment variables. zsh uses `ZDOTDIR` set in the global zshenv (`/etc/zsh/zshenv` and `/etc/zshenv`) and as image `ENV`; bash uses a baked launcher that runs `bash --noprofile --rcfile /opt/sandcast/bashrc`; OpenShell images also hook the system bashrc and `/etc/profile.d` for the shells OpenShell starts directly after clearing the environment; dotfiles go to system paths the tools read natively (`/etc/gitconfig`, `/etc/tmux.conf`, `/etc/xdg`) or to paths named by per-tool variables. (The original plan said `--noprofile --norc`, `/etc/zshenv` only and `XDG_CONFIG_HOME`; all three were corrected after the code review, see research R1, R3 and R5.) The writable workspace home is never auto-sourced. Capture gains a bounded dotfile allowlist (host-proposed, confirmed) plus explicit manifest paths, all passed through the existing redaction. The plan reclassifies any base-image tool found only under the overmount as missing so it is re-provisioned to a persistent path, and reports every relocation or re-provision.

## Technical Context

**Language/Version**: Go 1.25

**Primary Dependencies**: existing internal packages (`shell`, `build`, `plan`, `capture`, `probe`, `redact`, `manifest`, `harness/claudecode`); no new external dependency

**Storage**: files (`.sandcast/manifest.yaml`, the build context, the image); no database

**Testing**: `make test` (Go unit + golden), `make test-integration` (podman), `make test-gateway` (OpenShell v0.1.3)

**Target Platform**: Linux container images built with podman/docker; OpenShell sandbox runtime

**Project Type**: Go CLI plus a portable skill bundle

**Performance Goals**: no runtime cost; the feature is static image build configuration

**Constraints**: deterministic build (same inputs produce the same image configuration); no credentials in the image; nothing baked under the overmount home; podman-first

**Scale/Scope**: touches `internal/shell`, `internal/build`, `internal/plan`, `internal/capture`, `internal/manifest`, `internal/probe`, the capture skill, and docs. Bounded dotfile allowlist (about 5 entries for v1).

## Constitution Check

*GATE: must pass before Phase 0 research and again after Phase 1 design.*

- **I. Tests and documentation**: PASS. Every new behavior ships Go unit tests and golden coverage; docs updated in `docs/guides/capture.md`, `docs/guides/build.md`, `docs/configuration.md`. (FR-014)
- **II. Determinism before convenience**: PASS. The mechanism is static image `ENV` and baked files, computed deterministically in the plan; no runtime writes, no nondeterministic seeding. Golden files assert the Containerfile sections. (FR-004, SC-005)
- **III. Upstream formats over home-grown ones**: PASS. Uses the standard `ZDOTDIR`, the default `XDG_CONFIG_DIRS`, per-tool environment conventions and `/etc` system files rather than a sandcast-invented scheme.
- **IV. No credentials, ever**: PASS. Dotfiles flow through the existing `internal/redact` detection and placeholder rules before entering the build context. (FR-007, SC and US3-AS3)
- **V. Build and tool rules**: PASS. Uses make targets; podman-first; no docker-specific features; golden regen via `UPDATE_GOLDEN=1 go test ./...`.

Post-design re-check: still PASS. No new subsystem; the design extends existing packages and image sections.

## Project Structure

### Documentation (this feature)

```text
specs/003-runtime-home-shell-init/
├── plan.md              # this file
├── research.md          # Phase 0: decisions (mechanism, defaults, allowlist, rules)
├── data-model.md        # Phase 1: entities (dotfile selection, relocation record)
├── quickstart.md        # Phase 1: end-to-end validation scenarios
├── contracts/           # Phase 1: manifest dotfiles field, relocation notice shape
└── tasks.md             # Phase 2 (/speckit-tasks)
```

### Source code (repository root)

```text
internal/
  shell/        # curated shell config: extend to emit a ZDOTDIR tree and a baked bash rcfile
  build/        # sections.go: new relocation ENV, /etc/zshenv, baked rcfiles, home-launcher; golden updates
  plan/         # emit relocation ENV deterministically; reclassify /sandbox-only probe hits as missing
  capture/      # dotfile allowlist matching against the host home; manifest extra paths
  manifest/     # schema: dotfiles selection (allowlist confirmations + explicit paths)
  probe/        # expose binary Path so the plan can detect /sandbox-only tools (already present)
  redact/       # reused unchanged for dotfile contents
  harness/claudecode/  # align with the shared relocation prefix (CLAUDE_CONFIG_DIR precedent)
skills/capture/ # SKILL.md: propose dotfiles via the multi-select flow
docs/           # guides/capture.md, guides/build.md, configuration.md
```

## Phase 0: Research

See [research.md](./research.md). It resolves: the env-relocation mechanism per shell (zsh `ZDOTDIR`, bash baked launcher with `--noprofile --rcfile`, sh minimal default, plus the hooks for shells OpenShell starts directly), where relocation variables are set (image `ENV`), the bounded dotfile allowlist, the symlink/outside-project rule, and how `/sandbox`-only probe hits are reclassified in the plan delta without breaking the probe cache.

## Phase 1: Design and Contracts

- [data-model.md](./data-model.md): the dotfile selection entity, the relocation prefix layout, and the relocation notice.
- [contracts/](./contracts/): the manifest `dotfiles` field and the plan relocation-notice shape.
- [quickstart.md](./quickstart.md): runnable validation scenarios mapped to the spec's user stories.
