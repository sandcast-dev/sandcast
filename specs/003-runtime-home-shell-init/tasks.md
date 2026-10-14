---
description: "Task list for runtime home model and shell initialization"
---

# Tasks: Runtime home model and shell initialization

**Input**: Design documents from `specs/003-runtime-home-shell-init/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: included (constitution I requires tests with every feature).

**Organization**: grouped by user story. MVP is US1 + US2 (shell initialization correct and safe under the overmount).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1, US2, US3, US4

## Phase 1: Setup

- [x] T001 Add the relocation-prefix constants (`/opt/sandcast/zdotdir`, `/opt/sandcast/xdg`, `/opt/sandcast/bashrc`, `/opt/sandcast/shell-login`, `/etc/zshenv`) and the base-image overmount-home prefix accessor in `internal/plan/plan.go` (near the existing `ZshRCPath`/`BashRCPath` constants), with doc comments referencing the spec.

## Phase 2: Foundational (blocking prerequisites)

- [x] T002 Add a `RelocationEnv` helper in `internal/plan/plan.go` that returns the deterministic, sorted relocation environment variables (`ZDOTDIR`, `XDG_CONFIG_HOME`, and per-tool vars) to emit as image `ENV`, and wire it into the plan's `ImageSetup.Env` emission so the variables are set image-wide (not only in shell rc). Reference the `CLAUDE_CONFIG_DIR` precedent in `internal/harness/claudecode/scaffold.go`.
- [x] T003 [P] Add a `RelocationNotice` type and a sorted notices accumulator to `internal/plan/plan.go` per `contracts/relocation-notice.md` (kind relocated|reprovisioned, subject, from, to), surfaced in the plan output.
- [x] T004 [P] Unit test the relocation env and notice ordering determinism in `internal/plan/plan_test.go`.

## Phase 3: User Story 1 - Curated shell survives the overmount (P1) 🎯 MVP

**Goal**: an interactive zsh/bash shell in the sandbox loads the baked configuration even with the workspace mounted over the home.

**Independent test**: build an image, run it with a volume over `/sandbox`, open zsh and bash, confirm curated prompt/aliases/completions/env are active.

- [x] T005 [US1] Extend `internal/shell/shell.go` to render the curated zsh configuration as a `ZDOTDIR` tree (`.zshrc`, and `.zshenv`/`.zprofile` as needed) instead of only a single rc fragment, keeping the curated content identical.
- [x] T006 [US1] Extend `internal/shell/shell.go` to render the curated bash configuration as a standalone baked rcfile (`/opt/sandcast/bashrc`) suitable for `--rcfile`.
- [x] T007 [US1] In `internal/build/sections.go`, change the shell section to: write `/etc/zshenv` that sets `ZDOTDIR`; `COPY` the zdotdir tree to `/opt/sandcast/zdotdir`; bake `/opt/sandcast/bashrc`; stop appending source lines into `~/.zshrc`/`~/.bashrc` fallbacks.
- [x] T008 [US1] In `internal/build/sections.go` (extend the `loginShell` helper at line ~104 and the `CMD` template), bake the `/opt/sandcast/shell-login` launcher that execs `bash --noprofile --norc --rcfile /opt/sandcast/bashrc` for the bash case, set it as the created user's login shell and image `CMD`; for zsh, keep zsh as the login shell (ZDOTDIR handles relocation).
- [x] T009 [P] [US1] Unit test the shell rendering (zdotdir tree + bash rcfile) in `internal/shell/shell_test.go`.
- [x] T010 [US1] Regenerate and review the Containerfile golden files (`internal/build/testdata/Containerfile*.golden`, `internal/plan/testdata/plan.yaml.golden`) with `UPDATE_GOLDEN=1 go test ./...`; confirm only shell/ENV/launcher lines changed.

**Checkpoint**: zsh and bash load baked config; US1 independently testable.

## Phase 4: User Story 2 - The writable workspace home is never executed (P1) 🎯 MVP

**Goal**: a startup file in the mounted workspace home is never auto-sourced.

**Independent test**: plant `~/.zshrc`/`~/.bashrc` that touch a sentinel in the workspace; open shells; confirm no sentinel.

- [x] T011 [US2] Verify and lock in that `ZDOTDIR` (zsh) and `--noprofile --norc --rcfile` via the launcher (bash) prevent sourcing any `$HOME` startup file; add explicit assertions in a build-package test (new file `internal/build/sections_test.go`, or the existing `internal/build/assemble_test.go`) that the generated bash launcher carries `--noprofile --norc` and the zsh path sets `ZDOTDIR`.
- [x] T012 [US2] Implement the minimal safe default for non-zsh/bash login shells in `internal/build/sections.go` / `internal/plan/plan.go`: login shell `/bin/sh`, curated env via `/etc/profile.d/sandcast.sh`, and a capture notice; add a unit test.
- [x] T013 [P] [US2] Unit test (in `internal/plan/plan_test.go` or the build-package test from T011) that no generated image file sources a path under the overmount home, across zsh/bash/sh.

**Checkpoint**: US1 + US2 together = MVP (shell init correct and safe).

## Phase 5: User Story 3 - Dotfiles beyond the shell survive (P2)

**Goal**: selected host/manifest dotfiles are baked via relocation and used by their tools.

**Independent test**: capture a `.gitconfig` and `.tmux.conf`, build, run, confirm the tools read them; a token-shaped value is redacted.

- [x] T014 [US3] Add the `dotfiles` field to the manifest schema in `internal/manifest/` per `contracts/manifest-dotfiles.md` (load, validate, reject a relocation under the overmount home), with version-gate handling if required. Note the field-naming split: the manifest input is a list of `{path: ...}` entries (the contract shape), while the internal resolved `DotfileSelection` entity (data-model.md) carries the richer `source`/`host_path`/`relocated_to`/`env` fields derived during planning; do not expose the resolved fields in the manifest.
- [x] T015 [P] [US3] Add the bounded dotfile allowlist (R4: `.gitconfig`, `.config/git/config`, `.config/starship.toml`, `.tmux.conf`, `.inputrc`) and host-home matching to `internal/capture/` (new match path or extension of `match.go`), reusing symlink/outside-home and credential-name rules (R6). A listed or proposed dotfile that does not exist at capture/build time is reported and skipped, never fatal (spec Edge Case).
- [x] T016 [US3] Map each selected dotfile to its relocation target and env (R5) in `internal/plan/plan.go`, run contents through `internal/redact`, and emit a `relocated` notice per dotfile. When two sources (host allowlist and an explicit manifest path) resolve to the same dotfile, deduplicate deterministically (explicit manifest path wins) and report the resolution with a notice (spec Edge Case).
- [x] T017 [US3] In `internal/build/sections.go`, add a section that `COPY`s the redacted dotfiles to their relocation targets (`/etc/gitconfig`, `/opt/sandcast/xdg/...`) and sets per-tool env via the plan's `RelocationEnv`.
- [x] T018 [US3] Update `skills/capture/SKILL.md` to propose the dotfile allowlist through the multi-select flow (as MCP/plugins/skills already are) and write confirmed entries plus explicit paths to the manifest; bump `skills/VERSION` and both SKILL.md `sandcast_version` frontmatters.
- [x] T019 [P] [US3] Unit tests: manifest dotfiles load/validate (`internal/manifest/*_test.go`), allowlist matching (`internal/capture/*_test.go`), redaction of a token-shaped dotfile value, relocation targets and golden (`internal/build`, `internal/plan`).

**Checkpoint**: US3 independently testable on top of MVP.

## Phase 6: User Story 4 - Nothing vanishes under the overmount (P3)

**Goal**: a `/sandbox`-only base tool is reprovisioned; nothing baked lands under the overmount; decisions are reported.

**Independent test**: probe a base image whose only copy of a tool is under `/sandbox`; confirm the plan reports a reprovision and installs it elsewhere.

- [x] T020 [US4] In the `internal/plan` delta, reclassify a component whose probe `status==present` but whose `Path` is under the overmount home prefix as missing (schedule install to a persistent location) and emit a `reprovisioned` notice (R7); do not change probe caching.
- [x] T021 [US4] Add a plan guard that asserts no sandcast-managed baked path resolves under the overmount home, emitting a `relocated` notice where it corrects one.
- [x] T022 [P] [US4] Unit tests in `internal/plan/plan_test.go`: reclassification of a `/sandbox` probe hit, notice emission, and the no-baked-path-under-overmount guard.

## Phase 7: Polish and cross-cutting

- [x] T023 [P] Add the integration test from research R8 to `tests/integration/` (build-tagged): build with relocation, run with a volume mounted over the home, assert zsh/bash load baked config, planted workspace rc does not run, and a baked git/tmux config is in effect. Include a read-only-home case (mount the workspace over the home read-only) and assert the baked config still loads (spec Edge Case).
- [x] T024 [P] Update docs: `docs/guides/capture.md` (dotfile capture), `docs/guides/build.md` (home model and shell init under the overmount), `docs/configuration.md` (manifest `dotfiles` field).
- [x] T025 [P] Add a `CHANGELOG.md` entry for the runtime home model and shell initialization feature.
- [x] T026 Run `make fmt && make verify` and resolve any failures; confirm goldens are current and lint is clean.

## Dependencies and order

- Setup (T001) → Foundational (T002-T004) → US1 (T005-T010) → US2 (T011-T013) → US3 (T014-T019) → US4 (T020-T022) → Polish (T023-T026).
- US1 and US2 share the shell/launcher code; US2 largely validates and extends US1, so they land together as the MVP.
- US3 and US4 depend on Foundational (env + notices) but are independent of each other.

## Parallel execution examples

- Foundational: T003 and T004 in parallel after T002.
- US1: T009 in parallel with T010 prep; T005 and T006 touch the same file (`shell.go`) so run sequentially.
- US3: T015 and T019 subtasks across `manifest`/`capture`/`build` packages can parallelize where files differ.
- Polish: T023, T024, T025 in parallel; T026 last.

## MVP scope

US1 + US2 (T001-T013): the shell initializes correctly from baked config under the overmount and never executes the writable workspace home. US3 (dotfiles) and US4 (/sandbox reprovision) are incremental additions.
