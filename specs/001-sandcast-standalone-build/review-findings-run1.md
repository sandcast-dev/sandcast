# Deep Review Findings

**Date:** 2026-10-09
**Branch:** 001-sandcast-standalone-build
**Rounds:** 3 fix rounds, 4 review passes (initial review, re-reviews after rounds 1 and 2, final re-review after round 3)
**Gate Outcome:** FAIL (process rule: the final re-review after fix round 3 still found Important findings). All of them, 7 in total, were fixed after the loop and verified with tests and the full suites, but they were not re-reviewed.
**Invocation:** quality-gate (after_implement hook, through speckit-spex-gates-review-code), spec compliance 97% (see code-review.md)

## Summary

| Severity | Found | Fixed | Remaining |
|----------|-------|-------|-----------|
| Critical | 1 | 1 | 0 |
| Important | 52 | 52 (45 in the loop, 7 after the final round) | 0 |
| Minor | 117 | 74 | 43 |
| Notable | 14 | - | 14 |
| **Total** | **184** | **127** | **57** |

**Agents completed:** 5/5 in each of the 4 review passes (the first architecture pass was redispatched once after a rate limit).
**External tools:** skipped. The review was run with `--no-external`, so CodeRabbit, Copilot and Codex did not receive code.
**Agents failed:** none.

### How to read the gate outcome

The fix loop allows three rounds. Each re-review narrowed to the files the previous round changed. The count of Critical and Important problems went from 28 (1 Critical, 27 Important) to 11, then 7, then 7. Most later findings came from the credential heuristics in `internal/redact` and `internal/harness`, where every widening or narrowing of the rules exposed a new edge case. The 7 Important findings of the final pass are fixed, with tests (several checked by mutation), and `make verify`, `make test-integration` and `make test-gateway` pass on the final code. Because they were not reviewed again, the gate reports FAIL. A fresh `/speckit-spex-deep-review-run` over `internal/redact`, `internal/harness` and `internal/build` would settle it.

## Findings

Critical and Important findings are described in full. Minor findings follow in tables.

### FINDING-1
- **Severity:** Critical
- **Confidence:** 85
- **File:** internal/capture/match.go, internal/component/load.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`match.host_tools` in a component definition accepted any string. A project-tier component could list a relative path such as `./scripts/tool`, and `sandcast components match --host` resolved and executed it with `--version` on the host.

**Why this matters:**
Project-tier components come from the repository being captured. Running `components match --host` in an untrusted checkout would execute a file from that checkout on the developer's machine.

**How it was resolved:**
`component.Load` validates `host_tools` entries against `^[A-Za-z0-9][A-Za-z0-9._+-]*$`, so only bare program names load. `capture.matchOne` additionally skips names that contain `/` or `\` and only runs a resolved path when it is absolute.

### FINDING-2
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/build/build.go, internal/build/redactpass.go, internal/manifest/manifest.go, internal/pipeline/pipeline.go
- **Category:** security
- **Source:** security-agent (also reported by: correctness-agent, test-quality-agent)
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
The credential scan of the build directory ran only after both build passes. A credential in the staged build context (Containerfile, shell files, harness files, mise files, policy) was already baked into the image, and the final tag stayed in place when the scan hit. The manifest was only scanned on save, and stored fragments were not validated or scanned at plan time.

**Why this matters:**
Constitution IV and the spec require that credentials never enter the image. A scan that runs after the image exists cannot enforce that.

**How it was resolved:**
`build.Run` runs `RedactPass` over the staged context before pass 1 (skipping only the build log, the profile output, `verify.yaml` and `Containerfile.final`), and again over all outputs after pass 2; a post-build hit removes the final image with a non-cancellable context. `pipeline.Plan` calls `manifest.CheckSecrets` first and validates and scans every active stored fragment. CLI tests assert that no engine build happens for a manifest with a credential or an invalid fragment.

### FINDING-3
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/build/promote.go, internal/build/fragments.go
- **Category:** test-quality
- **Source:** test-quality-agent (also reported by: correctness-agent)
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
Promotion of a working fragment into a recipe stopped a continued `RUN` at a comment or blank line inside the continuation, and a bare `USER` instruction caused an index panic.

**Why this matters:**
Promotion is how the build skill turns repaired layers into reusable recipes. A truncated script produces a recipe that installs half a tool, and a panic aborts the command.

**How it was resolved:**
`fragmentScript` drops comment and blank lines inside a continued `RUN`, keeps shell heredoc `RUN` blocks whole, and returns an error for a bare `USER`. `ValidateFragment` refuses a bare `USER`. Tests cover each case.

### FINDING-4
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/build/promote.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`ENV` instructions with `$VAR` references or quoted values containing spaces were split on whitespace and promoted into wrong key/value pairs.

**Why this matters:**
A recipe with a mangled environment installs into the wrong paths or breaks PATH inside the image.

**How it was resolved:**
`parseEnv` uses a Dockerfile-style tokenizer (`envWords`) that honors quotes and escapes, and refuses `$` references, which cannot be expressed in a recipe's static environment.

### FINDING-5
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/build/assemble.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
When the base image reference already carried `@sha256:...`, `fromReference` appended the probed digest a second time.

**Why this matters:**
`FROM image@sha256:a@sha256:b` is an invalid reference; the build fails for every user who pins a base image by digest.

**How it was resolved:**
`fromReference` strips an existing digest before appending the probed one. A test covers a digest-pinned base.

### FINDING-6
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/build/build.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`stageFile` forced every staged harness file to mode 0644 and silently skipped symlinks.

**Why this matters:**
Executable hook scripts lost their executable bit inside the image, and a skipped symlink left a missing file without any message.

**How it was resolved:**
Staged files keep their permission bits plus read access (`perm|0o444`). Symlinks are followed only when they stay inside the source root (through `os.Root`); an escaping symlink or a directory symlink is an error.

### FINDING-7
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/plan/user.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
For the container target, a base image whose user has a numeric uid but no passwd entry produced an empty user name, which rendered as `USER root`.

**Why this matters:**
FR-040 requires the container target to keep the base image's user. Silently switching to root changes the security posture of the image.

**How it was resolved:**
`keptUser` uses the numeric uid as the name when the name is empty and the uid is greater than zero. A test covers it.

### FINDING-8
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/harness/claudecode/scaffold.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
Skill and marketplace sources in the harness section could point anywhere on the host, for example `~/.ssh`, and were copied into the build context and the image.

**Why this matters:**
A manifest edited by mistake, or by an untrusted contributor, could copy private keys or other host files into a shareable image.

**How it was resolved:**
`resolveSource` resolves symlinks first and accepts a source only when it lies strictly inside the project, `~/.claude/skills` or `~/.claude/plugins`. `~/.claude` itself (with its credentials file) is refused, and the project does not count when it is the home directory or above it. A missing source is an error. Discovery applies the same check and skips local marketplaces outside these roots with a note, so a development marketplace elsewhere in the home directory is no longer baked. Tests cover symlinked skills and marketplaces that escape the project, `~/.ssh`, the home directory itself, `../`, a project at home, and an accepted symlink between two allowed roots.

### FINDING-9
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/fsutil/layout.go, internal/pipeline/pipeline.go, internal/pipeline/build.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`.sandcast/build/<target>` could be a symlink committed to the repository, so plan and build outputs were written to an arbitrary host directory.

**Why this matters:**
Building an untrusted checkout could overwrite files outside the project.

**How it was resolved:**
`Layout.CheckNoSymlinks` rejects any symlink on the path from the project root to the build directory. `WritePlan` and `Verify` call it; the CLI maps the error to exit 2. A CLI test builds through a symlinked build directory and asserts exit 2, no engine build and nothing written outside the project.

### FINDING-10
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/harness/claudecode/discover.go, internal/harness/claudecode/scaffold.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
Discovery copied marketplace URLs with userinfo (`https://user:token@host/...`) into the manifest unredacted, and the scaffold rendered them into the Containerfile.

