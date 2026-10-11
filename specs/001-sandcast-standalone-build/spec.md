# Feature Specification: sandcast, a Standalone Sandbox Image Builder

**Feature Branch**: `001-sandcast-standalone-build`

**Created**: 2026-10-08

**Status**: Draft

**Input**: User description: "brainstorm/096-sandcast-standalone-build.md"

## Overview

sandcast is a standalone tool that turns a project directory into a sandbox image. It pours a project and its developer's tooling into a pre-configured base image, the way sand casting pours metal into a mold. The tool has two halves. A guided capture examines the project and the host and writes a descriptor (the manifest). A build turns the manifest plus a probed base image into a container image, detecting what the base image already provides and adding only what is missing, and produces the OpenShell provider profiles adapted to that image so the sandbox gets the right network policy at runtime.

The capability exists today inside cc-deck, tied to cc-deck's binary, its mandatory tool stack, a home-grown network policy catalog, and Claude Code as the only harness. sandcast extracts it into its own repository so that any project and any supported agent harness can use it. It drops the home-grown policy catalog in favor of upstream OpenShell provider profiles, and it drops most home-grown install recipes in favor of the mise tool manager. cc-deck is not switched to sandcast as part of this feature.

## Clarifications

### Session 2026-10-08

- Q: Which platform (operating system and architecture) does a build target? → A: One platform per build. A command-line flag on plan and build overrides an optional manifest field, which overrides the default of the host's platform. The lockfile must contain an entry for the selected platform.
- Q: Which generated files does a project commit so CI rebuilds without an agent, and where do the others live? → A: Two groups under one project-local sandcast directory. Committed inputs: manifest, lockfile, stored fragments, promoted recipes, learnings file. Disposable outputs in a git-ignored build directory below it: build plan, image definition, build log, adapted profile set. The probe cache lives in the user's cache directory, shared across projects.
- Q: Does an adapted profile keep the upstream profile id or get an id per image? → A: Keep the upstream id by default for workspace scope. For platform scope, or when the user asks, suffix the id with an image-derived label so several images coexist. The adapted profile set records the mapping from upstream id to adapted id.
- Q: Does verify need a gateway, what is its exit status on failure, and does build run it? → A: Verify runs under the container engine only. It reports every component's result, then exits non-zero if any check failed. Build does not run verify by itself; a flag on build runs both. Only the profile import step and the sandbox start need a gateway.
- Q: Does sandcast call the upstream OpenShell CLI for profile lint and import, or implement them itself? → A: Lint is implemented in the sandcast binary, mirroring the rules of the pinned release and tested against the vendored upstream examples. Import shells out to the upstream `openshell` CLI, which is required on the host only for the import step.

## Terminology

- **Component**: the unit of composition. A declarative definition of one tool, language or agent: how to detect that a project needs it, how to install it, how to check it, what it needs in the image, and which profiles it requires.
- **Component tiers**: the three places component definitions come from, in precedence order: shipped with the binary, the remote component registry, user or project-local files.
- **Host tools**: tools found installed on the developer's machine during capture, proposed as components.
- **Profile**: an upstream OpenShell provider profile. Three profile tiers: pinned upstream examples, the sandcast overlay, user or project-local files.
- **Probe**: a run inside a base image that records image facts (operating system, package manager, default user, shells) and per-component checks (present, missing, incompatible version).
- **Probe delta**: the per-component table of present, missing and incompatible results for one base image and one component set.
- **Gateway scope**: where a profile lives in an OpenShell gateway: `workspace` (the current workspace) or `platform` (global, visible to all workspaces).
- **Resolved path**: the absolute path of a component's executable inside the built image, found by inspecting the image after the build.
- **Target platform**: the operating system and architecture pair the image is built for (for example `linux/amd64`). One platform per build.
- **Sandcast directory**: the one project-local directory that holds the committed inputs: the manifest, the tool lockfile, stored fragments, promoted recipes and the learnings file.
- **Build directory**: a git-ignored directory below the sandcast directory that holds disposable outputs: the build plan, the image definition, the build log and the adapted profile set.
- **Probe cache**: the per-user cache directory holding probe results keyed by image digest, shared across projects.
- **Image label**: a short identifier derived from the manifest name or the image tag, used to suffix adapted profile ids when several images must coexist in one gateway namespace.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Build a sandbox image from a manifest (Priority: P1)

