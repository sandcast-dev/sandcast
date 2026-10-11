# Review Guide: sandcast, a Standalone Sandbox Image Builder

**Generated**: 2026-10-08 | **Spec**: [spec.md](spec.md)

## Why This Change

cc-deck contains a build system that turns a project directory into an OpenShell sandbox image, but it is locked to the cc-deck binary, its mandatory tool stack, a home-grown network policy catalog and Claude Code as the only harness. Anyone else who runs an agent in an OpenShell sandbox has to copy upstream example profiles and edit binary paths by hand, and the current build skill needed 8 to 9 repair iterations per image because the LLM guessed facts that code could have provided. sandcast extracts the capture-to-manifest-to-image flow into its own tool so any project and any supported harness can use it, with a deterministic core that CI can run without an agent.

## What Changes

A new repository ships a Go binary and a skill bundle. The binary takes a project-local manifest, probes the chosen base image, installs only the components the image lacks through the mise tool manager with a checked-in lockfile, builds the image with podman or docker, verifies every component inside it, and writes OpenShell provider profiles whose binary paths match that image, ready to lint and import into a gateway. The skill bundle, installed into Claude Code by the binary, captures a project and the developer's host tooling into the manifest, writes install layers for tools nobody has described yet, repairs failed builds, and promotes working layers into deterministic recipes. The home-grown policy catalog is dropped in favor of the upstream OpenShell profile schema. cc-deck is not changed by this feature. A plain container target without OpenShell conventions is a thin second output.

## How It Works

One Go module (Go 1.25, cobra, goccy/go-yaml, BurntSushi/toml) with `cmd/sandcast` and `internal/` packages that share typed documents: manifest, component, profile, probe result, build plan. Container engines, mise and the `openshell` CLI are called through `os/exec` behind small interfaces with fakes for tests. Shipped assets (about 30 component definitions, the 16 upstream OpenShell v0.1.2 example profiles plus 4 overlay profiles, the pinned default policy, the base image registry and the skills) are embedded through a root-level `embed.go`.

Pipeline: `plan` pulls the base image for one target platform, probes it with a shell script printing JSON lines (cached per image digest, with per-component partial probes), computes the delta, renders mise and recipe fragments, resolves profiles across three tiers, renders the static OpenShell policy, and writes one deterministic `plan.yaml`. `build` assembles a Containerfile from fixed-order sections plus any stored fragments, runs a two-pass build (pass 1 produces the image under a stage tag; a container of that image resolves real binary paths with `mise which`; pass 2 adds the adapted profile set under `/usr/share/sandcast/profiles/` and `io.sandcast.*` labels under the final tag). `verify` runs each component's check inside the image and exits non-zero on any failure. `profiles import` delegates to the upstream `openshell` CLI at the pinned release; lint is implemented in Go and tested against the vendored upstream profiles. All project files live under `.sandcast/`: committed inputs at the top (manifest, lockfile, fragments, promoted recipes, learnings) and git-ignored outputs under `build/`.

Delivery is in three phases matching the spec: core build (US1), capture plus the Claude Code adapter (US2, US4), repair, promotion and the container target (US3, US5). tasks.md has 87 tasks, 38 marked parallel.

## When It Applies

**Applies when**:
- A developer wants an OpenShell sandbox image that mirrors a project and their host tooling, with provider profiles that fit the image
- A repository checks in a manifest and lockfile and expects CI to rebuild the same image without an LLM
- An agent harness user wants the capture and build skills installed into Claude Code (first), later Codex, OpenCode and pi
- A plain podman or docker image of the same project is wanted, with an allowed-domains list instead of OpenShell policy

**Does not apply when**:
- cc-deck's own build package or its workspaces are involved: cc-deck is not switched to sandcast in this feature
- SSH or Ansible provisioning targets are needed: out of scope by decision
- Pre-built layer stack images are expected: parked; a published image can only be used as a probed base image
- Sandboxes must be started or managed: sandcast stops at a built and verified image plus importable profiles
- A pre-0.1 OpenShell with built-in profiles is in use: only v0.1.2 is supported

## Key Decisions