**Why this matters:**
A token in a marketplace URL would land in the manifest, the build context and an image layer.

**How it was resolved:**
Discovery drops URL userinfo from marketplace sources (only a plain `git` SSH user is kept) and adds a note; a marketplace whose URL still carries a credential is skipped. The scaffold refuses a source with a URL password, a credential shape or a placeholder. `redact` gained a `scheme://user:secret@host` shape (`KindURLSecret`) with positive and negative tests. In addition, a literal value under a secret-looking MCP env key is refused, a url-server credential must be a placeholder or a `${VAR}` reference, and the rendered `.claude.json` is scanned for credential shapes before it is written.

### FINDING-11
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/plan/fragments.go, internal/version/version.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
Images installed mise by piping the unpinned remote installer script into a root shell.

**Why this matters:**
The image content depended on whatever the installer endpoint served at build time, which breaks the pinning rule (constitution III) and is a supply-chain risk.

**How it was resolved:**
The mise fragment downloads the static release binary for `MisePin` (`mise-v2026.10.4-linux-<arch>-musl`) with connect and total timeouts and up to three attempts, verifies it against a per-architecture sha256 in `internal/version`, and installs it with mode 0755. The arm64 checksum was checked against a downloaded asset.

### FINDING-12
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/cli/root.go, internal/engine/exec.go, internal/gateway/openshell.go, internal/mise/lockcmd.go, internal/build/build.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
sandcast had no signal handling. Ctrl-C killed the process while podman, docker, mise or openshell children kept running, and temporary directories and stage images leaked.

**Why this matters:**
An interrupted build left orphaned engine processes and images, and the next run could collide with them.

**How it was resolved:**
`Execute` uses `signal.NotifyContext` for SIGINT and SIGTERM and exits 130 with "interrupted". Every child process gets `cmd.Cancel` (SIGTERM) and a 15 second `WaitDelay`. The build's failure cleanup (debug tag, stage removal) runs with `context.WithoutCancel`. A CLI test interrupts a build in pass 2 and asserts exit 130 and the debug tag.

### FINDING-13
- **Severity:** Important
- **Confidence:** 75
- **File:** internal/cli/build.go, internal/engine/exec.go, internal/cli/root.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
A failed build printed only the path of `build.log`, and `--verbose` had no effect outside `build`.

**Why this matters:**
Users and the build skill had to open a file to see the actual engine error; slow pulls looked like a hang.

**How it was resolved:**
A failed build without `--verbose` prints the last 25 lines of `build.log` to stderr. `--verbose` now traces every engine command line (`+ podman pull ...`) through the engine's `Trace` hook. Tests cover both.

### FINDING-14
- **Severity:** Important
- **Confidence:** 75
- **File:** internal/component/remote.go, internal/pipeline/pipeline.go, internal/cli/components.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`components refresh` reported every rejected file as a checksum mismatch, even when the download failed or the file did not parse, and an oversized file was silently truncated to 1 MiB and then reported as tampered.

**Why this matters:**
A network error looked like a supply-chain attack, and the real cause was lost.

