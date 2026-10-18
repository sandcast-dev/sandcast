# Implementation Plan: Field Feedback Fixes from First Real-World Use

**Branch**: `002-field-feedback-fixes` | **Date**: 2026-10-13 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/002-field-feedback-fixes/spec.md`

## Summary

Eleven open issues from the first real builds share one cause: an image passes `sandcast build --verify`, but the sandbox started from it differs from what the inputs promised, or the user has to find facts sandcast already has. The fixes stay inside the existing architecture (Go core plus skill bundle) and touch four areas:

1. **Plan and probe** compare probed versions against `mise.lock`, record every binary path, and treat binaries that live only in the workspace path as missing (R-01 to R-03).
2. **Image rendering and verify** move the session environment into shell startup files, protect `PATH` from home rc files, switch interactive sessions to zsh, and run session-condition checks in verify (R-04 to R-08).
3. **Build report, engine, gateway and init** report the normalized reference, the push result and digest, and warn about reference, emulation and remote-gateway pitfalls without touching the plan file (R-11 to R-17).
4. **Capture and discovery** report pinned versions, declared binaries, git remotes for local marketplaces and portability markers; skill snippets run in any shell (R-09, R-10, R-18, R-19).

Decisions and evidence are in [research.md](research.md), entity changes in [data-model.md](data-model.md), interface changes in [contracts/](contracts/), and validation scenarios in [quickstart.md](quickstart.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25` with `toolchain go1.25.1`), unchanged.

**Primary Dependencies**: Unchanged: `spf13/cobra`, `goccy/go-yaml`, `BurntSushi/toml`, `stretchr/testify`. No new module dependency: image reference normalization (R-12) and the `go.mod` directive reader (R-09) are small in-repo helpers. External executables through `os/exec`: `podman` or `docker`, `mise` (host, `sandcast lock`), `openshell` v0.1.3 (import step and the new best-effort gateway lookup), `git` (discovery, new, optional: absent git means local marketplaces are skipped as today).

**Storage**: Files only. Probe cache format version 2 (R-02) invalidates old check and facts entries by directory name. No other storage change.

**Testing**: `go test` table tests and golden files (plan, fragments, Containerfile, verify report, build report). New: snippet execution test under sh, bash and zsh (R-19), discovery tests with real throwaway git repos in `t.TempDir()`, `imageref` table tests, a podman integration test that reproduces the OpenShell session environment (`integration` tag), and a gateway test that runs `openshell sandbox exec` against a v0.1.3 gateway (`gateway` tag). Golden files regenerate with `UPDATE_GOLDEN=1 go test ./...`.

**Target Platform**: Unchanged. Binary on macOS and Linux (amd64, arm64); images `linux/amd64` or `linux/arm64`.

**Project Type**: Single Go module, CLI with embedded assets and the skill bundle.

**Performance Goals**: The extra probe work (walking `PATH` per binary, one rc scan) adds under one second to a full probe. Session checks add at most one extra container command block to verify, not an extra container.

**Constraints**: Byte-identical plans for identical inputs (FR-026 of 001): host, engine and gateway facts never enter the plan file (R-13, R-15). Plan schema version stays 1 and manifest schema version stays 1 (R-10, R-15). No engine-specific build features (constitution V): the digest read differs per engine but both use their standard CLI. Nothing sandcast installs may live under the workspace path (001 R-10, reinforced by R-03). No credentials in discovery output, remote URLs included (R-18).

**Scale/Scope**: 11 issues, 25 functional requirements, about 14 packages touched, two skill files, four docs files plus one new guide.

## Global Constraints

Every task inherits these rules, copied from the spec, the clarifications and the constitution:

