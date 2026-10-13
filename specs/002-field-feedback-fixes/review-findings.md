# Deep Review Findings

**Date:** 2026-10-13
**Branch:** 002-field-feedback-fixes
**Rounds:** 2
**Gate Outcome:** PASS
**Invocation:** quality-gate (ship pipeline, stage 7)

## Summary

| Severity | Found | Fixed | Remaining |
|----------|-------|-------|-----------|
| Critical | 0 | 0 | 0 |
| Important | 5 | 5 | 0 |
| Minor | 25 | 12 | 13 |
| Notable | 4 | - | 4 |
| **Total** | **34** | **17** | **17** |

**Agents completed:** 5/5 internal (Goal Alignment skipped: no PR) + CodeRabbit (2 runs). Round 2 re-reviewed the fix diff with one combined agent plus CodeRabbit `--uncommitted`.
**Agents failed:** Codex (usage limit reached, zero findings recorded). Copilot disabled in config.

Spec compliance findings from stage 1 (before the deep review) are listed in `REVIEW-CODE.md`.

## Findings

### FINDING-1
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/pipeline/profiles.go, internal/gateway/openshell.go
- **Category:** correctness
- **Source:** correctness-agent
- **Round found:** 1
- **Resolution:** fixed (round 1, OPENSHELL_GATEWAY_ENDPOINT and unlisted names in round 2)

**What is wrong:** The FR-014 remote gateway warning asked for the entry marked `active` and ignored the gateway `profiles import --gateway <name>` or `OPENSHELL_GATEWAY` selects.

**Why this matters:** Importing into a remote gateway while a local one is active gave no warning, the exact pitfall FR-014 targets; the reverse gave a false warning.

**How it was resolved:** `ActiveGateway` now picks the entry named by `--gateway` or `OPENSHELL_GATEWAY`, else the one whose endpoint `OPENSHELL_GATEWAY_ENDPOINT` names, else the active entry. A selected gateway that is not listed counts as unknown (no warning). Tests cover each selector.

### FINDING-2
- **Severity:** Important
- **Confidence:** 72
- **File:** internal/pipeline/profiles.go
- **Category:** production-readiness
- **Source:** production-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:** `openshell gateway list` ran with the command context, which has no deadline, before every OpenShell build when the CLI is installed.

**Why this matters:** A hanging openshell CLI blocked `sandcast build` indefinitely, although FR-014 says a gateway is never a requirement.

**How it was resolved:** `GatewayWarning` bounds the lookup with `GatewayLookupTimeout` (5s); a timeout yields no warning. `TestGatewayWarningBoundsLookup` uses a runner that blocks until its context ends.

### FINDING-3
- **Severity:** Important
- **Confidence:** 80
- **File:** tests/integration/core_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1, tmpfs option corrected in round 2)

**What is wrong:** US1 scenario 5 (empty workspace home, no zsh wizard) was never exercised: every container ran with the home directory the image built.

**Why this matters:** The wizard assertions passed only because of what `/sandbox` happened to contain.

**How it was resolved:** `TestSessionEnvironment` adds a pass with `--tmpfs /sandbox:rw,mode=1777,notmpcopyup` (podman copies image content into a tmpfs without `notmpcopyup`), asserts the home is empty, and checks the switched session is a login zsh with the curated rc (`LOGIN=y RC=1`), no wizard text, no `command not found`, and that `zsh -il` resolves every binary with empty stderr.

### FINDING-4
- **Severity:** Important
- **Confidence:** 80
- **File:** tests/integration/core_test.go
- **Category:** test-quality
- **Source:** test-quality-agent
- **Round found:** 1
- **Resolution:** fixed (round 1)

**What is wrong:** US1 scenario 1 requires every `MISE_*` variable in a login bash and any zsh; the test checked two variables in bash only.

**Why this matters:** Dropping the zshenv block for non-login zsh would have passed.

**How it was resolved:** The test reads the image's `Config.Env`, filters `MISE_*`, and compares it with `env` output in `bash -lc`, `zsh -c` and `zsh -il -c`; a plain `zsh -c` also resolves every binary.

### FINDING-5
- **Severity:** Important
- **Confidence:** 80
- **File:** internal/cli/verify.go
- **Category:** correctness (doc accuracy)
- **Source:** re-review (round 2)
- **Round found:** 2
- **Resolution:** fixed (round 2)

**What is wrong:** After the per-binary session check was reverted (it failed the integration suite, see REVIEW-CODE.md), `sandcast verify --help` still claimed that every component binary must resolve in the session.

**Why this matters:** The help text promised a check the code does not run.

**How it was resolved:** The help now says every component check runs again under session conditions. The FR-010 "every binary" gap stays open as an ambiguous compliance item.