**How it was resolved:**
`Refresh` records a reason per file (download failed, checksum mismatch, larger than 1 MiB, invalid component), writes `<path>\t<reason>` lines to `rejected.txt`, and returns `reasons` in the JSON report. `LoadCached` returns `Rejection` values (legacy lines read as checksum mismatch); plan notices and the CLI print the reason. Tests cover a 404 and an oversized file.

### FINDING-15
- **Severity:** Important
- **Confidence:** 75
- **File:** internal/cli/profiles.go, internal/pipeline/profiles.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
When an `openshell` call failed part way through `profiles import`, the command printed only the error and dropped the list of profiles the gateway had already accepted.

**Why this matters:**
The user could not tell which profiles exist on the gateway and whether a retry needs `--update`.

**How it was resolved:**
On a partial failure the command prints the partial report (JSON gains an `error` field) and exits 1. A CLI test makes the fake gateway fail after the first import.

### FINDING-16
- **Severity:** Important
- **Confidence:** 85
- **File:** .github/workflows/ci.yml, tests/integration/helpers_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
CI did not install mise, so the core integration tests skipped silently in CI.

**Why this matters:**
The end-to-end build path was never exercised in CI, while the job reported success.

**How it was resolved:**
CI installs the pinned mise release and verifies it against `SHASUMS256.txt`. `requireMise` and `requireEngine` fail instead of skipping when `CI` is set.

### FINDING-17
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/cli/refresh_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`components refresh` had no CLI test, and the rejection notice in the plan was untested.

**Why this matters:**
FR-017 behavior (reject tampered files, keep the previous copy, notify) could regress unnoticed.

**How it was resolved:**
CLI tests run refresh against an httptest registry, tamper with one file, and assert the rejected line, the kept copy and the plan notice.

### FINDING-18
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/cli/lock_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`sandcast lock` had no CLI test.

**Why this matters:**
The host mise version gate, exit codes and lockfile writing were unverified at the command level.

**How it was resolved:**
CLI tests use a fake `MiseRunner` to cover the default platform, the union of targets, version bumps with JSON output, a missing host mise (exit 3), a tool that cannot be locked (exit 1) and a manifest with nothing to lock.

### FINDING-19
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/cli/helpers_test.go, internal/cli/profiles_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
The fake gateway ignored the `--file` argument, so tests could not check what was imported; `resource_version` on update and platform scope imports were untested.

**Why this matters:**
FR-023 import behavior (create-only, update with resource version, suffixed ids at platform scope) was asserted only by call counts.

**How it was resolved:**
The fake gateway parses every imported or updated profile file and records id, resource version, binaries and scope. Tests assert the resource version on update, suffixed ids with `--global` at platform scope, and resolved binaries.

### FINDING-20
- **Severity:** Important
- **Confidence:** 75
- **File:** tests/integration/core_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
The integration test did not read the adapted profiles back from the built image.

**Why this matters:**
Pass 2 (profiles copied into the image, labels set) was only checked on the host side.

**How it was resolved:**
The integration test parses the `io.sandcast.profiles-index` label of the built image, deletes the host copy of the profiles, runs `profiles export --suffix` to read the adapted set back out of the image, and checks every non-glob binary path of that set.

### FINDING-21
- **Severity:** Important
- **Confidence:** 75
- **File:** internal/profile/mcp_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
No test showed that an MCP server already covered by the agent's own profile produces no generated profile.

**Why this matters:**
FR-039 requires generated profiles only for uncovered endpoints; duplicates would widen the policy.

**How it was resolved:**
`TestMCPProfilesCoveredByAgentProfile` asserts a notice and no generated profile for a covered server.

### FINDING-22
- **Severity:** Important
- **Confidence:** 75
- **File:** internal/cli/notices_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
Pipeline notices (tier overrides, pull failure fallback, base image not in the registry) were untested.

**Why this matters:**
These notices are the only signal that a definition was overridden or an image came from local storage.

**How it was resolved:**
CLI tests assert each notice.

### FINDING-23
- **Severity:** Important
- **Confidence:** 75
- **File:** tests/integration/helpers_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
Integration tests used the developer's real sandcast cache and config directories.

**Why this matters:**
Results depended on local state, and tests could overwrite a developer's probe cache or user registry.

**How it was resolved:**
Each integration test gets its own `SANDCAST_CACHE_DIR` and `SANDCAST_CONFIG_DIR`.

### FINDING-24
- **Severity:** Important
- **Confidence:** 90
- **File:** internal/harness/discovery.go, internal/redact/redact.go
- **Category:** architecture
- **Source:** architecture-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
The heuristic that decides whether a key names a credential existed twice, and the copies had drifted: discovery treated `GITHUB_PAT`, `BASIC_AUTH`, `ACCESS_KEY` and `*_CREDENTIALS` as secrets, while manifest validation and the build output scan did not.

**Why this matters:**
The same value was a secret during discovery and not a secret when the manifest or the build outputs were scanned, which undermines constitution IV.

**How it was resolved:**
`redact.SecretName` is the single definition. It includes the credential and credentials suffixes and treats a final KEY, AUTH or PAT as a credential only in upper-case env var style names with two or more words, which avoids false positives on fields such as `component_key`. `harness.SecretName` delegates to it. The redact test table covers both previous lists.

### FINDING-25
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/profile/mcp.go, internal/harness/discovery.go, internal/redact/redact.go
- **Category:** architecture
- **Source:** architecture-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
The env-reference regex was defined three times and the placeholder regex twice.

**Why this matters:**
The MCP profile credential and the scaffolded agent configuration agree on the credential variable only while these copies stay identical.

**How it was resolved:**
`redact.EnvRef` is the single parser; `profile.MCPCredentialVar` uses `redact.IsPlaceholder` and `redact.EnvRef`; the harness and profile copies are gone.

