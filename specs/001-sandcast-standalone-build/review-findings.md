# Deep Review Findings (run 2)

**Date:** 2026-10-09
**Branch:** 001-sandcast-standalone-build
**Rounds:** 3 of 3 fix rounds done. Round-3 fixes applied and re-reviewed 2026-10-09 (focused: security, correctness, test-quality).
**Gate Outcome:** PASS. The round-3 re-review found 0 Critical and 0 Important across all three dimensions. The 1 security Minor (symlinked profiles dir) was then fixed; 4 test-quality Minor hardening items remain as recorded follow-ups, none blocking.
**Invocation:** manual (`/speckit-spex-deep-review-run`)
**Previous run:** `review-findings-run1.md`

## Status after fix round 3

| Severity | Found (all passes) | Fixed | Open |
|----------|-------------------|-------|------|
| Critical | 3 | 3 | 0 |
| Important | 35 | 35 | 0 |
| Minor | ~84 | ~19 | ~65 |
| Notable | 14 | - | 14 |

The Important "Found" count is 35, not the 38 shown while paused: that estimate assumed the round-2 test-quality re-review (R3-5 to R3-7) would add about 3 Important findings. The rerun produced 0 Critical and 0 Important (3 Minor), so those placeholders dissolved. The four real open Important findings (R3-1 to R3-4) are all fixed in round 3.

Verification after fix round 3 (full combined changes of R3-1 to R3-4 plus the 3 Minor fixes):
- `make test`: pass
- `make lint`: 0 issues
- `make test-integration`: pass (194.0 s). The `tests/integration/core_test.go` assertion remains as updated in round 2 (flags naming `/sandbox/` paths are allowed; `go-proxy` must stay unflagged).
- `make test-gateway`: not run in this review.

Verification after fix round 2 (plus the integration test update below):
- `make test`: pass
- `make lint`: 0 issues
- `make test-integration`: pass (188.8 s). The first run after round 2 failed in `TestCoreFlow` because `pypi` is now flagged: the default NVIDIA community base image ships python under `/sandbox/.uv`, which the sandbox can write. That flag is the intended effect of the round-2 workdir fix. The assertion in `tests/integration/core_test.go` now allows flags that name `/sandbox/` paths and still requires `go-proxy` to be unflagged.
- `make test-gateway`: not run in this review.

External tools:
- CodeRabbit: the full run was refused (405 files, free limit 150). It ran per directory in all three passes; findings are merged below.
- Codex: failed (usage limit until 2026-10-17).
- Copilot: skipped (CLI not installed).

## Post-fix spec coverage

The fix loop removed three internal symbols; none dropped a spec requirement (verified 2026-10-09).

| Removed in | Symbol | Behavior it supported | Still covered by | Spec |
|------------|--------|----------------------|------------------|------|
| round 2 | `Options.LockHeld` | build locking / concurrent-build refusal | `build.LockDir`, `build.LockTarget` (lock held across plan, both passes, verify; plan/verify/export/import each lock once) | hardening, no dedicated FR |
| round 2 | old `Readapt` output parameter | re-adapt an adapted set on import, record upstream to adapted id mapping | `Readapt(dir, suffix, scope)` (returns `*profile.AdaptedSet`), called by import/export/fragments | FR-022a |
| round 2 | `isHTTPURL` | URL credential detection | `redact` (`KindURLSecret`, `KindURLPath`, URL query/fragment scan) | FR-008, SC-009 |

No remaining references to `LockHeld` or `isHTTPURL`. All spec requirements verified after the fix loop; 0 dropped.

## Important findings fixed in round 3

All four fixed and verified on 2026-10-09 (`make test`, `make lint`, `make test-integration` green). Owned by four parallel implementer agents with strict, non-overlapping file ownership.