### FINDING-6 to FINDING-17 (Minor, fixed)
- **Distinct skip reasons** (architecture, internal/harness/claudecode/discover.go): git missing, not the top level of a repository, no remote and a remaining credential now produce different skip notes. Fixed round 1.
- **verify help omitted `session.bash.startup`** (architecture, internal/cli/verify.go). Fixed round 1.
- **Switched session login and curated rc unasserted** (test-quality, tests/integration/core_test.go). Covered by the empty-home pass. Fixed round 1.
- **Name match not shown to come from the tracking ref; `@{u}` fallback untested** (test-quality, discover_test.go). New `upstream-market` fixture renames the marketplace locally and tracks only `origin/main`. Fixed round 1.
- **Docker Hub and gateway notices asserted by prefix only; no negative Docker Hub case** (test-quality, internal/cli/build_test.go). Fixed round 1.
- **unknown-version and mixed-path notice texts unasserted** (test-quality, internal/plan/delta_test.go). Fixed round 1.
- **Nested interactive bash re-switched to zsh** (CodeRabbit, internal/build/sections.go). Fixed in eaa967e.
- **Quickstart used wrong plan field names and build paths** (CodeRabbit, specs/002-field-feedback-fixes/quickstart.md). Fixed in eaa967e.
- **Interrupt snippet test could pass without the trap** (re-review, internal/skills/snippets_test.go): the stub exits 0 when it signals. Fixed round 2.
- **git honored the caller's `GIT_DIR`, `GIT_WORK_TREE`, `GIT_CONFIG_PARAMETERS`** (re-review, internal/harness/git.go): `gitEnv` drops repository-location and config-injection variables. Fixed round 2.
- **Gateway tests depended on the developer's `OPENSHELL_GATEWAY`** (re-review): the test app and `TestGatewayWarning` clear it. Fixed round 2.
- **`file:` remote proposed as portable** (correctness, part of FINDING-20): `portability` marks `file:` URLs not portable. Fixed round 1.

## Remaining Findings (Minor)

| # | Source | File | Description | Why not fixed |
|---|--------|------|-------------|---------------|
| R1 | correctness | internal/capture/match.go:96-111 | A `.tool-versions` or `mise.toml` pin is dropped from `pinned_versions` when the component was already matched as a host tool. | AMBIGUOUS: attach pins regardless of source, or promote the source to `project`; FR-004 speaks of project-sourced proposals. |
| R2 | correctness | internal/verify/verify.go | A component named `startup` collides with `session.zsh.startup`. | AMBIGUOUS: rename the startup keys (user-visible, documented) or use a separator keys cannot contain. |
| R3 | correctness | internal/harness/claudecode/discover.go | A remote that is a local path is proposed as a source that cannot resolve elsewhere. | AMBIGUOUS: skip it, or propose it marked not portable. |
| R4 | architecture | internal/plan/encode.go | `SessionEnvScript` variable is unused in production; goldens test a variant no image gets. | Refactor without behavior change; several layouts acceptable. |
| R5 | architecture | internal/imageref, internal/cli/init.go | Tag splitting rules duplicated three times. | Refactor; design choice. |
| R6 | architecture | internal/capture/pins.go | Third numeric version comparator. | Refactor; ordering semantics differ slightly. |
| R7 | architecture | internal/harness/claudecode/discover.go | URL credential handling duplicated in two paths. | Refactor. |
| R8 | architecture | internal/verify/verify.go | Condition derived from key prefixes. | Refactor. |
| R9 | security | internal/harness/claudecode/discover.go | `git status` can still run repository-configured `filter.*.clean` drivers. | AMBIGUOUS: skip the status probe when local filters exist, override each driver, or document the residual. |
| R10 | security | internal/imageref/imageref.go | Registry host component is not validated. | AMBIGUOUS: which hosts to accept (IPv6, underscores). The printed `sandbox create` command is now quoted. |
| R11 | production | internal/verify/verify.go, probe script | A hanging shell startup makes every session check wait the full timeout. | AMBIGUOUS: `timeout -k`, short-circuit after a failed startup check, or batch checks per shell. |
| R12 | production | internal/probe/cache.go | Old format cache directories are never pruned. | Deletes user cache data; policy choice. |
| R13 | production | internal/harness/claudecode/discover.go | git state is read once per plugin, not per marketplace. | Memoizing changes per-plugin notes; design choice. |

## Notable Observations

### NOTABLE-1
- **File:** internal/manifest/manifest.go
- **Category:** architecture
- **Description:** `Skeleton` takes a variadic options struct and ignores extra arguments.
- **Rationale:** Hides a required decision; harmless today.

### NOTABLE-2
- **File:** internal/build/sections.go, internal/plan/encode.go
- **Category:** security
- **Description:** Root shells in the image also prepend `/opt/mise/shims`, a directory the final user owns.
- **Rationale:** Not new (image ENV PATH did the same), relevant only for root sessions.

### NOTABLE-3
- **File:** internal/build/build.go
- **Category:** production-readiness
- **Description:** Push output reaches build.log only after the push ends.
- **Rationale:** Long pushes look stuck in the log.

### NOTABLE-4
- **File:** tests/integration/core_test.go
- **Category:** test-quality
- **Description:** `TestLockfileDecidesVersions` depends on `latest` differing from the base image's versions.
- **Rationale:** A base image refresh can make the correct `skip` fail the test.

## Post-Fix Spec Coverage

The fix rounds removed code once: the per-binary session check added in eaa967e was reverted in round 1 because it failed the integration suite (`ca-certificates` has no executable, `nft` lives in `/usr/sbin`). The reverted code was itself added during this review, so no requirement implemented by the feature commits was dropped. All FR-001 to FR-025 still have implementing code; see the compliance matrix in `REVIEW-CODE.md`.

## Test Suite Results

| Round | Test Command | Exit Code | Failures | Status |
|-------|-------------|-----------|----------|--------|
| baseline | make verify | 0 | 0 | passed |
| 1 (eaa967e) | make verify | 0 | 0 | passed |
| 1 (eaa967e) | make test-integration | 2 | 5 | failed: per-binary session check (reverted) |
| 2 | make verify | 0 | 0 | passed |
| 2 | make test-integration | 2 | 1 | failed: tmpfs copied image content (fixed with notmpcopyup) |
| 3 | make verify | 0 | 0 | passed |
| 3 | make test-integration | 0 | 0 | passed |
