# Code Review: sandcast standalone build

**Spec:** specs/001-sandcast-standalone-build/spec.md
**Date:** 2026-10-08
**Reviewer:** Claude (speckit.spex-gates.review-code)

## Compliance Summary

**Overall score: 97% (74 of 76 items compliant, 2 minor deviations)**

- Functional requirements: 42/43 (FR-014 minor deviation)
- Edge cases: 20/21 (edge case 3 minor deviation)
- Success criteria: 10/10 implemented (SC-007 verified without the credential-dependent inference call)
- Non-functional (plan performance goals): 2/2

Evidence: `make verify` (all unit packages, golangci-lint 0 issues), `make test-integration` (core flow on both targets, broken fragment, promotion flow, captured manifest), `make test-gateway` (OpenShell v0.1.2 gateway: import, duplicate import, sandboxes running `go version` and `claude --version`).

## Functional requirements

| Req | Implementation | Status | Notes |
|-----|----------------|--------|-------|
| FR-001 | go.mod (no cc-deck module), `internal/` | Compliant | No cc-deck import or concept. |
| FR-002 | `internal/pipeline`, `internal/cli` | Compliant | plan, build, verify, profiles export run without a skill (integration tests). |
| FR-003 | `internal/skills/install.go:36`, `internal/harness/claudecode/skills.go` | Compliant | `sandcast skills install --harness claude-code --scope user\|project`. |
| FR-004 | `internal/skills/check.go:50`, both SKILL.md Step 1 | Compliant | Exit 3 with which side to update. |
| FR-005 | `internal/engine/detect.go:38`, `exec.go` | Compliant | Flag, manifest, podman, docker fallback with notice; shared flag subset only. |
| FR-006 | README.md, docs/cli.md, docs/configuration.md, docs/guides/* | Compliant | |
| FR-007 | `internal/manifest/manifest.go:33` | Compliant | All listed fields; fresh schema. |
| FR-008 | `internal/manifest/manifest.go` (`refuseSecrets` in Save), `internal/cli/manifest.go:94`, `internal/redact` | Compliant | Save now refuses credential shapes (fixed during this review); validate reports them; discovery redacts. |
| FR-009 | `internal/manifest/version.go:18` | Compliant | Names both versions. |
| FR-010 | `internal/registry/registry.go:247`, `base-images.yaml` | Compliant | Notice for references outside the registry. |
| FR-011 | `internal/probe/probe.go:38`, `script.go` | Compliant | Facts plus per-component checks. |
| FR-012 | `internal/probe/cache.go` | Compliant | Keyed by digest and platform (refinement, research implementation notes); partial probes; `--no-cache`. |
| FR-013 | `internal/component/types.go`, `load.go:16` | Compliant | `endpoints` refused by name. |
| FR-014 | `internal/mise/config.go:44`, `lock.go`, `lockcmd.go`, `internal/plan/fragments.go` | Minor deviation | `core:rust` lock entries carry the exact version but no per-platform checksum (rustup verifies its toolchain); `mise.VersionOnlyBackends` accepts them. Recorded in research.md. |
| FR-015 | `internal/plan/fragments.go:91`, `delta.go` | Compliant | Probed package manager; script recipes. |
| FR-016 | `internal/component/tiers.go:57` | Compliant | |
| FR-017 | `internal/component/remote.go:63` | Compliant | Explicit refresh, pinned tag, per-file sha256, previous copy kept. |
| FR-018 | `internal/build/promote.go:40`, `internal/cli/fragments.go` | Compliant | Confirmation prompt unless `--yes`. |
| FR-019 | `internal/profile/types.go` | Compliant | Upstream schema only. |
| FR-020 | `profiles/upstream/v0.1.2` (16), `profiles/overlay` (4) | Compliant | Overlay passes lint unchanged. |
| FR-021 | `internal/profile/tiers.go:80` | Compliant | |
| FR-022 | `internal/profile/adapt.go:62`, `internal/build/build.go` | Compliant | Empty binaries flagged. |
| FR-022a | `internal/profile/adapt.go:249`, `index.go` | Compliant | Mapping in `index.yaml`. |
| FR-023 | `internal/profile/lint.go:68`, `internal/pipeline/profiles.go:150`, `internal/gateway` | Compliant | Lint in Go against vendored examples; import create-only; CLI version checked first (exit 3). |
| FR-024 | `internal/build/build.go:35` | Compliant | `/usr/share/sandcast/profiles` plus `io.sandcast.profiles-index` label. |
| FR-025 | `internal/plan/plan.go:86`, `types.go` | Compliant | Lockfile referenced by path, copied into the build context (contract R-13). |
| FR-026 | `internal/plan/encode.go`, `plan_test.go` | Compliant | Byte-identical plans in unit and integration tests. |
| FR-026a | `internal/pipeline/pipeline.go:120` | Compliant | |
| FR-027 | `internal/build/build.go:38` | Compliant | No build retry; download retries stay inside the mise layer (research notes). |
| FR-028 | `internal/build/fragments.go:178` | Compliant | |
| FR-028a | `internal/fsutil/layout.go:174` | Compliant | |
| FR-029 | `skills/build/SKILL.md`, `internal/build/learnings.go:88` | Compliant | Default 3 attempts. |
| FR-030 | `internal/verify/verify.go:90`, `internal/cli/build.go` | Compliant | Reports all, then exits 1; `--verify` only. |
| FR-031 | `internal/plan/fragments.go:62` | Compliant | Verified in an OpenShell sandbox (`go version`). |
| FR-032 | `internal/build/sections.go:14-15`, `plan/user.go` | Compliant | |
| FR-033 | `internal/policy/policy.go:131` | Compliant | No network entries. |
| FR-034 | `internal/capture/match.go:74`, capture skill | Compliant | |
| FR-035 | capture skill Step 4 | Compliant | |
| FR-036 | capture skill Step 2 | Compliant | |
| FR-037 | capture skill Rules 3 | Compliant | |
| FR-038 | `internal/harness/claudecode/*` | Compliant | Discovery, component, scaffold, skill dir. |
| FR-039 | `internal/profile/mcp.go:59`, `internal/build/build.go` (`withMCPProfiles`) | Compliant | Coverage judged by the agent's own profiles. |
| FR-040 | `internal/plan/user.go`, `internal/build/assemble.go`, `internal/pipeline/build.go:92` | Compliant | No policy, no sandbox user or `/sandbox`, allowed-domains file. |

## Edge cases

All 21 edge cases are handled; locations: pull failure (`pipeline.go`, no partial plan), registry notice, non-root base, no package manager and incompatible versions (`plan/delta.go`), conflicts (`component/conflicts.go`, plan exit 2, `Assemble`), flagged profiles, profile tier notices, partial probe, schema version, skill mismatch, stale fragments, lockfile platform gaps, platform precedence, offline host (`probe.go` local fallback, `mise.Lock` per-tool reasons), registry checksum rejection, empty capture, MCP secrets, retry limit, missing or wrong `openshell`, existing gateway ids.

- **Edge case 3 (minor deviation):** the spec says tool manager installs "go into that user's home". They go into `/opt/mise`, owned by the final user, because the Kubernetes driver masks the home (`/sandbox`). This is research decision R-04; the spec wording predates it.

## Extra behaviour not in the spec

All are recorded in contracts or research.md and are helpful rather than scope creep: implicit components (OpenShell packages, shell program, mise prerequisites, `git` for plugin marketplaces), base images resolved across targets, `init --force`, `probe --target`, `components match --harness`, `profiles import --gateway`, `learnings add --date`, the `SANDCAST_*` environment overrides, and `ENTRYPOINT []` in the footer.

## Recommendations

### Critical
- None.

### Spec evolution candidates
- [ ] FR-014: allow version-only lock entries for backends that verify their own downloads (`core:rust`).
- [ ] Edge case 3: say "into a directory owned by that user outside the home (`/opt/mise`)".

### Verification gaps
- SC-007's inference call (`claude -p` through the provider) needs an Anthropic credential and was not run.
- The `/sandcast-capture` and `/sandcast-build` skills were exercised step by step in this session, not through an interactive slash-command run.