### R3-1 FIXED Profile loading echoes file contents and follows symlinks (security)
- **Files:** `internal/profile/load.go:33-38`, `internal/profile/index.go:71-72`, `internal/pipeline/profiles.go:25-42`, `internal/pipeline/pipeline.go:110-119`, `internal/cli/profiles.go:51-63,98-123`
- **Problem:** `profile.Parse` and `profile.ReadIndex` wrap the goccy YAML error with `%w`. That error quotes source lines. A committed symlink such as `.sandcast/profiles/x.yaml -> ~/.aws/credentials` or `build/<t>/profiles/index.yaml -> ...` therefore prints host secrets in the error output, which reaches CI logs and the agent context. `LoadAdapted` and `profileFiles` follow symlinks inside `profiles/`. `profiles lint` with no argument runs no symlink check. The project tier is read with `os.DirFS`, which follows links that leave `.sandcast/profiles/`.
- **Fix:**
  1. Format decode errors with `yaml.FormatError(err, false, false)`.
  2. Refuse symlinked entries in `LoadAdapted` and `profileFiles`.
  3. Run `checkBuildDir` in `profiles lint`.
  4. Load the project tier through `os.OpenRoot(...).FS()`.
- **Resolution:** All four parts done. `profile.Parse` and `profile.ReadIndex` route decode errors through a `decodeError` helper (`yaml.FormatError(err, false, false)`); `LoadAdapted` refuses a symlinked `index.yaml` or profile file via a local `refuseSymlinkEntry`; `profileFiles` refuses symlinked directory entries; default `profiles lint` calls `checkBuildDir`; `pipeline.Profiles` reads the project tier via `os.OpenRoot(...).FS()` (`rootedDirFS` helper). Deviation: a small local `refuseSymlinkEntry` was added instead of reusing `fsutil.Layout.CheckNoSymlinkEntries`, because that helper needs a `Layout` that `LoadAdapted(dir string)` does not carry. Regression tests added for all three security properties plus the rooted-FS escape case.

### R3-2 FIXED Manifest diagnostics can echo a credential value (security, CodeRabbit major)
- **Files:** `internal/manifest/manifest.go:218-230` (Parse), `:366-379` (Save), `Validate` messages with `%q`
- **Problem:** `Validate` runs before `refuseSecrets`. Diagnostics quote field values (name, engine, platform, component keys, harness, shell program, allowed_domains_file), and strict decode errors can quote source snippets. A token pasted into one of those fields ends up in the error output.
- **Fix:** Scan for credential shapes first; when the YAML cannot be decoded, fall back to a text scan. Never quote a value that matches a credential shape.
- **Resolution:** `Parse` runs `refuseSecrets` before `Validate` on decode success, and a new `refuseSecretsRaw` scans the raw bytes before returning a decode error (so a token in an unknown field name or a malformed line is never echoed; `decodeDiagnostic` already uses `yaml.FormatError`). `Save` runs `refuseSecrets` before `Validate`. A `safeValue` helper returns `[redacted]` when `redact.Scan` matches, applied to every value-quoting `Validate` diagnostic and all three `CheckAllowedDomainsFile` messages. Detector reused: `redact.Scan` (same shape detector `refuseSecrets` uses). Tests cover every affected field, the allowed-domains path, Save, and both pre-decode cases.

### R3-3 FIXED `--init` fallback never fires on docker (correctness)
- **File:** `internal/engine/exec.go:178-205`
- **Problem:** `initMissing` requires exit code 125. The docker CLI maps "executable file not found" to 127 (`toStatusError`). Podman's start-time init error maps to 127 as well; only its create-time error exits 125.
- **Fix:** Accept exit codes 125, 126 and 127 when stderr carries an engine-level init marker. Tighten the markers to the engine messages: `container-init binary not found`, `could not find "catatonit"`, `lookup init binary`, `"docker-init": executable file not found`. Change the docker case in `TestRunFallsBackWithoutInit` to exit 127.
- **Resolution:** `initMissing` now requires both an exit code in {125, 126, 127} (`initExitCodes`) and one of the four tightened `initMarkers`; the loose `catatonit`/`docker-init` markers are gone. The docker case in `TestRunFallsBackWithoutInit` exits 127 and still falls back; a new negative case confirms a 127 in-container "command not found" (no init marker) does not retry. `go test ./internal/engine/`: 26 pass.

### R3-4 FIXED Build skill stall rule stops the wrong process (production readiness, security)
- **File:** `skills/build/SKILL.md:33-36`
- **Problem:**
  - `pgrep -f 'sandcast build'` also matches harness wrapper shells, the pgrep shell itself, and builds of other projects. The agent can interrupt the wrong process and leave the real build running with the lock held.
  - The rationale ("remove its lock") is wrong: the `.lock` file stays.
  - The rule also fires during phases that never write `build.log`: plan, pull, probe, resolve, push, verify, and bounded downloads of up to about 20 minutes. A missing `build.log` counts as a stall too.
  - The rerun of `not run:` checks has no limit and no next step.
