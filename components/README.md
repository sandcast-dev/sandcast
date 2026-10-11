# Shipped components

A component tells sandcast how to detect, install, check and allow one tool in a sandbox image.
This directory holds the components that ship with sandcast.
They are embedded in the binary and form the lowest of three tiers.

| Tier | Location | Notes |
|------|----------|-------|
| shipped | `components/` in this repository | Embedded in the binary. |
| registry | cache of the remote component registry | Refreshed only on an explicit command. |
| project | `.sandcast/components/` and `.sandcast/recipes/` in the project | Recipes are written by the promote step. |

A later tier overrides an earlier one by key, so a project can replace any shipped component with its own definition.

## File format

Each component is one YAML file named `<key>.yaml`.
The schema is `specs/001-sandcast-standalone-build/contracts/component.schema.yaml`, and the loader rejects unknown fields.

| Field | Required | Meaning |
|-------|----------|---------|
| `key` | yes | Kebab-case identifier, unique within a tier. It must match the file name. |
| `name` | yes | Display name. |
| `description` | no | One line shown in component listings. |
| `match` | no | When capture proposes the component (see below). |
| `installer` | yes | Exactly one of `mise`, `package` or `script`. |
| `check` | yes | The probe command and its expected outcome. |
| `image` | no | What the component needs from the image: policy paths, environment, directories, runtime globs. |
| `provides` | no | Binary names, used to detect two components that install the same binary. |
| `profiles` | no | OpenShell provider profile ids the component needs. |
| `targets` | no | `openshell`, `container`, or both (the default when omitted). |
| `promoted_from` | no | Set by the promote step on recipes. Never set it by hand. |

### match

- `files`: glob patterns relative to the project root, for example `go.mod` or `requirements*.txt`.
- `host_tools`: executable names looked up on the host `PATH`, for example `rg` for ripgrep.
- `harnesses`: agent harness names chosen during capture, for example `claude-code`.
- `always: true`: the component is part of every image of its targets. Only base system components use it.

### installer

`mise` is the default and the preferred installer.
It takes `tool` (a mise registry short name or `backend:name`), `version` (a selector, `latest` when omitted) and `bins` (the executables the tool installs).
Prefer a registry short name that resolves to a checksummed `aqua` or `core` backend, because the build installs from a lockfile with a checksum and a URL per platform.

`package` installs distribution packages through the package manager the probe found in the base image.
It takes `packages`, a map from `apt-get`, `dnf`, `yum` and `apk` to package lists, and `bins`.
Shipped package components list all four package managers, because names differ (for example `iproute2` on Debian and Alpine, `iproute` on Fedora and RHEL).

`script` is the escape hatch for tools that neither mise nor a package manager can install.
It takes `run` (a shell script), `as_root` (false by default) and `bins`.
Recipes written by the promote step use this kind.

### check

`command` is a POSIX `sh` command that runs inside the image as the final user, with standard input closed and standard error merged into standard output.
It must exit 0 only when the tool works, and it should print the version on the first line.
sandcast takes the version from the first line whose first number contains a dot, so output such as `go version go1.26.7`, `v24.15.0` or `jq-1.8.2` parses, while `iproute2-6.15.0` does not (the first number on that line is the `2` of the name).

`expect` refines the outcome:

- `exit`: the expected exit code, 0 when omitted.
- `version`: a version selector, used only when neither the manifest nor `installer.mise.version` sets one.
- `stdout_contains`: a string the output must contain, for example to tell two unrelated tools with the same name apart.

The same check decides whether the base image already provides the component and whether the built image works.
A check that is too lenient leaves a broken tool in place, so make it fail for the cases that matter.
The shipped components do this in several places: `python` requires pip, `node` requires npm, `fzf` requires the `--zsh` option that shell configurations use, `yq` requires the Mike Farah implementation, and `iproute2` rejects the busybox `ip` applet.

### image

