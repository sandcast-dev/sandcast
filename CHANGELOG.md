# Changelog

All notable changes to sandcast are recorded here.
Changing a pin below requires an entry in this file.

## Unreleased

First release of the standalone sandcast tool (feature 001-sandcast-standalone-build).

### Core build (User Story 1)

- `sandcast init`, `manifest validate`, `base-images list`, `components list|show|refresh`, `lock`, `probe`, `plan`, `build`, `verify`, `profiles lint|export|import`, `version`.
- Base image probe cached by image digest and platform, with partial probes for new or changed components and `--no-cache`.
- Component tiers: shipped (25 components), remote registry with checksum-verified refresh, project components and promoted recipes.
- mise as the installer: generated `mise.toml`, `sandcast lock` through the host mise, locked installs into `/opt/mise` with shims on `PATH`.
- Deterministic build plan (`plan.yaml`), Containerfile assembled from fixed sections, two-pass build with the adapted profile set copied into the image and `io.sandcast.*` labels.
- OpenShell provider profiles: the 16 upstream examples vendored at v0.1.2, an overlay for npm, crates.io, the Go module proxy and GitLab, built-in lint mirroring v0.1.2, adaptation of `binaries` to resolved paths, and import through the `openshell` CLI at workspace or platform scope.
- Image-embedded OpenShell policy from the pinned default plus component needs; no network entries.
- Redaction of credential shapes in manifests, fragments and every build output.

### Capture and harnesses (User Stories 2 and 4)

- Skill bundle (`/sandcast-capture`, `/sandcast-build`) with `sandcast skills install|check` for Claude Code.
- `sandcast components match` for project, host, harness and always proposals; `sandcast manifest set-shell`.
- Claude Code adapter: discovery of MCP servers, plugins and skills with secrets replaced by placeholders, a `70-harness` section with the configuration in `/opt/sandcast/claude-code`, and generated `mcp-<name>` profiles for MCP endpoints no agent profile covers.

### Repair, promotion and container target (User Stories 3 and 5)

- `sandcast fragments list|write`, `sandcast learnings add`, `sandcast promote`; stored fragments are reused by non-interactive builds and reported as stale once a recipe covers them.
- `--target container`: no OpenShell policy or sandbox user conventions, and an `allowed-domains.txt` from the referenced profiles.

### Pins

- OpenShell v0.1.2 (commit 6648bd0c): vendored example profiles, profile lint rules, default sandbox policy, `openshell` CLI for the import step, gateway image `ghcr.io/nvidia/openshell/gateway:0.1.2` for `make test-gateway`.
- mise v2026.10.4 inside images; host `mise` 2026.9.16 or newer for `sandcast lock`.
- Remote component registry v0.1.0.
- Manifest schema version 1, plan schema version 1, skill bundle 0.1.0.
