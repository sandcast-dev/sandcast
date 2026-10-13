# Changelog

All notable changes to sandcast are recorded here.
Changing a pin below requires an entry in this file.

## Unreleased

### Field feedback fixes (feature 002-field-feedback-fixes)

- #1: the lockfile decides tool versions; a base image tool at a different version than `mise.lock` is shadowed with the locked version.
- #2: binaries that resolve only under the workspace path are ignored for the OpenShell target and installed.
- #3: `sandcast components match` reports pinned versions from project files, and the capture skill proposes them.
- #4: manifest component entries can declare `bins` for components no tier defines.
- #5: harness discovery proposes a source for local marketplaces that are git checkouts with a remote.
- #6: skill bundle snippets run in sh, bash and zsh and always remove their temporary files.
- #7: harness discovery marks every skill and plugin source as portable or not portable.
- #8: `sandcast init` accepts `--image`, `--platform` and `--push`; reference and remote gateway warnings.
- #9: emulation notice for emulation-sensitive components and a cross-architecture build guide.
- #10: the build report carries the fully qualified image reference, the push result and the digest.
- #11: interactive and exec sessions get the mise tools, the `MISE_*` settings and the curated shell; verify checks the session environment.

### Compatibility

- Probe cache format 2: the first plan after the upgrade runs one full re-probe per base image.
- Skill bundle 0.2.0: `sandcast skills check` reports installed skills as outdated.
- Images with `shell.program: zsh` now switch interactive terminal sessions from bash to zsh. Set `SANDCAST_NO_SHELL_SWITCH=1` in the sandbox environment to stay in bash.

### Standalone build (feature 001-sandcast-standalone-build)

First release of the standalone sandcast tool (feature 001-sandcast-standalone-build).

### Core build (User Story 1)

- `sandcast init`, `manifest validate`, `base-images list`, `components list|show|refresh`, `lock`, `probe`, `plan`, `build`, `verify`, `profiles lint|export|import`, `version`.
- Base image probe cached by image digest and platform, with partial probes for new or changed components and `--no-cache`.
- Component tiers: shipped (25 components), remote registry with checksum-verified refresh, project components and promoted recipes.
- mise as the installer: generated `mise.toml`, `sandcast lock` through the host mise, locked installs into `/opt/mise` with shims on `PATH`.
- Deterministic build plan (`plan.yaml`), Containerfile assembled from fixed sections, two-pass build with the adapted profile set copied into the image and `io.sandcast.*` labels.
- OpenShell provider profiles: the 17 upstream examples vendored at v0.1.3, an overlay for npm, crates.io, the Go module proxy and GitLab, built-in lint mirroring v0.1.3, adaptation of `binaries` to resolved paths, and import through the `openshell` CLI at workspace or platform scope.
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

- OpenShell v0.1.3 (commit e1f3c82c): vendored example profiles, profile lint rules, default sandbox policy, `openshell` CLI for the import step, gateway image `ghcr.io/nvidia/openshell/gateway:0.1.3` for `make test-gateway`.
- mise v2026.10.4 inside images; host `mise` 2026.9.16 or newer for `sandcast lock`.
- Remote component registry v0.1.0.
- Manifest schema version 1, plan schema version 1, skill bundle 0.1.0.

### OpenShell pin refresh v0.1.2 to v0.1.3

- Re-vendored the upstream provider profiles from OpenShell v0.1.2 (commit 6648bd0c) to v0.1.3 (commit e1f3c82c). The 16 existing profiles are byte for byte identical; `oci-genai` is new, for 17 in total.
- Default sandbox policy moved from `policy/default-v0.1.2.yaml` to `policy/default-v0.1.3.yaml`; `restrictive_default_policy()` content is unchanged between the two releases.
- Ported the v0.1.3 stable profile validation into `internal/profile`: credential names and token_grant endpoint and audience-override selectors must be free of control characters; a token_grant endpoint may not use protocol `sql` or `tls: skip`; `2026-07-28` joins the supported MCP protocol revisions (the default revision stays `2025-11-25`); and a null value at a schema-declared object path is rejected before typed decoding.
- Relaxed the `sandcast profiles import` version check: it now accepts any `openshell` CLI release on the pinned minor line (any `0.1.x` for the v0.1.3 pin, pre-release suffixes included) instead of an exact match, so a future patch release needs no code change.
- Deferred to a follow-up: the experimental `ProviderFileProfile` (`files`) feature and the token_grant audience-override to endpoint binding-conflict checks, both of which upstream marks as subject to change.