### FINDING-26
- **Severity:** Important
- **Confidence:** 90
- **File:** internal/plan/plan.go, internal/plan/select.go
- **Category:** architecture
- **Source:** architecture-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
`plan.Build` rebuilt the mise tool list inline with its own copy of the version rule, while `MiseTools` (used by `lock`) did not sort as its comment claimed.

**Why this matters:**
FR-014 and FR-026 rely on the lockfile matching the rendered mise configuration; two copies of the version rule can drift.

**How it was resolved:**
One `miseTool` rule is used by both; `MiseTools` sorts by key; the comment states that lock includes tools the base image already has so the lockfile does not depend on the probe. A test covers ordering and the version rule.

### FINDING-27
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/build/resolve.go, internal/probe/script.go, internal/harness/claudecode/scaffold.go
- **Category:** architecture
- **Source:** architecture-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
There were two JSON escapers in sh and three `shellQuote` helpers. The resolve script's escaper needed `sed` and `tr` and deleted control characters instead of escaping them.

**Why this matters:**
Binary resolution failed in images without sed or tr, exactly the case the probe had been hardened against.

**How it was resolved:**
New package `internal/shell` with `Quote`, `QuoteIfNeeded` and `JSONHelpers` (builtins only). The probe, the resolve script and the scaffold use it, and the resolve script uses the Method constants. Tests run the escaper with an empty PATH and the resolve script with a PATH that holds only the tool.

### FINDING-28
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/cli/probe.go, internal/pipeline/probe.go
- **Category:** architecture
- **Source:** architecture-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:**
The `probe` command re-implemented the probe stage of `pipeline.Plan` line for line.

**Why this matters:**
`sandcast probe` and `sandcast plan` must report the same delta for the same image; the copies had already diverged in their error text.

**How it was resolved:**
`pipeline.ProbeBase` and `Probed.Delta` hold the stage; `Plan` and the probe command both call it, and the command only prints. A test covers probing a reference without a manifest.

### FINDING-29
- **Severity:** Important
- **Confidence:** 95
- **File:** internal/cli/components.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
`components refresh --json` returned before the rejection check and exited 0 when files were rejected.

**Why this matters:**
Automation reads JSON mode; a tampered registry file looked like a successful refresh (FR-017, exit code contract).

**How it was resolved:**
Both output modes share one exit path; a CLI test asserts exit 1 and the reason in JSON mode.

### FINDING-30
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/component/remote.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
An interrupted refresh, or one with a transient download failure, installed the partial staging directory and switched the active tag; cancelled downloads were recorded as rejections.

**Why this matters:**
A Ctrl-C or a network hiccup could drop registry components from the active tier and leave misleading "context canceled" notices in every later plan.

**How it was resolved:**
A cancelled context returns before the cache is touched. A download failure returns the report with `ErrIncomplete` and leaves the cache and the active tag unchanged (`installed: false`, accepted and kept lists cleared). The install moves the old directory aside and restores it if the rename fails. Tests cover download failure, cancellation and the CLI output.

### FINDING-31
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/cli/root.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
The registry fetch used `http.DefaultClient`, which has no timeout.

**Why this matters:**
A stalled server or proxy hung `components refresh` until a CI job timed out.

**How it was resolved:**
The CLI uses an HTTP client with a 60 second timeout (`RegistryTimeout`), documented in docs/cli.md.

### FINDING-32
- **Severity:** Important
- **Confidence:** 88
- **File:** internal/harness/claudecode/scaffold.go
- **Category:** architecture
- **Source:** architecture-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
The scaffold reimplemented a weaker subset of discovery's credential rules: an opaque token used as a marketplace URL user, an `access_token` query value and a short value after `--api-key` all passed into the Containerfile and `.claude.json`.

**Why this matters:**
The manifest can be hand-edited, so the scaffold is the real gate before the image (constitution IV, FR-008).

**How it was resolved:**
The scaffold dry-runs discovery's redactor on a copy of each MCP server and on each marketplace source (`sourceCarriesCredential`, shared with discovery) and refuses anything it would redact. Discovery now also redacts a user-only URL userinfo other than `git`. Scaffold tests cover the three bypasses.

### FINDING-33
- **Severity:** Important
- **Confidence:** 90
- **File:** internal/redact/redact.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
The round-1 consolidation counted a final key, auth or pat only in upper-case names, so lower-case flags, query parameters and camelCase keys (`--access-key`, `access_key=`, `privateKey`) were no longer detected.

**Why this matters:**
These names are where credentials appear in MCP commands and URLs; they reached the manifest and the image.

**How it was resolved:**
A credential qualifier before the final word (access, private, client, ...) counts in any case, and `SecretParam` handles flags and query parameters. Later rounds refined it further (FINDING-45). Tests cover each name.

### FINDING-34
- **Severity:** Important
- **Confidence:** 78
- **File:** internal/redact/redact.go, internal/harness/discovery.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
No pattern matched HTTP Basic credentials, and `Authorization` header arguments of MCP commands were not parsed.

**Why this matters:**
`mcp-remote --header "Authorization: Basic ..."` is a common configuration and reached the image unredacted.

**How it was resolved:**
A `basic-auth` shape (base64 of `user:password`) and header-argument redaction in discovery; PGP private key blocks were added in the same change. Tests cover both.

### FINDING-35
- **Severity:** Important
- **Confidence:** 90
- **File:** internal/cli/build_test.go, internal/engine/fake.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
The interrupted-build test could not detect a revert of the non-cancellable cleanup, because the fake engine ignored contexts.

**Why this matters:**
A regression would silently lose the debug image after Ctrl-C.