A developer has a manifest that names a base image, a set of components (languages, tools, an agent harness) and the OpenShell target. They run the build and receive an image that contains the base image plus every component the base image did not already provide, a set of OpenShell provider profiles whose binary paths match that image, and a verification report that each component works. No interactive agent is involved in this flow.

**Why this priority**: This is the deterministic core. Everything else (capture, repair, harness adapters) feeds into or wraps this flow. It is also the CI path: a manifest checked into a repository must rebuild the same image without a human or an LLM in the loop.

**Independent Test**: Hand-write a manifest for a small project (one language, two shell tools, one harness) against the registry's default OpenShell base image. Run plan, build and verify from the command line; these need only the container engine, and every component's probe check passes. Then import the adapted profiles into a gateway and start the image under OpenShell; this part requires a reachable OpenShell gateway.

**Acceptance Scenarios**:

1. **Given** a manifest naming a base image and components, **When** the user runs the plan step, **Then** the tool probes the base image, reports the probe delta, and writes a build plan the user can inspect.
2. **Given** a build plan, **When** the user runs the build step, **Then** an image is produced in which no component the probe marked present was installed again, every component marked missing or incompatible is installed, and a tool lockfile pins exact versions and checksums.
3. **Given** a built image, **When** the user runs the verify step, **Then** every component's probe check is executed inside the image under the container engine, a pass or fail is reported per component, and the step exits non-zero if any check failed.
4. **Given** the same manifest, the same lockfile, the same base image digest and the same target platform, **When** the user runs plan twice, **Then** the two build plans are identical.
5. **Given** a base image already probed for the same component set, **When** the user runs plan again and the image digest has not changed, **Then** the cached probe result is used and no container is started for probing.
6. **Given** a built image and the components' profile references, **When** the build finishes, **Then** sandcast writes one adapted profile per referenced profile with the `binaries` list rewritten to the resolved paths, and reports any profile whose binaries could not be resolved.
7. **Given** an adapted profile set and a reachable gateway, **When** the user runs the profile import step with a gateway scope, **Then** each profile passes the gateway's lint and is imported at that scope, and existing profiles with the same id are reported instead of silently overwritten.
9. **Given** an adapted profile set, **When** the user imports it at `workspace` scope, **Then** each profile keeps its upstream id; **When** the user imports it at `platform` scope or asks for suffixed ids, **Then** each id carries the image label as a suffix and the adapted profile set records the mapping from upstream id to adapted id.
8. **Given** a build fails and no skill is present, **When** the non-interactive build step ends, **Then** it exits with a non-zero status, leaves the image definition and the build log in the build directory, and does not retry.

---

### User Story 2 - Capture a project into a manifest (Priority: P2)

A developer opens their project with an agent harness that has the sandcast skills installed and asks for a capture. The capture examines the project (languages, build files, services), examines the host for companion tools and shell configuration, asks which agent harnesses to include, and proposes a component list. The developer confirms or edits the proposals and ends up with a manifest ready for User Story 1.

**Why this priority**: Capture is the part that makes the image mirror the developer's own environment. Without it the user writes manifests by hand, which works but loses the central idea of the tool.

**Independent Test**: Run the capture skill on the reference fixture project (a Go module plus a Python project with a zsh configuration that uses starship, fzf and lsd), on a host with those tools installed. The resulting manifest lists both languages, the detected host tools, the chosen harness, and a curated shell configuration. Running User Story 1 on that manifest succeeds.

**Acceptance Scenarios**:

