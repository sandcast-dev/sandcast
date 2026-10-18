# Feature Specification: Runtime home model and shell initialization

**Feature Branch**: `003-runtime-home-shell-init`

**Created**: 2026-10-14

**Status**: Draft

**Input**: Brainstorm `brainstorm/03-runtime-home-shell-init.md` (runtime home model and shell initialization under the OpenShell home overmount).

## Clarifications

### Session 2026-10-14 (resolved autonomously under ship pipeline, smart oversight)

- Q: Is the runtime seeding escape hatch implemented in this feature? -> A: No. Environment-variable relocation is the only mechanism built. Runtime seeding is documented as the sanctioned future fallback for tools that hardcode a home path, but is not implemented here (FR-013 tightened accordingly).
- Q: Which interactive shells must work under the overmount? -> A: zsh and bash. Any other login shell gets a minimal safe default and a capture notice (FR-011).
- Q: Does this feature configure how or where OpenShell mounts the workspace? -> A: No. The overmount path is a property of the base image that sandcast already knows; the feature adapts to it and never configures the runtime.
- Q: How is the dotfile allowlist bounded? -> A: A curated, bounded allowlist (the shell config is already handled; common additions include git, the prompt tool, editor basics, and tmux). Its exact membership is finalized in planning; explicit manifest paths cover anything the allowlist omits.
- Q: Is credential persistence across sandbox recreations in scope? -> A: No. Explicitly deferred to a separate feature.

## User Scenarios & Testing *(mandatory)*

In an OpenShell sandbox the final user's home is `/sandbox`, and the runtime mounts the workspace volume over that path. The home is therefore shadowed (image-baked content under it is hidden), writable, and tied to the workspace rather than to the image. These stories make the captured environment behave correctly under that overmount.

The actor throughout is the person who captures a project and runs the resulting sandbox image ("the user"). The system is sandcast (capture, plan, build) and the image it produces.

### User Story 1 - Curated shell works under the overmount (Priority: P1)

The user starts an interactive shell in the built sandbox and finds their curated shell environment active (prompt, aliases, completions, environment variables), even though the workspace volume is mounted over the home directory.

**Why this priority**: This is the core promise of the feature and the most visible failure today. If the shell does not initialize correctly under the overmount, the sandbox feels broken regardless of what else works. It is independently valuable on its own.

**Independent Test**: Build an image for a project whose capture curated a shell configuration, start a sandbox with a workspace mounted over the home, open an interactive shell, and confirm the curated prompt, aliases, completions, and environment variables are active.

**Acceptance Scenarios**:

1. **Given** a built image whose capture curated a zsh configuration, **When** the user opens an interactive zsh shell in a sandbox with the workspace mounted over the home, **Then** the curated prompt, aliases, and completions are active.
2. **Given** a built image whose capture curated a bash configuration, **When** the user opens an interactive bash shell in the same conditions, **Then** the curated configuration is active.
3. **Given** the same built image, **When** the user rebuilds it from the same inputs, **Then** the shell initialization behaves identically (deterministic).

### User Story 2 - The writable workspace home is never executed (Priority: P1)

A startup file that happens to live in the mounted workspace home (for example a `.zshrc`, `.zshenv`, `.zprofile`, or `.bashrc` committed into the project) is never auto-sourced by the sandbox's shell initialization, so an untrusted project cannot run code simply by being opened in a sandbox.

**Why this priority**: The sandbox commonly runs an agent against a project that may be untrusted. Because the home is the writable workspace, any rc file there that the shell auto-sources is a code-execution vector. Closing it is a security requirement and is tightly coupled to getting shell initialization right.

**Independent Test**: Place a marker-writing rc file (for example a `.zshrc` that creates a sentinel file) in the workspace, start a sandbox, open an interactive shell, and confirm the sentinel was not created and the marker command did not run.

**Acceptance Scenarios**:

1. **Given** a workspace that contains a `.zshrc` which would run a command on shell start, **When** the user opens an interactive zsh shell in the sandbox, **Then** that command does not run and the baked configuration is what loads.
2. **Given** a workspace that contains a `.bashrc` which would run a command on shell start, **When** the user opens an interactive bash shell, **Then** that command does not run.
3. **Given** a user who wants their project's own shell configuration applied, **When** they capture it into the image explicitly, **Then** it is applied from the baked location rather than by sourcing the live workspace.

