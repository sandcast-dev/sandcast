# Walkthrough Report

**Feature**: sandcast standalone build (001)
**Date**: 2026-10-10
**Workspace**: /Users/rhuss/Development/ai/cc-deck/sandcast/.claude/worktrees/001-sandcast-standalone-build/specs/001-sandcast-standalone-build/.walkthrough/20261010-172832-57273   (remove with: `rm -rf /Users/rhuss/Development/ai/cc-deck/sandcast/.claude/worktrees/001-sandcast-standalone-build/specs/001-sandcast-standalone-build/.walkthrough/20261010-172832-57273`)
**Result**: 2 passed, 0 failed, 0 skipped, 0 manual, 3 deferred

Environment: podman 6.1.3, sandcast binary (1f31df9-dirty), openshell CLI 0.1.2 (the pinned release), mise 2026.10.3, a reachable OpenShell gateway, base image `ghcr.io/nvidia/openshell-community/sandboxes/base:latest` cached locally.

## Scenario 1: Build a sandbox image from a manifest (P1, from US1)
**Verdict**: PASS

Driven live against podman and a real OpenShell gateway, on a copy of the reference fixture project in the run workspace.

### Step 1: Plan (US1 AS1, AS5)
- Command: `sandcast plan --target openshell --json`
- Ran by: agent
- Expected: probe the base image, report the probe delta, write an inspectable build plan.
- Actual: probed `ghcr.io/nvidia/openshell-community/sandboxes/base:latest`, reported "probe cache hit: no container started" (AS5), reported the delta (base-present components skipped, missing ones marked install), wrote `plan.yaml` (16 KB, 18 sections). A first run without a lockfile failed per component naming the platform, which is the documented lockfile edge case; `sandcast lock` wrote `.sandcast/mise.lock` (7 tools, linux-arm64) and the plan then succeeded.
- Artifact: `.sandcast/build/openshell/plan.yaml`

### Step 2: Determinism (US1 AS4)
- Command: `sandcast plan --target openshell` run twice, outputs compared with `diff`
- Ran by: agent
- Expected: two build plans are identical for the same manifest, lockfile, base digest and platform.
- Actual: the two plan.yaml files were byte-identical.

### Step 3: Build (US1 AS2)
- Command: `sandcast build --verify --target openshell`
- Ran by: agent
- Expected: an image in which no present component is reinstalled, every missing component is installed, and a lockfile pins exact versions and checksums.
- Actual: 12-step two-pass podman build produced `localhost/reference-project:latest`; only missing components were installed via mise; `.sandcast/mise.lock` pins each tool version and a sha256 checksum per platform.
- Artifact: `.sandcast/build/openshell/Containerfile`, `.sandcast/mise.lock`

### Step 4: Verify (US1 AS3)
- Command: `sandcast build --verify --target openshell` (verify phase)
- Ran by: agent
- Expected: every component's probe check runs inside the image, a pass or fail per component, non-zero exit if any failed.
- Actual: 12 passed, 0 failed. The non-zero-on-failure behavior is shown by Step 8.

### Step 5: Adapted profiles (US1 AS6)
- Command: inspected `.sandcast/build/openshell/profiles/` after the build
- Ran by: agent
- Expected: one adapted profile per referenced profile with `binaries` rewritten to resolved paths; any profile whose binaries could not be resolved is flagged.
- Actual: `claude-code.yaml`, `go-proxy.yaml`, `pypi.yaml`, `index.yaml` written. Binaries rewritten to resolved paths (for example `/opt/mise/installs/go/1.27.2/bin/go`). `pypi` flagged with the reason: binaries under a path the sandbox can write (`/sandbox/.uv/.../bin/python3.14`).

### Step 6: Profile import and conflict reporting (US1 AS7)
- Command: `sandcast profiles import --scope workspace --json`, then the same command again
- Ran by: agent
- Expected: each profile passes the gateway lint and is imported at the scope; existing ids are reported, never silently overwritten.
- Actual: first run imported `claude-code`, `go-proxy`, `pypi` (the gateway then listed them at workspace scope); the second run returned `imported: []` and reported all three as `conflicts`.

### Step 7: Import scope and id mapping (US1 AS9)
- Command: `sandcast profiles import --scope workspace --json` and `--scope platform --json`
- Ran by: agent
- Expected: workspace scope keeps the upstream id; platform scope suffixes the id with the image label and records the upstream-to-adapted mapping.
- Actual: workspace scope kept the upstream ids (mapping upstream == adapted). Platform scope produced `claude-code-reference-project`, `go-proxy-reference-project`, `pypi-reference-project`; the gateway listed them at platform scope; the build-dir `index.yaml` recorded `scope_hint: platform` and each upstream-to-adapted mapping (FR-022a).