1. **Given** a project directory with recognizable ecosystem files, **When** the capture runs, **Then** it proposes one component per detected language or service and explains what evidence triggered each proposal.
2. **Given** host tools that match component definitions, **When** the capture runs, **Then** detected tools are proposed as components and marked as detected, and undetected components from the component tiers are offered as optional additions.
3. **Given** a host shell configuration, **When** the capture runs, **Then** it produces a curated shell configuration for the image, strips host-specific plugin managers and paths, and adds any component that the curated configuration references (such as prompt or listing tools).
4. **Given** the capture starts, **When** it asks which harnesses to include, **Then** only the selected harnesses' adapters run their discovery, and unselected harnesses leave no trace in the manifest.
5. **Given** the capture finishes, **When** the manifest is written, **Then** the manifest contains no credential values, and any discovered configuration that embeds a secret is written with the secret replaced by a placeholder and reported to the user.

---

### User Story 3 - Repair a failed build and promote the fix (Priority: P3)

A developer builds an image whose manifest includes a tool that neither the tool manager nor any recipe can install. The plan lists the component as unresolved. The build skill writes the install layer itself, the build fails on a wrong archive layout, the skill fixes the layer, the build succeeds. The skill offers to promote the working layer into a project-local recipe. The next build of that project resolves the component deterministically from the recipe and no longer needs the skill.

**Why this priority**: This preserves the flexibility of the current LLM-driven build for tools nobody has described yet, while converting each success into a deterministic recipe. It is what keeps User Story 1 from being limited to known tools.

**Independent Test**: Add a component with no installer to a manifest. Run the build skill. Observe the self-written layer, a forced failure, the repair, the success, and the promotion. Run the non-interactive build from User Story 1 and confirm the promoted recipe is used.

**Acceptance Scenarios**:

1. **Given** a build plan with an unresolved component, **When** the build skill assembles the image definition, **Then** it writes a layer for the unresolved component and stores that fragment in the sandcast directory.
2. **Given** a build fails, **When** the skill repairs the image definition, **Then** the fix is recorded in a structured learnings file with the component, the failure, and the change, and the build is retried up to the configured limit (default 3).
3. **Given** a build succeeded with a self-written layer, **When** the user confirms promotion, **Then** a project-local component recipe is created and the stored fragment is marked as superseded.
4. **Given** stored fragments exist for unresolved components, **When** the non-interactive build runs, **Then** it reuses the stored fragments and does not require a skill or an agent.

---

### User Story 4 - Bake a harness with its local extensions (Priority: P4)

A developer uses two agent harnesses and wants both available inside the sandbox, each with the MCP servers, plugins and skills they use locally. During capture they select both. The resulting image has both agents installed, their configuration scaffolded, and the selected extensions pre-installed. The adapted profile set includes each agent's upstream profile with the agent's resolved path.

**Why this priority**: Harness independence is a stated goal, but it builds on capture and build. One harness adapter is enough to prove the shape; additional adapters are incremental.

**Independent Test**: Capture with two harnesses selected. Build. Start the sandbox with the agents' providers attached and run each agent's version command and list its installed extensions. Requires a reachable gateway and credentials for the agents' inference providers.

**Acceptance Scenarios**:

1. **Given** a harness is selected, **When** its adapter runs discovery, **Then** the user sees the locally configured MCP servers, plugins and skills for that harness and chooses which to bake in.
2. **Given** a harness is in the manifest, **When** the build runs, **Then** the agent binary is installed, verified by a post-install check, and its configuration scaffolded so it runs non-interactively inside the sandbox.
3. **Given** a harness is in the manifest, **When** the adapted profile set is written, **Then** the harness's upstream profile is included with the agent's resolved path, and each MCP server whose endpoint is not covered by any referenced profile produces an additional project-local profile.
4. **Given** the sandcast binary is installed on a host with a supported harness, **When** the user runs the skill installation command, **Then** the sandcast skills are installed where that harness discovers them and can be invoked under the sandcast name.