**How it was resolved:**
Every fake engine method fails with the context error after recording the call, like `exec.CommandContext`. The test asserts the debug tag, the removed stage tag and the printed line; a mutation check confirmed it fails when the fix is reverted.

### FINDING-36
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/pipeline/pipeline.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
No test covered the plan-time refusal of a stored fragment that carries a credential.

**Why this matters:**
Deleting the check would copy the credential into plan.yaml unnoticed.

**How it was resolved:**
A CLI test plants a token in a fragment and asserts exit 2, the fragment path in the message, no token in the output and no plan.yaml; a mutation check confirmed it.

### FINDING-37
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/build/build.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
The pre-build scan of host files staged for the harness, and the skip list of `RedactPass`, were untested.

**Why this matters:**
Moving the staging after the scan or skipping `harness/` would leak host credentials into the image without a failing test.

**How it was resolved:**
A build test stages a skill with a token and asserts the refusal and no engine build; a `RedactPass` test asserts that skip names apply only at the top level.

### FINDING-38
- **Severity:** Important
- **Confidence:** 75
- **File:** internal/cli/helpers_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
The fake gateway accepted any unrecognized call, and `--gateway`, `--workspace` and platform-scope updates were untested.

**Why this matters:**
Dropping the workspace from import calls would go unnoticed (FR-023).

**How it was resolved:**
The fake strips global flags and fails unknown calls. Tests assert that every call carries `--gateway` and `--workspace`, and that platform-scope export and update carry `--global`.

### FINDING-39
- **Severity:** Important
- **Confidence:** 92
- **File:** internal/redact/redact.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:**
After the round-1 consolidation, a key named `credential` or `credentials` counted as secret-holding, so the post-build scan flagged structural values in the vendored aws, aws-s3, codex, google-cloud and google-vertex-ai profiles.

**Why this matters:**
Every build that used one of these components would fail with a credential finding and lose its image.

**How it was resolved:**
A bare `credential(s)` field is not secret-holding (it names credentials); as the last word of a multi-word name it is. A regression test adapts every vendored and overlay profile and scans them together with every shipped component, policy and registry file.

### FINDING-40
- **Severity:** Important
- **Confidence:** 72
- **File:** internal/harness/discovery.go, internal/redact/redact.go
- **Category:** architecture
- **Source:** architecture-agent
- **Round found:** 3
- **Resolution:** fixed (round 3)

**What is wrong:**
The Authorization header rule existed only in discovery; `redact.Scan` (fragments, manifest validation, the build output scan) missed `Authorization: Token <opaque>`.

**Why this matters:**
A fragment with `curl -H "Authorization: token ..."` reached the image.

**How it was resolved:**
An `authorization-header` shape in redact (after Bearer and Basic, excluding references, templates and values without digits) and `redact.IsCredentialHeader`, used by discovery.

### FINDING-41
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/redact/redact.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 3
- **Resolution:** fixed (round 3)

**What is wrong:**
`SMTP_PASS`, `MYSQL_PWD`, and base64 `user:password` under Docker or npm `auth` fields were not detected.

**Why this matters:**
These are typical shell rc exports, and the curated rc is copied into the image.

**How it was resolved:**
`pass` and `pwd` join the final-word rule; an `auth` field holding base64 of `user:password` is a credential (`secretPair`). Tests cover each case and the shipped-content regression stays clean.

### FINDING-42
- **Severity:** Important
- **Confidence:** 72
- **File:** internal/redact/redact.go
- **Category:** security
- **Source:** security-agent
- **Round found:** 3
- **Resolution:** fixed (round 3)

**What is wrong:**
The round-2 narrowing dropped lower-case and camelCase multi-word names such as `aws_credentials` and `dbCredentials`.

**Why this matters:**
A literal value under such an MCP env key passed every check into `.claude.json`.

**How it was resolved:**
`credential(s)` as the last word of a multi-word name counts in any case; bare `credentials` stays clean.

### FINDING-43
- **Severity:** Important
- **Confidence:** 90
- **File:** internal/harness/discovery.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 3
- **Resolution:** fixed (round 3)

**What is wrong:**
Arguments such as `DB_PASSWORD=adm:hunter2` took the header branch, so only the part after the colon was replaced and the value was altered.

**Why this matters:**
Part of the credential leaked into the manifest and the image, and the server received a wrong value.

**How it was resolved:**
`assignment()` picks the branch by which separator comes first; header names must match `^[A-Za-z0-9][A-Za-z0-9_-]*$`; the joined `--header=Authorization: ...` form is handled. Tests cover each case.

### FINDING-44
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/redact/redact.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 3
- **Resolution:** fixed (round 3)

**What is wrong:**
Common credential flags and query parameters (`--account-key`, `subscription-key`, `--admin-key`, `--ado-pat`) were not recognized.

**Why this matters:**
Their values reached the manifest, plan.yaml and `.claude.json`.

**How it was resolved:**
For flags and query parameters, a name ending in key, auth or pat is a credential unless a plain qualifier (sort, cache, public, ...) precedes it. The same plain qualifiers stop `PUBLIC_KEY` or `CACHE_KEY` from being flagged.

### FINDING-45
- **Severity:** Important
- **Confidence:** 95
- **File:** internal/cli/profiles_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 3
- **Resolution:** fixed (round 3)

**What is wrong:**
The export credential test planted an invalid profile, so it passed through a parse error even with the scan removed.

**Why this matters:**
The only test of the export scan could not detect its removal.

**How it was resolved:**
The token sits in a field of a valid profile and the test asserts the credential message; a mutation check confirmed it.