1. **Upstream OpenShell profiles as the only endpoint format.** Alternatives: keep cc-deck's component catalog with endpoints, or define a sandcast endpoint schema. Rejected because upstream v0.1.2 already has a complete profile model and the catalog duplicated gateway knowledge. Components reference profile ids; sandcast adapts `binaries` per image.
2. **mise with a lockfile as the primary installer, recipes as the escape hatch.** Alternatives: GitHub-release recipes with asset verification (cc-deck's approach), aqua or ubi directly. Rejected because mise's lockfile version 3 carries per-platform checksums, `--locked` installs fail on tampering, and a macOS-generated lockfile installs on Linux (verified).
3. **Plan in Go, assembly of unknown components by the skill, promotion into recipes.** Alternatives: strict template-only builder (loses flexibility for unknown tools), skill-first with thin helpers (not reproducible, policy correctness left to the LLM). Chosen so known components are deterministic and unknown ones converge to recipes.
4. **Lint implemented in Go, import delegated to the upstream CLI.** Alternatives: shell out for both, or reimplement both against the gateway API. The upstream CLI needs a gateway even to lint local files, so Go lint keeps plan and build self-contained; import inherently talks to a gateway and the CLI handles authentication and scope.
5. **One target platform per build, flag over manifest over host.** Alternatives: host platform only, or multi-platform manifest lists. Chosen to keep CI builds for a different gateway architecture reproducible without `podman manifest` complexity.
6. **Adapted profile ids keep the upstream id at workspace scope and get an image-label suffix at platform scope or on request.** Alternatives: always upstream id (collisions), always suffixed (breaks how upstream users reference providers). The set index records the mapping.
7. **Two-pass build with the profile set copied into the image and labels.** Alternative: cc-deck's label stamping through a Docker API socket, which breaks podman-first; or deterministic paths without a probe, which per-tool bin layouts prevent.
8. **Tools under `/opt/mise`, shell configuration under `/etc`, nothing under `/sandbox`.** Alternative: install into the user's home. Rejected because the OpenShell Kubernetes driver mounts a workspace volume over `/sandbox`, masking anything there.
9. **Committed inputs and disposable outputs split under `.sandcast/`.** Alternative: commit the plan too for review diffs, or keep fragments in a user cache. Chosen so a checkout rebuilds in CI and the plan stays an output.
10. **Verify runs under the engine only; a gateway is needed only for import and sandbox start.** Alternative: verify as a real sandbox under a gateway. Chosen to keep the CI path gateway-free.

## Areas Needing Attention

- **`/etc/openshell` reserved-path contradiction.** Upstream lists `/etc/openshell` among paths an image must not occupy, while its documentation instructs `COPY policy.yaml /etc/openshell/policy.yaml`. The plan writes only that file there and relies on the first gateway integration test to confirm it is honored. If it is not, the fallback is passing `--policy` at sandbox start.
- **Go reimplementation of upstream lint can drift.** The rule list is pinned to v0.1.2 and the vendored examples are the regression oracle, but refresh and token-grant blocks are only enum-checked and passed through. A pin refresh must re-derive the rules.
- **Shims on PATH versus real paths in profiles.** Processes run through mise shims that exec the real binary; profiles must list the real install paths. If a tool wraps its child processes differently, the adapted profile could miss a binary. `runtime_globs` per component is the escape hatch.
- **mise is a hard dependency at build time and a network dependency.** Locked installs still download artifacts from the locked URLs and ping the mise versions host unless disabled; air-gapped builds are explicitly out of scope.
- **Agent configuration under a masked home on Kubernetes.** Harness scaffolding writes into the home directory, which the Kubernetes driver masks under `/sandbox`. Flagged for the phase 2 adapter design; not solved here.
- **Skill files are executable content.** The build skill writes Containerfile fragments; the rules in contracts/skill-bundle.md constrain what it may write, and a structural test checks the skills, but LLM-written fragments are reviewed only by the user before promotion.
- **Remote component registry is a new repository** (`sandcast-components`) that does not exist yet; the default pin must be created alongside the first release.

## Open Questions

- Whether `/etc/openshell/policy.yaml` is read from the image on every driver (verified by task T047).
- How harness configuration should survive a masked `/sandbox` on Kubernetes (phase 2 design, task T072 notes).
- Shell scope for the curated configuration: zsh is the reference; bash support is a secondary path through `/etc/profile.d`.

## Review Checklist

- [ ] Key decisions are justified
- [ ] Breaking changes are documented with migration guidance (none: new repository, fresh manifest schema, no cc-deck reader by decision)
- [ ] Scope matches the stated boundaries
- [ ] Success criteria are achievable
- [ ] No unstated assumptions
- [ ] No credential value can reach the manifest, plan, fragments, profiles, image or logs (constitution IV; redaction tasks T009, T040, T055)
- [ ] Overlay profiles pass the upstream lint unchanged and follow the upstream header convention (SC-010)
- [ ] Pins (OpenShell v0.1.2, mise v2026.10.4) are constants with a documented refresh procedure
- [ ] Plan output is byte-identical for identical inputs (SC-002, golden tests)
- [ ] Skill files are reviewed with the same rigor as Go source (constitution V)

---

<!-- Code phase sections are appended below this line by the phase-manager command -->