- `read_only` and `read_write`: absolute paths added to the image filesystem policy. The upstream default policy does not include `/opt`, so a component that installs outside `/opt/mise` must list its paths.
- `env`: environment variables set in the image. Variables that change where an installer writes, such as `CARGO_HOME`, must be in effect when the tool is installed.
- `dirs`: directories to create, each with an `owner` of `user` (the default) or `root`.
- `runtime_globs`: absolute path globs appended to the `binaries` of every profile the component references. Use them for interpreters and for real binaries behind proxies or symlinks, for example `/opt/mise/installs/node/*/bin/node`.

Nothing may live under `/sandbox`.
The OpenShell Kubernetes driver mounts the workspace volume over that path, which hides anything the image put there.
Tools go under `/opt/mise`, and caches or homes that a component relocates go under other `/opt` directories, as `go` does with `/opt/go`.

### provides

`provides` lists the binaries the component puts on the `PATH`.
When two selected components provide the same binary, the plan reports a conflict and does not render the image definition.
When `provides` is empty, sandcast falls back to the installer `bins`, and then to the key.

### profiles

`profiles` lists OpenShell provider profile ids.
Each id must resolve in a profile tier at plan time: the vendored upstream profiles, the sandcast overlay (`npm`, `crates-io`, `go-proxy`, `gitlab`), or the project profiles.
After the build, sandcast copies each referenced profile and rewrites its `binaries` to the resolved paths in the image.

A component never carries network endpoints.
The loader rejects a top-level `endpoints` key by name.
Network access belongs in an OpenShell profile, so add or extend a profile in the overlay or the project tier instead.

## Adding a component

1. Pick the key and create `<key>.yaml` with the same key.
2. For a mise tool, confirm the registry entry with `mise registry <name>` and prefer the entry that maps to an `aqua` or `core` backend.
3. Confirm that the tool locks for both Linux platforms: put it in a scratch `mise.toml` and run `mise lock --platform linux-x64,linux-arm64`. Every tool entry needs a checksum and a URL for each platform.
4. Write a check that fails when the tool is missing or unusable and prints the version on its first line. Run it as a non-root user in an image without the tool and in an image with it.
5. List the profile ids the tool needs to reach the network, and add `runtime_globs` when the process that opens connections is not the binary that `bins` names.
6. Run `make test`. The test in `internal/component/shipped_test.go` loads every file in this directory and checks keys, profile ids, installer fields, conflicts and the `/sandbox` rule. Add the new key to its list of shipped keys.

## Shipped set

| Key | Installer | Tool or packages | Profiles | Targets |
|-----|-----------|------------------|----------|---------|
| `go` | mise | `go` | `go-proxy` | both |
| `python` | mise | `python` | `pypi` | both |
| `node` | mise | `node` | `npm` | both |
| `rust` | mise | `rust` | `crates-io` | both |
| `uv` | mise | `uv` | `pypi` | both |
| `pnpm` | mise | `pnpm` | `npm` | both |
| `starship` | mise | `starship` | | both |
| `fzf` | mise | `fzf` | | both |
| `lsd` | mise | `lsd` | | both |
| `ripgrep` | mise | `ripgrep` | | both |
| `bat` | mise | `bat` | | both |
| `zoxide` | mise | `zoxide` | | both |
| `jq` | mise | `jq` | | both |
| `yq` | mise | `yq` | | both |
| `gh` | mise | `gh` | `github` | both |
| `claude-code` | mise | `claude` | `claude-code` | both |
| `codex` | mise | `codex` | `codex` | both |
| `opencode` | mise | `opencode` | | both |
| `zsh` | package | `zsh` | | both |
| `bash` | package | `bash` | | both |
| `git` | package | `git` | `github`, `gitlab` | both |
| `curl` | package | `curl` | | both |
| `ca-certificates` | package | `ca-certificates` | | both |
| `iproute2` | package | `iproute2`, `iproute` on dnf and yum | | openshell |
| `nftables` | package | `nftables` | | openshell |