### User Story 3 - Dotfiles beyond the shell survive the overmount (Priority: P2)

The user's wider dotfiles (for example git configuration, a prompt tool's configuration, an editor's basics) that capture proposes from the host home, or that the user lists explicitly in the manifest, are present and used by their tools inside the running sandbox.

**Why this priority**: Extends the environment fidelity beyond the shell. Valuable, but the shell stories are what make or break the first release, so this follows them.

**Independent Test**: Capture a project on a host that has a `.gitconfig` (and one other supported dotfile), build the image, start a sandbox, and confirm the tools read the captured configuration (for example `git config --get` returns the captured value).

**Acceptance Scenarios**:

1. **Given** a host home with a supported dotfile (for example `.gitconfig`) that the user confirms during capture, **When** the image is built and a sandbox started, **Then** the corresponding tool uses that configuration under the overmount.
2. **Given** a dotfile path the user lists explicitly in the manifest, **When** the image is built, **Then** that dotfile is baked and active in the sandbox.
3. **Given** a captured dotfile that contains a value resembling a credential, **When** capture processes it, **Then** the credential is redacted and never written into the image.

### User Story 4 - Nothing vanishes under the overmounted home (Priority: P3)

Nothing sandcast bakes lands under the overmounted home, and a base-image tool that exists only under that home is recognized as unavailable at runtime and re-provisioned to a persistent location, with sandcast reporting what it relocated or re-provisioned.

**Why this priority**: Prevents the subtler failure where a tool or file passes the build but disappears at runtime. Important for correctness, but less immediately visible than the shell stories.

**Independent Test**: Probe a base image in which a required tool exists only under the home path; confirm the plan treats it as missing and schedules it for a persistent location, and that the plan reports the decision.

**Acceptance Scenarios**:

1. **Given** a base image whose only copy of a required tool lives under the overmounted home, **When** sandcast plans the build, **Then** the tool is treated as not present and provisioned to a persistent location, and the plan reports this.
2. **Given** any baked configuration or dotfile, **When** sandcast builds the image, **Then** none of it resolves under the overmounted home.
3. **Given** sandcast relocates a path or re-provisions a component because of the overmount, **When** the user reads the plan or build output, **Then** a notice names what was relocated or re-provisioned.

### Edge Cases

- A user has neither zsh nor bash as their login shell: capture records the shell it cannot curate and the sandbox still starts with a minimal, safe default rather than failing.
- The workspace home is read-only at runtime: the baked configuration still loads (it does not depend on writing into the home).
- A captured dotfile is a symlink or points outside the project: it is handled under the same credential-safety and copy rules capture already applies to skills, or is skipped with a notice.
- A dotfile the user lists explicitly does not exist on the host at capture time: capture reports it and continues rather than failing the whole capture.
- Two sources define the same dotfile (host allowlist and an explicit manifest path): the resolution is deterministic and the result is reported.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The sandbox's interactive shell MUST load the curated, image-baked configuration regardless of the workspace volume being mounted over the home directory.
- **FR-002**: The baked shell initialization MUST NOT auto-source shell startup files that live in the mounted workspace home.
- **FR-003**: The baked environment MUST be defined by image content outside the home directory, so it is independent of whatever the mounted workspace contains.
- **FR-004**: Shell initialization MUST behave identically across rebuilds from the same inputs (deterministic), consistent with the project's determinism principle.
- **FR-005**: Capture MUST propose, from a curated allowlist, the user's dotfiles found in the host home and let the user confirm which to bake, reusing the existing confirmation flow.
- **FR-006**: The manifest MUST accept explicit dotfile paths to bake, in addition to the host-proposed allowlist.
- **FR-007**: Every baked dotfile MUST pass the existing credential-redaction rules so that no credential value enters the image.
- **FR-008**: sandcast MUST ensure that nothing it bakes (shell configuration, dotfiles, tools) resolves under the overmounted home path.
- **FR-009**: sandcast MUST treat a base-image tool that exists only under the overmounted home as not present at runtime, and provision it to a persistent location instead.
- **FR-010**: sandcast MUST report, in the plan or build output, each path it relocated or component it re-provisioned because of the overmount.
- **FR-011**: The feature MUST support zsh and bash. For any other login shell, capture MUST record that it could not curate the shell and the image MUST still start with a minimal safe default.
- **FR-012**: A user MUST be able to apply their own project shell configuration only by capturing it explicitly into the image, never by having the sandbox source the live workspace.
- **FR-013**: The primary path MUST NOT depend on seeding configuration into the live home. Runtime seeding is NOT implemented in this feature; it is documented as the sanctioned future escape hatch for tools that hardcode a home path.
- **FR-014**: The feature MUST ship with tests and documentation, consistent with the project constitution.

