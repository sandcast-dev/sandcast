# Building an image

This guide follows a manifest through `sandcast lock`, `sandcast plan`, `sandcast build` and `sandcast verify`, explains what ends up in the image and why, and covers what happens when a build fails: fragments, learnings and promotion. The last section describes the container target.

Every step here runs without an agent. The `/sandcast-build` skill wraps the same commands and adds the repair loop described in [the build skill](#the-build-skill).

## The example

The examples use this manifest, built with podman on a linux/arm64 host:

```yaml
schema_version: 1
name: demo
base_image: nvidia-community-base
components:
  - key: go
  - key: starship
  - key: fzf
  - key: claude-code
shell:
  program: zsh
targets:
  openshell:
    image: localhost/demo
```

## Step 1: lock the tools

```sh
sandcast lock --platform linux/arm64
```

`go`, `starship`, `fzf` and `claude-code` are installed by mise. `sandcast lock` resolves each one with the host mise and writes `.sandcast/mise.lock` with the exact version, the download URL and the checksum for every requested platform. The build later installs in locked mode, so it downloads exactly those files and fails on a checksum mismatch. Lock once per platform you build for, and commit the lockfile.

## Step 2: plan

```sh
sandcast plan
```

The plan step does four things: it selects the platform and the base image, probes the base image, decides per component what to install, and writes the plan.

### The probe

sandcast pulls the base image for the selected platform, reads its digest, and runs one throwaway container with a POSIX `sh` script. The script records the image facts (operating system, package manager, default user and its home, available shells) and runs the `check` command of every component. Each check ends in one of three states:

- `present`: the check passed and the version satisfies the selector.
- `incompatible`: the check passed, but the version does not satisfy the selector.
- `missing`: the check failed.

The results are cached by image digest and platform, and each component check separately by a hash of its command. The second plan of the example printed `probe cache hit: no container started` and finished in under a second. When you add a component, only that component is probed. `--no-cache` discards the cached results of the digest.

If the pull fails but an image of the right platform exists locally, sandcast probes the local image and prints a notice. If neither works, the plan fails with the registry entry name, the reference and the engine error, and writes nothing.

### The probe delta

From each check result, the plan derives an action:

| Status | Installer | Action |
|--------|-----------|--------|
| `present` | any | `skip`: nothing is installed. |
| `missing` | any | `install`. A `package` component on an image without a supported package manager, or without packages for the probed one, becomes `unresolvable`. |
| `incompatible` | `mise` | `shadow`: mise installs the required version under `/opt/mise`, whose shims come first on the `PATH`. |
| `incompatible` | `package`, `script` | `unresolvable`, with the version conflict named. |

**The lockfile decides.** For a `mise` component that the base image provides (`present` or `incompatible`) and that `mise.lock` locks for the target platform, the locked version wins: when the probed version equals it, the action is `skip`; when it differs, or when the probe could not read a version, the action is `shadow` and mise installs the locked version over the base image's copy. There is no per-component opt-out. To keep the base image's version, drop the component or select a version range the base image satisfies and run `sandcast lock` again. A component without a lock entry follows the table above. A lock entry that lacks the target platform fails the plan, as before. The delta records the locked version as `locked_version` and the plan carries a notice for every such decision:

```text
lockfile-shadow: gh: base image has 2.93.0, mise.lock pins 2.102.0 for linux/arm64; installing 2.102.0
unknown-version: codex: base image version could not be read; installing locked 0.162.1
```

**The workspace path.** For the OpenShell target, OpenShell mounts the workspace volume over the overmount home of the base image (`base_image.overmount_home`, `/sandbox` on the community base image), which hides whatever the image put there. The probe records every `PATH` hit of the component's first binary, without following symlinks (other binaries and resolved paths are follow-up task T060). When all of them lie under the overmount home, the component counts as missing and is installed, or, when no definition or fragment can install it, stays unresolved with a `hidden` relocation notice; the plan says which paths it ignored (`ignored_paths` in the delta). When only the first hit lies there and another copy exists outside, the outside copy decides; because the probed version came from the hidden copy, it counts as unknown, so a locked mise component is shadowed. The container target has no workspace volume and keeps binaries under the home directory.

```text
workspace-path: python: first PATH hit /sandbox/.venv/bin/python3 lies under the overmount home /sandbox and is ignored; /usr/local/bin/python3 decides
workspace-path: image: base image PATH directory /sandbox/.venv/bin lies under the overmount home /sandbox and is left out of the image PATH and the session PATH; a tool there is hidden once the workspace is mounted, and a virtual environment the project creates there is active only after it is activated
```

The second form reports a base image `PATH` directory under the overmount home: it is left out of the image `PATH` (which OpenShell's default agent inherits) and of [the session environment](#the-session-environment), because the workspace mounted there may come from an untrusted project.

Plan notices with a kind (`lockfile-shadow`, `unknown-version`, `workspace-path`, `home-rc-path`, `reference`) follow the other notices, sorted by kind and component, and `sandcast build` repeats them in its report. `sandcast probe` shows the base image as it is, without the lockfile comparison and the workspace rule.

For the example, the first plan probed nine components and printed:

```text
COMPONENT        STATUS   INSTALLED  REQUIRED  ACTION
ca-certificates  present  -          -         skip
claude-code      present  2.1.156    -         skip
curl             present  8.5.0      -         skip
fzf              missing  -          -         install
go               missing  -          -         install
iproute2         present  6.1.0      -         skip
nftables         present  1.0.9      -         skip
starship         missing  -          -         install
zsh              missing  -          -         install
```

The base image already ships Claude Code, so sandcast does not install it again.

### Implicit components

The manifest lists four components, but the plan has nine. sandcast adds components on its own, each with a notice:

| Component | Added when |
|-----------|------------|
| the shell program (`zsh` or `bash`) | `shell.program` is set |
| each key in `shell.components` | the curated shell configuration references it |
| the agent component of each harness | the harness is in the manifest |
| `git` | a harness lists plugins (plugin installs clone their marketplaces) |
| `iproute2`, `nftables` | the target is `openshell` (the sandbox supervisor needs them) |
| `curl`, `ca-certificates` | at least one component uses the mise installer |

A component whose `targets` excludes the target being built is skipped with a notice.

### Unresolved components and conflicts

A component is unresolved when no tier defines it and no stored fragment exists, or when its action is `unresolvable` and no stored fragment exists. Unresolved components do not fail the plan. They are listed with their reason, so that you or the build skill can [write a fragment](#fragments). An unresolved component marked `optional: true` is skipped at build time.

When two components provide the same binary, the plan reports a conflict, writes nothing and exits 2. Remove one of the two from the manifest.

The plan also exits 2 without writing anything when the lockfile lacks an entry for the selected platform, or when a component references a profile id that no profile tier defines.

### The plan file

The plan is written to `.sandcast/build/<target>/plan.yaml`, next to `mise/` and `policy.yaml`. It is one YAML document with these sections, in this order:

| Section | Content |
|---------|---------|
| `schema_version`, `sandcast_version` | Plan schema version (1) and the binary version. |
| `target`, `platform` | The target, and the platform with the source that selected it (`flag`, `manifest` or `host`). |
| `engine` | The engine and why it was chosen (`flag`, `manifest`, `detected` or `fallback`). |
| `base_image` | The manifest input, the registry entry, the reference, the digest and the registry notes. |
| `image_facts` | The probed operating system, package manager, default user and shells. |
| `final_user` | The user the image runs as, and whether sandcast creates it. |
| `image` | Environment variables and directories the components need. |
| `probe_delta` | One entry per component with status, versions and action. |
| `components` | Per component: tier, source file, installer kind, action, the rendered fragment, the binaries, runtime globs, profile ids and the check that verify runs. |
| `mise` | The generated `mise.toml`, the lockfile path and the mise platform. |
| `profiles` | The referenced profile ids with their winning tier and source file. |
| `policy` | The image-embedded OpenShell policy (OpenShell target only). |
| `shell` | The shell program, the path of the curated configuration in the image and its text. |
| `unresolved`, `conflicts`, `notices` | What blocks the build and what sandcast decided on its own. |

The plan contains no timestamps, every list is sorted, and the encoder settings are fixed. The same manifest, lockfile, component and profile definitions, base image digest and platform always give a byte-identical file. The build and verify steps read only the plan.

## Step 3: build

```sh
sandcast build --verify
```

`build` runs the plan again (the probe cache makes that fast), refuses with exit 2 when there are conflicts or blocking unresolved components, assembles the Containerfile, and builds the image in two passes. For the example, both passes took about 72 seconds.

### The Containerfile

`.sandcast/build/<target>/Containerfile` is generated from the plan in a fixed section order. Do not edit it: sandcast rewrites it on every build. Change the manifest, the component definitions or the stored fragments instead. Every section after the header starts with an explicit `USER`, so a section never depends on where the previous one ended.

| Section | Content |
|---------|---------|
| `00-header` | `FROM <reference>@<digest>`, so the build uses exactly the probed image. |
| `10-system-packages` | One layer that installs every `package` component through the probed package manager (`apt-get`, `dnf`, `yum` or `apk`), with the packages sorted and deduplicated. For apt-get it creates `/var/lib/apt/lists/partial` first and cleans the lists afterwards. |
| `20-user` | The final user, `/sandbox` owned by that user (OpenShell target), the component directories, and the component environment variables. |
| `30-mise` | The mise layout and one `mise install --locked` for all mise tools. See [the mise layout](#the-mise-layout). |
| `40-recipes` | One layer per `script` component in key order, as a `RUN` heredoc with `set -eux`, as the final user or as root when the recipe sets `as_root`. |
| `50-fragments` | One block per stored fragment in key order, each preceded by a comment with its key and file and by `USER root`. |
| `60-shell` | The session environment script, the curated shell configuration relocated outside the home, and for the OpenShell target the final user's login shell and the hooks for shells that OpenShell starts directly (see [the home model](#the-home-model-and-shell-initialization) and [the session environment](#the-session-environment)). |
| `62-dotfiles` | One `COPY` per baked dotfile to its relocation target (for example `/etc/gitconfig`, `/etc/tmux.conf`, `/etc/xdg/...`). Omitted when the manifest lists no dotfiles. |
| `70-harness` | One block per harness in the manifest: for Claude Code, the configuration in `/opt/sandcast/claude-code` (`CLAUDE_CONFIG_DIR`), the MCP servers, the skills and the plugins. See [baking harness extensions](capture.md#baking-harness-extensions). |
| `80-policy` | `COPY policy.yaml /etc/openshell/policy.yaml` (OpenShell target only). |
| `90-footer` | `WORKDIR /sandbox` (OpenShell target) or the final user's home (container target), `USER <final user>`, `ENTRYPOINT []` and the `CMD`. The `CMD` is `["/bin/zsh", "-l"]` for zsh, `["/opt/sandcast/shell-login"]` for bash (the baked launcher, which makes bash interactive with its own rcfile), and `["/opt/sandcast/sh-login"]` otherwise (the baked sh launcher). Clearing the entrypoint matters because the community base image sets `/bin/bash` as its entrypoint; without that, a manual `podman run` would not start the login shell. OpenShell uses neither for its sessions: it starts `/bin/bash` itself, which the shell hooks of the `60-shell` section cover. |

Sections without content (no packages, no mise tools, no recipes) are left out.

**The final user.** When the base image runs as a non-root user, that user stays the final user and keeps its home. When it runs as root and the target is `openshell`, sandcast creates the user `sandbox` with uid and gid 1000 and home `/home/sandbox`, and first removes any existing user or group with id 1000 (such as the `ubuntu` user of `ubuntu:24.04`). The container target never creates a user and keeps root as the final user. System packages are installed as root either way.

**The shell.** The login shell in `CMD` (and of a created user) is the manifest's shell program, otherwise zsh when the plan includes the `zsh` component, otherwise bash when the probe found it in the base image, otherwise `/bin/sh`. The configuration is baked outside the home; see [the home model](#the-home-model-and-shell-initialization) for how each shell initializes under the overmount. The curated file starts with two lines sandcast adds: `_sandcast_path 2>/dev/null || true`, which puts the mise shims back in front of `PATH` after a system profile reset it, and `SANDCAST_RC_LOADED=1` (not exported), which lets verify confirm that a session read the file.

**Nothing under `/sandbox`.** OpenShell's Kubernetes driver mounts the workspace volume over `/sandbox`, which hides anything an image put there. sandcast installs tools under `/opt/mise`, shell and dotfile configuration under `/opt/sandcast` and `/etc`, and harness configuration under `/opt/sandcast`, and in OpenShell images `/sandbox` stays an empty working directory. A base-image tool whose only copy lives under `/sandbox` is hidden at runtime, so the plan treats it as missing and re-provisions it to a persistent location, with a notice. The plan fails when anything it bakes or declares would lie under `/sandbox`: the shell configuration and hooks, a dotfile target, a component or harness directory, or a component, harness or fragment policy path (`/sandbox` itself, the workspace directory, is allowed).

### The home model and shell initialization

On the OpenShell sandbox base images the final user's home is `/sandbox`, and the runtime mounts the workspace volume over it. A registry entry can name another path with `overmount_home` (see the [configuration reference](../configuration.md#the-user-registry-file)): it becomes the image `WORKDIR`, which the OpenShell podman driver mounts the workspace over, and the plan records the effective value as `base_image.overmount_home`. The Kubernetes driver of OpenShell v0.1.3 always mounts at `/sandbox` whatever the `WORKDIR`, so another value is for the podman driver only, and the plan says so in a notice. This guide writes `/sandbox` for it. The home is therefore shadowed, writable and tied to the workspace rather than the image. sandcast makes the interactive environment survive this by baking everything outside the home and pointing programs at it through environment variables, never by writing into the home.

- **zsh.** The global zshenv sets `ZDOTDIR=/opt/sandcast/zdotdir` (also set as image `ENV`, so every process inherits it). zsh reads its global zshenv first and unconditionally, before any `$HOME` file, so it reads its startup from the `ZDOTDIR` tree and never from a workspace `$HOME/.zshenv` or `$HOME/.zshrc`. The location of the global zshenv is a build option of zsh: Debian and Ubuntu read `/etc/zsh/zshenv` and never `/etc/zshenv`, other distributions read `/etc/zshenv`. sandcast writes the export to both, so the relocation holds even when a runtime clears the image `ENV` (OpenShell starts its shells with a cleared environment, and so do `su -l` and `env -i`). The curated configuration is baked at `/opt/sandcast/zdotdir/.zshrc`.
- **bash.** bash has no `ZDOTDIR` equivalent, so sandcast bakes a launcher at `/opt/sandcast/shell-login` that runs `exec bash --noprofile --rcfile /opt/sandcast/bashrc "$@"`. `--noprofile` means bash reads none of `~/.bash_profile`, `~/.bash_login` and `~/.profile` in the workspace, and `--rcfile` makes the curated configuration replace `~/.bashrc`. The launcher deliberately does not pass `--norc`, because bash ignores `--rcfile` whenever `--norc` is set. Like the sh launcher below, it drops `-l` and `--login` from the options it forwards (`-ilc` becomes `-ic`): a login bash reads no rcfile, so the curated configuration would be lost, and an interactive login bash runs the workspace `~/.bash_logout` on exit. The system bashrc (`/etc/bash.bashrc` on Debian and Ubuntu) lies outside the home and is still read. The launcher is the final user's login shell and the image `CMD`.
- **other login shells.** When the login shell is neither zsh nor bash the image does not fail: sandcast bakes a launcher at `/opt/sandcast/sh-login` that points `$ENV` at the minimal `/etc/profile.d/sandcast.sh` default and runs `exec /bin/sh "$@"`. Because the launcher execs a non-login shell, `/bin/sh` reads `$ENV` (for interactive shells) but never a `~/.profile` in the workspace home. The launcher drops `-l` and `--login` from the options it forwards (`-lc` becomes `-c`), so a caller that asks for a login shell (`su -l`, an editor's environment probe) cannot make `/bin/sh` read that file either; arguments after the command string are passed on unchanged. The launcher is the final user's login shell and the image `CMD`, and capture records that the shell was not curated.

In OpenShell images the shell above (zsh, or the launcher) becomes the final user's passwd login shell even when that user is kept from the base image: the community base image keeps `sandbox` with `/bin/bash`, and a login through passwd (`su -`, sshd) would otherwise start `bash -l`, which reads a workspace `~/.bash_profile` or `~/.profile`. The `60-shell` section runs `usermod -s` (with `/usr/sbin` and `/sbin` added to the search path, because the community base image's `PATH` lacks them), falls back to rewriting the `/etc/passwd` entry on images without `usermod`, and adds the shell to `/etc/shells`. The container target leaves the base image user untouched, except that a zsh plan sets a kept user's login shell to `/bin/zsh` as well (spec 002 FR-008).

**Shells that OpenShell starts.** OpenShell v0.1.x does not start the image `CMD` or the passwd shell for its sessions. It runs `/bin/bash` directly (`/bin/sh` when the image has no bash): with `-i` under a pseudo-terminal for a terminal session, with `-lc` or `-c` for `openshell sandbox exec` (login shell by default), and as `bash -l` for its default agent. Exec and terminal sessions start with a cleared environment (the image `ENV` is gone); the default agent inherits the image `ENV`. The hooks source `/etc/profile.d/sandcast-env.sh`, which carries the relocation variables, so both cases agree. The image `PATH` of an OpenShell image leaves out every base image `PATH` directory under the overmount home (`/sandbox/.venv/bin` on the community base image), and the session environment script drops any such entry it inherits, so the default agent's login shell and every command it runs never look a program up in the mounted workspace either. bash reads two system files before any file in the home, and sandcast hooks both in OpenShell images:

- **`/etc/profile.d/zz-sandcast-shell.sh`** runs last in `/etc/profile` for every login shell. It sources the session environment script (`sandcast-env.sh`, which `/etc/profile` read just before it; the relocation variables are part of it) and then replaces the shell before bash reaches the home: an interactive login bash execs the curated shell; a non-interactive login bash (`bash -lc 'cmd'`, `bash -l script`, `bash -l` reading its input) execs a bash that reads no startup file and runs the same command, script or input with the same arguments and process id. The replacement keeps the caller's options, so `-e`, `-u`, `-x`, `-o pipefail` and every other `set -o` and `shopt` option still apply (they are forwarded as `-o` and `-O`; `interactive`, `login_shell`, `monitor`, `history`, `emacs`, `vi` and `interactive-comments` describe the kind of shell and are left to the replacement), and with them the exit status: `bash -le -c 'false; echo x'` prints nothing and exits 1, as it does without the hook. A script is passed after `--`, so one named like an option runs, and the input is recognized by the name bash was started under (the path or base name of `$BASH`, with or without the login `-` prefix, or `sh`), so a renamed bash reads its input too. Because the replacement is not a login shell, `~/.bash_logout` is never read either. Another POSIX login shell (`sh -lc` with dash or busybox ash) gets `HOME` pointed at `/opt/sandcast/home` while it looks for `$HOME/.profile`; the baked `.profile` there restores `HOME`.
- **The system bashrc** (`/etc/bash.bashrc` on Debian and Ubuntu, `/etc/bash/bashrc` on Alpine) gets a block prepended, before the distribution's early return for non-interactive shells. The block sources the session environment script first, because an interactive bash reads no profile. An interactive non-login bash (OpenShell's terminal session) then execs the curated shell: the launcher for a bash image, zsh for a zsh image. In an image whose login shell is the sh launcher (neither zsh nor bash was curated), an interactive bash is replaced by `bash --noprofile --norc`, bash without any startup file, not by `/bin/sh`. The bash the launcher starts reads the system bashrc too; it is recognized by the process id the launcher exported (`SANDCAST_SHELL_PID`) and continues to the baked rcfile. The launcher exports that guard only when its bash will be interactive (`-i`, or neither `-c` nor a script operand with a terminal on stdin and stderr), because only an interactive bash reaches the hook that removes it again; for `shell-login -c 'cmd'` (`su - sandbox -c`, a tool running `$SHELL -c`) the guard is removed, so an interactive bash that the command execs is not waved past the hook. A bash started by sshd with `-c`, the only non-interactive bash that reads the system bashrc, is replaced by a bash that reads no startup file. The block is delimited by marker lines: every build first removes the block of a previous sandcast build from the system bashrc (a team base image that sandcast built, with other targets, or a rebuild after a sandcast upgrade), together with that build's login hook file, on both targets and with a `removing` line in `build.log`, and then prepends the current block on the OpenShell target. The sandcast block in the global zshenv is replaced by its installer the same way, so a layered image never keeps the shell mechanism of its base.

A bash the user starts inside a curated session (`SANDCAST_SESSION` is set by the launchers, the global zshenv and the hooks) stays bash: in a bash image it goes through the launcher, in a zsh image it starts without any rc file. A top-level interactive bash in a zsh image, which includes `podman run -it --entrypoint /bin/bash`, becomes zsh. In a zsh image the global zshenv and the hooks also export `SHELL=/bin/zsh`: OpenShell sets `SHELL=/bin/bash` for its sessions, so tmux, an editor's terminal or any other program that starts `$SHELL` from the curated zsh would otherwise get a login bash that the hooks turn into that bare nested bash. In a bash image `SHELL` is the passwd shell, the launcher, so such programs get the curated bash. Login shells, scripts and commands keep their program. With `SANDCAST_NO_SHELL_SWITCH` set in the sandbox environment, a top-level interactive bash in a zsh image stays bash without startup files instead of becoming zsh (see [the session environment](#the-session-environment)). The replacement keeps exported variables, but shell functions, aliases and unexported variables that `/etc/profile.d` scripts set before the hook do not reach the new shell.

The hooks and the launcher never resolve bash through the `PATH`: the hooks exec `$BASH`, the running binary, and the build substitutes the absolute path of the image's bash (found on `/usr/local/bin:/usr/bin:/bin` only) into the launcher. A curated rc commonly puts `$HOME/bin` first on the `PATH`, and `$HOME` is the workspace, so a `bin/bash` wrapper committed into a project would otherwise run whenever a hook handed an explicit `/bin/bash -i` to the launcher.

When the curated shell cannot be executed (a layered image removed it, or the runtime applies a filesystem policy stricter than the image policy, which grants `/opt/sandcast` read-only), an interactive session does not end with status 126: the hook prints one `sandcast:` line naming the target and the policy grant and starts `bash --noprofile --norc` instead, which reads no workspace file but has no curated configuration either. The command of a `bash -i -c` session still runs.

These guarantees are verified by `make test-integration` (an OpenShell spawn simulation with planted workspace startup files under `env -i` and `script`) and `make test-gateway` (the same checks through a real OpenShell v0.1.3 gateway).

**Known limitations.**

- bash builds without a system bashrc (Fedora, RHEL and the official `bash` image) read nothing outside the home before `~/.bashrc`, so an interactive non-login bash that a runtime starts directly on such a base image reads the workspace `~/.bashrc`. Login shells, the launcher and zsh are not affected. `build.log` records a line when the image's bash has no system bashrc. The shipped OpenShell base images are Ubuntu and are covered.
- A POSIX shell other than bash that sources `/etc/profile` by hand without being a login shell keeps `HOME` pointed at `/opt/sandcast/home` until it is reset, because only a login shell reads the `.profile` that restores it.
- For a POSIX login shell other than bash (dash, busybox ash), `HOME` points at `/opt/sandcast/home` from the hook until the baked `.profile` restores it. Every `/etc/profile.d` script that sorts after `zz-sandcast-shell.sh` (a name such as `zz_tool.sh` or `zz-zz-later.sh`) therefore runs with that `HOME` in such a shell. Name profile scripts so they sort before `zz-sandcast-shell.sh`.
- In an image whose login shell is the sh launcher, the interactive bash that replaces a hooked bash runs without any startup file, so it has no bash completion and the default prompt; the distribution's system bashrc is not read either.
- When the curated shell cannot be executed, the session continues in `bash --noprofile --norc` after one `sandcast:` line (see above): usable, but without the curated configuration, so the line is worth reading.
- The bashrc hook replaces every interactive non-login bash that reads the system bashrc, including one a tool starts with its own `--rcfile` or `--init-file` (an editor's shell integration): the tool's init file never loads in OpenShell images. `bash --norc` skips the system bashrc and is not affected.
- Every login shell is redirected, root's included: `su -` as root lands in the curated shell of the image with the relocation variables set.
- The replacement of a non-interactive login bash keeps the options, the command, the arguments and the exit status, but not shell functions, aliases or unexported variables that earlier `/etc/profile.d` scripts set, and not a `BASH_ENV` file that the original bash had already read.
- The login hook tells `bash -l script` from `bash -l` reading its input by `$0`, so a script whose name is exactly the name bash was started under or `sh` (`bash -l sh` with a script called `sh` in the current directory) is read as input instead. Name the script differently or give its path.
- The container target gets no hooks and no passwd change apart from the zsh login shell of a kept user in a zsh plan; there the curated configuration reaches the launcher (the `CMD`) and zsh through `ZDOTDIR`, but not a bash started directly.
- The image `PATH` and the session `PATH` leave out the base image's directories under the overmount home, but other base image variables that name a path there stay in the image `ENV`: the community base image sets `VIRTUAL_ENV=/sandbox/.venv` and `UV_PYTHON_INSTALL_DIR=/sandbox/.uv/python`, so a tool that reads `VIRTUAL_ENV` (uv does) uses a virtual environment at that path in the workspace without activation, from any working directory. Projects that must not run workspace code through such a tool unset the variable in the sandbox environment or use a base image without it.
- The plan knows zsh from the base image's shells and from the `zsh` component. A zsh that a recipe or fragment of another component installs is unknown to it, so the image gets no global zshenv block for that zsh: on the OpenShell target it reads its startup files from the workspace home, and the new-user wizard is not disabled. Declare zsh as the `zsh` component (or as `shell.program`) instead.

Because the workspace home is never auto-sourced, a startup file committed into a project (an untrusted `.zshrc` or `.bashrc`) is never run simply by opening the project in a sandbox. The baked configuration loads the same way whether the mounted home is writable or read-only, because it does not depend on writing into the home. Relocation variables (`ZDOTDIR` and per-tool variables such as `STARSHIP_CONFIG` and `INPUTRC`; never `XDG_CONFIG_HOME`, which tools need to write their own configuration) are emitted as image `ENV` by the plan, deterministically and sorted, and the hooks, the launchers and the global zshenv source the session environment script, which carries them, for shells that start with a cleared environment. A relocation variable always wins over a component or harness value of the same name, and the plan reports each replaced value. An image `BASH_ENV`, `ENV` or `ZDOTDIR`, or a relocation variable, whose final value lies under the overmounted home or is not a plain absolute path (`$HOME/.bashrc` and `~/.shrc` are expanded by the shell at run time) fails the plan.

### The mise layout

The `30-mise` section installs mise v2026.10.4 to `/usr/local/bin/mise` and sets up this layout:

| Path or variable | Value | Purpose |
|------------------|-------|---------|
| `MISE_DATA_DIR` | `/opt/mise` | Installed tools, under `/opt/mise/installs/<tool>/<version>/`. Owned by the final user. |
| `MISE_CONFIG_DIR` | `/etc/mise` | `mise.toml` and `mise.lock`, copied from the build directory and made world-readable. |
| `MISE_TRUSTED_CONFIG_PATHS` | `/etc/mise` | The only configuration mise trusts in the image. |
| `MISE_CACHE_DIR`, `MISE_STATE_DIR` | `/tmp/sandcast-mise/cache`, `/tmp/sandcast-mise/state` | Used during the install and removed afterwards. |
| `MISE_USE_VERSIONS_HOST` | `0` | No lookups on the mise versions host. |
| `PATH` | `/opt/mise/shims:$PATH` | Shims on the `PATH` through `ENV`. |
| `/etc/profile.d/sandcast-env.sh` | | The session environment script: exports every `MISE_*` variable above and the component environment, and puts the shims first on the `PATH`. See [the session environment](#the-session-environment). |

The mise binary itself comes from the `mise.run` installer at the pinned version; the layer downloads the installer to a file and tries up to three times, so a transient network error does not fail the build. The install runs as the final user with `mise install --locked --yes` (tried a second time on failure, for the same reason), followed by `mise reshim`. These retries stay inside one layer: a failed build is never retried. mise's cache and state stay out of `/opt/mise`, because the OpenShell policy makes that directory read-only at runtime; they live under `/tmp/sandcast-mise` during the install and are removed afterwards.

Because the shims are on the `PATH` through `ENV`, every process started with the image environment finds the tools without shell activation, including the child processes of an agent. OpenShell sessions do not keep the image environment; [the session environment](#the-session-environment) explains how they get the same tools and settings. In OpenShell images no shell reads the home `~/.bashrc` (see [the home model](#the-home-model-and-shell-initialization)), so the community base image's `~/.bashrc` cannot reset the `PATH` and drop the shims.

### The session environment

OpenShell v0.1.3 starts its exec and terminal sessions with a cleared environment (the default agent inherits the image `ENV`). Such a session gets only `HOME`, `USER`, `TERM`, `SHELL`, the default `PATH` `/usr/local/bin:/usr/bin:/bin` and the variables of the sandbox spec, and it always starts bash: `openshell sandbox connect` runs an interactive `bash -i` and `openshell sandbox exec` runs `bash -lc '<command>'`. The `ENV` lines of the image never reach such a session, and `$SHELL` or the login shell in `/etc/passwd` are ignored. sandcast therefore sets up the session environment through one script that every entry point it controls sources before it replaces the shell:

| File | When | Content |
|------|------|---------|
| `/etc/profile.d/sandcast-env.sh` | Images with mise tools, and OpenShell images whose components, harnesses or relocated dotfiles set environment variables or whose base image `PATH` has directories beyond the OpenShell default | Exports every `MISE_*` variable of the `30-mise` layer and the environment variables of the components, the harnesses and the relocation (for example `RUSTUP_HOME`, `CARGO_HOME`, `GOPATH`, `GOTOOLCHAIN`, `CLAUDE_CONFIG_DIR`, `ZDOTDIR` or `STARSHIP_CONFIG`; the relocation values win), defines `_sandcast_path` and calls it. `_sandcast_path` puts `/opt/mise/shims` first on the `PATH` and, for the OpenShell target, the base image's `PATH` directories beyond `/usr/local/bin:/usr/bin:/bin` (for example `/usr/local/sbin`, `/usr/sbin` or `/opt/conda/bin`, as the probe recorded them, in the base image's order) right after the shims; a directory with characters other than letters, digits and `._+@/-` is left out, and so is a directory under the overmount home (`/sandbox/.venv/bin` on the community base image): the workspace mounted there may come from an untrusted project, and a command looked up in it during shell startup (Ubuntu's system bashrc calls `groups`) would run project code, so no session `PATH` ever names it. For the same reason the script first drops, once per session, every entry equal to or under the overmount home from the `PATH` it inherits (the image `PATH` in the default agent's session, or a `PATH` the runtime set), together with every entry that is not absolute (`.`, `bin`, `~/.local/bin` or an empty entry), which a shell resolves from the working directory or the home, both the workspace in a session. The strip runs only while `SANDCAST_PATH_STRIPPED` is unset and exports it, so a child shell that sources the script again (every zsh through the global zshenv, a login bash, the launchers) keeps a directory under the home that the curated rc or the user put on the `PATH` later, such as an activated virtual environment; the cleared environment of an OpenShell session and the image `ENV` never carry the marker, and the plan refuses a component that sets it. The strip is not part of `_sandcast_path` either, so the prompt repair never removes such a directory. The image `PATH` itself leaves those entries out as well (the plan rewrites a component `PATH` the same way, keeping its own directories outside the home, with a notice); the plan reports each left-out entry as a `workspace-path` notice; a tool the base image installed there is re-provisioned by the workspace rule, and a virtual environment there joins the `PATH` only when it is activated. A login shell reads the file through `/etc/profile`, before the login hook. In bash it also adds `_sandcast_path` to `PROMPT_COMMAND` and, in a login bash that is not interactive, sets a `DEBUG` trap that runs `_sandcast_path` once right before the first command and then removes itself; that repair matters on the container target, where a bash started directly still reads the image home's rc files; in OpenShell images no home rc file runs, so it only puts the shims first again after a curated rc that changed `PATH`. |
| The login hook `/etc/profile.d/zz-sandcast-shell.sh` and the block at the top of the system bashrc | OpenShell target | Both source `sandcast-env.sh` first, so the shell they replace a login or an interactive bash with inherits the exported session environment (see [the home model](#the-home-model-and-shell-initialization)). |
| The launchers `/opt/sandcast/shell-login` and `/opt/sandcast/sh-login` | bash and sh images, both targets | Source `sandcast-env.sh` before they start bash or sh, so `su -` and a `$SHELL -c` command get the session environment without reading any startup file. |
| The global zshenv (`/etc/zsh/zshenv` and `/etc/zshenv`) | zsh images and images that have zsh (in the base image or as a component), both targets | The sandcast block sources `sandcast-env.sh` and defines `zsh-newuser-install` as a no-op, so zsh never starts its new-user wizard in a home directory without `.zshrc` (an empty workspace volume). In a zsh image it also exports `ZDOTDIR`, `SANDCAST_SESSION` and `SHELL`; in an OpenShell image with another curated shell it exports `ZDOTDIR` and `SANDCAST_SESSION`, so a zsh the user starts never reads a workspace `.zshrc` (the launcher stays `$SHELL`); in a container image with another curated shell a zsh keeps the image home's own startup files. |

The curated rc files (`/opt/sandcast/zdotdir/.zshrc` and `/opt/sandcast/bashrc`) start with `_sandcast_path 2>/dev/null || true`, which puts the shims back in front of `PATH` after a system profile reset it, and `SANDCAST_RC_LOADED=1`, which `verify` reads.

**Home directory rc files that reset `PATH`.** The community base image's `/sandbox/.bashrc` sets `PATH` to a fixed value, and `/sandbox/.profile` sources it. In OpenShell images no shell ever reads them (the hooks replace the shell first), so they cannot drop the shims and the plan reports nothing about them; the probe still records which of `.bashrc`, `.profile` and `.bash_profile` in the base user's home assign `PATH` without referencing `$PATH`, as an image fact. For the container target, where a bash started directly reads the image home's rc files and the base image user stays the final user, the plan reports each one:

```text
home-rc-path: image: /root/.bashrc resets PATH; bash sessions that read it lose the directories set before it
```

**Interactive sessions and zsh.** In a zsh image (`shell.program: zsh`, or a `zsh` component without a shell section), the bashrc hook hands the interactive bash that OpenShell starts for a terminal session to `/bin/zsh -i`, and the login hook hands an interactive login bash to `/bin/zsh -l -i`; both read the curated rc through `ZDOTDIR`. A non-interactive bash (`bash -lc`, agent tool calls, pipes) keeps bash and runs the command itself, without any startup file from the workspace. Set `SANDCAST_NO_SHELL_SWITCH=1` in the sandbox environment (for example `openshell sandbox create --env SANDCAST_NO_SHELL_SWITCH=1`) to stay in bash: the hooks then start `bash --noprofile --norc -i` with the session environment, which reads no startup file from the workspace home either. The final user's login shell is `/bin/zsh` as well, so that an OpenShell release that honors the login shell needs no hook.

`verify` checks all of this under session conditions, see [step 4](#step-4-verify).

### The image policy

For the OpenShell target, `policy.yaml` is the sandbox policy the OpenShell supervisor reads from `/etc/openshell/policy.yaml`. It starts from the restrictive default policy of OpenShell v0.1.3 and adds the paths the components and harnesses need (`/opt/sandcast/claude-code` is read-write when Claude Code is baked):

```yaml
# OpenShell sandbox policy generated by sandcast. Do not edit.
# Base: OpenShell v0.1.3 restrictive_default_policy() plus component image needs.
# Network rules come from provider profiles at runtime, never from this file.
version: 1
filesystem_policy:
  include_workdir: true
  read_only:
    - /bin
    - /dev/urandom
    - /etc
    - /lib
    - /opt/mise
    - /opt/sandcast
    - /proc
    - /usr
    - /var/log
  read_write:
    - /dev/null
    - /opt/go
    - /tmp
landlock:
  compatibility: best_effort
process:
  run_as_user: sandbox
  run_as_group: sandbox
network_policies: {}
```

The network section is always empty. Network access comes at runtime from the provider profiles attached to the sandbox, never from the image (see the [profiles guide](profiles.md)). `/opt/sandcast` is always added as read-only, because the baked shell configuration, the shell launchers and relocated dotfiles live there and the upstream default policy covers nothing under `/opt`; without it, landlock denies the sandbox user its own rc files and login shell. A harness that needs a writable subtree, such as `/opt/sandcast/claude-code`, lists it as read-write, and landlock grants the union of both rules there. `/opt/mise` is added as read-only whenever mise tools are installed, and component `read_only` and `read_write` paths are merged in. The `/opt/<key>` tree of every component installed from a stored fragment is added as read-only too, which is why a fragment unpacks its files into `/opt/<key>`.

`run_as_group` follows the final user's primary group, but OpenShell v0.1.3 rejects group 0 (root). When the kept base user's primary group is GID 0 (the OpenShift style of a numeric non-root user with group root), `run_as_group` uses the user's UID instead, and the plan prints a notice to that effect. Files the image gives that user stay accessible through ownership.

### Two passes

**Pass 1** builds the Containerfile under a per-run stage tag, `<image>:sandcast-stage-<random>`. The random suffix keeps a concurrent build of the same image name from replacing the stage between the two passes, which pass 2 builds from.

**Path resolution** then runs one container of the stage image as its final user. For every binary a mise component installs, sandcast asks `mise which <bin>` and follows symlinks with `readlink -f`, so a profile names the real binary and never a shim. For other components, and for mise components the base image already provided, it uses `command -v <bin>` plus `readlink -f`. In the example, `claude` resolved to `/usr/local/bin/claude` and `go` to `/opt/mise/installs/go/<version>/bin/go`.

**Adaptation** copies every referenced provider profile, rewrites its `binaries` to the resolved paths plus the components' runtime globs, lints the result with the OpenShell v0.1.3 rules, and writes the set to `.sandcast/build/<target>/profiles/`. A profile with no resolved binary is kept with an empty list and flagged, because an unflagged inert profile would silently deny traffic. The [profiles guide](profiles.md#adaptation) has the details.

**Pass 2** builds `Containerfile.final`, a one-layer build from the stage image that copies the adapted profiles to `/usr/share/sandcast/profiles/`, under the final tag, with these labels:

| Label | Value |
|-------|-------|
| `io.sandcast.version` | The sandcast binary version. |
| `io.sandcast.manifest-name` | The manifest `name`. |
| `io.sandcast.profiles-path` | `/usr/share/sandcast/profiles` |
| `io.sandcast.profiles-index` | JSON with the adapted ids and the flagged ids, for example `{"ids":["claude-code","go-proxy"],"flagged":[]}`. |

A runtime that starts sandboxes from the image can read the label to learn which providers the image needs, without access to the build directory:

```sh
podman image inspect localhost/demo:latest --format '{{index .Labels "io.sandcast.profiles-index"}}'
```

After pass 2, sandcast removes the per-run stage image. With `--keep-stage` it instead retags the stage to the stable `<image>:sandcast-stage` and keeps it. It then scans every file in the build directory for credential-shaped values and pushes the image when `--push` or the target field `push: true` asks for it. The push and its output go to `build.log` under a `### sandcast push: <ref>` heading, and the build report shows the fully qualified reference, whether the image was pushed, the registry digest and the `openshell sandbox create --from <ref>` command. A gateway on a cluster can pull only a pushed registry reference, often for another architecture than your machine's; the [cross-architecture guide](cross-arch.md) shows how to build and push such an image.

## Step 4: verify

```sh
sandcast verify
```

`verify` runs every component check of the plan inside the built image, as the final user, in one container under the container engine. No gateway is involved. It reports every result before it exits, writes `verify.yaml` next to the plan, and exits 1 when any check failed. `sandcast build --verify` runs the build and then verify. For the example, verify passed 9 of 9 component checks.

For the OpenShell target, verify always adds session checks in the same container, with no flag to turn them off. Each runs under `env -i HOME=<home> USER=<user> TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin`, the environment of an OpenShell session:

| Check | Condition | Passes when |
|-------|-----------|-------------|
| `session.bash-login.<component>` | `session:bash-login` | Every binary of the component that resolves in the image environment resolves through the session `PATH`, and the component's check passes in `bash -lc '<check>'`, as `openshell sandbox exec` runs it. A binary that does not resolve in the image environment either is skipped and noted. |
| `session.bash-interactive.<component>` | `session:bash-interactive` | The same check, fed on stdin to `bash -i` as in `openshell sandbox connect`, passes in the shell the bashrc hook hands that bash to (the curated zsh, the bash launcher, or bash without startup files), which reads the check from stdin with the same positional parameters. No home rc file runs. |
| `session.zsh.<component>` | `session:zsh` | In a zsh image (`shell.program: zsh`, or a `zsh` component without a shell section), the same in a login `zsh -il -c '<check>'`, as the login hook starts zsh for an interactive login bash. |
| `session.zsh.startup` | `session:zsh` | In a zsh image, an interactive zsh reads the curated rc and writes nothing to stderr during startup, apart from the `can't change option: zle` note that appears only without a terminal. |
| `session.bash.startup` | `session:bash-interactive` | In a bash image (`shell.program: bash`, or bash from the base image without a shell section), an interactive bash reads the curated rc and writes nothing to stderr during startup, apart from bash's own notes about the missing terminal. |

A session check is the component's own check with the same expected exit code, output and version, so the component's binary has to resolve through the session `PATH` and run with the session's `MISE_*` settings. Session checks appear in their own `session checks` group of the console report and carry their `condition` in `verify.yaml` and the JSON report (`image` for the component checks). A failing session check fails verify like any other check, so an image whose tools are only on the image `PATH` does not pass.

A check that does not finish within the check timeout is reported as `FAIL (timeout)` rather than as missing, so a stalled check is told apart from an absent tool; its output starts with `timeout: the check did not finish within <duration>`. When the container itself is stopped before it reaches every check, the check that was running is a timeout too, and each check after it is reported with an output that starts with `not run:`, so nothing treats a component that was never checked as missing.

## Concurrent runs

`plan`, `build`, `verify`, `profiles export` and `profiles import` take an exclusive lock on the `.lock` file in the target's build directory for the whole run, so two sandcast processes never read or write one build directory, or build one target, at the same time. The lock is per target: a run against the `openshell` target and a run against the `container` target proceed side by side.

While one of these runs holds the lock, another exits 1 with `another sandcast build is running for target <target> in <dir>; wait for it to finish`. The `.lock` file stays in the build directory by design. It is empty, it never enters an image, it is ignored together with the rest of the build directory, and removing it would let a later run lock a fresh file while an earlier run still held the old one.

## Containers without an init process

sandcast starts its throwaway containers (the probe, the path resolution pass and verify) with `--init`, so that a cancelled run reaches the container as a signal. When the engine cannot find its init binary (`catatonit` for podman, `docker-init` for docker), sandcast repeats the run once without `--init`, prints a notice that says so, and leaves `--init` out for the rest of the invocation. Both podman and docker trigger this fallback. It fires only on an engine-level exit code (125, 126 or 127) together with a stderr message that names the missing init binary, so an ordinary command that is not found inside a container never sets it off.

## When a build fails

A failed `sandcast build` exits 1 and is never retried. It leaves `Containerfile` and `build.log` in `.sandcast/build/<target>/` for inspection.

- **Pass 1 failed.** No image is kept. `build.log` contains the engine output after the line `### sandcast pass 1: <image>:sandcast-stage`. Find the failing step and map it to its section through the section comments in the Containerfile.
- **A later step failed** (path resolution, profile adaptation or lint, or pass 2). The pass-1 image is kept as `<image>:sandcast-debug`, so you can look inside it:

  ```sh
  podman run --rm -it --entrypoint /bin/sh localhost/demo:sandcast-debug
  ```

Problems that sandcast detects before building exit 2 instead, with nothing built: an invalid manifest, a lockfile gap, an unknown profile reference, a binary conflict, or an unresolved component that is not optional.

For a failure in a package layer, a mise tool or a recipe, fix the input (the manifest, the component definition, the lockfile) and build again. For a component that no definition can install, write a fragment.

## Fragments

A fragment is a piece of Containerfile for one component, stored in `.sandcast/fragments/<key>.containerfile`. Fragments are committed with the project, so the non-interactive build reuses them without an agent. The build skill writes them, and you can too.

```sh
sandcast fragments write mytool --file /tmp/mytool.containerfile
```

The plan uses a stored fragment for a component when no tier defines the component, or when the definition is `unresolvable` on this base image. The fragment goes into the `50-fragments` section. For a key that no tier defines, verify runs `command -v` for each binary the manifest entry declares in `bins`, else `command -v <key>`, so the fragment must put each of these executables on the final user's `PATH`. `sandcast manifest validate` accepts a manifest key that has an active stored fragment.

`sandcast fragments write` validates the fragment before storing it:

- The key must be a kebab-case component key.
- Only the instructions `RUN`, `COPY`, `ENV`, `USER`, `WORKDIR`, `ARG` and `LABEL` are allowed. `FROM` is refused because a fragment is a snippet, not a stage. `ADD` is refused because it fetches URLs without a checksum, and `SHELL` because podman ignores it for OCI images.
- The fragment needs at least one instruction, and every heredoc must be terminated.
- The fragment must not end with `USER root`. Switch back to the final user after system changes.
- Credential-shaped values are refused.

The stored file starts with a header line, for example `# sandcast-fragment: key=mytool written=2026-10-08 status=active`. Writing the same key again replaces the fragment. In the Containerfile, sandcast prefixes every fragment with a comment and `USER root`, so a fragment always starts as root.

The build skill follows stricter rules, and they serve hand-written fragments well too:

- One `RUN` per fragment, starting with `set -eux`.
- Pinned download URLs only, with a sha256 check of every downloaded file before it is used.
- Binaries in `/usr/local/bin`, unpacked trees in `/opt/<key>`.
- Create and chown every parent directory the final user writes to, not only the leaf.
- Nothing under `/sandbox`, `/opt/mise`, `/etc/mise` or the OpenShell reserved paths.

```dockerfile
# mytool 1.4.2 from https://github.com/example/mytool/releases/tag/v1.4.2
USER root
RUN set -eux; \
    case "$(uname -m)" in \
      x86_64) asset=mytool-1.4.2-x86_64-unknown-linux-musl.tar.gz; sum=SHA256_OF_THE_X86_64_ASSET;; \
      aarch64) asset=mytool-1.4.2-aarch64-unknown-linux-gnu.tar.gz; sum=SHA256_OF_THE_AARCH64_ASSET;; \
      *) echo "unsupported architecture: $(uname -m)" >&2; exit 1;; \
    esac; \
    curl -fsSL -o /tmp/mytool.tar.gz "https://github.com/example/mytool/releases/download/v1.4.2/${asset}"; \
    echo "${sum}  /tmp/mytool.tar.gz" | sha256sum -c -; \
    install -d /opt/mytool; \
    tar -xzf /tmp/mytool.tar.gz -C /opt/mytool --strip-components=1; \
    install -m 0755 /opt/mytool/mytool /usr/local/bin/mytool; \
    rm -f /tmp/mytool.tar.gz
USER sandbox
```

**Stale fragments.** When a component that has a stored fragment later resolves through a definition (for example after you added a component file, or after a promotion), the plan uses the definition and reports the fragment as stale in its notices. The fragment is not used.

**Superseded fragments.** `sandcast promote` marks the fragment `status=superseded`. A superseded fragment is never used; it stays in place as a record of what the recipe came from.

`sandcast fragments list` shows every stored fragment with its status and date.

## Learnings

`.sandcast/learnings.yaml` records build failures and the changes made for them, one entry per attempt. The build skill appends to it with `sandcast learnings add`:

```sh
sandcast learnings add --component mytool --attempt 1 \
  --failure "binary not found at the archive root" \
  --change "extract with --strip-components=1" --outcome unfixed
```

```yaml
# sandcast learnings: build failures and the changes made for them (written by the build skill).
  - attempt: 1
    component: mytool
    failure: binary not found at the archive root
    change: extract with --strip-components=1
    outcome: unfixed
    date: "2026-10-08"
```

Every entry needs a component key, a failure, a change, an attempt number of 1 or more, an outcome of `fixed` or `unfixed`, and a date in `YYYY-MM-DD` form. An entry whose text contains a credential-shaped value is refused. Commit the file: it tells the next person, and the next skill run, which changes did not work.

## The build skill

`/sandcast-build` in Claude Code runs the steps above and handles the cases the binary leaves open:

1. It runs `sandcast skills check` and stops on a version mismatch.
2. It runs `sandcast plan --json`. For every blocking unresolved component it writes a fragment from the plan's facts (package manager, final user, platform) and stores it with `sandcast fragments write`. For optional unresolved components it asks first.
3. It runs `sandcast build --verify --json`. When a fragment it wrote fails, it records the failure with `sandcast learnings add`, fixes the fragment and builds again, up to three builds by default (`--max-attempts`). Failures outside its fragments are reported, not edited.
4. For every fragment that now builds and verifies, it asks whether to promote it into a recipe.
5. For the OpenShell target, it offers `sandcast profiles import` when the `openshell` CLI is present.

The skill never edits the manifest or the files in `.sandcast/build/`. When the retry limit is reached, it leaves the last Containerfile and build log in place and names them together with the debug image.

## Promotion

A working fragment can become a recipe: a project-local component that installs the tool deterministically, without the skill.

```sh
sandcast promote mytool --bins mytool
```

The command prints the recipe and asks for confirmation (`--yes` skips the question). It writes `.sandcast/recipes/mytool.yaml` with a `script` installer and `promoted_from: fragments/mytool.containerfile`, and marks the fragment superseded. From the next plan on, the component resolves through the recipe in the `40-recipes` section.

The conversion works like this:

- `RUN` steps, including heredoc bodies and continuation lines, become `installer.script.run`.
- `ENV` lines become `image.env`.
- `WORKDIR <dir>` becomes `mkdir -p <dir> && cd <dir>`.
- The `USER` lines decide `as_root`: steps that run as root (no `USER`, `root`, `0`, or a user other than the final user of the last plan) give `as_root: true`, steps that run as the final user give `as_root: false`.
- When a tier already defines the key, its `check`, `provides`, `profiles`, `targets`, `match` and `image` are carried over. Otherwise the check runs `command -v` for every binary, as the fragment's check did.

Promotion refuses fragments that a script cannot express: fragments with `COPY`, `ARG` or `LABEL`, fragments that mix root and final-user `RUN` steps, and fragments without a `RUN` step. Keep such a fragment as it is, or rewrite it with `RUN` steps only. Review the generated recipe like code before committing it, because every non-interactive build of the project runs it.

## The container target

The container target builds the same components as a plain image for running directly under podman or docker, without OpenShell. Declare it in the manifest next to or instead of `openshell`:

```yaml
targets:
  openshell:
    image: localhost/demo
  container:
    image: localhost/demo-plain
```

```sh
sandcast lock --platform linux/arm64
sandcast build --target container --verify
podman run --rm -it localhost/demo-plain:latest
```

Compared with the OpenShell target:

| Aspect | `openshell` | `container` |
|--------|-------------|-------------|
| Policy | `policy.yaml` at `/etc/openshell/policy.yaml` (`80-policy`) | none |
| `iproute2`, `nftables` | added implicitly | not added; their shipped definitions apply only to `openshell` |
| Default registry entry | `nvidia-community-base` | `fedora-42` |
| Allowed domains | none | `allowed-domains.txt` in `.sandcast/build/container/` |
| Final user | the base image's non-root user, or `sandbox` (uid 1000) created by sandcast | the base image's user as it is, root included (root on `fedora-42`) |
| Working directory | `/sandbox`, owned by the final user | the final user's home (`/root` for root) |
| `ENTRYPOINT []`, `CMD` | `["/bin/zsh", "-l"]` for zsh, the baked launcher without `-l` otherwise (`["/opt/sandcast/shell-login"]` or `["/opt/sandcast/sh-login"]`), as in the `90-footer` row above | the same |
| mise layout, harness configuration, adapted profiles, `io.sandcast.*` labels | as described above | the same |

`allowed-domains.txt` lists the endpoint hosts of every referenced profile, sorted and unique, one per line, wildcards as written in the profiles. Use it to configure an egress proxy or firewall for the container. Set `targets.container.allowed_domains_file` to choose another file name.

One manifest can build both targets from the same base image. When `base_image` names a registry entry of the other target (such as `nvidia-community-base` with `--target container`), sandcast uses that entry and prints a notice. Run `sandcast lock` once; the lockfile covers the mise tools of every declared target.
