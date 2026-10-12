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
| `60-shell` | The curated shell configuration under `/etc` (see below). |
| `70-harness` | One block per harness in the manifest: for Claude Code, the configuration in `/opt/sandcast/claude-code` (`CLAUDE_CONFIG_DIR`), the MCP servers, the skills and the plugins. See [baking harness extensions](capture.md#baking-harness-extensions). |
| `80-policy` | `COPY policy.yaml /etc/openshell/policy.yaml` (OpenShell target only). |
| `90-footer` | `WORKDIR /sandbox` (OpenShell target) or the final user's home (container target), `USER <final user>`, `ENTRYPOINT []` and `CMD ["<login shell>", "-l"]`. Clearing the entrypoint matters because the community base image sets `/bin/bash` as its entrypoint; without that, a manual `podman run` would not start the login shell. OpenShell replaces both anyway. |

Sections without content (no packages, no mise tools, no recipes) are left out.

**The final user.** When the base image runs as a non-root user, that user stays the final user and keeps its home. When it runs as root and the target is `openshell`, sandcast creates the user `sandbox` with uid and gid 1000 and home `/home/sandbox`, and first removes any existing user or group with id 1000 (such as the `ubuntu` user of `ubuntu:24.04`). The container target never creates a user and keeps root as the final user. System packages are installed as root either way.

**The shell.** A zsh configuration is copied to `/etc/zsh/zshrc.d/sandcast.zsh` and sourced from `/etc/zsh/zshrc` (or `/etc/zshrc` when the image uses that path). A bash configuration is copied to `/etc/profile.d/sandcast.sh` and sourced from `/etc/bash.bashrc` or `/etc/bashrc` when they exist. The login shell in `CMD` (and of a created user) is the manifest's shell program, otherwise zsh when the plan includes the `zsh` component, otherwise bash when the probe found it in the base image, otherwise `/bin/sh`.

**Nothing under `/sandbox`.** OpenShell's Kubernetes driver mounts the workspace volume over `/sandbox`, which hides anything an image put there. sandcast installs tools under `/opt/mise`, shell configuration under `/etc` and harness configuration under `/opt/sandcast`, and in OpenShell images `/sandbox` stays an empty working directory.

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
| `/etc/profile.d/sandcast-mise.sh` | | Puts the shims back on the `PATH` for login shells whose system profile resets it, as Debian's `/etc/profile` does. |

The mise binary itself comes from the `mise.run` installer at the pinned version; the layer downloads the installer to a file and tries up to three times, so a transient network error does not fail the build. The install runs as the final user with `mise install --locked --yes` (tried a second time on failure, for the same reason), followed by `mise reshim`. These retries stay inside one layer: a failed build is never retried. mise's cache and state stay out of `/opt/mise`, because the OpenShell policy makes that directory read-only at runtime; they live under `/tmp/sandcast-mise` during the install and are removed afterwards.

Because the shims are on the `PATH` through `ENV`, every process in the image finds the tools without shell activation, including the child processes of an agent. One exception applies to images built on the community base image: an interactive bash that is not a login shell resets `PATH` through the base user's `~/.bashrc` and loses the shims. Login shells and processes that are not shells keep them.

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

The network section is always empty. Network access comes at runtime from the provider profiles attached to the sandbox, never from the image (see the [profiles guide](profiles.md)). `/opt/mise` is added as read-only whenever mise tools are installed, and component `read_only` and `read_write` paths are merged in. The `/opt/<key>` tree of every component installed from a stored fragment is added as read-only too, which is why a fragment unpacks its files into `/opt/<key>`.

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

After pass 2, sandcast removes the per-run stage image. With `--keep-stage` it instead retags the stage to the stable `<image>:sandcast-stage` and keeps it. It then scans every file in the build directory for credential-shaped values and pushes the image when `--push` or the target field `push: true` asks for it.

## Step 4: verify

```sh
sandcast verify
```

`verify` runs every component check of the plan inside the built image, as the final user, in one container under the container engine. No gateway is involved. It reports every result before it exits, writes `verify.yaml` next to the plan, and exits 1 when any check failed. `sandcast build --verify` runs the build and then verify. For the example, verify passed 9 of 9 checks.

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

The plan uses a stored fragment for a component when no tier defines the component, or when the definition is `unresolvable` on this base image. The fragment goes into the `50-fragments` section. For a key that no tier defines, verify runs `command -v <key>`, so the fragment must put an executable with exactly that name on the final user's `PATH`. `sandcast manifest validate` accepts a manifest key that has an active stored fragment.

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
- When a tier already defines the key, its `check`, `provides`, `profiles`, `targets`, `match` and `image` are carried over. Otherwise the check is `command -v <first binary>`.

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
| `ENTRYPOINT []`, `CMD ["<login shell>", "-l"]` | as described above | the same |
| mise layout, harness configuration, adapted profiles, `io.sandcast.*` labels | as described above | the same |

`allowed-domains.txt` lists the endpoint hosts of every referenced profile, sorted and unique, one per line, wildcards as written in the profiles. Use it to configure an egress proxy or firewall for the container. Set `targets.container.allowed_domains_file` to choose another file name.

One manifest can build both targets from the same base image. When `base_image` names a registry entry of the other target (such as `nvidia-community-base` with `--target container`), sandcast uses that entry and prints a notice. Run `sandcast lock` once; the lockfile covers the mise tools of every declared target.