- **Fix:**
  - Record `$!` when starting the build in the background, and ask the user before sending `kill -INT` to that PID.
  - Say the lock is released and the file stays.
  - Treat a missing `build.log` as "not started".
  - Apply the threshold only after `### sandcast pass 1` appears, or have sandcast write phase markers.
  - Rerun `not run:` checks once; if the same check times out again, report it and stop.
  - Keep the phrases pinned in `internal/skills/structure_test.go`.
- **Duplicates:** this finding merges QA2, QA3, QA4, QP5, QP6, QC3, QC4 and QS4.
- **Resolution:** The stall rule in `skills/build/SKILL.md` no longer uses `pgrep -f`. The skill records the build PID with `pid=$!` and signals only that pid (`kill -INT "$pid"`) after asking the user, with "Never discover a process by pattern" and the reason. A missing `build.log` is "not started"; the threshold applies only after `### sandcast pass 1` appears. Lock wording corrected (lock released on exit, `.lock` file stays). `not run:` reruns at most once, then reports and stops. `internal/skills/structure_test.go`: all existing pins kept; four new pins added (`pid=$!`, `### sandcast pass 1`, `Never discover a process by pattern`, `kill -INT "$pid"`). Follow-up noted: sandcast emitting explicit phase markers to `build.log` would let the skill distinguish a bounded download from a true compile stall without relying on the single pass-1 line (a Go change in `internal/build`, not implemented). `go test ./internal/skills/`: 9 pass.

### Test-quality re-review: no Critical or Important findings
The round-2 test-quality re-review was rerun on 2026-10-09 over the full round-2 file list with spec-anchored validation. Result: 0 Critical, 0 Important, 3 Minor. Every round-1 and round-2 Critical/Important fix has a real regression test that asserts on behavior (symlink refusal and untouched link targets, `allowed_domains_file` traversal and reserved names, `--init` fallback for podman and docker stderr shapes, build lock and concurrent-build refusal across verify/export/import, probe output cap at 2048 bytes, GID 0 policy rewrite, `compinit -i -D`, scaffold credential env-key naming, export/verify atomic swap). FR-022a import-mapping persistence and the US1/US5 acceptance scenarios (SC-002, SC-004 in `tests/integration/core_test.go`) are covered. Per-test isolation (`t.TempDir`, `t.Setenv`, per-test HOME) is used consistently; no empty stubs or `t.Skip`-as-coverage. The three Minor coverage gaps are recorded under "Open Minor findings" and are not in the fix loop. Nothing here changes the round-3 queue.

## Round-3 re-review (gate) residual Minor findings

From the focused round-3 re-review (2026-10-09). Gate is PASS; these are non-blocking.

Security (1 Minor, confidence 72) FIXED 2026-10-09:
- `internal/pipeline/pipeline.go` (`rootedDirFS`), `internal/pipeline/profiles.go` (`LoadAdapted`): the R3-1 symlink defense refused symlinked profile files and a symlinked `build/<target>` parent, but not a committed symlink AT the profile directory itself. On review, the adapted-set dir `build/<target>/profiles` reached through the cli commands was already caught by `checkBuildDir` via `CheckNoSymlinkEntries`, because `fsutil.buildEntries` includes `profiles`; the genuinely unprotected path was the project tier `.sandcast/profiles` read by `pipeline.Profiles`, where `rootedDirFS` used `os.Stat` + `os.OpenRoot` and `os.OpenRoot` follows a symlinked root (os.Root only blocks escapes from within the tree, confirmed empirically). Fix: `rootedDirFS` now `Lstat`s the dir and refuses a symlink before `os.OpenRoot`; `LoadAdapted` now refuses a symlinked profiles dir as defense in depth for any caller that does not go through `checkBuildDir`. Regression tests added: `TestProfilesRefusesSymlinkedProjectTierDir` (symlinked `.sandcast/profiles` is refused and the target secret is not read) and `TestLoadAdaptedRefusesSymlinkedProfilesDir`. `checkBuildDir` was left unchanged: `CheckNoSymlinkEntries` already covers the `build/<target>/profiles` case. Not higher severity: the secret-echo vector was already closed (decode errors route through `yaml.FormatError`), import/export run `build.RedactPass` before the gateway, and lint prints only ids/fields.
- Notable (confidence 60, no fix required under the threat model): TOCTOU between the `os.Lstat` in `refuseSymlinkEntry` and the later `os.ReadFile`. Not actionable for committed-symlink threats; a concurrent local attacker would already need write access.