---

### User Story 5 - Build a plain container image (Priority: P5)

A developer wants the same project image without OpenShell conventions, for running directly under a container engine. They set the target to container. The build produces an image without the sandbox user and policy conventions but with the same components, plus an allowed-domains list derived from the same profiles.

**Why this priority**: A thin variant of User Story 1 that broadens the audience. It must not complicate the OpenShell path.

**Independent Test**: Build the same manifest with both targets. The container-target image runs the component probe checks under the container engine without OpenShell.

**Acceptance Scenarios**:

1. **Given** a manifest with the container target, **When** the build runs, **Then** no OpenShell policy file or sandbox user convention is applied and verify passes under the container engine.
2. **Given** a manifest with both targets, **When** the user builds each, **Then** verify passes for the same component set in both images.
3. **Given** a manifest with the container target, **When** the build finishes, **Then** an allowed-domains list is written that contains every endpoint host of the referenced profiles.

---

### Edge Cases

- Base image cannot be pulled or probed: plan fails with the registry entry name, the reference, and the engine's error. No partial plan is written.
- Base image is not in the registry: the manifest may name any image reference; the plan proceeds with a notice that no registry notes apply.
- Base image runs as a non-root user: system packages are still installed, and the image's final user is unchanged. Tool manager installs go into that user's home.
- Base image has no supported package manager: components that need system packages are reported as unresolvable in the plan; tool manager installs still proceed because they do not depend on the package manager.
- Component present in the base image at an incompatible version: the plan marks it incompatible. A tool-manager-installed component is installed at the required version on a path that shadows the base image's binary. A recipe-installed component is installed the same way if its recipe supports it; otherwise the plan reports it as unresolvable with the version conflict named.
- Two components provide the same binary: the plan reports a conflict and refuses to render until the manifest removes one.
- A referenced profile has no binary that resolves in the built image: the adapted profile is written with an empty binaries list and flagged, because an unflagged inert profile would silently deny traffic.
- A referenced profile id exists in more than one profile tier: the later tier wins and the plan reports which file was used.
- A component set changes after a probe: image facts are reused from the digest cache; only the new or changed components are checked in a partial probe.
- Manifest schema version newer than the binary supports: the binary refuses with a message naming both versions.
- Skill bundle and binary versions mismatch: the skill detects the mismatch at start and tells the user which to update.
- Stored fragment exists and a promoted recipe now covers the same component: the plan uses the recipe and reports the fragment as stale.
- Tool manager lockfile missing an entry for the selected target platform: plan fails for that component with the platform named, so the user can extend the lockfile on the host.
- Target platform given on the command line differs from the manifest field: the command-line value wins and the plan records which source selected the platform.
- Host is offline: cached probe results are used; lockfile resolution and release checks fail per component with a clear reason.
- Remote component registry content fails its checksum: the file is rejected, the cached previous version is kept if present, and the plan reports the rejection.
- Capture runs on a project with no recognizable ecosystem files: it reports nothing detected and still offers components from the tiers and host tools.
- Harness adapter discovery finds an MCP server definition with an embedded secret: the secret is replaced by a placeholder, the user is told how to supply it as a provider credential at runtime.
- Retry limit reached in the build skill: the build stops, the learnings file holds every attempt, and the last image definition is left in place for inspection.
- The upstream `openshell` CLI is missing or at a different release than the pin when the import step runs: the step fails before contacting the gateway, naming the required release. Plan, build, verify and profile export are unaffected.
- Gateway rejects an adapted profile on import because the id already exists: sandcast reports the conflict and offers the update path, suffixed ids, or a different gateway scope; it never deletes profiles.

## Requirements *(mandatory)*

### Functional Requirements

**Repository and packaging**

