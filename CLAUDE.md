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
- Components reference OpenShell provider profiles. No sandcast endpoint schema. Upstream example profiles are vendored at a pinned OpenShell release (v0.1.2), with a same-schema overlay for npm, crates.io, the Go module proxy and GitLab.
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

- Use make targets (`make test`, `make lint`, `make verify`) once they exist. Do not run `go build` directly.
- Container runtime: podman by default. Never require docker-specific features.
- Pin external tools: OpenShell release for vendored profiles, mise release for the installer.
- Skill files under the skill bundle are executable code. Review them with the same rigor as Go source.

## Project Structure (planned)

```text
cmd/            # CLI entry point
internal/       # Go packages: manifest, registry, probe, components, profiles, plan, build, verify
skills/         # portable skill bundle (capture, build) installed into harnesses
profiles/       # vendored upstream profiles (pinned) and the sandcast overlay
specs/          # feature specifications
brainstorm/     # design notes
docs/           # README, command reference, configuration reference
```