- "The plan MUST be deterministic: the same manifest, the same lockfile, the same component and profile definitions, the same base image digest and the same target platform MUST yield an identical plan." (001 FR-026). Engine, host and gateway facts go to the console and the build report only (spec FR-016, R-15).
- "When they differ, or when the present version cannot be determined, the component MUST be planned as `shadow` with the locked version." (FR-001, clarified)
- Session checks run on every verify for the OpenShell target, with no flag to skip them (FR-010, clarified).
- With `shell.program: zsh`, the image carries both the guarded switch hook and the zsh login shell for the final user (FR-008, clarified).
- `sandcast init` stays non-interactive; remote-gateway values arrive through flags (FR-015, clarified).
- "No credential value may appear in the manifest, the build plan, stored fragments, adapted profiles, the image, or the logs." (constitution IV). Git remote URLs are stripped of userinfo before they appear anywhere (FR-019).
- "Use make targets (`make test`, `make lint`, `make verify`). Never run `go build` directly." (constitution V)
- Pins unchanged: OpenShell v0.1.3, mise v2026.10.4, manifest schema 1, plan schema 1. Probe cache format becomes 2. Skill bundle version becomes 0.2.0.
- Documentation ships in the same phase as the change (constitution I).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | How the design satisfies it | Pre-research | Post-design |
|-----------|-----------------------------|--------------|-------------|
| I. Tests and documentation | Every requirement has a unit or golden test; session behavior has podman integration and gateway tests (FR-025); snippets have an execution test. `docs/cli.md`, `docs/configuration.md`, `docs/guides/build.md`, `docs/guides/capture.md`, README and the new `docs/guides/cross-arch.md` are tasks inside each phase. | PASS | PASS |
| II. Determinism before convenience | Lockfile comparison and workspace rule use only plan inputs (R-01, R-03). Host, engine and gateway notices stay out of the plan file (R-13, R-15). Discovery reads local git state only, no fetch (R-18). Pinned versions are facts reported by the binary; the skill only proposes them (FR-005). | PASS | PASS |
| III. Upstream formats | No profile schema change. mise lockfile read through its own format. OpenShell behavior is matched, not patched; the upstream login-shell request is a separate, user-approved issue (R-20). | PASS | PASS |
| IV. No credentials | Remote URLs pass through `harness.SplitUserinfo`; the build report holds references and digests only; the session env script holds only `PATH` and `MISE_*` values that already sit in the image `ENV`. | PASS | PASS |
| V. Build and tool rules | Make targets only. `Push` and `HostArch` are implemented for podman and docker through their standard CLIs. Skill changes are covered by structure and execution tests. | PASS | PASS |

No violations. Complexity Tracking stays empty.

## Project Structure

### Documentation (this feature)