- **FR-001**: sandcast MUST live in its own repository with no dependency on cc-deck code or on cc-deck runtime concepts (workspaces, sessions, the cc-deck tool stack).
- **FR-002**: sandcast MUST ship as a command-line binary plus a skill bundle. The binary MUST be able to run plan, build, verify and profile export from a manifest without any skill or agent.
- **FR-003**: The binary MUST be able to install its skill bundle into a supported harness on the host. The first release MUST support Claude Code.
- **FR-004**: The skill bundle and the binary MUST carry compatible version markers, and each MUST detect and report a mismatch.
- **FR-005**: sandcast MUST use podman as the default container engine. docker MUST be usable when selected explicitly in the manifest or on the command line, and MUST be selected automatically with a printed notice when podman is absent. No engine-specific feature may be required.
- **FR-006**: The sandcast repository MUST ship, with this feature, a README covering installation and the capture and build flow, a command reference for every subcommand and flag, and a configuration reference for the manifest, the base image registry, the component definition format and the profile tiers.

**Manifest**

- **FR-007**: The manifest MUST be a single project-local file with one documented name, a schema version, a base image selection, a component list, a harness list, shell configuration, an optional engine selection, an optional target platform, and a target section supporting `openshell` and `container`. The schema is defined fresh; no reader for cc-deck's `build.yaml` is provided.
- **FR-008**: The manifest MUST NOT contain credential values. Discovered configuration that embeds a secret MUST be stored with a placeholder.
- **FR-009**: The binary MUST refuse a manifest whose schema version it does not support, naming both versions.

**Base images**

- **FR-010**: sandcast MUST read a base image registry listing allowed base images with a name, a reference, a default flag and free-text notes, with one entry per target type marked as default. A manifest MAY name an image reference outside the registry; the registry then contributes nothing but a notice.
- **FR-011**: sandcast MUST probe a base image before planning, recording image facts (operating system, package manager, default user, available shells) and, for each component in the manifest, whether it is present, missing or at an incompatible version.
- **FR-012**: Image facts MUST be cached by image digest. Per-component results MUST be cached by image digest, component key and the component's probe check, so that adding or changing a component triggers a partial probe for that component only. The cache lives in the user's cache directory and is shared across projects. The user MUST be able to force a full re-probe.

**Components and installation**

- **FR-013**: A component MUST be a declarative definition carrying: a key; a display name; match conditions, meaning the project file patterns, host tool names and harness names whose presence makes capture propose the component; an installer; a probe check (a command and the expected outcome); image needs, meaning filesystem policy entries (read-only or read-write paths), environment variables to set, and directories to create with their owner; and a list of OpenShell profile ids it requires. A component MUST NOT carry network endpoints itself.
- **FR-014**: The primary installer MUST be the mise tool manager: a component names a mise tool and a version range. sandcast MUST generate the tool manager configuration and a lockfile with exact versions and per-platform checksums, and builds MUST install in locked mode.
- **FR-015**: A component MAY instead carry a recipe (system package, or a scripted install) for tools the tool manager cannot install. System packages MUST be installed through the package manager found by the probe.
- **FR-016**: Components MUST be resolved from three tiers with documented precedence: shipped with the binary, the remote component registry cached locally, and user or project-local definitions. A later tier overrides an earlier one with the same key.
- **FR-017**: The remote component registry MUST be a configurable source pinned to a release by default. It MUST be refreshed only on an explicit command, never implicitly during a build. Its index MUST carry a checksum per file, and sandcast MUST reject any file whose checksum does not match the index.
- **FR-018**: A working self-written layer MUST be promotable into a project-local component recipe through a user-confirmed step.

**Profiles**