Test quality (4 Minor, hardening only) all FIXED 2026-10-10:
- FIXED `internal/profile/load_test.go` `TestReadIndexDoesNotEchoSourceLine`: added the "raw decoder WOULD leak" counter-check, decoding the doc with `yaml.Strict()` (what `ReadIndex` uses) and asserting the raw error carries the secret, so the test cannot pass vacuously.
- FIXED `internal/profile/types_test.go` port case: renamed to "negative port rejected" and changed the expectation from goccy's exact "cannot unmarshal -1" phrasing to the offending value `-1` being echoed (plus the always-checked file context). goccy reports type errors by source location, not field name, so the field name cannot be asserted; the value-echo assertion is robust across goccy releases.
- FIXED `internal/pipeline/profiles_import_test.go` `TestProfilesProjectTierDoesNotEscapeRoot`: now also asserts the error names the offending entry (`x.yaml`) and contains `escapes` (the os.Root "path escapes from parent" containment message), a stronger wrong-reason guard than `NotContains(secret)` alone.
- FIXED `internal/skills/structure_test.go`: added a `forbiddenPhrases` map and `TestStructureForbiddenPhrases`; the build skill must not contain the exact superseded command `pgrep -f 'sandcast build'` (the current file mentions `pgrep` only inside the prohibition), so a partial revert that restores the old stall instruction fails.

## Open Minor findings (not in the fix loop)