### Step 8: Build fails, no retry, artifacts left (US1 AS8)
- Command: `sandcast build --target openshell` on a project with an unresolved component, and again with a fragment whose `RUN` exits 7
- Ran by: agent
- Expected: the non-interactive build exits non-zero, leaves the image definition and build log, and does not retry.
- Actual: the unresolved-component build exited non-zero with remediation guidance and left `plan.yaml`/`policy.yaml` (it refused before rendering a Containerfile). The failing-fragment build rendered the Containerfile, failed at the fragment step on pass 1, exited non-zero, left `Containerfile` (879 B) and `build.log` (546 B), and did not retry.

## Scenario 2: Capture a project into a manifest (P2, from US2)
**Verdict**: DEFERRED
Capture is driven by the interactive, LLM-driven capture skill reading the project and host state and asking the user which harnesses to include. It was not exercised in this walkthrough. The deterministic sub-commands it orchestrates (`sandcast harness discover`, `sandcast components match`, `sandcast manifest set-shell`, `sandcast manifest validate`, `sandcast lock`) are unit and integration tested; `sandcast lock` was exercised live in Scenario 1. Recommend a manual capture run on the reference fixture on a host with the expected tools.

## Scenario 3: Repair a failed build and promote the fix (P3, from US3)
**Verdict**: DEFERRED
The repair loop is driven by the build skill (an LLM writing and fixing install layers). It was not exercised end to end. The deterministic parts were seen in passing in Scenario 1 Step 8: `sandcast fragments write` stored a fragment and the non-interactive build consumed it. The learnings file, the retry limit, and `sandcast promote` were not exercised. Recommend a manual run of the build skill on a manifest with an unresolvable component.

## Scenario 4: Bake a harness with its local extensions (P4, from US4)
**Verdict**: DEFERRED
Requires two selected harnesses and inference-provider credentials to start the agents inside the sandbox. Not exercised. The claude-code harness profile was produced and imported in Scenario 1 (its adapted profile with resolved path reached the gateway). The full two-harness bake and in-sandbox agent run were not. Recommend a manual run with credentials available.

## Scenario 5: Build a plain container image (P5, from US5)
**Verdict**: PASS

Driven live on the same reference project with a `container` target added to the manifest.

### Step 1: Container build and verify (US5 AS1, AS2)
- Command: `sandcast build --verify --target container`
- Ran by: agent
- Expected: no OpenShell policy file or sandbox user convention is applied, and verify passes under the container engine.
- Actual: built `localhost/reference-project-plain:latest`; verify reported 10 passed, 0 failed. The container build directory has no `policy.yaml` and the container Containerfile has no policy COPY. Note: both targets end with `USER sandbox`, inherited from the OpenShell community base image's final user, not imposed by sandcast's container target; a non-OpenShell base would keep its own user.
- Artifact: `.sandcast/build/container/Containerfile`, `.sandcast/build/container/verify.yaml`

### Step 2: Same component set in both targets (US5 AS2)
- Command: compared verify output of the openshell and container builds
- Ran by: agent
- Expected: verify passes for the same component set in both images.
- Actual: the openshell image verified 12 components (including the implicitly added `iproute2` and `nftables`), the container image verified the 10 user-declared components; both all pass.

### Step 3: Allowed-domains list (US5 AS3)
- Command: inspected `.sandcast/build/container/allowed-domains.txt` against the profile endpoint hosts
- Ran by: agent
- Expected: an allowed-domains list containing every endpoint host of the referenced profiles.
- Actual: `allowed-domains.txt` holds the 12 sorted, unique endpoint hosts of the three profiles (api.anthropic.com, statsig.anthropic.com, sentry.io, proxy.golang.org, sum.golang.org, storage.googleapis.com, pypi.org, files.pythonhosted.org, github.com, objects.githubusercontent.com, api.github.com, downloads.python.org), matching the profile endpoints exactly.

## Coverage

### User stories
| Story | Scenario | Verdict |
|-------|----------|---------|
| US1 | Scenario 1 (9 acceptance scenarios, all demonstrated) | PASS |
| US2 | Scenario 2 (capture) | DEFERRED |
| US3 | Scenario 3 (repair and promote) | DEFERRED |
| US4 | Scenario 4 (harness bake) | DEFERRED |
| US5 | Scenario 5 (3 acceptance scenarios, all demonstrated) | PASS |

### Functional requirements
The coverage helper could not map FRs to stories (the spec does not tag FRs per story), so every FR is listed as an orphan below. Scenario 1 and Scenario 5 exercised a broad subset in practice (plan, probe cache, delta, lockfile, build, verify, adapted profiles and flagging, gateway import with scopes and conflict reporting, id mapping persistence, build-failure handling, container target, allowed-domains).

### Orphan FRs
FR-001, FR-002, FR-003, FR-004, FR-005, FR-006, FR-007, FR-008, FR-009, FR-010, FR-011, FR-012, FR-013, FR-014, FR-015, FR-016, FR-017, FR-018, FR-019, FR-020, FR-021, FR-022, FR-022a, FR-023, FR-024, FR-025, FR-026, FR-026a, FR-027, FR-028, FR-028a, FR-029, FR-030, FR-031, FR-032, FR-033, FR-034, FR-035, FR-036, FR-037, FR-038, FR-039, FR-040
