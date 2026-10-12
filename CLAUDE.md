# sandcast Development Guidelines

sandcast turns a project directory into a sandbox image. A guided capture examines the project and the host and writes a manifest. A build turns the manifest plus a probed base image into a container image, adds only what the base image lacks, and produces OpenShell provider profiles adapted to that image.

The idea originated in cc-deck (`../cc-deck`). The brainstorm and the feature specification were moved here on 2026-10-08. cc-deck keeps its own build package; sandcast does not depend on cc-deck code.

## Workflow

Spec-driven development with spex. The current feature is `specs/001-sandcast-standalone-build/`.
Read `.specify/memory/constitution.md` before any change.

Key documents:
- `specs/001-sandcast-standalone-build/spec.md`: the feature specification, reviewed and gated.
- `brainstorm/01-sandcast-standalone-build.md`: problem framing, approaches, decisions, and the OpenShell and mise research.
- `brainstorm/reference/`: cc-deck brainstorms that fed the design (build integration, multi-agent builds, build skill iteration lessons, NemoClaw base image, image tool plugins, profile delegation).

## Design Decisions (do not re-litigate without a brainstorm)

- Separate repository, independent of cc-deck.
- OpenShell sandbox images first, plain container images second. No SSH target.
- Go CLI plus a portable skill bundle. The binary runs plan, build, verify and profile export without any agent. The binary installs its skills into Claude Code first; Codex, OpenCode and pi follow.
- Base image registry plus a live probe per image digest, cached.
- Components reference OpenShell provider profiles. No sandcast endpoint schema. Upstream example profiles are vendored at a pinned OpenShell release (v0.1.3), with a same-schema overlay for npm, crates.io, the Go module proxy and GitLab.
- mise is the primary installer with a lockfile. Recipes are the escape hatch.
- Podman by default, docker through the same command surface.
- Capture is LLM-driven and writes the manifest. The Go core produces a build plan. The skill assembles the image definition, repairs failures, and promotes working layers into recipes.
- Credentials never enter the manifest, the plan, the fragments, the profiles, or the image.

## Content Creation

When creating or editing documentation (Markdown, AsciiDoc, README):
- Use the prose plugin with the `cc-deck` voice profile if available, otherwise plain professional prose without contractions.
- Never use em-dashes or en-dashes.
- One sentence per line in AsciiDoc files.

## Build and Tool Rules

- Use make targets. Do not run `go build` directly.
  - `make build`: the binary at `./sandcast`
  - `make test`, `make lint`, `make verify` (test plus lint), `make fmt`
  - `make test-integration`: build-tagged tests against podman (`tests/integration/`, tag `integration`)
  - `make test-gateway`: additionally starts a throwaway OpenShell v0.1.3 gateway (tag `gateway`, `tests/integration/gateway.sh`)
  - `make cross`: linux and darwin binaries for amd64 and arm64 in `dist/`
- Golden files regenerate with `UPDATE_GOLDEN=1 go test ./...` (see `tests/golden/README.md`).
- Container runtime: podman by default. Never require docker-specific features.
- Pin external tools: OpenShell release for vendored profiles, mise release for the installer.
- Skill files under the skill bundle are executable code. Review them with the same rigor as Go source.

## Project Structure

```text
cmd/sandcast/       # CLI entry point (wiring only)
embed.go            # package sandcast: embeds components/, profiles/, policy/, skills/, base-images.yaml
internal/
  cli/              # cobra commands, one file per command group, no business logic
  pipeline/         # gathers plan inputs, runs plan, build, verify, profile import and export
  manifest/         # .sandcast/manifest.yaml schema, load, validate, version gate
  registry/         # base image registry (shipped plus ~/.config/sandcast/base-images.yaml)
  engine/           # podman and docker through os/exec, plus a scripted fake
  probe/            # probe script, digest and platform keyed cache, check runner
  component/        # component schema, three tiers, remote registry refresh, conflicts, versions
  mise/             # mise.toml rendering, mise.lock reading, mise lock wrapper, platforms
  profile/          # OpenShell profile schema, tiers, lint, adaptation, index, MCP profiles, domains
  policy/           # image-embedded OpenShell policy
  plan/             # delta, final user, fragments, deterministic plan
  build/            # Containerfile sections, two-pass build, resolution, fragments, learnings, promotion, redaction pass
  verify/           # component checks in the built image
  gateway/          # openshell CLI wrapper for profile import
  harness/          # adapter interface and registry; claudecode/ adapter
  skills/           # skill bundle, installer, version check
  capture/          # component matching against a project and the host
  redact/           # credential detection and placeholders
  fsutil/           # .sandcast layout, cache and config dirs, atomic writes
  version/          # binary version and pins
  testutil/         # golden-file helper
components/         # shipped component definitions
profiles/           # upstream/v0.1.3 (vendored, unmodified) and overlay/
policy/             # pinned default policy (default-v0.1.3.yaml)
skills/             # portable skill bundle: VERSION, capture/, build/
base-images.yaml    # shipped base image registry
docs/               # cli.md, configuration.md, pins.md, guides/
tests/              # fixtures/reference-project, golden/README.md, integration/
specs/              # feature specifications
brainstorm/         # design notes
```
