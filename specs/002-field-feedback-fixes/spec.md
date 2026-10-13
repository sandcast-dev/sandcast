# Feature Specification: Field Feedback Fixes from First Real-World Use

**Feature Branch**: `002-field-feedback-fixes`

**Created**: 2026-10-13

**Status**: Draft

**Input**: User description: "let's read all open issues on GitHub and create if possible a single spec to fix them all at once."

## Overview

The first sessions that built real projects with sandcast against `nvidia-community-base`, OpenShell v0.1.3, local podman and a remote Kubernetes gateway produced eleven GitHub issues ([#1](https://github.com/sandcast-dev/sandcast/issues/1) to [#11](https://github.com/sandcast-dev/sandcast/issues/11)).
They share one theme: an image passes `sandcast build --verify`, yet the sandbox a user starts from it differs from what the manifest, the lockfile and the project promised, or the user has to discover by hand facts that sandcast already knows.

This feature closes that gap along four lines:

1. **The image matches its inputs**: the lockfile and the project's pinned versions decide tool versions, and no tool depends on a path the workspace mount hides ([#1](https://github.com/sandcast-dev/sandcast/issues/1), [#2](https://github.com/sandcast-dev/sandcast/issues/2), [#3](https://github.com/sandcast-dev/sandcast/issues/3)).
2. **An interactive session gets the curated shell**: tools, `MISE_*` settings, the selected shell program and the curated rc survive the way OpenShell starts sessions, and verify tests that ([#11](https://github.com/sandcast-dev/sandcast/issues/11)).
3. **The image reaches the gateway that runs it**: sandcast reports the exact reference, the push result and the platform, and warns about the pitfalls that otherwise surface only as `ImagePullBackOff` or emulator crashes ([#8](https://github.com/sandcast-dev/sandcast/issues/8), [#9](https://github.com/sandcast-dev/sandcast/issues/9), [#10](https://github.com/sandcast-dev/sandcast/issues/10)).
4. **Capture works from facts the binary reports**: declared binaries, git remotes for local marketplaces, portability markers, and skill snippets that run in any shell ([#4](https://github.com/sandcast-dev/sandcast/issues/4), [#5](https://github.com/sandcast-dev/sandcast/issues/5), [#6](https://github.com/sandcast-dev/sandcast/issues/6), [#7](https://github.com/sandcast-dev/sandcast/issues/7), and the capture part of [#11](https://github.com/sandcast-dev/sandcast/issues/11)).

Terminology follows `specs/001-sandcast-standalone-build/spec.md`.

## Clarifications

### Session 2026-10-13

- Q: When the base image already ships a tool at a different version than `mise.lock` pins, which version goes into the image? → A: The lockfile wins. The locked version is installed over the base image's copy (`shadow`), and a plan notice names both versions. There is no per-component opt-out to keep the base image's version.
- Q: Do the session environment checks run on every verify for the OpenShell target, or only behind a flag? → A: On every verify (and therefore every `build --verify`) for the OpenShell target, as separate report entries that count toward the exit status. There is no flag to skip them.
- Q: With `shell.program: zsh`, how does an interactive OpenShell session end up in zsh while OpenShell v0.1.3 always starts bash? → A: Both. A guarded hook in the system bash startup switches interactive terminal sessions to zsh, and the final user's login shell is set to zsh so that an OpenShell release honoring the login shell needs no hook. An upstream OpenShell issue asks for the login shell to be honored.
- Q: If the base image provides a locked tool but its version cannot be read, is the locked version installed? → A: Yes. An unreadable present version counts as differing from the locked version: the component is planned as `shadow` with a notice that the present version is unknown.
- Q: How does `sandcast init` help a user who targets a remote gateway? → A: With flags, not prompts. New init flags set the target image reference, the manifest platform and push (for example `--image`, `--platform`, `--push`). Without them init writes the `localhost/<name>` default and prints the note. The capture skill asks where the image will run and passes the same flags.

## Terminology

- **Session environment**: the environment an interactive or exec session inside an OpenShell sandbox actually receives. OpenShell clears the image environment and sets only `HOME`, `USER`, `TERM`, `SHELL`, a default `PATH` (`/usr/local/bin:/usr/bin:/bin`) and variables from the sandbox spec, and it always starts bash.
- **Workspace path**: the directory OpenShell may mount the workspace volume over at runtime. For the OpenShell target this is the final user's home directory (`/sandbox` on `nvidia-community-base`). Content the image places there can vanish at runtime.
- **Pinned version**: a tool version a project declares in a well-known file (for example `rust-toolchain.toml`, `.python-version`, `go.mod`), reported together with the file it came from.
- **Locked version**: the exact version of a mise tool recorded in `.sandcast/mise.lock` for the target platform.
- **Remote gateway**: an OpenShell gateway whose sandbox driver does not share the build engine's local image storage (for example a Kubernetes driver on a cluster), so it can start only images it can pull from a registry.
- **Portable source**: a skill or plugin source that resolves the same on any machine that runs plan and build (a git remote, a published marketplace, a path inside the project). A source under `~/.claude/skills`, `~/.claude/plugins` or another host directory is not portable.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - An interactive sandbox session gets the curated shell and the tools (Priority: P1)

A developer built an image with `shell.program: zsh`, a curated rc and mise tools, and verify passed. They start a sandbox and connect. Today they land in plain bash with the base image prompt, none of the mise tools on `PATH`, and `exec zsh` prints errors from the curated rc plus the zsh new-user wizard. After this feature the session opens in zsh with the curated prompt, aliases and completions, every component's binary resolves, and the `MISE_*` settings are in effect. Non-interactive commands (`bash -lc ...`, agent tool calls) keep working and find the same tools.

**Why this priority**: The curated shell is a core promise of capture, and a session without the tools on `PATH` defeats the image for both humans and agents. Verify passing on a broken image erodes trust in every other check.

**Independent Test**: Build the reference fixture project with `shell.program: zsh` on `nvidia-community-base`. Start a container with the OpenShell session environment (cleared environment, default `PATH`, `HOME` set to the final user's home, bash as the started program) and confirm that an interactive terminal session runs zsh with the curated rc and no errors, and that `bash -lc 'command -v <bin>'` resolves every component binary. Then repeat against a real OpenShell gateway with `openshell sandbox connect`.

**Acceptance Scenarios**:

1. **Given** an image for the OpenShell target, **When** a session starts with the session environment, **Then** every component binary resolves through `PATH` and every `MISE_*` variable the image needs is set, in both a login bash and any zsh.
2. **Given** a base image whose home-directory rc file resets `PATH` (as `nvidia-community-base` does in `/sandbox/.bashrc`), **When** a login or interactive bash starts, **Then** the mise shims are still on `PATH` after that rc file ran, and the plan reports that the base image ships such a file.
3. **Given** `shell.program: zsh`, **When** OpenShell starts bash for an interactive terminal session, **Then** the session switches to zsh as a login shell, the curated rc is read, and no command-not-found or new-user-install output appears.
4. **Given** `shell.program: zsh`, **When** a non-interactive command runs through bash (`bash -lc ...`, a pipe, no terminal), **Then** bash runs the command itself and does not switch shells.
5. **Given** a home directory without a `.zshrc` (the workspace volume is empty), **When** zsh starts, **Then** the zsh new-user wizard does not run.
6. **Given** a built image for the OpenShell target whose shell setup is broken (for example the shims are missing from the session `PATH`), **When** the user runs verify, **Then** verify reports the shell components as failed under session conditions and exits non-zero, even though the same checks pass with the image environment intact.

---

### User Story 2 - The lockfile and the project's pinned versions decide what is in the image (Priority: P1)

A developer runs `sandcast lock`, then `sandcast build --verify`. Today the image ships the base image's `gh`, `node`, `python` and `codex` because the base image already provides them, although `mise.lock` pins other versions; `python` even comes from the final user's home, which the workspace volume hides at runtime; and a project that pins `rust 1.95.0` in `rust-toolchain.toml` gets rust 1.99.0, so rustup downloads a toolchain at runtime. After this feature, the versions in the image are the locked versions, every component binary lives outside the workspace path, and capture proposes the project's pinned versions.

**Why this priority**: A lockfile that is silently ignored breaks the determinism principle and the reasonable expectation of everyone who ran `sandcast lock`. A tool that vanishes under the workspace mount passes verify but fails in the sandbox, the same class of trust failure as User Story 1.

**Independent Test**: Use a manifest on `nvidia-community-base` with unpinned `gh`, `node`, `python` and `codex`, run `sandcast lock` for both platforms, then plan, build and verify. Every version verify reports equals the locked version, the plan shows `shadow` (not `skip`) for each differing component with a notice naming both versions, and no resolved path lies under the final user's home. Separately, run `sandcast components match` on a project with `rust-toolchain.toml`, `.python-version` and `go.mod` and check the reported pinned versions.

**Acceptance Scenarios**:

1. **Given** a mise component the base image provides at version A and a lockfile that locks version B for the target platform, **When** the user runs plan, **Then** the component is planned as `shadow` with B, and the plan carries a notice naming the component, A, B and the decision.
2. **Given** a mise component the base image provides at exactly the locked version, **When** the user runs plan, **Then** the component is planned as `skip` as today.
3. **Given** a component the base image provides, but no lockfile entry exists for it, **When** the user runs plan, **Then** behavior is unchanged from today (present means `skip`, an explicit version selector can still make it incompatible).
4. **Given** a base image whose only binary for a component lies under the workspace path, **When** the user plans for the OpenShell target, **Then** the component is treated as missing, it is installed like any other component, and the plan carries a notice naming the path that was ignored.
5. **Given** the same base image and the container target, **When** the user plans, **Then** a binary under the final user's home still counts as present, because no workspace mount replaces that directory.
6. **Given** a project with `rust-toolchain.toml` (`channel = "1.95.0"`), `.python-version` (`3.14.5`) and `go.mod` (`go 1.26.7`), **When** the user runs `sandcast components match . --json`, **Then** the `rust`, `python` and `go` proposals each carry the pinned version and the file it came from.
7. **Given** a pinned version in a project file, **When** the capture skill writes the manifest, **Then** it proposes that version as the component's version selector and the user can accept or change it.

---

### User Story 3 - The built image reaches the gateway that will run it (Priority: P2)

A developer on an arm64 Mac targets an OpenShell gateway on an amd64 OpenShift cluster. Today `sandcast init` defaults to `localhost/<name>` and the host platform, so sandbox creation keeps pulling an image the cluster cannot reach, built for the wrong architecture. When they fix that, `mise install` for rust crashes under QEMU emulation. When they build on a remote amd64 podman host and push, nothing confirms the push, nothing prints the reference to start a sandbox from, and a mistyped Docker Hub reference plus anonymous pull rate limits surface only as `ImagePullBackOff`. After this feature, sandcast tells them, before and after the build, which reference and platform the gateway needs, whether the push happened, and what will go wrong.

**Why this priority**: Every remote-gateway user hits at least one of these, and each costs a round of cluster event digging. The fixes are warnings, report fields and documentation; none changes what goes into the image.

**Independent Test**: With a fake gateway that reports a remote driver, run init, plan and build and check each notice. Build with `--push` against a local registry and check the report fields and the console line. Validate manifests with `docker.io/<name>` and `localhost/<name>` references. Follow the cross-architecture guide end to end with a remote podman connection.

**Acceptance Scenarios**:

1. **Given** a successful build, **When** the build finishes, **Then** the console output and the JSON report contain the fully qualified image reference and a ready-to-run `openshell sandbox create --from <ref>` command.
2. **Given** a build with push enabled, **When** the push succeeds, **Then** the report records that the image was pushed and the registry digest, and the build log contains the push; **When** push is not enabled, **Then** the report records that the image was not pushed.
3. **Given** a manifest whose OpenShell target image is `docker.io/<name>` without a namespace, **When** the user runs `sandcast manifest validate` or plan, **Then** a warning explains that the reference resolves to `docker.io/library/<name>`.
4. **Given** a push to Docker Hub, **When** the build reports the push, **Then** a notice explains that anonymous pulls from cluster nodes are rate limited and that the gateway's sandbox service account needs a pull secret, or the image should go to a registry the cluster already authenticates to.
5. **Given** an active gateway that sandcast can identify as remote and an image reference starting with `localhost/`, **When** the user runs build or profile import, **Then** sandcast warns that the gateway cannot pull that reference and names the manifest fields to change (`targets.openshell.image`, `targets.openshell.push`).
6. **Given** an active gateway that reports its node architecture, **When** the build platform differs from it, **Then** sandcast warns and names the `platform` field.
7. **Given** `sandcast init` without an image reference flag, **When** it writes the OpenShell target, **Then** the reference defaults to `localhost/<name>` and the user is told that this works only with a local gateway that shares the build engine's storage. **Given** init with image reference, platform and push flags, **Then** the manifest carries those values and no note is printed. **Given** the capture skill, **When** the user says the image runs on a remote gateway, **Then** the skill passes those flags to init.
8. **Given** a build platform that differs from the native architecture of the machine the engine runs on, and a plan that contains a component known to break under emulation (rust at least), **When** the user runs plan or build, **Then** a notice names the component and points to the cross-architecture guide, and the plan file itself is unchanged. **Given** the same manifest built through a remote engine connection whose host matches the build platform, **Then** no notice appears.
9. **Given** the documentation, **When** a user looks for cross-architecture builds, **Then** a guide covers Rosetta in the podman machine, building through a remote amd64 podman connection (including the SSH key and socket path pitfalls), and CI.

---

### User Story 4 - Capture works from facts the binary reports (Priority: P3)

A developer runs the capture skill with Claude Code. Today the skill has to work out by hand which of 13 skipped marketplaces have a usable git remote, recognize by path that a skill under `~/.claude/skills` makes the manifest host-specific, add a symlink so verify finds `helix` when the executable is `hx`, and its own shell snippets fail in zsh and leave temporary files with the curated rc behind. After this feature the binary reports these facts and the skill acts on them.

**Why this priority**: Each item is a workaround the skill or the user already performs; the fixes reduce friction and remove a credential-hygiene gap (temporary files), but no image is wrong without them.

**Independent Test**: Run `sandcast harness discover claude-code --json` against a fixture home with a local marketplace that is a git checkout with a GitHub remote, a dirty working tree, and a skill under `~/.claude/skills`. Check the proposed source, the notes and the portability markers. Build a manifest with an undefined component that declares `bins: [hx]`. Run each skill snippet under sh, bash and zsh.

**Acceptance Scenarios**:

1. **Given** a manifest entry for a component no tier defines, with a declared list of binaries, **When** verify runs, **Then** it checks each declared binary instead of the component key, and `sandcast promote` defaults its binaries to the declared list.
2. **Given** a manifest entry for a component no tier defines and no declared binaries, **When** verify runs, **Then** behavior is unchanged (the key is checked).
3. **Given** a plugin whose marketplace is a local directory that is a git repository with a remote, **When** discovery runs, **Then** the plugin is reported with a proposed source (`owner/repo` for GitHub, the remote URL otherwise) instead of being skipped, and a note states the remote URL, whether the local checkout has unpushed commits or uncommitted changes, and whether the marketplace name in the remote's marketplace file matches the locally registered name.
4. **Given** a plugin whose local marketplace directory has no git remote, **When** discovery runs, **Then** it is skipped as today, with the reason stated.
5. **Given** skills and plugin sources under host directories, **When** discovery runs, **Then** each is marked not portable with a reason, and every other source is marked portable.
6. **Given** a proposed plugin or a non-portable source, **When** the capture skill presents it, **Then** the plugin stays opt-in as today, and the skill bases its copy-into-project offer on the portability marker rather than on parsing the path.
7. **Given** any snippet in the skill bundle, **When** it runs in sh, bash or zsh, **Then** it succeeds, propagates the sandcast command's exit status, and removes its temporary file on every exit path.
8. **Given** a host rc whose curated version keeps a `compdef` line for a completion that a stripped plugin used to provide, **When** the capture skill curates the rc, **Then** it adds a guarded line that loads the tool's own zsh completion before that `compdef` line.

### Edge Cases

- The lockfile locks a version for a component on one platform but not the target platform: plan fails for that component with the platform named, as today (001 edge case); the new comparison does not run.
- The probe cannot determine the present version (the check passes, version output unparsable): the component is treated as differing from the locked version and shadowed, with a notice that the present version is unknown.
- A component is present both under the workspace path and elsewhere on `PATH`: the binary outside the workspace path decides; no notice is emitted.
- The base image's final user's home is not the OpenShell workspace path for a future base image: the workspace path comes from the base image facts and the target, not from a hard-coded `/sandbox`.
- A project pins a non-version value (`channel = "stable"`, `requires-python = ">=3.12"`, `engines.node = "^20"`): the raw value is reported with its file and marked as a range or channel, not as an exact version; the skill does not turn it into an exact pin.
- Several files pin the same tool (`.tool-versions` and `.python-version`), or a `go.work` covers several modules: each source is reported; for go the highest `go` or `toolchain` directive across modules is the proposal.
- `shell.program: bash`: no shell switch is installed; the startup-file environment, the `PATH` protection and the session checks in verify still apply.
- zsh is not present in the final image although `shell.program: zsh`: the switch hook does nothing (it guards on the zsh binary), and verify reports the shell component as failed.
- The `openshell` CLI is absent, or the gateway does not expose its driver or node architecture: no remote-gateway or architecture warning is emitted, and build continues; plan, build and verify never require a gateway.
- A push fails: the build fails as today, and the report records that the image was not pushed.
- The local marketplace's git remote is unreachable or the remote marketplace file is not available locally: the plugin is still proposed with the remote, and the note states that the name match could not be checked.
- A git remote URL embeds credentials (`https://user:token@host/...`): the credential is stripped before the URL appears in a proposal, a note, or the manifest.
- A declared binary list on an undefined component conflicts with a binary another component provides: the existing conflict check applies to declared binaries as well.

## Requirements *(mandatory)*

### Functional Requirements

**Image matches its inputs** ([#1](https://github.com/sandcast-dev/sandcast/issues/1), [#2](https://github.com/sandcast-dev/sandcast/issues/2), [#3](https://github.com/sandcast-dev/sandcast/issues/3))

- **FR-001**: For a mise component the base image provides, the plan MUST compare the present version with the locked version for the target platform. When they differ, or when the present version cannot be determined, the component MUST be planned as `shadow` with the locked version. When they are equal, it MUST be planned as `skip`.
- **FR-002**: Every plan decision that departs from a plain present-or-missing result (a lockfile-driven shadow, a binary ignored under the workspace path, an unknown present version) MUST produce a plan notice naming the component and both facts, and the build report MUST repeat these notices.
- **FR-003**: The probe MUST record the resolved path of each present component. For the OpenShell target, a component whose binaries resolve only under the workspace path MUST be treated as missing.
- **FR-004**: `sandcast components match` MUST report, for each project-sourced proposal, every pinned version found in the project together with its source file and whether it is an exact version or a range or channel. Supported sources at least: `go.mod` and `go.work` (`go` and `toolchain` directives), `rust-toolchain.toml` and `rust-toolchain` (`channel`), `.python-version`, `pyproject.toml` (`requires-python`), `.nvmrc`, `.node-version`, `package.json` (`engines.node`), `.tool-versions` and `mise.toml`.
- **FR-005**: The capture skill MUST propose an exact pinned version reported by the binary as the component's version selector and let the user accept or change it.

**Session environment** ([#11](https://github.com/sandcast-dev/sandcast/issues/11))

- **FR-006**: For the OpenShell target, everything an interactive or exec session needs (the mise shims on `PATH` and every `MISE_*` variable the image sets) MUST be established by system shell startup files read by a login bash and by every zsh, not only by image environment variables.
- **FR-007**: The mise shims MUST remain on `PATH` in an interactive or login bash after the final user's home-directory rc files ran, even when such a file resets `PATH`. The plan MUST report when the base image ships a home-directory rc file that resets `PATH`.
- **FR-008**: With `shell.program: zsh`, an interactive terminal session that OpenShell starts in bash MUST switch to a zsh login shell, through a guarded hook in the system-wide bash startup. Non-interactive bash invocations and sessions without a terminal MUST NOT switch. The image MUST also set the final user's login shell to zsh.
- **FR-009**: With `shell.program: zsh`, the zsh new-user wizard MUST be disabled for all users in the image.
- **FR-010**: For the OpenShell target, every verify run (including `build --verify`) MUST additionally run, without an opt-in or opt-out flag, the shell-related checks (every component binary resolves; the selected shell program starts, reads the curated rc, and writes nothing to stderr during startup) under session conditions: a cleared environment with only `HOME`, `USER`, `TERM` and the OpenShell default `PATH`, through a login bash, and through an interactive zsh when `shell.program` is zsh. These results MUST appear as separate entries in the verify report and count toward the exit status.

**Image delivery to the gateway** ([#8](https://github.com/sandcast-dev/sandcast/issues/8), [#9](https://github.com/sandcast-dev/sandcast/issues/9), [#10](https://github.com/sandcast-dev/sandcast/issues/10))

- **FR-011**: The build report (console and JSON) MUST contain the fully qualified image reference, a ready-to-run `openshell sandbox create --from <ref>` command for the OpenShell target, whether the image was pushed, and the registry digest when it was. The build log MUST record the push.
- **FR-012**: `sandcast manifest validate` and plan MUST warn when a target image is a Docker Hub reference without a namespace, naming the reference it resolves to.
- **FR-013**: When a build pushes to Docker Hub, the report MUST carry a notice about anonymous pull rate limits from cluster nodes and the need for a pull secret on the gateway's sandbox service account.
- **FR-014**: When the `openshell` CLI is available and identifies the active gateway as remote, build and profile import MUST warn about a `localhost/` image reference and name the manifest fields to change. When the gateway reports its node architecture and it differs from the build platform, they MUST warn and name the `platform` field. Neither warning MAY make a gateway a requirement for plan, build or verify.
- **FR-015**: `sandcast init` MUST accept flags that set the target image reference, the manifest's target platform and push for the selected target. It MUST stay non-interactive. When no image reference is given, it MUST write the `localhost/<name>` default and state that this reference works only with a local gateway sharing the build engine's storage. The capture skill MUST ask where the image will run and, for a remote gateway, pass the image reference, platform and push through these flags.
- **FR-016**: A component definition MUST be able to declare that it is known to fail under user-mode emulation. The shipped rust component MUST carry this declaration. When the build platform's architecture differs from the native architecture of the machine the container engine runs on (the remote host for a remote engine connection) and the plan contains such a component, plan and build MUST print a notice and the build report MUST carry it. The notice MUST NOT be written into the plan file, because the engine's architecture is not a plan input and the plan stays deterministic.
- **FR-017**: A guide in `docs/guides/` MUST cover cross-architecture builds: Rosetta in the podman machine, a remote amd64 podman connection selected per build (including the SSH identity limitations and the macOS socket path length limit, with the socket-forward workaround), and CI.

**Capture facts from the binary** ([#4](https://github.com/sandcast-dev/sandcast/issues/4), [#5](https://github.com/sandcast-dev/sandcast/issues/5), [#6](https://github.com/sandcast-dev/sandcast/issues/6), [#7](https://github.com/sandcast-dev/sandcast/issues/7), [#11](https://github.com/sandcast-dev/sandcast/issues/11))

- **FR-018**: A manifest component entry MUST be able to declare its binaries. For a component no tier defines, verify MUST check the declared binaries, the conflict check MUST include them, and `sandcast promote` MUST default its binaries to the declared list. For a component a tier defines, a declared list MUST be rejected by validation with a message, so there is one source of truth.
- **FR-019**: Harness discovery MUST propose a source for a plugin whose local marketplace directory is a git repository with a remote, instead of skipping it, with a note that states the remote URL, local divergence (unpushed commits, uncommitted changes) and whether the remote's marketplace name matches the local one. The plugin MUST stay opt-in. Credentials embedded in a remote URL MUST be stripped.
- **FR-020**: Harness discovery MUST mark every reported skill and plugin source as portable or not portable, with a reason for each non-portable one.
- **FR-021**: The capture skill MUST base its offer to copy a source into the project on the portability marker.
- **FR-022**: Every shell snippet in the skill bundle MUST run unchanged in sh, bash and zsh, MUST NOT assign shell special parameters, MUST propagate the exit status of the sandcast command it wraps, and MUST remove its temporary files on every exit path. A test MUST execute each snippet under each of the three shells.
- **FR-023**: The capture skill's rc curation MUST add a guarded load of the tool's own zsh completion before any kept `compdef` line whose target completion a stripped plugin provided.

**Documentation and tests**

- **FR-024**: Every user-visible change MUST be documented in the same branch: new manifest fields and component fields in `docs/configuration.md`, new report fields, notices and warnings in `docs/cli.md`, the session environment behavior in the build guide, the discovery changes in the capture guide, and the README where the flow changes.
- **FR-025**: The session environment behavior (FR-006 to FR-010) MUST have an integration test against podman that reproduces the OpenShell session environment, and a gateway test that starts a real sandbox and checks the session.

### Key Entities

- **Notice**: A message attached to a component or to the image. Kind, the component when it applies, and the facts that triggered it. Kinds derived from plan inputs (lockfile shadow, workspace path ignored, unknown present version, home rc resets `PATH`, namespace-less Docker Hub reference) are written into the plan and repeated in the build report. Kinds derived from the host, the engine or the gateway (emulation risk, remote gateway with a `localhost/` reference, gateway architecture mismatch, Docker Hub rate limits) appear only on the console and in the build report.
- **Probe component result**: Extended with the present version and the resolved paths, so the plan can compare against the lockfile and the workspace path.
- **Pinned version proposal**: Tool key, raw value, source file, exact or range or channel.
- **Manifest component entry**: Extended with an optional list of declared binaries for components no tier defines.
- **Component definition**: Extended with an optional marker that the component is known to fail under user-mode emulation.
- **Build report**: Extended with the fully qualified image reference, the sandbox create command, the push result and the registry digest.
- **Verify report entry**: Extended with the condition the check ran under (image environment or session environment, and which shell).
- **Discovered source**: Extended with a portability marker and reason, and for proposed marketplace plugins the remote, divergence and name-match notes.

## Delivery Phases

The fixes are independent of each other inside each phase. Planning SHOULD split them into three phases so that the trust-critical fixes ship first:

1. **Image correctness**: User Stories 1 and 2. FR-001 to FR-010, FR-025, with their documentation. Closes [#1](https://github.com/sandcast-dev/sandcast/issues/1), [#2](https://github.com/sandcast-dev/sandcast/issues/2), [#3](https://github.com/sandcast-dev/sandcast/issues/3) and the image part of [#11](https://github.com/sandcast-dev/sandcast/issues/11).
2. **Delivery to the gateway**: User Story 3. FR-011 to FR-017, with their documentation. Closes [#8](https://github.com/sandcast-dev/sandcast/issues/8), [#9](https://github.com/sandcast-dev/sandcast/issues/9), [#10](https://github.com/sandcast-dev/sandcast/issues/10).
3. **Capture facts**: User Story 4. FR-018 to FR-023, with their documentation. Closes [#4](https://github.com/sandcast-dev/sandcast/issues/4), [#5](https://github.com/sandcast-dev/sandcast/issues/5), [#6](https://github.com/sandcast-dev/sandcast/issues/6), [#7](https://github.com/sandcast-dev/sandcast/issues/7) and the capture part of [#11](https://github.com/sandcast-dev/sandcast/issues/11).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For the `#1` reproduction manifest (unpinned `gh`, `node`, `python`, `codex` on `nvidia-community-base`), all four versions reported by verify equal the locked versions.
- **SC-002**: For the registry's default OpenShell base image, no component's resolved path in the built image lies under the workspace path, and the adapted `pypi` profile is no longer flagged.
- **SC-003**: A sandbox started from the reference fixture image with `shell.program: zsh` through a real OpenShell gateway opens in zsh with the curated prompt, produces zero error lines on start, and resolves 100% of component binaries in the session.
- **SC-004**: An image with a deliberately broken session setup fails verify, while the same image with a correct setup passes. Verify never passes an image whose session setup fails the checks of FR-010.
- **SC-005**: For a project pinning rust, python and go in project files, the capture produces a manifest with those versions, and running a command in the project inside the sandbox triggers no toolchain download.
- **SC-006**: A user who pushes an image and copies the printed `openshell sandbox create` command starts a sandbox from the right reference on the first attempt.
- **SC-007**: Each of the four pitfalls observed in the field (a `localhost/` reference with a remote gateway, a platform mismatch with the gateway nodes, a namespace-less Docker Hub reference, an emulation-sensitive component in a cross-architecture build) produces a warning or notice before the user starts a sandbox.
- **SC-008**: On a fixture home with local marketplaces that are git checkouts (GitHub remote, non-GitHub remote, dirty working tree, mismatched marketplace name) plus one without a remote, every marketplace with a remote is proposed with the correct source and notes, the one without a remote is skipped with a reason, and the capture skill needs no manual remote lookup.
- **SC-009**: Every skill bundle snippet passes under sh, bash and zsh and leaves zero temporary files behind in success and failure runs.

## Assumptions

- **Lockfile wins over the base image** ([#1](https://github.com/sandcast-dev/sandcast/issues/1), confirmed in Clarifications): the issue offered two options, shadowing with the locked version or only a notice. This spec chooses shadowing without a per-component opt-out, because the lockfile is the deterministic input (constitution principle II) and users who run `sandcast lock` expect it to decide. A user who wants the base image's version can drop the component or select a version range that the base image satisfies and relock. Image size grows by the shadowed tools; that is accepted.
- **Workspace path rule applies to the OpenShell target only** ([#2](https://github.com/sandcast-dev/sandcast/issues/2)): the container target has no workspace volume over the home directory. The workspace path is the final user's home in the OpenShell v0.1.3 line; if a later OpenShell release mounts elsewhere, the pin refresh updates the rule.
- **The shell switch lives in the image**, not in OpenShell ([#11](https://github.com/sandcast-dev/sandcast/issues/11)): OpenShell v0.1.3 always starts bash and ignores `$SHELL` and `/etc/passwd`. The image therefore carries both the hook and the login shell (confirmed in Clarifications). An upstream OpenShell issue asking to honor the login shell is a follow-up outside this feature's code, filed only after the user approves its text; this feature does not depend on it. Once a pinned OpenShell release honors the login shell, the hook can be retired with the pin refresh.
- **Session checks run under the container engine**, reproducing the session environment; they do not need a gateway, in line with the 001 rule that verify runs without one. The gateway test in FR-025 is an additional integration test, not part of `sandcast verify`.
- **Remote gateway detection is best effort**: it uses only what the pinned `openshell` CLI exposes about the active gateway. If the CLI exposes neither driver nor node architecture, the related warnings are not emitted, and the init and capture guidance (FR-015) carries the message instead.
- **Marketplace name checks use local git state only**: discovery reads the remote's marketplace file from the locally known remote-tracking ref and does not fetch, so capture works offline. The note says when that ref is missing.
- **Emulation-sensitive components are declared, not hard-coded**: only rust is marked in this feature; other components can be marked as reports come in.
- **Podman remote connections need no sandcast change**: building through `CONTAINER_CONNECTION` already works ([#9](https://github.com/sandcast-dev/sandcast/issues/9)); this feature documents it and adds the push reporting.
- The OpenShell pin stays at v0.1.3 and the mise pin is unchanged.