- **FR-019**: sandcast MUST use the upstream OpenShell provider profile schema as its only network endpoint format and MUST NOT define a sandcast endpoint schema.
- **FR-020**: sandcast MUST carry the upstream example profiles from a pinned OpenShell release, plus an overlay of additional profiles in the same schema. The overlay MUST guarantee coverage for at least npm, crates.io, the Go module proxy and GitLab, and overlay profiles MUST be written so they can be contributed upstream unchanged.
- **FR-021**: Profiles MUST be resolved from three tiers with documented precedence: the pinned upstream set, the sandcast overlay, and user or project-local profiles. A later tier overrides an earlier one with the same id.
- **FR-022**: After a build, sandcast MUST write an adapted profile set: one copy of every profile referenced by the included components and harnesses, with `binaries` rewritten to the resolved paths in the built image. Profiles with no resolvable binary MUST be flagged.
- **FR-022a**: An adapted profile MUST keep the upstream profile id by default. When imported at `platform` scope, or when the user asks for suffixed ids, the id MUST be suffixed with the image label. The adapted profile set MUST record the mapping from each upstream id to its adapted id so that the runtime that starts the sandbox can find the right provider.
- **FR-023**: sandcast MUST validate every adapted profile against the upstream lint rules before writing it. The lint MUST be implemented in the sandcast binary, mirror the rules of the pinned OpenShell release, and be tested against the vendored upstream example profiles, so that plan, build and profile export need no OpenShell tooling on the host. sandcast MUST provide an import step that imports the set into a gateway at a user-chosen gateway scope (`workspace` or `platform`) without deleting or overwriting existing profiles unless asked. The import step delegates to the upstream `openshell` CLI, which is required on the host only for that step; when it is absent, the import step MUST fail with a message naming the required release.
- **FR-024**: The adapted profile set MUST be discoverable from the built image, so that a runtime can learn which providers a sandbox from this image needs without access to the build directory.

**Plan and build**

- **FR-025**: The plan step MUST produce a machine-readable build plan containing the probe delta, the tool manager configuration and lockfile, rendered fragments for every resolved component, the list of referenced profiles, and the list of unresolved components.
- **FR-026**: The plan MUST be deterministic: the same manifest, the same lockfile, the same component and profile definitions, the same base image digest and the same target platform MUST yield an identical plan.
- **FR-026a**: Each build MUST target exactly one platform (operating system and architecture). The platform is selected, in precedence order, by a command-line flag on the plan and build steps, then by the manifest's optional target platform field, then by the host's platform. The plan MUST record the selected platform and the source that selected it, the base image MUST be pulled and probed for that platform, and the lockfile MUST contain an entry for it.
- **FR-027**: The build step MUST assemble an image definition from the plan's fragments and any stored fragments for unresolved components, and MUST build it with the container engine. A failed non-interactive build MUST exit with a non-zero status, leave the image definition and the build log in the build directory, and MUST NOT retry.
- **FR-028**: Fragments written by the build skill for unresolved components MUST be stored in the sandcast directory so that a non-interactive build can reuse them.
- **FR-028a**: All sandcast files in a project MUST live under one documented project-local sandcast directory, split into two groups. Committed inputs at its top level: the manifest, the tool lockfile, stored fragments, promoted recipes and the learnings file. Disposable outputs in a build directory below it that sandcast marks as git-ignored: the build plan, the image definition, the build log and the adapted profile set. A checkout containing only the committed inputs MUST be sufficient for a non-interactive plan, build and verify.
- **FR-029**: The build skill MUST retry a failed build up to a configurable limit with a default of 3, recording each failure and fix in a structured learnings file.
- **FR-030**: The verify step MUST run every included component's probe check inside the built image under the container engine, without a gateway. It MUST report every component's result before exiting and MUST exit non-zero if any check failed. The build step MUST NOT run verify by itself; a flag on build MUST run build and verify in sequence. Only the profile import step requires a reachable gateway.
- **FR-031**: Tools installed by the tool manager MUST be reachable by any process in the image without shell activation, so that an agent's child processes find them.

**OpenShell target**