### Key Entities *(include if feature involves data)*

- **Curated shell configuration**: the image-baked shell setup (prompt, aliases, completions, environment) that loads outside the home. Already produced by capture for the shell; this feature guarantees it survives the overmount.
- **Dotfile selection**: the set of non-shell home dotfiles to bake, formed from a host-proposed allowlist (confirmed) plus explicit manifest paths, each subject to redaction.
- **Relocation record / notice**: the report of any path relocated or component re-provisioned because it would otherwise land under the overmounted home.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a sandbox whose workspace is mounted over the home, an interactive shell shows the curated prompt, aliases, completions, and environment in 100% of builds produced by this feature.
- **SC-002**: A startup file placed in the workspace home is executed zero times during shell initialization.
- **SC-003**: Every dotfile the user selects (allowlist-confirmed or manifest-listed) is readable by its tool inside the running sandbox.
- **SC-004**: No file sandcast manages resolves under the overmounted home across the supported base images; any case that would is corrected automatically and reported.
- **SC-005**: Rebuilding the same inputs yields identical shell-initialization and dotfile-placement configuration (byte-identical where the project already asserts determinism).
- **SC-006**: A base-image tool present only under the overmounted home is reported and re-provisioned, so it is available in the running sandbox in 100% of such cases.

## Assumptions