### FINDING-46
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/harness/claudecode/scaffold_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 3
- **Resolution:** fixed (round 3)

**What is wrong:**
The url-server literal check lost its only test when the dry-run gate started refusing the same input earlier.

**Why this matters:**
Removing the check would bake `Authorization: Bearer <literal>` into `.claude.json`.

**How it was resolved:**
A case with a plain env key that only the url-server check refuses; a mutation check confirmed it.

### FINDING-47
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/harness/discovery.go, internal/redact/redact.go
- **Category:** architecture
- **Source:** architecture-agent (also reported by: security-agent)
- **Round found:** final re-review
- **Resolution:** fixed after the final round (tests; not re-reviewed)

**What is wrong:**
Header redaction treated the first word of every credential header value as a scheme. For `Cookie: session=SECRET; theme=dark` the session cookie stayed in clear text and only `theme=dark` was replaced.

**Why this matters:**
The session cookie reached the manifest and the image's `.claude.json` (FR-008).

**How it was resolved:**
`redact.HeaderSecret` keeps a scheme word only for Authorization and Proxy-Authorization; for every other credential header the whole value is the secret. The scan side gained Cookie and Set-Cookie in the header shape. Tests cover a multi-cookie header.

### FINDING-48
- **Severity:** Important
- **Confidence:** 92
- **File:** internal/cli/root.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Round found:** final re-review
- **Resolution:** fixed after the final round (not re-reviewed)