- **FR-032**: For the OpenShell target, the image MUST follow the sandbox conventions: the sandbox user as the final user, the working directory the supervisor expects, and the policy file at the path the supervisor reads.
- **FR-033**: The image-embedded policy MUST contain the static sections (filesystem, landlock, process) derived from the manifest and the included components' image needs. Its network section MUST contain only the entries present in the pinned OpenShell release's default policy, and nothing else. Tool and provider endpoints MUST NOT be baked into the image policy; they come from profiles at runtime.

**Capture**

- **FR-034**: The capture skill MUST examine the project directory for ecosystem evidence (languages, build files, services) and propose components with the evidence shown.
- **FR-035**: The capture skill MUST examine the host for host tools and shell configuration, and MUST produce a curated shell configuration for the image that strips host-specific elements and adds components the curated configuration references.
- **FR-036**: The capture skill MUST ask upfront which harnesses to include and MUST run discovery only for the selected harnesses.
- **FR-037**: The capture skill MUST write the manifest and MUST NOT write any image definition itself.

**Harness adapters**

- **FR-038**: A harness adapter MUST provide: discovery of local MCP servers, plugins and skills; the agent's component definition (installer, probe check, profile ids); configuration scaffolding for non-interactive use inside the image; and the location where the sandcast skill bundle is installed for that harness.
- **FR-039**: An MCP server whose endpoint is not covered by any referenced profile MUST produce a project-local profile in the upstream schema, bound to the agent's resolved path, with any credential expressed as a profile credential declaration rather than a value.

**Container target**

- **FR-040**: For the container target, the build MUST apply the same components without OpenShell conventions, verify MUST run under the container engine, and an allowed-domains list MUST be derived from the referenced profiles' endpoint hosts.

### Key Entities

- **Manifest**: The project-local descriptor. Schema version, base image selection, components, harnesses, curated shell configuration, engine selection, optional target platform, targets. Produced by capture or by hand, consumed by plan.
- **Base image registry entry**: Name, reference, default flag, notes. Scoped per target type.
- **Probe result**: Image facts for one base image digest plus per-component checks keyed by digest, component key and probe check.
- **Component**: Key, name, match conditions, installer (tool manager entry or recipe), probe check, image needs, required profile ids. Lives in one of three component tiers.
- **Recipe**: An installer for a component the tool manager cannot handle: system package or scripted install. A promoted recipe is a project-local component created from a working fragment.
- **Component registry index**: The remote tier's list of component files with a checksum per file, pinned to a release.
- **Tool lockfile**: Exact versions, per-platform artifact URLs and checksums for every tool-manager-installed component. Generated on the host, consumed in the build.
- **Profile**: An upstream OpenShell provider profile: id, category, optional credentials, endpoints, binaries. Lives in one of three profile tiers.
- **Adapted profile set**: The profiles referenced by one build, with binaries rewritten to resolved paths and a mapping from upstream id to adapted id. Written to the build directory, discoverable from the image, importable into a gateway.
- **Build plan**: Machine-readable output of plan, written to the build directory. Target platform and its selection source, probe delta, tool manager config and lockfile, rendered fragments, referenced profiles, unresolved components.
- **Fragment**: One rendered or self-written piece of the image definition for one component. Stored fragments live in the sandcast directory and are committed.
- **Learnings entry**: Structured record of one build failure and its fix: component, failure, change, attempt number. The learnings file lives in the sandcast directory and is committed.
- **Harness adapter**: Per-harness provider of discovery, the agent component, scaffolding, and skill install location.
- **Image policy**: The static OpenShell sandbox policy embedded in the image: filesystem, landlock, process, and the default network entries.
- **Target**: The output convention, `openshell` or `container`.

## Delivery Phases

The feature is large. Planning MUST split it into three independently shippable phases, each with its own tasks:

