# Code Review: Field Feedback Fixes (002)

**Spec:** specs/002-field-feedback-fixes/spec.md
**Diff reviewed:** main..HEAD (b344590, f9283e2, 0fd004d, fe605cb) plus review fixes
**Date:** 2026-10-13
**Reviewer:** Claude (speckit-spex-gates-review-code, ship pipeline stage 7, ask: smart)

## Compliance Summary

**Overall score: 97.6% (41 of 42 checks fully compliant)**

- Functional requirements: 25/25
- Edge cases: 16/17 (go.work highest directive partial)
- Spec-declared constants: none (no `## Constants` section)

Before the review fixes, 34 of 42 checks were fully compliant (81%). Commit eaa967e closed FR-018 (diagnostic path, promote check), FR-019 (enclosing repository, unknown divergence, credential skip reason), FR-022 (temporary file on every exit path), FR-024 (documentation gaps) and the credential edge case. After the deep review the score stood at 92.9% (39/42), with FR-003 and FR-010 partial and four Important items needing a decision. The user decided all four (spec Clarifications, session "code review"); the resolution commit implements them, which brings the score to 41/42. The go.work edge case stays partial (O5, still AMBIGUOUS).

## Compliance Matrix

| Item | Implementation | Status | Notes |
|------|----------------|--------|-------|
| FR-001 lockfile decides | internal/plan/delta.go, plan.go | Compliant | equal skips, differing or unknown shadows |
| FR-002 notices | delta.go, plan.go, cli/build.go | Compliant | repeated in the build report |
| FR-003 probe paths, workspace rule | probe/script.go, plan/delta.go, plan/user.go | Compliant | spec now states the current rule (PATH hits of the primary binary, paths not resolved); every binary with resolved targets deferred to T060 |
| FR-004 pinned versions | capture/pins.go, match.go | Compliant | unreadable pin files now produce a note |
| FR-005 skill proposes exact pin | skills/capture/SKILL.md | Compliant | |
| FR-006 session env via startup files | plan/encode.go, build/sections.go, probe/script.go | Compliant | MISE_* asserted in bash -lc, zsh -c and zsh -il; base image PATH directories beyond the OpenShell default re-exported from the probe's new `path` fact |
| FR-007 PATH survives home rc | encode.go, probe/script.go, plan.go | Compliant | home rc notices only when the base user is the final user |
| FR-008 zsh switch hook, login shell | build/sections.go, assemble.go | Compliant | switched zsh carries SANDCAST_NO_SHELL_SWITCH=1, so nested bash stays bash |
| FR-009 no new-user wizard | sections.go zshenv block | Compliant | now tested with an empty home |
| FR-010 session checks on every verify | verify/verify.go, cli/verify.go | Compliant | every declared binary that resolves in the image environment must resolve in the session; others are skipped with `notes`; components without executables keep their check; zsh checks use `zsh -il` |
| FR-011 image ref, push, digest | cli/build.go, build/build.go, engine/exec.go | Compliant | `--digestfile` with podman 6.1.3 remote verified against a local registry |
| FR-012 Docker Hub namespace warning | manifest.go, pipeline.go | Compliant | |
| FR-013 Docker Hub rate-limit notice | cli/build.go | Compliant | |
| FR-014 remote gateway warnings | gateway/openshell.go, pipeline/profiles.go | Compliant | selected gateway honored; lookup bounded to 5s; architecture warning has no code by clarification |
| FR-015 init flags | cli/init.go, manifest.go | Compliant | note now suggests `--force` |
| FR-016 emulation notice | component/types.go, rust.yaml, pipeline.go | Compliant | printed before the build as well |
| FR-017 cross-arch guide | docs/guides/cross-arch.md | Compliant | |
| FR-018 declared bins | manifest.go, plan.go, validate.go, cli/fragments.go | Compliant | conflict path `$.components[i].bins[j]`; promoted recipe checks every binary; promote removes the redundant `bins` and the next plan accepts the manifest |
| FR-019 git remote marketplaces | harness/claudecode/discover.go, harness/git.go | Compliant | top-level check, null unknown divergence, hardened git env |
| FR-020 portability marker | discover.go | Compliant | `file:` URLs not portable |
| FR-021 copy offer on marker | skills/capture/SKILL.md | Compliant | |
| FR-022 portable snippets | skills/*/SKILL.md, skills/snippets_test.go | Compliant | subshell with EXIT, HUP, INT, TERM traps; interrupt case tested |
| FR-023 compdef completion | skills/capture/SKILL.md | Compliant | now in the capture guide |
| FR-024 documentation | docs/, README, CHANGELOG | Compliant | notice kinds, session checks, discovery fields added |
| FR-025 integration and gateway tests | tests/integration | Compliant | |
| 17 edge cases | | 16 compliant | go.work: the summary picks the first exact pin, not always the highest directive (AMBIGUOUS) |

## Stage 1 Findings (compliance audit)

Fixed in eaa967e:
- Discovery proposed a marketplace through an enclosing repository (dotfiles home). Now requires the repository top level.
- `unpushed_commits` reported 0 when no tracking ref existed. Now `null` (unknown).
- A remote URL with a credential in the query was skipped with a reason claiming there was no remote. Now a distinct reason.
- Remote names reached git as possible options, and `git status` honored `core.fsmonitor`. Now full ref names and `core.fsmonitor=false`.
- Skill snippets left the temporary file on an interrupt. Now traps in a subshell.
- Promote weakened the check to the first binary; conflict diagnostics pointed at `$.components`.
- A target image with its own tag became `image:tag:latest`. Validation now rejects it.
- Init note, gateway warning wording for `localhost:<port>`, early runtime notices, unreadable pin note, home rc notice for created users, documentation gaps.

Verified, no change: podman remote `--digestfile` writes the registry digest (podman 6.1.3, local registry).

## Resolved by user decision

| # | Item | Severity | Resolution |
|---|------|----------|------------|
| O1 | FR-010: session checks did not test every binary | Important | Every declared binary that resolves in the image environment (build-time image PATH) must resolve in the session, else exit 127; binaries that do not resolve there are skipped with a note (`notes` in verify.yaml and JSON, one `note:` console line per component); a component whose only binary name is its key and has no executable (ca-certificates) keeps its component check. Contract, docs and FR-010 updated. |
| O2 | Base image tools outside the default PATH never reached a session | Important | The probe records the base image PATH (`path` fact, cache format 3); `_sandcast_path` re-adds the directories beyond `/usr/local/bin:/usr/bin:/bin` in the base image's order after the shims (OpenShell target, plain absolute paths only). Unit tests under sh, dash, bash and zsh; integration test checks `nft` from `/usr/sbin` in a session. |
| O3 | FR-003: first binary only, no symlink resolution | Important | Deferred: T060 added to tasks.md; FR-003, the edge case, the data model and the key entity now state the current rule and reference T060. Probe protocol for check paths unchanged. |
| O4 | Promote left `bins`, so the next plan rejected the entry | Important | `sandcast promote` removes the redundant `bins` from the promoted entry through the regular manifest save path (re-encodes, comments not kept); test runs `manifest validate` and `plan` after promote. FR-018, docs and the build skill updated. |

## Open Items (not fixed)

| # | Item | Severity | Status | Options |
|---|------|----------|--------|---------|
| O5 | go.work proposal can be lower than the highest directive | Minor | AMBIGUOUS | Treat a two-part go directive as exact, or always propose the highest pin for go. |
| O6 | Profile override notices follow the kinded notices; docs say the reverse | Minor | AMBIGUOUS | Reorder in code or adjust docs. |
| O7 | Fedora-style images read `/etc/bashrc` only through `~/.bashrc`; an empty home gets neither the session block nor the hook | Minor | AMBIGUOUS | Also append to `/etc/profile.d`, or document the limit. |
| O8 | DEBUG trap stays active in `bash -l script.sh` | Minor | AMBIGUOUS | Needs a different approach than R-05. |
| O9 | Unqualified image names are reported as `docker.io/...` although podman tags `localhost/...` | Minor | AMBIGUOUS | Engine-specific reporting, or a note. |
| O10 | `init --push` without `--image` writes `localhost/<name>` with push enabled | Minor | AMBIGUOUS | Usage error or a note. |
| O11 | SSH user in `ssh://user@host` is stripped as a credential | Minor | AMBIGUOUS | Strip only for http(s), or only passwords. |
| O12 | Credential in a remote URL query string: skipped, not stripped | Minor | AMBIGUOUS | Strip query and fragment, or keep skipping (now with a correct reason). |
| O13 | tasks.md T059: mise shims read project `.tool-versions` and warn about tools missing from the image lockfile | Info | open task | Research a mise setting that keeps image shims on the image configuration. |

## Deep Review Report

Gate: PASS (after fix round 2). Details: `review-findings.md`.

| Severity | Found | Fixed | Remaining |
|----------|-------|-------|-----------|
| Critical | 0 | 0 | 0 |
| Important | 5 | 5 | 0 |
| Minor | 25 | 12 | 13 |
| Notable | 4 | - | 4 |

The Important open items in the table above are compliance-stage findings that need a decision; they are tracked there and are not deep review findings.

| Agent | Found | Fixed | Remaining | Status |
|-------|-------|-------|-----------|--------|
| Correctness | 4 | 2 | 3 | completed (one partially fixed) |
| Architecture & Idioms | 8 | 2 | 5 (+1 Notable) | completed |
| Security | 3 | 0 | 2 (+1 Notable) | completed (sandbox command quoting applied) |
| Production Readiness | 5 | 1 | 3 (+1 Notable) | completed |
| Test Quality | 7 | 6 | 0 (+1 Notable) | completed |
| Goal Alignment | 0 | 0 | 0 | skipped (no PR) |
| CodeRabbit (external) | 2 | 2 | 0 | completed (round 2: 0 findings) |
| Copilot (external) | 0 | 0 | 0 | skipped (disabled in config) |
| Codex (external) | 0 | 0 | 0 | failed (usage limit) |
| Re-review (round 2) | 5 | 5 | 0 | completed |

MVP: Architecture & Idioms (8 findings).

Key fixes applied:
1. Gateway warning follows the selected gateway and is bounded to 5s (correctness, production).
2. Integration test covers an empty workspace home and `MISE_*` in every shell (test quality).
3. Nested interactive bash stays bash after the switch (CodeRabbit).
4. git discovery ignores the caller's `GIT_DIR` and config-injection variables (re-review).
5. Distinct discovery skip reasons and accurate verify help (architecture).

## Test Results

- `make verify`: pass (unit tests and golangci-lint, 0 issues).
- `make test-integration` (podman 6.1.3): pass (648s after the review fixes; 340s after the user decisions).
- `make test-gateway`: not run in this stage (needs a throwaway OpenShell gateway).