**What is wrong:**
The deferred `stop()` of `signal.NotifyContext` cancels the context, so a run that was never interrupted could print "interrupt received" while exiting (about 5 in 300 runs in the reviewer's reproduction).

**Why this matters:**
The skills read sandcast's stderr; a false interrupt line can make an agent retry or report a failure.

**How it was resolved:**
The message is printed only when `context.Cause` shows a signal rather than the plain cancellation of `stop()`.

### FINDING-49
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/harness/discovery.go
- **Category:** security
- **Source:** security-agent
- **Round found:** final re-review
- **Resolution:** fixed after the final round (tests; not re-reviewed)

**What is wrong:**
The two-argument header form (`--headers X-Api-Key VALUE`, `-H Authorization "Token VALUE"`) was not redacted.

**Why this matters:**
mcp-proxy configurations use this form; the values reached the image whatever their length.

**How it was resolved:**
Discovery remembers a credential header name that follows a flag and redacts the next argument through `HeaderSecret`. Tests cover both forms and the nested `--env=KEY=value` form.

### FINDING-50
- **Severity:** Important
- **Confidence:** 72
- **File:** internal/redact/redact.go
- **Category:** security
- **Source:** security-agent
- **Round found:** final re-review
- **Resolution:** fixed after the final round (tests; not re-reviewed)

**What is wrong:**
`Scan`, `ScanYAML` and `RedactPass` judged query parameters and `--flag=value` pairs with the field-name rule, so `?key=...`, `--key=...` and `--account-key=...` in staged skill files passed the build scan.

**Why this matters:**
A skill with a literal API key in a URL was baked into the image.

**How it was resolved:**
In query and flag positions the scan uses `SecretParam`; a Google API key shape and `passphrase` were added. Tests cover each case and the shipped-content regression stays clean.

### FINDING-51
- **Severity:** Important
- **Confidence:** 85
- **File:** internal/redact/redact.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** final re-review
- **Resolution:** fixed after the final round (tests; not re-reviewed)

**What is wrong:**
`IsCredentialHeader` fell back to the field-name rule, so `Ocp-Apim-Subscription-Key` and `X-Auth` were not credential headers.

**Why this matters:**
Their values reached the manifest and `.claude.json`.

**How it was resolved:**
Header names use the parameter rule (`SecretParam`); `Idempotency-Key` stays plain. Tests cover both names.

### FINDING-52
- **Severity:** Important
- **Confidence:** 90
- **File:** internal/redact/redact.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** final re-review
- **Resolution:** fixed after the final round (tests only)

**What is wrong:**
Two of the three clauses of the credential-word rule (upper-case env names and a qualifier before a middle word) had no test.

**Why this matters:**
Reverting either clause would let `client_credentials_json` or `AWS_CREDENTIALS_B64` pass every scan unnoticed.

**How it was resolved:**
Rows for `client_credentials_json`, `AWS_CREDENTIALS_B64` (true) and `aws_credentials_b64` (false).

### FINDING-53
- **Severity:** Important
- **Confidence:** 90
- **File:** internal/build/build.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** final re-review
- **Resolution:** fixed after the final round (tests only)

**What is wrong:**
The report of a kept stage that could not be removed, and the "was removed because it failed the credential scan" message, were untested.

**Why this matters:**
A regression would leave a credential-bearing stage image behind silently.

**How it was resolved:**
A test with `--keep-stage` and an engine that cannot remove the stage asserts the message and the reported stage; the existing leak test asserts the removal message.

## Minor Findings (fixed)

| Round | Source | File | Finding | Resolution |
|-------|--------|------|---------|------------|
| 1 | production | internal/component/remote.go | Refresh installed the cache non-atomically | Backup-rename install (round 2) |
| 1 | production | internal/cli/root.go | No HTTP client timeout | 60 s client timeout (round 2) |
| 1 | production | internal/plan/fragments.go | No curl timeouts | Timeouts and retries on the pinned mise download |
| 1 | architecture | internal/build/build.go | Debug tag lifecycle gaps | Non-cancellable bounded cleanup, stage reported when tagging fails, stale debug image removed |
| 2 | production | internal/cli/root.go | Second Ctrl-C swallowed, cleanup unbounded | Default handling restored after the first signal; cleanup bounded to 30 s |
| 2 | production | internal/build/build.go | Stage removal failure invisible, interrupted success exits 0 | Exit 130 whenever the context is cancelled; failures reported (rounds 2 to final) |
| 2 | production | internal/plan/fragments.go | mise download loop exited 0 after three failures | Explicit failure with a message after the third attempt |
| 2 | production | resolve, export, probe | Engine stderr repeated | `engine.WithStderr`, one dispatch path |
| 2 | production, architecture | internal/engine/exec.go | `ImageExists` not traced | All engine calls go through `Exec.exec` |
| 2 | architecture | internal/plan/select.go | mise tool ignored the effective selector | `miseTool` uses `Wanted.Selector()` |
| 2 | architecture | internal/pipeline/profiles.go, internal/plan/fragments.go | Duplicated constants | Derived from `profile.IndexFile` and `mise.ImageConfigDir` |
| 2 | architecture | internal/build/build.go | Build errors flattened the cause | Both errors wrapped with `%w` |
| 2 | security | internal/build/build.go | `.git` copied into the image | VCS directories skipped when staging |
| 2 | security | internal/redact/redact.go | PGP private key blocks not detected | Pattern extended |
| 2 | security | internal/cli/profiles.go | Export without the symlink check | `CheckNoSymlinks` before export |
| 2 | correctness | internal/harness/claudecode/scaffold.go | Numeric final user refused | Numeric user and group accepted |
| 2 | test-quality | several | Invalid component, implicit git, intermediate symlink, stageFile refusals, isolated `sandcastCmd` untested | Tests added |
| 3 | architecture | scaffold, discovery, build, pipeline, engine | Dead env check, `harness.SecretName` alias, duplicated source predicate, stderr tail, incomplete report lists, `ImportReport.Error` set by the CLI, literal `verify.yaml`, tag default twice | Each removed or unified |
| 3 | production | cli, probe, build, docs | Ctrl-C at the promote prompt, interrupted verify recorded missing checks, kept stage after a credential finding, docs wording | `readLine`, `RunChecks` stops on interrupt, stage removed, docs fixed |
| 3 | security | redact, discovery, pipeline, build | Flow values, joined header flags, Cookie, unscanned export and import, profiles reaching the image before a scan | Each fixed with tests |
| 3 | correctness | redact, pipeline, cli | Upper-case public fields flagged, scaffold validation exit 1, invalid `--ref` exit 1 | Plain qualifiers, `harness.ErrInvalidManifest`, `ValidTag` |
| 3 | test-quality | several | Final scan, invalid group, harness exit 2, flow items, header exclusions, Exec errors, refresh lists, removal failure | Tests added |
| final | architecture | internal/component/remote.go, internal/pipeline/profiles.go | Tag check duplicated; scan errors lacked location | `ValidTag`/`ErrInvalidTag`; errors name the image or directory |
| final | production | cli, build, component | `--ref current` corrupted the cache; removed image not reported; null JSON arrays | Allowlist with reserved name; removal reported; empty arrays |
| final | security | redact, discovery | `passphrase`; `--env=KEY=value` | Added |
| final | correctness | redact, discovery, build, verify | `$HOME/...` paths flagged; `SKIP_AUTH=1` flagged; flow brackets lost on Replace; `verify.yaml` unscanned | `IsPathLike`, plain qualifiers, narrowed span, report scanned on write and excluded from image scans |
| final | test-quality | redact, discovery, cli, component, build | Qualifier entries, header exclusions, header-name guard, Forceable values, profiles before pass 2, EOF, reserved tag, kept list | Tests added |

## Notable Observations

### NOTABLE-1
- **File:** internal/redact/redact.go, internal/harness/discovery.go
- **Category:** architecture, security
- **Source:** synthesis of all review passes
- **Description:** Credential detection by name and shape heuristics did not converge during the review. Every widening produced false positives on shipped content, and every narrowing reopened a leak.
- **Rationale:** A deny-by-default rule for the places where credentials actually flow (every MCP env value, header value and secret-flag argument must be a `${SANDCAST_SECRET_N}` placeholder or a `${VAR}` reference unless explicitly allow-listed) would replace most of the heuristics with one checkable rule. The scans would then remain a backstop.

### NOTABLE-2
- **File:** internal/component/remote.go
- **Category:** security
- **Source:** security-agent
- **Description:** The registry index checksums come from the same tag as the files, and a tag can be moved.
- **Rationale:** Compile the sha256 of the pinned `index.yaml` into the binary, or pin the registry to a commit, as was done for the mise binary.

### NOTABLE-3
- **File:** internal/engine/exec.go
- **Category:** production-readiness
- **Source:** production-readiness-agent
- **Description:** Containers run `/bin/sh` as PID 1 without `--init`, so forwarded signals are ignored and an interrupt waits for the 15 s `WaitDelay`; a hung check can leave an orphaned container.
- **Rationale:** Name each run and remove it on cancellation, or add `--init`.

### NOTABLE-4
- **File:** internal/build/build.go
- **Category:** security
- **Source:** security-agent
- **Description:** Archives and other compressed files in staged skill or marketplace trees are copied into the image without a scan that can see inside them.
- **Rationale:** Refuse or report archives by magic bytes, as was done for VCS directories.

### NOTABLE-5
- **File:** .github/workflows/ci.yml, tests/integration/gateway_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Description:** CI does not run `make test-gateway`, and the gateway tests skip silently without the openshell CLI.
- **Rationale:** An optional or nightly CI job with the pinned openshell CLI would catch drift between `internal/gateway` and the real gateway.

### NOTABLE-6
- **File:** internal/build/build.go, internal/harness/claudecode/scaffold.go, internal/fsutil/layout.go, internal/pipeline/pipeline.go
- **Category:** architecture
- **Source:** architecture-agent
- **Description:** The "path inside root" check is hand-written in several places with two different rules.
- **Rationale:** One `fsutil.Within` helper would make the containment guarantees uniform.

### NOTABLE-7
- **File:** internal/cli/adapters.go
- **Category:** architecture
- **Source:** architecture-agent
- **Description:** Harness adapters are registered only through a blank import in the CLI package.
- **Rationale:** A library user of `pipeline.Plan` gets "no adapter" without a compile-time signal.

### NOTABLE-8 to NOTABLE-14
Short notes, kept for brainstorming:
- The profile index digest is the stage image ID, not a registry digest (correctness).
- The test gateway runs on the sandbox network (security).
- mise plugin backends run on the host during `sandcast lock` (security).
- The build skill should treat build logs as untrusted data (security).
- The probe cache is never evicted (production).
- The host platform comes from the binary architecture, not from the engine (production).
- `SortedKeys` helpers are duplicated across packages (architecture).

## Post-Fix Spec Coverage

Code was removed during the fix loop: duplicated heuristics, the CLI probe logic and the shell helpers. Every FR-NNN implementation listed in code-review.md was re-checked after round 1 and again on the final code. The functions and files are present and exercised by tests: `refuseSecrets`/`CheckSecrets` (FR-008), `probe.Probe`/`Script` (FR-011), `MiseTools`/`MiseFragment`/`RenderConfig` (FR-014), `ResolveScript`/`Resolve` (FR-022), `MCPProfiles`/`MCPCredentialVar` (FR-039), `component.Refresh` (FR-017), `pipeline.Import` (FR-023), `FinalUserFor` (FR-040), and the rest of the matrix.

All spec requirements verified after the fix loop.

## Test Suite Results

| Round | Test Command | Exit Code | Failures | Status |
|-------|-------------|-----------|----------|--------|
| 1 | make verify | 0 | 0 | passed |
| 1 | make test-integration, make test-gateway | 0 | 0 | passed |
| 2 | make verify, make test-integration | 0 | 0 | passed |
| 3 | make verify, make test-integration | 0 | 0 | passed |
| after final round | make verify (23 packages, lint 0 issues), make test-integration (205 s), make test-gateway (62 s) | 0 | 0 | passed |

Test suite passed in all fix rounds. Mutation checks with `go test -overlay` confirmed that the new tests for the interrupt cleanup, the stored-fragment scan, the export scan and the url-server literal check fail when the guarded code is reverted.

## Additional Fix After the Review

The harness discovery fixtures lived under `testdata/.../.claude/` and `testdata/project/.mcp.json`, which the repository's `.gitignore` excludes, so a fresh clone (and CI) would have lacked them. The fixtures are now stored as `dot-claude/`, `dot-mcp.json` and so on, and `discover_test.go` restores the leading dots after copying them to a temporary directory. No `.mcp.json` file is part of the repository.

## Remaining Findings

No Critical or Important finding is open. The gate reports FAIL only because the 7 Important findings of the final re-review were fixed after the last allowed round and have not been reviewed again.

Open Minor findings, for a follow-up (duplicates across passes are merged):

| Source | File | Finding |
|--------|------|---------|
| architecture | internal/harness/discovery.go | `Redactor` keeps its own numbering instead of one `redact.Replacer`; one secret can get two placeholders |
| architecture | internal/harness/discovery.go, internal/redact/redact.go | `Forceable` and `secretValue` overlap; `manifest validate` and the scaffold can disagree on short values |
| architecture | internal/engine/exec.go, internal/probe/probe.go | Ownership of stderr in `Engine.Run` errors is by substring matching |
| architecture | internal/cli/fragments.go, internal/pipeline/pipeline.go | Fragment credential scan implemented in two places |
| architecture | internal/cli/profiles.go | `profiles export` gathers its inputs in the CLI |
| architecture | internal/redact/redact.go | `ReplaceFrom` has no production caller |
| architecture | internal/engine, internal/gateway, internal/mise | Graceful-cancel setup copied three times |
| architecture | internal/pipeline/probe.go, internal/cli/probe.go | `Probed.Delta` duplicates the delta loop; probe command builds `Env` by hand; `Probed.Set` unused |
| architecture | internal/cli | Build layout and plan loading reconstructed per command; manifest validation logic in the CLI |
| architecture | internal/verify, internal/component, internal/mise | `checkHash` duplicates `CheckHash`; `compareVersions` duplicates `component.Compare` |
| architecture | several | Dead exported API, literal constants, `plan.HarnessComponent` global, YAML encoder options copied, plan problems implemented three times |
| production | internal/build | No lock against concurrent builds of the same project |
| production | internal/plan | Host paths in plan.yaml affect determinism across hosts |
| production | internal/probe | No per-check timeout; pull on every plan; no engine version check |
| security | internal/component | Registry trust anchor (see NOTABLE-2) |
| security | internal/plan | Declarative component fields rendered into RUN lines without validation |
| security | internal/plan/fragments.go | `/opt/mise` writable by the final user |
| test-quality | tests/integration, internal/cli | Weak verify assertions, platform source flag, capture test claims, docker path, test globals, gateway volume reset, vacuous "no retry" check |
| test-quality | internal/engine, internal/probe, internal/pipeline | `gracefulCancel`, `ImageExists`/`ImageID`, the passwd fallback in the probe and `ExportFromImage` file name sanitization have no direct tests |
