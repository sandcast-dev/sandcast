# Implementation Plan: sandcast, a Standalone Sandbox Image Builder

**Branch**: `001-sandcast-standalone-build` | **Date**: 2026-10-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/001-sandcast-standalone-build/spec.md`

## Summary

sandcast is a Go command-line binary plus a portable skill bundle. The binary runs the deterministic pipeline from a project-local manifest: probe a base image (cached by digest), resolve components from three tiers, generate a mise configuration and lockfile, render a build plan, assemble and build the image with podman or docker in two passes, verify every component inside the image, and write an adapted OpenShell provider profile set whose binary paths match the image. The skill bundle, installed into Claude Code first, drives the LLM-side work: capture of the project and host into the manifest, fragments for unresolved components, repair of failed builds, and promotion of working layers into recipes.

Technical approach: one Go module with a thin cobra command layer over `internal/` packages that share typed documents (manifest, component, profile, probe result, build plan). Every file the binary reads or writes is YAML or TOML with a documented schema (see [contracts/](contracts/)). Container engines, mise and the `openshell` CLI are driven through `os/exec` behind small interfaces so tests can substitute fakes. Profile lint is implemented in Go against the vendored v0.1.2 examples. Research decisions are in [research.md](research.md); entities in [data-model.md](data-model.md); validation scenarios in [quickstart.md](quickstart.md).

## Technical Context

**Language/Version**: Go 1.25 (`go 1.25` directive with a pinned `toolchain`; the host has Go 1.27.1).

**Primary Dependencies**: `github.com/spf13/cobra` (CLI), `github.com/goccy/go-yaml` (YAML with unknown-field rejection and line numbers), `github.com/BurntSushi/toml` (mise.toml and mise.lock), `github.com/opencontainers/go-digest`, `github.com/stretchr/testify` (tests). External executables through `os/exec`: `podman` or `docker`, `mise` (host side for `mise lock`; pinned v2026.10.4 inside images), `openshell` 0.1.2 (import step only). No container client library, no OpenShell SDK (R-01, R-02, R-11).

**Storage**: Files only. `.sandcast/` committed inputs, `.sandcast/build/` git-ignored outputs, `os.UserCacheDir()/sandcast/` for the probe cache and the remote component registry cache (R-12). No database.

**Testing**: `go test` with table tests (testify), golden files for every renderer (plan, fragments, policy, adapted profiles, mise.toml), a fake engine and fake exec for command tests, lint tests over all vendored upstream profiles plus a negative corpus. Build tags `integration` (needs podman) and `gateway` (needs podman plus a v0.1.2 gateway container). Make targets `test`, `lint` (golangci-lint v2), `verify`, `test-integration`, `test-gateway` (R-19).

**Target Platform**: Binary on macOS and Linux (amd64, arm64). Images target one `linux/amd64` or `linux/arm64` per build, selected by flag, manifest field or host default (FR-026a).

**Project Type**: Single Go module: CLI binary with embedded assets (components, profiles, policy default, base image registry, skill bundle).

**Performance Goals**: A plan on a cached probe completes in under 2 seconds without starting a container. A full probe of a base image completes in under 60 seconds with the image already pulled. Build time is dominated by the engine and downloads and is not a sandcast target.

**Constraints**: Byte-identical plans for identical inputs (FR-026). No credentials in any written file or log (FR-008, SC-009). Upstream profile schema unchanged (FR-019). podman default, docker through the same flags, no engine-specific features (FR-005). Offline plan from cache plus lockfile. Nothing sandcast installs may live under `/sandbox`, because the Kubernetes driver mounts a volume there (R-10).

**Scale/Scope**: Tens of components per manifest; about 30 shipped components; 16 vendored upstream profiles plus 4 overlay profiles; one user and one project per invocation.

## Global Constraints

Every task inherits these rules, copied from the spec and the constitution:

- "The manifest MUST NOT contain credential values. Discovered configuration that embeds a secret MUST be stored with a placeholder." (FR-008) and "No credential value may appear in the manifest, the build plan, stored fragments, adapted profiles, the image, or the logs." (constitution IV)
- "The plan MUST be deterministic: the same manifest, the same lockfile, the same component and profile definitions, the same base image digest and the same target platform MUST yield an identical plan." (FR-026)
- "sandcast MUST use podman as the default container engine. docker MUST be usable when selected explicitly in the manifest or on the command line, and MUST be selected automatically with a printed notice when podman is absent. No engine-specific feature may be required." (FR-005)
- "sandcast MUST use the upstream OpenShell provider profile schema as its only network endpoint format and MUST NOT define a sandcast endpoint schema." (FR-019)
- "Each build MUST target exactly one platform (operating system and architecture). The platform is selected, in precedence order, by a command-line flag on the plan and build steps, then by the manifest's optional target platform field, then by the host's platform." (FR-026a)
- "Tools installed by the tool manager MUST be reachable by any process in the image without shell activation, so that an agent's child processes find them." (FR-031)
- "Use make targets (`make test`, `make lint`, `make verify`). Never run `go build` directly." (constitution V)
- Pins: OpenShell v0.1.2 (commit 6648bd0c), mise v2026.10.4, manifest schema version 1, plan schema version 1. "Pinned upstream releases (OpenShell, mise) are refreshed deliberately, with a changelog entry, never implicitly." (constitution III)
- Nothing sandcast installs may live under `/sandbox` (R-10).
- Documentation ships in the same branch as the change (constitution I).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | How the design satisfies it | Pre-research | Post-design |
|-----------|-----------------------------|--------------|-------------|
| I. Tests and documentation | Unit tests per package, golden tests per renderer, integration and gateway tags. README, `docs/cli.md`, `docs/configuration.md`, guides for capture, build and profiles are tasks inside each delivery phase, never follow-ups. | PASS | PASS |
| II. Determinism before convenience | The binary never calls an LLM, never guesses release assets (mise lockfile), never retries. Plan encoder sorts every list and omits timestamps. Discovery, fragments and repair live in the skill; `sandcast promote` is the promotion path into recipes; `sandcast lock` into the lockfile. | PASS | PASS |
| III. Upstream formats | Profiles use the v0.1.2 schema verbatim; lint mirrors the upstream rule list and is tested against the vendored examples; overlay profiles follow the upstream header convention. mise.toml and mise.lock are mise's formats. Pins are constants (`OpenShellPin = v0.1.2`, `MisePin = v2026.10.4`) with a changelog entry per refresh. | PASS | PASS |
| IV. No credentials | `internal/redact` runs on the manifest before write, on every build output and on the build log; harness discovery returns placeholders only. | PASS | PASS |
| V. Build and tool rules | Makefile first task. Engine interface uses only the common podman and docker flag subset. Skill files have a structural test (frontmatter, required steps) and are reviewed like Go. | PASS | PASS |

No violations. Complexity Tracking stays empty.

## Project Structure

### Documentation (this feature)

```text
specs/001-sandcast-standalone-build/
├── plan.md                  # This file
├── research.md              # Phase 0: decisions R-01 to R-20
├── data-model.md            # Phase 1: entities and files
├── quickstart.md            # Phase 1: validation scenarios
├── contracts/
│   ├── cli.md                        # command surface per phase
│   ├── manifest.schema.yaml
│   ├── component.schema.yaml
│   ├── plan.schema.yaml
│   ├── adapted-profiles.schema.yaml
│   └── skill-bundle.md               # skill contract and rules
└── tasks.md                 # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
cmd/sandcast/main.go       # entry point; wiring only
embed.go                   # package sandcast: //go:embed components profiles policy skills base-images.yaml
internal/
├── cli/                   # cobra commands, one file per command group; no business logic
├── manifest/              # schema, load, validate, version gate (FR-007 to FR-009)
├── registry/              # base image registry, shipped plus user file merge (FR-010)
├── engine/                # podman and docker through exec; interface plus fake (FR-005)
├── probe/                 # probe script, runner, digest cache for facts and per-component checks (FR-011, FR-012)
├── component/             # schema, three-tier loader, remote registry refresh with checksums (FR-013, FR-016, FR-017)
├── mise/                  # mise.toml rendering, lockfile reading, `mise lock` wrapper, platform mapping (FR-014)
├── profile/               # upstream schema, three-tier loader, lint, adaptation, id mapping, set index (FR-019 to FR-024)
├── policy/                # image-embedded OpenShell policy (FR-032, FR-033)
├── plan/                  # delta, resolution, fragment rendering, deterministic encode (FR-025, FR-026)
├── build/                 # Containerfile sections, two-pass build, path resolution, fragments store (FR-027, FR-028)
├── verify/                # per-component checks in the built image (FR-030)
├── gateway/               # openshell CLI wrapper for import (FR-023)
├── harness/               # adapter interface; claudecode/ (FR-038, FR-039)
├── skills/                # bundle embed, version check, installer per harness (FR-003, FR-004)
├── redact/                # secret detection and placeholders (FR-008)
├── capture/               # component matching against project files and host tools (FR-034, FR-035 helpers)
├── pipeline/              # gathers plan inputs, runs plan, build, verify and import (above plan and build)
├── fsutil/                # .sandcast layout, build dir, cache dir, atomic writes, gitignore
├── version/               # binary version and the OpenShell, mise and component registry pins
└── testutil/              # golden-file helper shared by renderer tests
components/                # shipped component definitions (YAML), embedded
profiles/
├── upstream/v0.1.2/       # vendored upstream example profiles, unmodified
└── overlay/               # npm, crates-io, go-proxy, gitlab
policy/
└── default-v0.1.2.yaml    # pinned default policy (filesystem and landlock sections)
base-images.yaml           # shipped base image registry
skills/
├── VERSION
├── capture/SKILL.md
└── build/SKILL.md
docs/
├── cli.md
├── configuration.md
└── guides/{capture,build,profiles}.md
tests/
├── fixtures/reference-project/   # Go module, Python project, zsh config (SC-003)
├── golden/README.md              # how to regenerate golden files; the files live next to their package in internal/*/testdata
└── integration/                  # build-tagged podman and gateway tests
Makefile
.golangci.yml
```

**Structure Decision**: One Go module. `cmd/` holds the entry point, `internal/` every package, so nothing is importable from outside and the cc-deck independence holds in both directions. Embedded assets (`components/`, `profiles/`, `policy/`, `skills/`, `base-images.yaml`) stay at the repository root as CLAUDE.md plans them, embedded through a root-level `embed.go`, so the same directories are the documentation source and the review target. Go's embed cannot reach outside a package directory, which is why the embed declaration lives at the root rather than in `internal/`.

## Delivery Phases (task grouping for `/speckit-tasks`)

The spec requires three independently shippable phases. Each phase ends with passing `make verify`, updated documentation, and a quickstart scenario that runs.

**Phase 1: Core build** (US1; FR-001 to FR-033 including FR-022a, FR-026a, FR-028a). Packages: fsutil, manifest, registry, engine, probe, component (shipped and remote tiers), mise, profile (tiers, lint, adaptation, index), policy, plan, build (sections, two-pass, resolution, stored-fragment reuse), verify, gateway, cli, redact, embed. Assets: components, vendored profiles, overlay, policy default, base-images.yaml. Docs: README, cli.md, configuration.md, guides/build.md, guides/profiles.md. Tests: golden suites, lint corpus, integration and gateway targets. Quickstart scenarios 1, 2, 3, 7.

**Phase 2: Capture and first harness** (US2, US4; FR-034 to FR-039). Packages: capture, harness (interface plus claudecode), skills (bundle, installer, check). Assets: skills/capture, skills/build (initial build skill limited to plan-build-verify orchestration and fragment writing), reference fixture. Build section `70-harness`. Docs: guides/capture.md, cli.md additions. Quickstart scenario 4.

**Phase 3: Repair, promotion and container target** (US3, US5; FR-018, FR-028, FR-029, FR-040). Commands: fragments list and write, promote, learnings add. Build skill repair loop and promotion flow. Container target sections and allowed-domains output. Docs updates. Quickstart scenarios 5, 6.

## Key Design Points (cross-cutting)

- **Two-pass build** (R-09): pass 1 produces the full image under a stage tag; path resolution and verify run against it; pass 2 adds the adapted profile set under `/usr/share/sandcast/profiles/` and the `io.sandcast.*` labels under the final tag.
- **mise layout** (R-04): `MISE_DATA_DIR=/opt/mise`, config at `/etc/mise`, shims on `PATH` through `ENV`, `mise install --locked` as the final user, `mise reshim` after install. Profile binaries use the real install paths from `mise which`, never shim paths.
- **OpenShell image contract** (R-10): non-root final user with passwd entry (created as `sandbox` uid 1000 when the base runs as root), `/sandbox` owned by that user and set as `WORKDIR`, `iproute2` and `nftables` installed, policy at `/etc/openshell/policy.yaml` with an empty `network_policies` map, nothing under the reserved OpenShell paths, shell configuration under `/etc`.
- **Lint in Go** (R-07): rule list from `validate_profile_set` at v0.1.2 plus the policy host rules; refresh and token-grant blocks pass through.
- **Id strategy** (R-08): upstream id at workspace scope; `<id>-<image label>` at platform scope or with `--suffix`; mapping recorded in `profiles/index.yaml`.
- **Deterministic plan** (R-13): one YAML document, sorted lists, fragment text embedded, no timestamps; golden tests compare bytes.

## Risks and open items for tasks

- `/etc/openshell` appears in the upstream reserved-path list while the documented policy location is `/etc/openshell/policy.yaml`. The first gateway integration test confirms the file is honoured; if not, fall back to passing `--policy` from the build directory and record the change in research.md.
- Agent configuration scaffolded under the home directory is masked on the Kubernetes driver (workspace volume at `/sandbox`). Phase 2 adapter design must choose between `/etc/skel`-style seeding and a first-run copy; flagged for the phase 2 tasks.
- goccy/go-yaml encoder behaviour for multi-line strings must be pinned by golden tests early, because the plan embeds fragment text.
- The reference fixture needs `starship`, `fzf` and `lsd` on the host for SC-003; CI runs the capture scenario manually until a hermetic host fixture exists.

## Complexity Tracking

No constitution violations to justify.