1. **Core build**: User Story 1 with the OpenShell target. Manifest, base image registry, probe and cache, component tiers with mise and recipes, profile tiers and adapted profile sets, plan, build, verify, profile import, documentation. Requirements FR-001 to FR-033, including FR-022a, FR-026a and FR-028a.
2. **Capture and first harness**: User Stories 2 and 4 with the Claude Code adapter. Skill bundle and its installer. Requirements FR-034 to FR-039.
3. **Repair, promotion and container target**: User Stories 3 and 5. Requirements FR-018, FR-028, FR-029 and FR-040, plus the related edge cases.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A manifest with only components from the shipped and registry tiers builds and verifies non-interactively, with no agent involved, on the first attempt.
- **SC-002**: Running plan twice on the same manifest, lockfile, base image digest and target platform yields byte-identical plans.
- **SC-003**: A capture on the reference fixture project (Go module, Python project, zsh configuration using starship, fzf and lsd) produces a manifest that builds successfully with at most one repair iteration. The baseline is the 8 to 9 iterations recorded for the current cc-deck build skill in brainstorm 072.
- **SC-004**: A second plan on an already probed base image with an unchanged component set completes without starting a probe container.
- **SC-005**: A promoted recipe removes the need for the build skill on the next build of the same project: the non-interactive build succeeds.
- **SC-006**: Every adapted profile produced for the registry's default OpenShell base image passes the upstream lint and imports into a gateway without edits.
- **SC-007**: A sandbox started from a built image with its adapted profiles attached can install a package for each included language and reach the included agent's inference endpoint, with no `credential_endpoint_mismatch` and no denied request for the included tools.
- **SC-008**: A user who installs the binary and runs the skill installation command can invoke the capture skill in their harness without any manual file copying.
- **SC-009**: No credential value appears in the manifest, the plan, the stored fragments, the adapted profiles, or the built image for any capture that encountered a secret.
- **SC-010**: Every overlay profile is accepted by the upstream lint unchanged, so it can be contributed upstream as is.

## Assumptions

- The implementation lives in a new `sandcast` repository. This specification lives in cc-deck because the idea originated here; the plan and tasks phases will target the new repository.
- cc-deck keeps its embedded build package and its policy catalog. Whether cc-deck later adopts sandcast is a separate decision and not part of this feature.
- Podman is the default engine; docker is accepted through the same command surface. No engine-specific features are used.
- Upstream OpenShell is pinned to release v0.1.2 for the vendored example profiles, the lint rules and the default policy. The `openshell` CLI at that release is a host requirement for the profile import step only. In this line, profiles are import-only, credential-free profiles are valid, profile binaries are static paths, and a sandbox gets profile-derived network policy only through attached provider instances. sandcast does not support the pre-0.1 built-in profile model. Refreshing the pin is a deliberate maintenance step.
- Whatever starts the sandbox (cc-deck, a script, a human) is responsible for creating provider instances from the adapted profiles, attaching them, and supplying credentials. sandcast never handles credential values.
- The tool manager is mise, pinned to a release that supports per-platform lockfile checksums and locked installs; the exact release is fixed in the plan. It installs tools independent of the base image's package manager; the base system (shell, git, TLS roots, compilers) comes from the base image or from system package recipes.
- The harness adapter interface is designed so that Codex, OpenCode and pi adapters can be added without changing the core; this is a design constraint for the plan, not a tested requirement of this feature.
- Where the adapted profile set is recorded (image metadata, a sidecar file, or both) is decided in the plan; the requirement is only that a runtime can discover it from the image.
- The manifest schema is defined fresh. Conversion of cc-deck `build.yaml` manifests is out of scope; it may become a converter if cc-deck adopts sandcast.
- Skills are authored once in a portable format and adapted per harness by the adapter at install time. The exact format per harness is a planning concern.
- Pre-built layer stack images are out of scope. A published image can be used as a base image and will be probed like any other.
- SSH and Ansible provisioning targets are out of scope.
- Test environment: plan, build and verify need only the container engine. The profile import step of User Story 1, User Story 4, SC-006 and SC-007 need a reachable OpenShell gateway; SC-007 additionally needs credentials for the included agent's inference provider. The reference fixture project for SC-003 lives in the sandcast repository.