- The chosen mechanism is environment-variable relocation (configuration baked outside the home and pointed at by environment, as the existing shell-config curation already demonstrates), with runtime seeding only as a documented escape hatch. Approach details are left to planning; relocating the home directory itself was rejected during brainstorming.
- Supported interactive shells are zsh and bash, matching the shells capture curates today. Other shells are out of scope beyond a minimal safe default.
- Persisting runtime state or credentials (for example an agent's OAuth token) across sandbox recreations is out of scope for this feature and deferred.
- The feature builds on the existing capture, redaction, probe, plan, and build packages rather than introducing a new subsystem.
- OpenShell mounts the workspace over the home at a known path (`/sandbox` on the current base images); the exact path is a property of the base image and is already known to sandcast.
- Credential redaction already implemented for shell and skill capture is reused unchanged for dotfiles.

## Reconciliation with spec 002

Spec 002 (field feedback fixes) added a session environment for OpenShell sessions, which start with a cleared environment, and a switch of interactive terminal sessions to zsh. Both features landed in the same release. The principle for the merge: no startup file from the workspace home ever runs (this spec, FR-002), because the workspace mounted over the home may come from an untrusted project; the content of spec 002 (the session environment and its verify checks) is kept and adapted to that model.

- **D1, startup files.** This spec's mechanism stays whole: `ZDOTDIR` from the global zshenv and the image `ENV`, the bash and sh launchers as passwd shell and `CMD`, the login hook and the block prepended to the system bashrc. The zsh switch hook of spec 002 and its append to the system bashrc are gone; the prepended bashrc hook does the switching. The curated rc is loaded only through `ZDOTDIR/.zshrc` and the launcher's `--rcfile`, never by sourcing it from the system zshrc or bashrc. `/etc/profile.d/sandcast.sh` is the sh launcher's profile script. A base image built by a sandcast before this release carries that layout's blocks (the session environment and zsh switch blocks appended to the system bashrc and zshenv, the curated rc sourced from the system zshrc or bashrc, the rc files at `/etc/zsh/zshrc.d/sandcast.zsh` and `/etc/profile.d/sandcast.sh`); every build removes them from a layered base before it writes its own, so a layered image never keeps the shell mechanism of its base.
- **D2, session environment.** `/etc/profile.d/sandcast-env.sh` (spec 002) is the single source of the session environment: the `MISE_*` settings, the component, harness and relocation environment (the relocation values win, as before) and the shims first on `PATH`. Every entry point this spec controls sources it before it replaces the shell: `/etc/profile` for login shells, the login hook, the bashrc hook, both launchers and the global zshenv block. The hooks no longer export the relocation variables themselves. The bash `PROMPT_COMMAND` and `DEBUG` repairs of the script stay for the container target, where a directly started bash still reads the image home's rc files; in OpenShell images no home rc file runs, so they only put the shims first again after a curated rc that changed `PATH`. The session `PATH` never names a base image `PATH` directory under the overmount home (`/sandbox/.venv/bin` on the community base image): the workspace mounted there may come from an untrusted project, and a command looked up in it during shell startup (Ubuntu's system bashrc calls `groups`) would run project code. This narrows spec 002 FR-006 for directories inside the workspace; the plan reports each dropped directory, a tool the base image installed there is re-provisioned by the workspace rule, and a virtual environment the project creates there is active only after it is activated. The rule holds for sessions that keep the image environment as well (OpenShell's default agent inherits the image `ENV`): the image `PATH` of an OpenShell image leaves those directories out (the plan sets it to the base image's `PATH` without them and replaces a component `PATH`, with a notice), and the script drops every entry equal to or under the overmount home from the `PATH` it inherits, once, in its body before `_sandcast_path` runs, so the prompt repair never strips a directory a curated rc or the user put on `PATH` later.
- **D3, `SANDCAST_NO_SHELL_SWITCH`.** The opt-out stays. In a zsh image the hooks then replace the interactive bash with `bash --noprofile --norc -i` with the session environment, which reads no workspace rc either.
- **D4, curated rc prefix.** The two lines of spec 002 (`_sandcast_path 2>/dev/null || true` and `SANDCAST_RC_LOADED=1`) are prepended to this spec's curated rc files, so verify's startup checks keep working.
- **D5, verify.** All session checks of spec 002 stay. `session:bash-interactive` no longer passes "after the home rc files ran": the bashrc hook hands the interactive bash to the curated shell, which reads the check from stdin with the same positional parameters, and no home rc file runs. The checks pass against images built with the merged model under OpenShell-like conditions. The terminal condition of spec 002 FR-008 (sessions without a terminal must not switch) is retired: a `bash -i` that stayed bash, terminal or not, would read the workspace `~/.bashrc`, so the bashrc hook hands every interactive bash to the curated shell.
- **D6, workspace rule.** One rule for tools found only under the workspace path, keyed on this spec's overmount home (`base_image.overmount_home`, the registry entry's `overmount_home`, `/sandbox` by default), with spec 002's probe data (every `PATH` hit, cache format 3). A tool whose every hit lies under the overmount home is re-provisioned and reported as this spec's relocation notice; the mixed case keeps spec 002's `workspace-path` and `unknown-version` notices.
- **D7, `home-rc-path`.** In OpenShell images the home rc files are never sourced, so the notice is not reported there; the probe fact stays in the image facts. The container target keeps the notice ("bash sessions that read it lose the directories set before it").
- **Interactive sessions.** OpenShell's terminal session (`bash -i`) becomes an interactive, non-login curated zsh (`/bin/zsh -i`), as this spec defines; spec 002 started a login zsh. The login hook starts `zsh -l -i` for an interactive login bash. A bash started inside the curated zsh stays bash (`SANDCAST_SESSION`); `$SHELL` is `/bin/zsh` in the curated zsh. The switch happens with or without a terminal. With `SANDCAST_NO_SHELL_SWITCH` the hooks keep bash without startup files and leave `SHELL` as the runtime set it. An image whose curated shell is not zsh but which has zsh gets the zshenv block too: on the OpenShell target it relocates zsh's startup files and marks the session, on the container target it only sources the session environment and disables the new-user wizard. A zsh plan sets a kept user's login shell to `/bin/zsh` on both targets (spec 002 FR-008).
