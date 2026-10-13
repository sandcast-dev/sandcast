# Changelog

All notable changes to sandcast are recorded here.
Changing a pin below requires an entry in this file.

## Unreleased

### Field feedback fixes (feature 002-field-feedback-fixes)

Fixes for the eleven issues from the first real builds ([#1](https://github.com/sandcast-dev/sandcast/issues/1) to [#11](https://github.com/sandcast-dev/sandcast/issues/11)).

#### The image matches its inputs (#1, #2, #3)

- The lockfile decides tool versions: a mise component that the base image provides at a version other than the one `mise.lock` locks for the target platform, or at a version the probe cannot read, is planned as `shadow` and installed at the locked version; an equal version stays `skip`. There is no per-component opt-out. The delta records `locked_version`.
- The probe records every `PATH` hit of a component's binary. For the OpenShell target, a binary that lies only under the workspace path (the final user's home, `/sandbox` on the community base) counts as missing and is installed; when only the first hit lies there, another copy decides and its version counts as unknown. The delta records `ignored_paths`. The container target is unchanged.
- New plan notices with a stable `<kind>: <subject>: <message>` form, sorted after the other notices: `lockfile-shadow`, `unknown-version`, `workspace-path`, `home-rc-path` (a home rc file of the base image resets `PATH`) and `reference`. `sandcast build` repeats them in its report (`notices`).
- `sandcast components match` reports `pinned_versions` (value, file, `exact`, `range` or `channel`) from `go.mod`, `go.work`, `rust-toolchain.toml`, `rust-toolchain`, `.python-version`, `pyproject.toml`, `.nvmrc`, `.node-version`, `package.json`, `.tool-versions` and `mise.toml`, a `PINNED` table column, and `notes` for pin files it cannot parse. `.tool-versions` and `mise.toml` entries count as evidence.

#### Sessions get the curated shell (#11)

- `/etc/profile.d/sandcast-env.sh` replaces `/etc/profile.d/sandcast-mise.sh`. It exports every `MISE_*` setting and the component and harness environment (for example `RUSTUP_HOME`, `GOPATH`, `CLAUDE_CONFIG_DIR`) and puts the mise shims first on `PATH`; in bash it re-applies the shims through `PROMPT_COMMAND` and, for `bash -lc`, a self-removing `DEBUG` trap, so home rc files that reset `PATH` no longer hide the tools. The system bash rc of OpenShell images and the system zshenv source it.
- With `shell.program: zsh`, an interactive terminal session that OpenShell starts in bash switches to a zsh login shell (`SANDCAST_NO_SHELL_SWITCH=1` keeps bash), the final user's login shell is `/bin/zsh` for created and kept users, and the zsh new-user wizard is disabled.
- The curated rc starts with two lines that re-apply the shims and mark the file as read.
- `sandcast verify` (and `build --verify`) always adds session checks for the OpenShell target: every component check runs again under OpenShell's cleared environment through `bash -lc`, `bash -i` and, with zsh, `zsh -i -c`, plus a startup check of the selected shell. Results carry a `condition` and count toward the exit status; the console shows them in a `session checks` group.

#### The image reaches the gateway (#8, #9, #10)

- The build report adds `image_ref`, `sandbox_create` (`openshell sandbox create --from <ref>`), `pushed` and `digest`; `build.log` records the push. The engine reads the registry digest with `podman push --digestfile` or from docker's push output.
- `sandcast init` takes `--image name[:tag]`, `--platform` and `--push`, and explains that the default `localhost/<name>` works only with a local gateway that shares the engine's image storage.
- `sandcast manifest validate` reports `warnings` (console `warning:` lines) for a Docker Hub reference without a namespace; `valid` and the exit status are unchanged, and plan repeats it as a `reference` notice.
- Runtime notices that never enter the plan file: `emulation` (a component with the new `emulation_unsafe` field, set on `rust`, in a build whose platform differs from the engine host's architecture), `gateway` (a `localhost/` image while the active `openshell` gateway is remote; also in the `profiles import` report as `warnings`) and `docker-hub` (anonymous pull rate limits after a push to `docker.io`).
- New guide `docs/guides/cross-arch.md`: Rosetta in the podman machine, a remote amd64 podman connection (`CONTAINER_CONNECTION`, SSH identity limits, the socket-forward workaround, the macOS socket path limit) and CI.

#### Capture works from facts the binary reports (#3, #4, #5, #6, #7, #11)

- Manifest component entries can declare `bins` for components that no tier defines: verify checks them, the conflict check includes them, `sandcast promote` defaults `--bins` to them, and validation and plan reject `bins` on a tier-defined key.
- `sandcast harness discover` marks every skill and plugin `portable` (with a `reason` when not) and proposes plugins whose local marketplace directory is a git checkout with a remote, with a `remote` object (`url` without credentials, `unpushed_commits`, `uncommitted_changes`, `name_match`). Discovery reads local git state only and never fetches.
- Skill snippets run unchanged in sh, bash and zsh, remove their temporary file on every path and propagate the exit status of `sandcast`; a test runs every snippet under the three shells.
- The capture skill proposes exact pinned versions, asks where the image runs and passes `--image`, `--platform` and `--push` to init for a remote gateway, bases its copy offer on the portability marker, presents remote notes for proposed marketplace plugins, and loads a tool's own zsh completion before a kept `compdef` line whose plugin was removed. The build skill declares `bins` instead of adding symlinks.

#### Compatibility

- Probe cache format 2: the first plan after the upgrade runs one full re-probe per base image.
- Skill bundle 0.2.0: `sandcast skills check` reports installed skills as outdated; run `sandcast skills install`.
- Images with `shell.program: zsh` now switch interactive terminal sessions from bash to zsh. Set `SANDCAST_NO_SHELL_SWITCH=1` in the sandbox environment to stay in bash.
- Images with a base tool at another version than `mise.lock` now install the locked version and grow accordingly.
- The `engine.Engine` interface gains `HostArch`, and `Push` returns the registry digest.
- Pins unchanged: OpenShell v0.1.3, mise v2026.10.4, manifest schema 1, plan schema 1.

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
