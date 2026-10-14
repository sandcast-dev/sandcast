# Deep Review Findings

**Date:** 2026-10-14
**Branch:** 003-runtime-home-shell-init
**Rounds:** 1
**Gate Outcome:** PASS
**Invocation:** ship pipeline (smart oversight)

Full detail, per-finding fixes, and the compliance matrix are in `REVIEW-CODE.md` (section "Deep Review Report"). This file is the deep-review artifact summary.

## Summary

| Severity | Found | Fixed | Remaining |
|----------|-------|-------|-----------|
| Critical | 1 | 1 | 0 |
| Important | 8 | 8 | 0 |
| Minor | 7 | 1 | 6 |
| Notable | 5 | 0 | 5 |
| **Total** | **21** | **10** | **11** |

**Agents completed:** 5/5. External tools skipped (CodeRabbit service unavailable on free CLI allowance; Copilot CLI not installed; Codex usage limit reached). None treated as a gate failure.

## Fixed (Critical + Important + one Minor)

- SEC-1 (Critical): dotfile relocation target command injection — `safeTargetRe` validation + template quoting.
- SEC-2 (Important): sh fallback sourced workspace `~/.profile` — non-login sh launcher (`shell.ShLoginLauncher`, `plan.ShLoginPath`).
- SEC-3 / COR-2 (Important): project-relative dotfile symlink escape — project-root containment in `dotfileInputs`.
- ARCH-1 (Important): dead `loser` return removed.
- ARCH-2 (Important): `build.WorkDir = plan.OvermountHome` (single source for the overmount path).
- PROD-1 (Important): `dotfiles` registered as a reserved build entry (`build.DotfileContextDir` + `fsutil.buildEntries`).
- PROD-2 (Important): injective `DotfileContextFile` encoding.
- TEST-1 (Important): new `internal/pipeline/dotfiles_test.go` covering `dotfileInputs`.
- TEST-2 (Important): `TestResolveDotfilesRejectsOvermountTarget` now exercises the real guard.
- COR-1 (Minor): `ProposeDotfiles` skip reasons surfaced via `Result.DotfileNotices` and the CLI.

## Remaining (non-gating)

Minor: reprovisionTarget branch coverage, FR-011 plan-notice test, SC-002 behavioral proof integration-only, ProposeDotfiles skip-branch coverage, RelocationNotice.String() assertion.
Notable (future work): consolidate within/withinDir helpers; factor the three env merges; RCPath/RCPath0 naming; container-target relocation notice wording; reprovision notice `To` accuracy.

## Test Suite Results

`make verify` (unit tests + golangci-lint): passed, 0 lint issues. No spec requirement dropped during the fix loop (14/14 FR verified post-fix).