```text
specs/002-field-feedback-fixes/
├── spec.md              # Feature specification (clarified)
├── plan.md              # This file
├── research.md          # Phase 0: decisions R-01 to R-20
├── data-model.md        # Phase 1: entity changes
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── cli.md           # Changed commands, flags, console and JSON output
│   ├── manifest-component.md  # Manifest `bins`, component `emulation_unsafe`
│   ├── reports.md       # Plan notices, build report, verify report, discovery and match JSON
│   └── image-shell.md   # Files the image carries for the session environment
├── checklists/requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

Only paths this feature touches:

```text
internal/
├── mise/lock.go                 # Locked(tool, platform) (R-01)
├── probe/                       # script.go, parse.go, types.go, cache.go: paths, home_rc_resets_path, FormatVersion 2 (R-02, R-05)
├── plan/                        # delta.go, plan.go, user.go, fragments.go, encode.go: lock comparison, workspace rule, notices, env script, declared bins (R-01, R-03, R-04, R-10, R-15)
├── build/                       # sections.go, assemble.go, build.go: 60-shell rendering, login shell, push logging, Result fields (R-06, R-07, R-11)
├── verify/verify.go             # session requests, Result.Condition (R-08)
├── engine/                      # engine.go, exec.go, fake.go: Push digest, HostArch (R-11, R-13)
├── imageref/                    # new: Normalize, DockerHubNoNamespace (R-12)
├── gateway/openshell.go         # ActiveGateway (R-14)
├── manifest/manifest.go         # ComponentRef.Bins, Warnings(), Skeleton options (R-10, R-16, R-17)
├── component/types.go           # EmulationUnsafe (R-13)
├── capture/                     # match.go, new pins.go (R-09)
├── harness/                     # adapter.go (Extension, Env.Git), claudecode/discover.go (R-18)
├── pipeline/                    # pipeline.go, build.go, profiles.go, validate.go: runtime notices, gateway warnings, tier-aware bins check (R-10, R-13, R-14)
├── cli/                         # init.go, build.go, plan.go, manifest.go, fragments.go (promote), profiles.go
└── skills/                      # structure_test.go, new snippets_test.go (R-19)
components/rust.yaml             # emulation_unsafe: true
skills/                          # VERSION 0.2.0, capture/SKILL.md, build/SKILL.md
docs/                            # cli.md, configuration.md, guides/build.md, guides/capture.md, new guides/cross-arch.md
README.md
.github/workflows/ci.yml         # install zsh on ubuntu-latest
tests/
├── fixtures/pinned-project/     # new: rust-toolchain.toml, .python-version, go.mod, go.work, package.json, .tool-versions
└── integration/                 # core_test.go (session env), gateway_test.go (session via openshell sandbox exec)
```

**Structure Decision**: No new top-level directories. One new internal package (`internal/imageref`) because both the build report and manifest validation need reference normalization and neither package should own it.

## Delivery Phases

The spec's three phases map to three task groups on one branch, each closing its issues and shipping its docs. Phases are independent except that phase 2's build-report `notices` field (R-15) is introduced in phase 1, because phase 1 notices must already reach the report.

| Phase | Scope | Research | Closes |
|-------|-------|----------|--------|
| 1. Image correctness | FR-001 to FR-010, FR-025 | R-01 to R-08, R-15 (plan side and report `notices`) | [#1](https://github.com/sandcast-dev/sandcast/issues/1), [#2](https://github.com/sandcast-dev/sandcast/issues/2), [#3](https://github.com/sandcast-dev/sandcast/issues/3) (binary side, FR-004), [#11](https://github.com/sandcast-dev/sandcast/issues/11) image part |
| 2. Delivery to the gateway | FR-011 to FR-017 | R-11 to R-14, R-16, R-17 | [#8](https://github.com/sandcast-dev/sandcast/issues/8), [#9](https://github.com/sandcast-dev/sandcast/issues/9), [#10](https://github.com/sandcast-dev/sandcast/issues/10) |
| 3. Capture facts | FR-005, FR-018 to FR-023 | R-09 (skill side), R-10, R-18, R-19 | [#4](https://github.com/sandcast-dev/sandcast/issues/4), [#5](https://github.com/sandcast-dev/sandcast/issues/5), [#6](https://github.com/sandcast-dev/sandcast/issues/6), [#7](https://github.com/sandcast-dev/sandcast/issues/7), [#11](https://github.com/sandcast-dev/sandcast/issues/11) capture part, [#3](https://github.com/sandcast-dev/sandcast/issues/3) skill side |

Note: the spec places FR-004 (pinned versions in `components match`) under "image matches its inputs"; the binary side ships in phase 1 and the skill side (FR-005) in phase 3, together with the skill version bump.

**Phase 1 ordering risk**: the `DEBUG` trap for `bash -lc` (R-05) is a spike that runs first in phase 1. Its outcome decides between the trap and the `/usr/local/bin` wrapper fallback before the rendering tasks start.

## Risks

| Risk | Mitigation |
|------|------------|
| `DEBUG` trap does not fire as expected under `bash -lc` on bash 5.2 | Spike first (R-05); fallback wrappers; the session check in verify catches regressions. |
| Shadowing every differing locked tool grows images (`node`, `python`, `gh`, `codex` on the community base) | Accepted in the clarification; the plan notice names each shadow so users can relock or pin. |
| Probe cache format bump forces one full re-probe for every user | Expected once; noted in the changelog and the build guide. |
| Existing manifests with `shell.program: zsh` change behavior (interactive bash now switches to zsh) | Intended by FR-008; escape hatch `SANDCAST_NO_SHELL_SWITCH` documented. |
| Gateway architecture warning cannot fire against v0.1.3 | Documented in R-14; init and capture guidance carry the platform message. |
| Ubuntu CI lacks zsh | CI installs zsh; the snippet test fails loudly instead of skipping. |

## Complexity Tracking

No constitution violations to justify.
