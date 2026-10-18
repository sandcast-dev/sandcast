# Configuration reference

This page describes every file sandcast reads or writes, where it lives, and the rules each one follows.

| File or directory | Owner | Purpose |
|-------------------|-------|---------|
| `.sandcast/manifest.yaml` | you or the capture skill | [The manifest](#the-manifest) |
| `.sandcast/mise.lock` | `sandcast lock` | [The lockfile](#the-lockfile) |
| `.sandcast/components/`, `.sandcast/recipes/` | you, `sandcast promote` | [Project component tier](#component-tiers) |
| `.sandcast/profiles/` | you | [Project profile tier](#profile-tiers) |
| `.sandcast/fragments/`, `.sandcast/learnings.yaml` | the build skill, `sandcast fragments write`, `sandcast learnings add` | [Fragments and learnings](#the-sandcast-directory) |
| `.sandcast/build/<target>/` | `sandcast plan`, `sandcast build`, `sandcast verify` | [Build outputs](#build-outputs) |
| `<config dir>/base-images.yaml` | you | [User base image registry](#base-image-registry) |
| `<cache dir>/` | sandcast | [Probe and registry cache](#cache-directory) |

## The manifest

The manifest is `.sandcast/manifest.yaml`. The capture skill writes it, `sandcast init` creates a skeleton, and you can edit it by hand. `sandcast manifest validate` checks it.

```yaml
schema_version: 1
name: demo
base_image: nvidia-community-base
platform: linux/arm64
components:
  - key: go
  - key: python
    version: "3.13"
  - key: my-internal-tool
    optional: true
shell:
  program: zsh
  rc: |
    alias ls=lsd
  components: [lsd]
dotfiles:
  - path: ~/.gitconfig
  - path: ~/.config/starship.toml
targets:
  openshell:
    image: localhost/demo
  container:
    image: localhost/demo-plain
```

| Field | Type | Required | Rules |
|-------|------|----------|-------|
| `schema_version` | integer | yes | Must be `1`. A newer version is refused with a message naming both versions, before any other field is read. |
| `name` | string | yes | Kebab-case, 1 to 63 characters (`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`). It becomes the image label that suffixes profile ids and the value of the `io.sandcast.manifest-name` label. |
| `base_image` | string | yes | A registry entry name, a registry reference, or any other image reference. See [resolving the base image](#resolving-the-base-image). |
| `engine` | `podman` or `docker` | no | Container engine. The `--engine` flag overrides it. Without either, sandcast uses podman when it is on the `PATH` and docker otherwise, with a notice. |
| `platform` | `linux/amd64` or `linux/arm64` | no | Target platform. The `--platform` flag overrides it. The default is the host architecture. |
| `components` | list | yes | The components to include. See below. |
| `harnesses` | list | no | Agent harnesses. See [Harnesses](#harnesses). |
| `shell` | object | no | The curated shell configuration. See below. |
| `dotfiles` | list | no | Non-shell home dotfiles to bake. See below. |
| `targets` | object | yes | At least one of `openshell` and `container`. See below. |

The manifest is decoded strictly: an unknown field anywhere is an error with its line number. It must not contain credential values. `sandcast manifest validate` reports every value that looks like a credential, and `sandcast manifest set-shell` refuses to write one. Use `${SANDCAST_SECRET_N}` placeholders and supply the real value at runtime as a provider credential (see [secrets handling](guides/capture.md#secrets)).

### components

Each entry selects one component by key.

| Field | Type | Default | Rules |
|-------|------|---------|-------|
| `key` | string | | Kebab-case. Keys must be unique in the list. The key must resolve in a [component tier](#component-tiers) or have an active stored fragment, unless the entry is optional. |
| `version` | string | the component's own selector | Version selector that overrides the component default, for example `"1.25"`, `">=3.12"`, `"^1.2"` or `"~1.2"`. The lockfile pins the exact version. |
| `optional` | boolean | `false` | An optional component that does not resolve is reported but does not block the build. |
| `bins` | list of names | the key | The executables of a component that no tier defines, for example `bins: [hx]` for `helix`. Each name matches `[A-Za-z0-9._+-]+` and appears once. `verify` checks each with `command -v`, the binary conflict check includes them, and `sandcast promote` uses them as the recipe's binaries when `--bins` is not given and then removes `bins` from the entry, because the recipe defines the component from then on. A key that a component tier defines must not set `bins`: its binaries come from the definition, and validation and plan exit 2. The field is additive, so the manifest schema version stays 1. |

You do not need to list components that sandcast adds by itself: the shell program, the components in `shell.components`, the agent component of each harness, `git` when a harness lists plugins, `iproute2` and `nftables` for the OpenShell target, and `curl` and `ca-certificates` whenever a mise tool is installed. The [build guide](guides/build.md#implicit-components) explains these additions.

### shell

| Field | Type | Rules |
|-------|------|-------|
| `program` | `zsh` or `bash` | Required when `shell` is present. The shell becomes a component and the login shell of the image: `/bin/zsh`, or the baked bash launcher. A user sandcast creates gets it on both targets; a user kept from the base image gets it on the OpenShell target for both programs, and on the container target only for `zsh`. With `zsh`, the zsh new-user wizard is disabled, and on the OpenShell target the shell hooks hand an interactive terminal session that OpenShell starts in bash to the curated zsh. Set `SANDCAST_NO_SHELL_SWITCH=1` in the sandbox environment to stay in bash; that bash reads no startup file from the workspace home either. See [the home model](guides/build.md#the-home-model-and-shell-initialization) and [the session environment](guides/build.md#the-session-environment). |
| `rc` | multi-line string | The curated configuration. It is baked outside the home so it survives the OpenShell workspace mount over the home: zsh reads it from the `ZDOTDIR` tree (`/opt/sandcast/zdotdir/.zshrc`), and bash reads it as the rcfile the baked launcher passes (`/opt/sandcast/bashrc`). The live workspace home is never auto-sourced. sandcast prepends two lines that re-apply the mise shims and mark the file as read for verify. Write it with `sandcast manifest set-shell`. |
| `components` | list of keys | Components the configuration references, such as a prompt or a listing tool. Each one is added to the image. |

See the [build guide](guides/build.md#the-home-model-and-shell-initialization) for how the shell initializes under the overmount.

### dotfiles

Non-shell home dotfiles to bake into the image. Capture proposes a bounded host-home allowlist (git, the prompt tool, tmux and readline basics) and you may add explicit paths. Each entry records only a source path; its contents are read at build time, passed through credential redaction, and baked to a persistent location outside the home.

The sources are bounded because a manifest is committed with the project: a manifest in a project you did not write must not be able to pull your credentials into an image. There is deliberately no option to widen the bounds. Copy a file into the project to bake one that lives elsewhere.

| Field | Type | Rules |
|-------|------|-------|
| `path` | string | Required. The host source. A `~/` path resolves against the host home and must be a dotfile allowlist entry (`.gitconfig`, `.config/git/config`, `.config/starship.toml`, `.tmux.conf`, `.inputrc`) or lie in one of these `~/.config` entries: `git/`, `starship.toml`, `nvim/`, `helix/`, `tmux/`, `alacritty/`, `kitty/`, `wezterm/`, `ghostty/`, `bat/`, `ripgrep/` (a directory entry covers every file below it). Nothing else under `~/.config` is read, because that directory also holds credential stores (`gh`, `gcloud`, `sops`, `op`, `containers`, `rclone`) whose contents no redactor can recognize reliably; the exact spelling must match. A project-relative path resolves against the project root and must stay inside it, and an absolute path is accepted only when it resolves inside the project. `~user` paths are not supported. Credential stores (`.ssh`, `.aws`, `.gnupg`, `.kube`, `.docker`, `.config/gh`, `.config/sops`, `.config/op`, `.config/gcloud`, `.config/containers`, `.config/rclone`, `.config/hcloud`, `.config/doctl`, `.config/wrangler`, `.config/helm/registry`), `.netrc`, history files and credential-looking names in any path segment (a dotenv file, a key or certificate extension, or `token`, `secret`, `credential`, `password` in a name) are refused without regard to case, both as written and after following symlinks, and only regular files of at most 256 KiB are read. An entry outside these bounds, missing, or unreadable is reported and skipped, never fatal; the notice names the bound. |

The plan chooses the relocation target per dotfile (there is no override in v1): git goes to `/etc/gitconfig` and tmux to `/etc/tmux.conf`, which both tools read natively; starship goes to `/etc/xdg/starship.toml` with `STARSHIP_CONFIG`; readline goes to `/opt/sandcast/inputrc` with `INPUTRC`, and the baked file starts with `$include /etc/inputrc` because `INPUTRC` replaces the system file too; anything else goes under `/etc/xdg`, the system directory in the default `XDG_CONFIG_DIRS` search path, keeping the part of the source path below its `.config/` segment wherever that segment sits (`~/.config/nvim/init.lua` and a project copy `dotfiles/.config/nvim/init.lua` both go to `/etc/xdg/nvim/init.lua`); a source without such a segment keeps its whole path below `/etc/xdg`. sandcast never sets `XDG_CONFIG_HOME`: tools write their own configuration there (`gh auth login`, `helm repo add`), and an image-wide value pointing at a baked, root-owned tree would make them fail. A target that is not a clean path of safe segments (no `.` or `..`) is skipped with a notice. When two entries resolve to the same target, the explicit manifest path wins over an allowlist proposal (between two entries of the same kind, the smaller path wins), deterministically, and the plan reports the resolution. git configuration is the exception, because git reads both of its files: one `~/.config/git/config` (or a project copy that keeps the `.config/git/config` layout) and one `~/.gitconfig` are merged into `/etc/gitconfig` in git's order, the XDG file first and `~/.gitconfig` last, so a key set in both keeps the `~/.gitconfig` value. Each part starts with a `# sandcast: from <path>` comment, the plan lists the first file under `merged_from`, and both are reported. No credential value is ever written: a detected credential becomes a `${SANDCAST_SECRET_N}` placeholder.

### targets

| Field | Type | Default | Rules |
|-------|------|---------|-------|
| `targets.<target>.image` | string | | Required. Image name without tag; validation rejects a tag or digest here (use `tag`). |
| `targets.<target>.tag` | string | `latest` | Image tag. |
| `targets.<target>.push` | boolean | `false` | Push the final image after the build, like `sandcast build --push`. `sandcast build --push=false` skips the push for one build. |
| `targets.container.allowed_domains_file` | string | `allowed-domains.txt` | File name of the allowed-domains list in `.sandcast/build/container/`. The value must be a plain file name: a single path element with no `/` or `\`, not blank, and not `.` or `..`. It must not start with a dot (sandcast keeps hidden files of its own there), and it must not be a name sandcast writes in that directory itself (`.lock`, `build.log`, `plan.yaml`, `policy.yaml`, `verify.yaml`, `Containerfile`, `Containerfile.final`, `profiles`, `mise`, `shell`, `harness`, `dotfiles`), compared without regard to case. Only the container target accepts this field. |

`<target>` is `openshell` or `container`. Commands without `--target` use `openshell` when it is declared, otherwise `container`.

### Harnesses

A harness entry bakes an agent and the extensions you chose into the image. The capture skill writes these entries from `sandcast harness discover`. The [capture guide](guides/capture.md#baking-harness-extensions) describes what ends up in the image.

```yaml
harnesses:
  - name: claude-code
    mcp_servers:
      - name: notes
        command: [npx, -y, "@example/notes-mcp"]
        env:
          NOTES_API_KEY: "${SANDCAST_SECRET_1}"
          LOG_LEVEL: info
      - name: docs
        url: https://docs.example.com/sse
        env:
          DOCS_TOKEN: "${SANDCAST_SECRET_2}"
    plugins:
      - name: github@claude-plugins-official
        source: anthropics/claude-plugins-official
    skills:
      - name: review
        source: .claude/skills/review
```

| Field | Type | Rules |
|-------|------|-------|
| `name` | string | One of `claude-code`, `codex`, `opencode` or `pi`. Only `claude-code` has an adapter in this release; `sandcast plan` refuses the other names with exit 2. The harness's agent component (`claude-code`) is added to the image implicitly. |
| `mcp_servers[].name` | string | Required and unique within the harness. A URL server's name also forms the id of its generated profile, `mcp-<name>`. |
| `mcp_servers[].command` | list of strings | A local (stdio) server: the command and its arguments. Exactly one of `command` and `url` is required. |
| `mcp_servers[].url` | string | A remote server, `http` or `https`. A URL whose path ends in `/sse` is configured as an SSE server, any other as streamable HTTP. |
| `mcp_servers[].env` | map of strings | Values are literals, `${SANDCAST_SECRET_N}` placeholders, or references such as `${GITHUB_TOKEN}`, never credentials. A command server gets them as its environment. A URL server takes at most one entry, which is sent as a bearer `Authorization` header. |
| `plugins[].name` | string | `name` or `name@marketplace`. Listing any plugin adds the `git` component, because plugin installs clone their marketplaces. |
| `plugins[].source` | string | The marketplace the plugin comes from: `owner/repo`, a git or HTTPS URL, or a directory on the host (`/...`, `~/...`, `./...` or `../...`). `anthropics/claude-plugins-official` (or an empty source) means the official marketplace. |
| `skills[].name` | string | A single directory name; the skill is installed under that name. |
| `skills[].source` | string | The skill directory on the host. A relative path resolves against the project root and `~/` against your home directory, but only a directory inside the project tree or inside `~/.claude/skills` is copied into the image. A user-scope source (`~/...`) that does not exist on the build host fails plan and build, because such a source exists only on the machine that captured the manifest. The message tells you to copy the skill directory into the project as `.claude/skills/<name>`, commit it, and set the source there, which is why the example above uses a project path. |

A placeholder in `env` is written into the image configuration as a reference to the variable it is assigned to: `NOTES_API_KEY: "${SANDCAST_SECRET_1}"` becomes `${NOTES_API_KEY}`, which the agent reads from the sandbox environment at runtime. A placeholder inside a command argument or a URL stays as it is and is read from the variable `SANDCAST_SECRET_N`.

## Base image registry

The base image registry lists the base images sandcast knows, grouped by target type. Each target type has exactly one default entry, which `sandcast init` uses when you pass no `--base-image`.

### Shipped entries

| Target | Name | Reference | Default | Notes |
|--------|------|-----------|---------|-------|
| `openshell` | `nvidia-community-base` | `ghcr.io/nvidia/openshell-community/sandboxes/base:latest` | yes | Ubuntu 24.04 with user `sandbox` and home `/sandbox`. No zsh, starship or lsd. |
| `openshell` | `nvidia-ubuntu` | `nvcr.io/nvidia/base/ubuntu:24.04` | | OpenShell's default workload image. Runs as root; sandcast creates the sandbox user. |
| `container` | `fedora-42` | `registry.fedoraproject.org/fedora:42` | yes | Fedora 42 with dnf. Runs as root, and the container target keeps root as the final user. |
| `container` | `ubuntu-24.04` | `docker.io/library/ubuntu:24.04` | | Ubuntu 24.04 with apt-get. Runs as root and ships a user named ubuntu with uid 1000. |

### The user registry file

Add or replace entries in `base-images.yaml` in the sandcast configuration directory. The directory is `$SANDCAST_CONFIG_DIR` when set, else `$XDG_CONFIG_HOME/sandcast` when `XDG_CONFIG_HOME` is set, else `~/.config/sandcast`. A missing file is fine.

```yaml
schema_version: 1
targets:
  openshell:
    - name: team-base
      ref: quay.io/example/team-sandbox:2026.10
      default: true
      notes: Team base image with the internal CA bundle.
```

| Field | Rules |
|-------|-------|
| `schema_version` | Must be `1`. |
| `targets` | Map of target type (`openshell` or `container`) to a list of entries. |
| `name` | Required, unique within a target type. |
| `ref` | Required. Image reference. |
| `default` | At most one entry per target type in the file. |
| `notes` | Free text, shown in the plan as base image notes. |
| `overmount_home` | Optional, `openshell` entries only. The `WORKDIR` sandcast writes into images built from this entry, and the path it treats as the home that the workspace volume is mounted over. Default `/sandbox`, the home of the OpenShell sandbox base images. The OpenShell podman driver mounts the workspace at the image `WORKDIR`, so another value holds there; the Kubernetes driver of OpenShell v0.1.3 always mounts at `/sandbox` regardless of the `WORKDIR`, so use another value only with the podman driver. The plan prints a notice to that effect for any value other than `/sandbox`. It must be an absolute path of letters, digits, `.`, `_` and `-` without `.` or `..` segments, and neither a well-known system directory (`/`, `/etc`, `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/var`, `/opt`, `/root`, `/boot`, `/proc`, `/sys`, `/dev`, `/run`, `/tmp`, `/home`) nor a path below one (`/usr/local`, `/var/lib/work`), because the build gives the workspace directory to the final user; a subdirectory of `/home` (a user's home directory such as `/home/dev`) is accepted. The plan records the effective value as `base_image.overmount_home`, refuses anything it would bake or declare under it, and re-provisions a base-image tool found only there (see [the home model](guides/build.md#the-home-model-and-shell-initialization)). |

The user file merges over the shipped registry by entry name, per target type:

- A user entry with a new name is added.
- A user entry with the name of a shipped entry replaces it. It keeps the shipped default flag unless the user file marks a default of its own for that target type.
- A user entry marked as default clears the shipped default of its target type.

After merging, every target type must have exactly one default. Unknown fields in either file are errors. `sandcast base-images list` shows the merged result.

### The workspace path

For the OpenShell target, the workspace rule is keyed on the overmount home of the base image: the registry entry's `overmount_home`, recorded in the plan as `base_image.overmount_home`, `/sandbox` by default (see [the user registry file](#the-user-registry-file)). OpenShell mounts the workspace volume over it at runtime, which hides whatever the image put there. The probe records every `PATH` hit of a component's primary binary. A tool whose every hit lies under the overmount home counts as missing: sandcast installs it to a persistent location and reports a relocation notice (`component <key> was found only under <path> ...; it is provisioned to <location> instead`, or `... so it is unresolved` when no definition or fragment can install it). A tool whose first `PATH` hit lies there but which also exists elsewhere is taken from the other copy, with a `workspace-path` notice; its probed version came from the hidden copy, so it counts as unknown, and a locked mise component is shadowed with an `unknown-version` notice. The delta records the ignored hits as `ignored_paths`. The base image's `PATH` directories under the overmount home are left out of the image `PATH` and never put on the session `PATH` either (see [the session environment](guides/build.md#the-session-environment)); the plan reports each one as a `workspace-path` notice. A tool found only under the overmount home that no definition or fragment can install is reported with the `hidden` notice kind and stays unresolved. The container target has no workspace volume, so the rule is off there.

### Resolving the base image

The manifest field `base_image` is resolved against the entries of the target being built:

1. An entry whose name matches.
2. An entry whose reference matches.
3. A registry entry name of the other target type. The entry is used with a notice, so one manifest builds both targets from the same base image (for example `nvidia-community-base` with `--target container`).
4. Anything else is taken as an image reference outside the registry, with a notice that no registry notes apply.

## Component definitions

A component tells sandcast how to detect, install, check and allow one tool. Each component is one YAML file named `<key>.yaml`, and the loader rejects unknown fields. The [component README](../components/README.md) lists the shipped set and gives guidance for writing new definitions.

```yaml
key: go
name: Go
description: Go toolchain with the module cache under /opt/go.
match:
  files: [go.mod, go.work]
installer:
  mise:
    tool: go
    version: latest
    bins: [go, gofmt]
check:
  command: go version
image:
  env:
    GOPATH: /opt/go
    GOTOOLCHAIN: local
  dirs:
    - path: /opt/go
      owner: user
  read_write: [/opt/go]
  runtime_globs:
    - /opt/mise/installs/go/*/bin/*
provides: [go, gofmt]
profiles: [go-proxy]
```

| Field | Required | Meaning |
|-------|----------|---------|
| `key` | yes | Kebab-case identifier. It must match the file name. |
| `name` | yes | Display name. |
| `description` | no | One line shown in listings. |
| `match` | no | When the capture proposes the component. |
| `installer` | yes | Exactly one of `mise`, `package` and `script`. |
| `check` | yes | The command that decides whether the tool is present and working. |
| `image` | no | What the component needs from the image. |
| `provides` | no | Binaries the component puts on the `PATH`, used for conflict detection. |
| `profiles` | no | OpenShell provider profile ids the component needs. |
| `targets` | no | `openshell`, `container`, or both (the default when omitted). |
| `promoted_from` | no | Set by `sandcast promote` on recipes. |
| `emulation_unsafe` | no | `true` when the tool is known to fail under user-mode emulation (QEMU). `plan` and `build` print an `emulation` notice when the target platform's architecture differs from the container engine host's. The shipped `rust` component sets it. See the [cross-architecture guide](guides/cross-arch.md). |

A component never carries network endpoints. A top-level `endpoints` key is rejected by name, with a message that points to `profiles`. Network access belongs in an OpenShell provider profile.

### match

| Field | Meaning |
|-------|---------|
| `files` | Glob patterns relative to the project root, such as `go.mod` or `requirements*.txt`. |
| `host_tools` | Executable names looked up on the host `PATH`, such as `rg`. |
| `harnesses` | Harness names chosen during capture, such as `claude-code`. |
| `always` | `true` proposes the component for every project. Only base system components use it. |

Host detection is a closed set: a tool is proposed from the host only when some component names it in `host_tools`. The capture guide covers [how to extend and manage that list](guides/capture.md#managing-the-detected-host-tool-list).

### installer

| Kind | Fields | Meaning |
|------|--------|---------|
| `mise` | `tool` (required), `version` (default `latest`), `bins` | A mise registry short name or `backend:name`. Installed in locked mode from `.sandcast/mise.lock`. This is the preferred installer. |
| `package` | `packages` (required), `bins` | A map from `apt-get`, `dnf`, `yum` and `apk` to package lists. Installed through the package manager the probe found. |
| `script` | `run` (required), `as_root` (default `false`), `bins` | A shell script for tools that neither mise nor a package manager can install. It runs as the final user unless `as_root` is set. Promoted recipes use this kind. |

`bins` lists the executables the installer puts in place. sandcast resolves them to absolute paths in the built image and writes those paths into the adapted profiles. When `bins` is empty, `provides` is used, and then the key.

### check

| Field | Default | Meaning |
|-------|---------|---------|
| `command` | | Required. A POSIX `sh` command that runs inside the image as the final user. It must exit with the expected code only when the tool works, and it should print the version on its first line. |
| `expect.exit` | `0` | Expected exit code. |
| `expect.version` | none | Version selector, used only when neither the manifest nor `installer.mise.version` sets one. |
| `expect.stdout_contains` | none | A string the output must contain. |

The same check runs twice: in the base image during the probe, to decide whether the component is already present, and in the built image during verify. The status is `present` when the check passes and the version satisfies the selector, `incompatible` when the check passes with a version outside the selector, and `missing` otherwise. sandcast takes the version from the first line of output whose first number contains a dot.

Version selectors accept `latest` (any version), a prefix such as `1.25` (1.25 or any 1.25.x), an exact version, comparisons (`>=1.2`, `>1.2`, `<=1.2`, `<1.2`, `=1.2`), `^1.2` (same major version) and `~1.2` (same major and minor version).

### image

| Field | Meaning |
|-------|---------|
| `read_only`, `read_write` | Absolute paths added to the filesystem policy of OpenShell images. The upstream default policy does not cover `/opt`, so a component that installs outside `/opt/mise` and `/opt/sandcast` (which sandcast always grants read-only) lists its paths. |
| `env` | Environment variables set in the image, before the mise install. The relocation variables sandcast sets for the shell and dotfiles (`ZDOTDIR`, `STARSHIP_CONFIG`, `INPUTRC`) win over a component value, with a notice. For the OpenShell target, a `BASH_ENV`, `ENV` or `ZDOTDIR` value under the overmount home (`base_image.overmount_home`, `/sandbox` by default), or one that is not a plain absolute path (`$HOME/.bashrc`, `~/.shrc`), fails the plan, because a shell would read its startup file from the mounted workspace. When the base image's `PATH` names a directory under the overmount home, the plan sets the image `PATH` to the base image's `PATH` without those directories and replaces a component `PATH` value with it, with a notice (see [the workspace path](#the-workspace-path)). |
| `dirs` | Directories to create, each with `path` and an `owner` of `user` (default) or `root`. |
| `runtime_globs` | Absolute path globs appended to the `binaries` of every profile the component references, for interpreters and real binaries behind proxies, for example `/opt/mise/installs/node/*/bin/node`. |

Nothing a component installs may live under `/sandbox`. The OpenShell Kubernetes driver mounts the workspace volume over that path.

### Component tiers

Component definitions come from three tiers. A later tier overrides an earlier one with the same key.

| Order | Tier | Location |
|-------|------|----------|
| 1 | `shipped` | `components/` in the sandcast repository, embedded in the binary |
| 2 | `registry` | the cached remote component registry at the active tag |
| 3 | `project` | `.sandcast/components/<key>.yaml` in the project |
| 3 | `project` | `.sandcast/recipes/<key>.yaml` in the project, written by `sandcast promote`; a recipe overrides a component in `.sandcast/components/` with the same key |

When a definition of a component in the manifest overrides another, the plan records a notice naming the winning and the losing file. `sandcast components list` shows the winning tier of every key, and `sandcast components show <key>` prints the effective definition.

### Remote component registry

The remote registry lets component definitions change without a new sandcast release. sandcast never contacts it during a plan or a build. `sandcast components refresh` is the only command that downloads from it.

| Item | Value |
|------|-------|
| Base URL | `https://raw.githubusercontent.com/rhuss/sandcast-components`, overridable with `SANDCAST_COMPONENT_REGISTRY_URL` |
| Default tag | `v0.1.0`, overridable with `sandcast components refresh --ref <tag>` |
| Index | `<base>/<tag>/index.yaml` with `schema_version: 1`, `release`, and `files` (each with `path` and `sha256`) |
| Cache | `<cache dir>/components/<tag>/` |
| Active tag | `<cache dir>/components/current`, written by the last refresh that installed its files, including one that rejected some files and exited 1; the pinned tag until the first refresh |

A file whose sha256 differs from the index, that is larger than 1 MiB or that does not parse as a component is rejected and listed with its reason in `<cache dir>/components/<tag>/rejected.txt`. The copy of that file previously cached under the same tag is kept when there is one, so refreshing to a new tag can drop a rejected component from the registry tier. A file that cannot be downloaded, or an interrupt, leaves the whole cache and the active tag unchanged. Plans report rejected files as notices. Until the first refresh the registry tier is empty.

## Profile tiers

OpenShell provider profiles come from three tiers. A later tier overrides an earlier one with the same id. Two files with the same id inside one tier are an error.

| Order | Tier | Location | Content |
|-------|------|----------|---------|
| 1 | `upstream` | `profiles/upstream/v0.1.3/`, embedded | The 17 example profiles of OpenShell v0.1.3, unmodified |
| 2 | `overlay` | `profiles/overlay/`, embedded | `npm`, `crates-io`, `go-proxy`, `gitlab`, `claude-code-subscription` |
| 3 | `project` | `.sandcast/profiles/` in the project | Your own profiles in the same schema |

Profile files use the upstream OpenShell provider profile schema without extensions. When a referenced profile id is defined in more than one tier, the plan records which file won. Besides the three tiers, a build generates an `mcp-<name>` profile for every harness MCP server whose endpoint no agent profile covers; it appears in the adapted set with the tier `project` (see [harness MCP servers](guides/profiles.md#harness-mcp-servers)). The [profiles guide](guides/profiles.md) covers lint, adaptation and import.

## The lockfile

`.sandcast/mise.lock` pins every mise-installed component to an exact version, with a checksum and a download URL per platform. It is mise's own lockfile format (version 3). Only `sandcast lock` writes it, through the host mise; commit it with the manifest.

| Build platform | mise platform in the lockfile |
|----------------|-------------------------------|
| `linux/amd64` | `linux-x64` |
| `linux/arm64` | `linux-arm64` |

The lockfile covers the mise tools of every target the manifest declares. `sandcast plan` checks it for the selected platform: a missing lockfile, a tool without an entry that matches its version selector, or an entry without a checksum and a URL for the platform exits 2 with the platform and the tool named. The one exception is the `core:rust` backend, which is locked by version only because rustup verifies the toolchain download itself. Run `sandcast lock --platform <platform>` to extend the lockfile, and `sandcast lock --bump` to move `latest` and prefix selectors to newer releases.

From the lockfile and the components, the plan generates `mise/mise.toml` for the image:

```toml
# Generated by sandcast. Do not edit: change the manifest and run sandcast plan.

[settings]
lockfile = true
locked = true
lockfile_platforms = ["linux-arm64"]
use_versions_host = false
trusted_config_paths = ["/etc/mise"]

[tools]
fzf = "latest"
go = "latest"
starship = "latest"
```

## The sandcast directory

All sandcast files in a project live under `.sandcast/`. The top level holds committed inputs, and `build/` holds disposable outputs. A checkout with only the committed inputs is enough for a non-interactive `sandcast plan`, `sandcast build` and `sandcast verify`.

| Path | Committed | Written by | Content |
|------|-----------|------------|---------|
| `manifest.yaml` | yes | capture skill, `init`, `manifest set-shell`, you | The manifest. |
| `mise.lock` | yes | `lock` | The tool lockfile. |
| `fragments/<key>.containerfile` | yes | `fragments write` | Install fragments for unresolved components, with a header line `# sandcast-fragment: key=<key> written=<date> status=active\|superseded`. |
| `recipes/<key>.yaml` | yes | `promote` | Project-local components promoted from working fragments. |
| `learnings.yaml` | yes | `learnings add` | A list of entries with `attempt`, `component`, `failure`, `change`, `outcome` (`fixed` or `unfixed`) and `date`. |
| `components/<key>.yaml` | yes | you | Project-local component definitions. |
| `profiles/<id>.yaml` | yes | you | Project-local provider profiles. |
| `.gitignore` | yes | `init`, `plan`, `build` | Contains `build/`. |
| `build/<target>/` | no | `plan`, `build`, `verify` | Build outputs, see below. |

### Build outputs

`.sandcast/build/<target>/` holds the outputs of the last plan, build and verify for one target. The directory is ignored by git and can be deleted at any time.

| File | Written by | Content |
|------|------------|---------|
| `plan.yaml` | `plan`, `build` | The build plan. |
| `mise/mise.toml` | `plan`, `build` | The generated mise configuration, when mise tools are planned. |
| `mise/mise.lock` | `plan`, `build` | A copy of `.sandcast/mise.lock` for the build context. |
| `policy.yaml` | `plan`, `build` | The image-embedded OpenShell policy (OpenShell target only). |
| `Containerfile` | `build` | The pass-1 image definition. |
| `Containerfile.final` | `build` | The pass-2 image definition that adds the adapted profiles. |
| `shell/sandcast.zsh` or `shell/sandcast.sh` | `build` | The curated shell configuration for the build context. |
| `shell/sandcast-env.sh` | `build` | The session environment script, installed as `/etc/profile.d/sandcast-env.sh` (see [the session environment](guides/build.md#the-session-environment)). |
| `dotfiles/<encoded target>` | `build` | The redacted content of each baked dotfile, staged for the build context under its relocation target with `/` and `%` percent-encoded (for example `dotfiles/etc%2Fgitconfig`). |
| `harness/` | `build` | Harness files staged for the build context. |
| `build.log` | `build` | The engine output of both passes. |
| `profiles/<id>.yaml` | `build`, `profiles export` | The adapted provider profiles. |
| `profiles/index.yaml` | `build`, `profiles export` | The adapted profile set index. |
| `verify.yaml` | `verify`, `build --verify` | The verify report: `image`, `results` (per component `component_key`, `ok`, `status`, `version`, `output`), `passed`, `failed`. |
| `allowed-domains.txt` | `build` | Sorted, unique endpoint hosts of the referenced profiles (container target only; the name follows `allowed_domains_file`). |

After a successful build, sandcast scans every file in the build directory for credential-shaped values and fails the build with the file and location of each finding.

## Cache directory

sandcast keeps per-user state that is shared across projects in its cache directory: `$SANDCAST_CACHE_DIR` when set, else `sandcast` inside the operating system's user cache directory (`~/Library/Caches/sandcast` on macOS, `$XDG_CACHE_HOME/sandcast` or `~/.cache/sandcast` on Linux).

```text
<cache dir>/
├── probe/
│   ├── facts/<digest-hex>_<os>-<arch>.json
│   └── checks/<digest-hex>_<os>-<arch>/<component-key>-<check-hash>.json
└── components/
    ├── current
    └── <tag>/
```

Probe results are keyed by the image digest together with the platform, because docker reports the same index digest for every architecture of a multi-platform image. Image facts (operating system, package manager, default user, shells) are stored once per digest and platform. Each component check is stored separately under a hash of its command and expected outcome, so adding or changing a component probes only that component. A plan whose components are all cached starts no container. `sandcast plan --no-cache` and `sandcast probe --no-cache` discard the entries of the digest and probe again. Deleting the directory is always safe.

## Environment variables

| Variable | Used by | Meaning |
|----------|---------|---------|
| `SANDCAST_CACHE_DIR` | every command that probes or reads the component registry | Overrides the [cache directory](#cache-directory). |
| `SANDCAST_CONFIG_DIR` | `base-images list`, `init`, `probe`, `plan`, `build` | Overrides the configuration directory that holds the user `base-images.yaml`. |
| `XDG_CONFIG_HOME` | the same | When set and `SANDCAST_CONFIG_DIR` is not, the configuration directory is `$XDG_CONFIG_HOME/sandcast`. |
| `SANDCAST_COMPONENT_REGISTRY_URL` | `components refresh` | Overrides the base URL of the remote component registry. |
| `OPENSHELL_GATEWAY`, `OPENSHELL_GATEWAY_ENDPOINT` | `profiles import` | Not read by sandcast. The `openshell` CLI inherits them and uses them to select the gateway. |
| `CONTAINER_CONNECTION`, `CONTAINER_HOST` | every command that runs podman | Not read by sandcast. podman inherits them and builds, runs and pushes on the selected connection, for example a remote amd64 host. The `emulation` notice compares the target platform with that host's architecture. See the [cross-architecture guide](guides/cross-arch.md). |
| `DOCKER_HOST` | every command that runs docker | Not read by sandcast. docker inherits it, with the same effect as the podman variables. |
| `SANDCAST_NO_SHELL_SWITCH` | zsh images (`shell.program: zsh`, or a `zsh` component without a shell section) | Not read by sandcast. Set in a sandbox's environment, it keeps an interactive OpenShell session in bash instead of handing it to the curated zsh: the shell hooks start `bash --noprofile --norc -i` with the session environment, which reads no startup file from the workspace home either. A bash started inside the curated zsh stays bash without it (`SANDCAST_SESSION`). |