From the test-quality re-review (2026-10-09), all three fixed 2026-10-09:
- FIXED `internal/probe/probe.go:339-345`: the `tail(s, n)` helper and the `maxStderr = 2048` stderr cap in `runProbe` (line 333) had no test driving stderr past 2048 bytes, so the truncation branch and the boundary never ran. Added `TestTail` in `internal/probe/probe_test.go` (empty, under-cap, exactly-at-cap, over-cap) asserting the `...` marker, the result length, and that the retained bytes are the most recent ones. (was confidence 65)
- FIXED `internal/pipeline/profiles.go` `replaceAdapted`: the double-failure rollback branch (staged rename fails, then the rollback rename of the previous set also fails, so `keep = true` and the "the previous set is at %s" error) was untested. Directory permissions cannot reach it (the swap and its rollback need write on the same two directories, so they fail or succeed together). Added an unexported `renameFn = os.Rename` seam and `TestReplaceAdaptedKeepsThePreviousSetWhenBothRenamesFail` in `profiles_import_test.go`, which lets the first rename succeed and the next two fail, then asserts the error names the preserved `previous` directory and that the set still exists there. (was confidence 60)
- FIXED `internal/pipeline/profiles_import_test.go` conflict subcase of `TestImportLeavesTheBuildDirectoryWhenNothingChanged`: it asserted only that `rep.Index` is empty and the index file is unchanged, not that conflict reporting worked. Tightened to assert `rep.Conflicts == ["github-demo"]` and `rep.Imported == ["go-proxy-demo"]` (the review's suggested "`rep.Imported` empty" was wrong: `go-proxy-demo` is genuinely imported to the gateway before the conflict short-circuits the directory write). (was confidence 55)

From the round-2 re-review:
- `internal/cli/verify.go`, `profiles.go`: the symlink and lock policy is split between cli (`checkBuildDir`) and pipeline (`lockBuildDir`). `pipeline.Verify` repeats `CheckNoSymlinks`.
- `internal/build/build.go:52`: the `ErrBuildRunning` text says "build" even when verify, import or export holds the lock. It also exits 1 with no JSON marker, and `build` takes the lock only after plan and pull.
- `internal/pipeline/build.go:105-120`: the `Rel` check in `allowedDomainsPath` can never fail and is dead code.
- The containment predicate is duplicated in several places. `harness/claudecode/discover.go:486` (`tildePath`) still uses the old `HasPrefix(rel, "..")`.
- `internal/pipeline/profiles.go`: `Readapt` and `LoadAdapted` are exported with no outside callers, and the index is read twice. `LoadAdapted` does not check for empty or duplicate upstream ids.
- `internal/probe/script.go`: `MaxOutput` is documented in characters, but the main path counts bytes.
- `internal/engine/exec.go`: without `--init`, a container that hits the deadline is not removed. Fix by giving runs a name and calling `rm -f` on cancel.
- `internal/probe/probe.go`: probe timeout notices reach stderr but not `plan.Notices` or the JSON output.
- `internal/fsutil/layout.go:206`: a symlinked `.sandcast/.gitignore` is refused with exit 1 instead of exit 2.
- `internal/cli/profiles.go:144-151`: export reads `plan.yaml` before `checkBuildDir` and the lock. `lastPlanUser` in `fragments.go` has the same order.
- Writers under `.sandcast/` (fragments, promote, learnings, lock, set-shell, `EnsureGitignore`) follow a symlinked `.sandcast`, `fragments/` or `recipes/` directory.
- `skills/capture/SKILL.md`: an existing `compinit` or `compinit -C` in the host rc is not rewritten to `compinit -i -D`.
- `internal/harness/discovery.go`: `trimURL` is O(n²) on long runs of trailing brackets.
- `internal/cli/profiles.go:189-205`: import checks `index.yaml` before taking the lock.
- `internal/manifest/manifest.go`: duplicate harness names are not refused, and a tag embedded in `image` is not checked.

From the round-0 and round-1 reviews (still open): see the agent notes in the session. The main ones:
- Dead exported API: `Engine.Labels`, `probe.Options.Offline`, `redact.Replace`.
- Duplicated enums and key regexes.
- Duplicated YAML encoder options (Notable).
- `plan.Profile.AdaptedID` is always equal to the id.
- MCP profile notices are discarded.
- A verify failure prints as "build failed".
- `ParseVersion` takes the leftmost match.
- The remote registry fallback looks up the previous copy in the wrong tag directory.
- MCP profile ids collide across harnesses.
- `ScanYAML` deduplicates by (line, kind).
- `secretValue` requires at least 12 characters.
- `ValidHost` checks only the character set (`.com` and `foo..com` pass).
- The registry refresh `http.DefaultClient` has no timeout.
- `ImageExists` swallows errors.
- `verify.Run` swallows `ImageID` errors.
- The skill examples lack `--target` / `--platform`.

## Notable observations (for brainstorming)

- `/opt/mise` is owned by the final user. Its integrity rests on the read-only policy plus landlock (`best_effort`). The alternatives each cost something: a separate chown layer adds image size, treating it as writable flags every mise profile, and installing as root breaks R-04.
- The rust component's `/opt/rustup` runtime glob is flagged by design. It is the known exception in the shipped-components test.
- `components/opencode.yaml` has no model-provider profile. The adapter comes in a later phase, and the provider is the user's choice.
- Base-image tools that exist only under `/sandbox` count as present at probe time, but the Kubernetes driver mounts the workspace over `/sandbox`.
- Startup files in a workspace-writable home (`.zshenv`, `.zprofile`, `.bashrc`) are the same code-execution vector as `.zcompdump`.
- The registry index has no authenticity check. The capture skill reads raw rc files into the model context.
- Probe, registry and engine caches grow without bound. There is no `Pdeathsig` for child processes.
- `build.Run` is long. The harness redaction code belongs in `redact`. The `Fake` engine sits in a production package.
- A base user with GID 0 gets its UID as `run_as_group`, which may coincide with a privileged group.

## Fixed in this run (summary)

**Round 1 (21 Critical/Important):**
- `allowed_domains_file` path traversal and unchecked host characters (Critical).
- Containerfile injection through component fields and probe-derived user facts.
- Go toolchain glob in a writable directory.
- URL path secrets and missing token shapes.
- Wrong `EnvVar` for partial redactions.
- Fragment `/opt/<key>` missing from the policy.
- Missing user-scope skill sources: clear error plus copy-into-project guidance.
- Import mapping persistence (FR-022a).
- Business logic in `cli/env.go` (`ValidateReferences`).
- Harness-component global.
- Concurrent builds: per-run stage tag plus build lock.
- No probe or verify timeouts.
- Skill guidance for long-running commands.
- GID 0 policy failure.
- Test isolation (per-test HOME) and missing tests for verify exit codes, CheckHash and delta edge cases.
- Three capture skill command examples (CodeRabbit).

**Round 2 (9 Critical/Important, plus coordinator fixes):**
- `build.log` and `.lock` followed committed symlinks (Critical): `O_NOFOLLOW`, `CheckNoSymlinkEntries`.
- Reserved `allowed_domains_file` names.
- Export and verify locking with an atomic swap.
- `--init` fallback (podman).
- Embedded and percent-encoded URL secrets.
- Workdir-writable binaries are flagged.
- Scaffold credential error names the env key.
- Pipeline lock tests and `LockTools` tests.
- Probe output capped at 2048 bytes (a 1 MB output used to spin the shell).
- Timeout notices and `not run:` status.
- `compinit -i -D` in place of `-C` (CodeRabbit major).
- `plan` exits 2 for symlinked build entries.
- `--init` notice shown to the user.

## Docs follow-ups (applied 2026-10-09)

All applied by five doc agents with strict file ownership, each verifying claims against the shipped code before editing (no em-dash, no en-dash, no contractions, Markdown so not one sentence per line; contract YAML revalidated). Verification: no dashes in any edited doc, all contract schemas parse. Docs are not compiled, so Go build/test/lint are unaffected.

- `docs/configuration.md`: FIXED `allowed_domains_file` rules (plain name, no leading dot, not a reserved build-dir name, with the exact reserved list); skill source example now `.claude/skills/review` with the accurate nuance that `~/.claude/skills` sources are supported when present and fail with copy-into-project guidance when absent; go component example gains `GOTOOLCHAIN: local` and drops the stale toolchain glob.
- `docs/guides/profiles.md`: FIXED removed the Go toolchain glob from prose and the YAML example; documented both flag reasons (no binary resolved; binary under a writable path); added a brief symlink-defense note to the Lint section.
- `docs/guides/build.md` and `docs/cli.md`: FIXED per-run stage tag, a Concurrent runs section with the exact build-lock busy message and the `.lock`-stays note, verify `timeout` and `not run:` statuses, import rewriting `index.yaml` (plus `index` in the JSON report), GID 0 run_as_group fallback, read-only `/opt/<key>` fragment trees, and the `--init` fallback on both podman and docker.
- `docs/guides/capture.md`: FIXED embedded URL redaction (user-info password and secret query/fragment params caught by the general scan; the credential-like path segment incl. the 40-hex case caught only by the harness discovery redactor), placeholder `${SANDCAST_SECRET_N}`.
- Contracts and `research.md`: FIXED `manifest.schema.yaml` allowed_domains_file rules and the component-key dedup; `component.schema.yaml` field patterns; `adapted-profiles.schema.yaml` documents the real `ValidHost` (character set only, with wildcard shape and blocked destinations enforced separately in `hostrules.go`); `research.md` R-18 lists the shipped token shapes.

Reality-check corrections made during this pass (the code does not match the original follow-up wording):
- Duplicate HARNESS names are not refused; only duplicate component keys are. Docs document the real rule.
- There is no embedded-tag check on a target `image`. Nothing was added about it.
- `ValidHost` checks only the character set, not wildcard shape or blocked destinations (those live in `hostrules.go`).
- A `~/.claude/skills` source is valid when it exists; the failure is a non-existent user-scope source, not the `~/` form itself.
- `/opt/<key>` is a promotion convention (`build/promote.go`), not a component-schema rule, so it was not added to the component schema.

## Files changed in round 2

internal/build/{build.go,build_test.go,resolve.go,resolve_test.go}, internal/cli/{build_test.go,buildlock_test.go,lock_test.go,plan.go,profiles.go,profiles_test.go,root.go,verify.go}, internal/component/shipped_test.go, internal/engine/{exec.go,exec_test.go}, internal/fsutil/{layout.go,layout_test.go}, internal/harness/{discovery.go,discovery_test.go}, internal/harness/claudecode/{scaffold.go,scaffold_test.go}, internal/manifest/{manifest.go,manifest_test.go}, internal/pipeline/{allowed_domains_test.go,build.go,lock_test.go,locktools_test.go,pipeline.go,profiles.go,profiles_export_test.go,profiles_import_test.go}, internal/plan/delta_test.go, internal/probe/{probe.go,script.go,timeout_test.go}, internal/skills/structure_test.go, skills/build/SKILL.md, skills/capture/SKILL.md, tests/fixtures/reference-project/{README.md,expected-manifest.yaml}, tests/integration/core_test.go
