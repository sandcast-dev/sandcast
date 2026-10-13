# Command reference

This page lists every `sandcast` command and flag. Run `sandcast <command> --help` for the same information in the terminal.

```text
sandcast [global flags] <command> [<subcommand>] [flags] [arguments]
```

Commands are grouped the way they are used:

- [Core build](#core-build): `init`, `manifest validate`, `base-images list`, `components list`, `components show`, `components refresh`, `lock`, `probe`, `plan`, `build`, `verify`, `profiles lint`, `profiles export`, `profiles import`, `version`
- [Capture and harness](#capture-and-harness): `components match`, `harness list`, `harness discover`, `skills install`, `skills check`, `manifest set-shell`
- [Repair and promotion](#repair-and-promotion): `fragments list`, `fragments write`, `promote`, `learnings add`
- [Container target](#container-target): `build --target container`

`sandcast completion <bash|zsh|fish|powershell>` prints a shell completion script, and `sandcast help <command>` prints the help of a command.

## Global flags

Every command accepts these flags.

| Flag | Default | Meaning |
|------|---------|---------|
| `--project <dir>` | the nearest ancestor of the current directory that contains `.sandcast/`, else the current directory | The project directory. An explicit value is used as is, without searching ancestors. |
| `--engine <podman\|docker>` | the manifest field `engine`, then podman when it is on the `PATH`, then docker with a notice | The container engine. |
| `--json` | off | Print one JSON document to stdout. Commands without JSON output ignore the flag. |
| `--verbose` | off | Print progress details and every container engine command line to stderr. `sandcast build --verbose` also streams the engine output. |

## Exit codes

| Code | Meaning | Typical triggers |
|------|---------|------------------|
| 0 | Success | |
| 1 | The operation failed | The image build failed; a verify check failed; `lock` could not lock every tool; `components refresh` rejected a file; `profiles import` found existing profile ids; a promotion was declined; the base image could not be pulled or probed; another sandcast run holds the build lock for the target (`another sandcast build is running for target <target> in <dir>; wait for it to finish`). |
| 2 | Usage or validation error | Unknown flags or a wrong number of arguments; an `--engine` value other than `podman` or `docker`; no manifest; an invalid manifest or an unsupported `schema_version`; an unsupported platform; a target the manifest does not declare; a lockfile without an entry for the selected platform; a component that references an unknown profile id; a binary conflict between components; blocking unresolved components at build time; profile lint diagnostics; an invalid fragment or learnings entry; a fragment that cannot be promoted; `verify` without a plan; `profiles import` without an adapted profile set; an unknown harness name. |
| 3 | External tool missing or at the wrong version | No container engine (or the selected one is not on the `PATH`); host mise missing or older than 2026.9.16; the `openshell` CLI missing or not on the 0.1.x line (pinned to v0.1.3); installed skills that do not match the binary. |
| 130 | Interrupted | SIGINT or SIGTERM. sandcast stops the running engine or `openshell` command with SIGTERM, waits up to 15 seconds, and keeps the pass-1 image as `<image>:sandcast-debug` when a build was interrupted after pass 1; image cleanup is limited to 30 seconds. A second Ctrl-C ends sandcast at once. |

## Output conventions

Human-readable output goes to stdout as aligned tables or `key: value` lines. Progress lines, notices and diagnostics go to stderr, for example `probe cache hit: no container started` or `notice: podman not found on PATH; using docker`.

With `--json`, a command prints exactly one JSON document to stdout, with two-space indentation. Diagnostics still go to stderr. These commands support `--json`: `version`, `manifest validate`, `base-images list`, `components list`, `components show`, `components match`, `components refresh`, `lock`, `probe`, `plan`, `build`, `verify`, `profiles lint`, `profiles export`, `profiles import`, `harness list`, `harness discover`, `skills install`, `skills check` and `fragments list`. The commands that change files on request (`init`, `manifest set-shell`, `fragments write`, `promote`, `learnings add`) print one confirmation line and ignore `--json`.

Errors are printed as `sandcast: <message>` on stderr. A command that has already reported its problems in its regular output (for example `manifest validate` or `verify`) exits without an extra error line.

## Targets and platforms

Commands that work on one target take `--target openshell` or `--target container`. The default is `openshell` when the manifest declares it, otherwise `container`. A target the manifest does not declare exits 2.

Commands that pull or build images take `--platform linux/amd64` or `--platform linux/arm64`. The platform is selected in this order: the `--platform` flag, the manifest field `platform`, then the host architecture as `linux/<arch>`. The plan records the value and its source (`flag`, `manifest` or `host`).

## Core build

### sandcast init

Create `.sandcast/` with a manifest skeleton and `.sandcast/.gitignore`.

```text
sandcast init [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--name <name>` | the directory name, converted to kebab-case | Manifest name. |
| `--base-image <name-or-ref>` | the registry default for the target | Registry entry name or image reference. |
| `--target <openshell\|container>` | `openshell` | The target the skeleton declares. |
| `--force` | off | Overwrite an existing manifest. |

`init` works in `--project` or the current directory and never searches ancestors. The skeleton declares the image `localhost/<name>` for the chosen target and an empty component list. An existing manifest without `--force` exits 2.

```console
$ sandcast init --name demo
created .sandcast/manifest.yaml (base image nvidia-community-base, target openshell)
created .sandcast/.gitignore
```

### sandcast manifest validate

Validate `.sandcast/manifest.yaml`: the schema, the schema version, the component and profile references, and credential-shaped values.

```text
sandcast manifest validate
```

Each component key must resolve in a component tier or have an active stored fragment, unless the entry is `optional: true`. Each profile id those components reference must resolve in a profile tier. An unknown harness name is reported. The container target's `allowed_domains_file`, when set, must be a plain file name that does not start with a dot and is not a name sandcast writes in the build directory itself; the openshell target must not set it at all. Every diagnostic carries the YAML path and, when known, the line number. Any diagnostic exits 2.

```console
$ sandcast manifest validate
.sandcast/manifest.yaml:9: $.components[4].key: component "nosuchtool" is not defined in any tier (shipped, registry, .sandcast/components, .sandcast/recipes) and has no stored fragment
```

The JSON report has `valid`, `manifest` and `diagnostics` (each with `path`, `line` and `message`).

### sandcast base-images list

List the merged base image registry: the shipped entries plus the user file `base-images.yaml` in the sandcast configuration directory (see [the configuration reference](configuration.md#base-image-registry)).

```text
sandcast base-images list [--target <openshell|container>]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--target <openshell\|container>` | both targets | Only list the entries of one target type. |

The default entry of each target is listed first.

```console
$ sandcast base-images list
TARGET     NAME                   DEFAULT  REFERENCE
container  fedora-42              yes      registry.fedoraproject.org/fedora:42
container  ubuntu-24.04                    docker.io/library/ubuntu:24.04
openshell  nvidia-community-base  yes      ghcr.io/nvidia/openshell-community/sandboxes/base:latest
openshell  nvidia-ubuntu                   nvcr.io/nvidia/base/ubuntu:24.04
```

### sandcast components list

List the winning definition of every component key across the component tiers.

```text
sandcast components list [--tier <shipped|registry|project>]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--tier <shipped\|registry\|project>` | all tiers | Only list components whose winning tier is the given one. |

The table shows the key, the winning tier, the installer kind (`mise`, `package` or `script`), the referenced profile ids and the source file. The JSON output adds the name and the match conditions of each component.

### sandcast components show

Print the effective definition of one component as YAML, preceded by a comment with its source file and tier.

```text
sandcast components show <key>
```

An unknown key exits 2.

```console
$ sandcast components show go
# components/go.yaml (shipped tier)
key: go
name: Go
...
profiles:
  - go-proxy
```

### sandcast components refresh

Fetch the remote component registry at the pinned or a given tag and verify every file against the checksums in its index. This is the only command that downloads component definitions; plan and build never do.

```text
sandcast components refresh [--ref <tag>]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--ref <tag>` | the pinned `v0.1.0` | Registry release tag to fetch. |

The base URL is `https://raw.githubusercontent.com/rhuss/sandcast-components`, overridable with `SANDCAST_COMPONENT_REGISTRY_URL`. The command fetches `<base>/<tag>/index.yaml` and every file it lists, with a 60 second limit per request. A file whose sha256 does not match the index, that is larger than 1 MiB or that is not a valid component definition is rejected, and the previously cached copy of that file is kept when there is one. A file that cannot be downloaded, or an interrupt, leaves the cache and the active tag unchanged. Any rejection exits 1, in text and in JSON mode. A refresh that installed its files makes `<tag>` the active registry tag for later commands.

The summary line has the form `registry <tag>: <n> accepted, <m> rejected (cache <dir>)`, followed by one `rejected: <file> (<reason>)` line per rejected file and the kept copies when there are any. When a download failed, only the `rejected:` lines are printed, because nothing was installed. The JSON report adds `reasons`, which maps each rejected file to its reason, and `installed`, which is false when the cache was left unchanged.

### sandcast lock

Resolve every mise tool the manifest needs, for all declared targets, with the host mise and write `.sandcast/mise.lock` with exact versions and a checksum and URL per platform.

```text
sandcast lock [--platform <platform>]... [--bump]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--platform <platform>` | the manifest `platform`, else the host | `linux/amd64` or `linux/arm64`. Repeat the flag or separate values with commas to lock several platforms. |
| `--bump` | off | Re-resolve fuzzy versions such as `latest` or `1.25` to the newest matching release. Without it, versions already in the lockfile are kept. |

`lock` runs `mise lock` in a temporary workspace that is isolated from your own mise configuration. When one tool cannot be locked (for example offline or without a matching release), the others are still locked, the failed ones are listed, and the command exits 1. A missing host mise or one older than 2026.9.16 exits 3.

```console
$ sandcast lock --platform linux/arm64
.sandcast/mise.lock written for [linux-arm64]: 4 tool(s) locked
```

### sandcast probe

Probe a base image and print its facts and the probe delta for the manifest's components.

```text
sandcast probe [<ref>] [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--platform <platform>` | the manifest `platform`, else the host | Platform to pull and probe. |
| `--target <openshell\|container>` | the default target | Target whose components are checked. |
| `--no-cache` | off | Discard the cached results for this image digest and platform, and probe again. |

Without `<ref>`, the command probes the manifest's base image. With `<ref>` and no manifest, it prints only the image facts. The facts are the operating system, the package manager, the default user and the available shells. A cache hit prints `probe cache hit: no container started` on stderr.

### sandcast plan

Probe the base image, resolve components and profiles from their tiers, check the lockfile, and write the build plan.

```text
sandcast plan [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--target <openshell\|container>` | the default target | Target to plan. |
| `--platform <platform>` | the manifest `platform`, else the host | Target platform. |
| `--no-cache` | off | Discard the cached probe results for the base image digest and probe again. |
| `--out <dir>` | `.sandcast/build/<target>` | Write the plan outputs into this directory instead. |

`plan` writes `plan.yaml`, `mise/` (when mise tools are planned) and `policy.yaml` (OpenShell target). The plan is deterministic: the same manifest, lockfile, definitions, base image digest and platform give byte-identical output. A lockfile gap, an unknown profile reference or an unsupported schema version exits 2 and writes nothing. Binary conflicts exit 2 after printing the plan summary (or the JSON plan), and nothing is written. Unresolved components do not fail the plan; they are listed so that you or the build skill can supply fragments.

```console
$ sandcast plan
probing ghcr.io/nvidia/openshell-community/sandboxes/base:latest for linux/arm64
probe cache hit: no container started
target:           openshell
platform:         linux/arm64 (from host)
engine:           podman (detected)
base image:       ghcr.io/nvidia/openshell-community/sandboxes/base:latest [nvidia-community-base]
digest:           sha256:04dd51f7...
package manager:  apt-get
final user:       sandbox

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

PROFILE      TIER      SOURCE
claude-code  upstream  profiles/upstream/v0.1.3/claude-code.yaml
go-proxy     overlay   profiles/overlay/go-proxy.yaml
notice: added zsh implicitly: shell program
...
wrote .sandcast/build/openshell/plan.yaml
```

With `--json`, the command prints the plan itself, with the same fields as `plan.yaml`.

Each `probe_delta` entry of a mise component that `mise.lock` locks for the target platform carries `locked_version`, and entries the workspace rule changed carry `ignored_paths`. Notices with a kind have the form `<kind>: <component or image>: <message>`, follow the other notices sorted by kind and component, and appear as `notice:` lines:

| Kind | Meaning |
|------|---------|
| `lockfile-shadow` | The base image has a different version than `mise.lock`; the locked version is installed. |
| `unknown-version` | The base image's version could not be read, or came from a copy under the workspace path; the locked version is installed. |
| `workspace-path` | A binary under the workspace path (the final user's home, OpenShell target) was ignored. |
| `home-rc-path` | A home directory rc file of the base image resets `PATH`. |

See [the build guide](guides/build.md#the-probe-delta) for the rules.

### sandcast build

Plan, assemble the Containerfile from the plan and the stored fragments, build the image in two passes, and write the adapted profile set.

```text
sandcast build [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--target <openshell\|container>` | the default target | Target to build. |
| `--platform <platform>` | the manifest `platform`, else the host | Target platform. |
| `--verify` | off | Run every component check in the built image afterwards, plus the session checks for the OpenShell target (see [sandcast verify](#sandcast-verify)). |
| `--push` | off | Push the final image. The target field `push: true` has the same effect. |
| `--no-cache-layers` | off | Build pass 1 without the engine layer cache. |
| `--keep-stage` | off | Keep the pass-1 image as `<image>:sandcast-stage`. |

`build` runs the plan first, using the probe cache. It refuses to start with exit 2 when the plan has binary conflicts or blocking unresolved components; optional unresolved components are skipped with a message. Pass 1 builds the full image under a per-run stage tag, `<image>:sandcast-stage-<random>`, so a concurrent build of the same image name does not replace it between the passes. sandcast then resolves every component binary inside that image, writes the adapted profiles, and pass 2 adds them under `/usr/share/sandcast/profiles` together with the `io.sandcast.*` labels under the final tag. The [build guide](guides/build.md) describes each step.

A failed build exits 1, leaves `Containerfile` and `build.log` in the build directory, and is never retried. Without `--verbose` it prints the last 25 lines of `build.log` to stderr. When pass 1 completed and a later step failed, the pass-1 image is kept as `<image>:sandcast-debug`. With `--verify`, a failed check also exits 1 after every result has been reported.

```console
$ sandcast build --verify
building localhost/demo:latest (log: .sandcast/build/openshell/build.log)
image:          localhost/demo:latest
containerfile:  .sandcast/build/openshell/Containerfile
build log:      .sandcast/build/openshell/build.log
profiles:       .sandcast/build/openshell/profiles

COMPONENT        RESULT  VERSION  OUTPUT
...
verify localhost/demo:latest: 9 passed, 0 failed
```

The JSON report has `image`, `stage_image`, `containerfile`, `build_log`, `profiles`, `flagged` (adapted ids of profiles without a resolved binary), `allowed_domains` (container target), `verify`, `resolutions` (component, binary, resolved path and method), `notices` and `error`. `notices` repeats the plan notices (see [sandcast plan](#sandcast-plan)); the console prints each one as a `notice:` line before the verify report. With `--verify`, the verify report includes the session checks and their `condition`.

### sandcast verify

Run every component check inside the built image as the image's final user, under the container engine. No gateway is needed.

```text
sandcast verify [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--target <openshell\|container>` | the default target | Target whose plan supplies the checks. |
| `--image <ref>` | the target's `image:tag` | Image to verify. |

`verify` reads the checks from `.sandcast/build/<target>/plan.yaml`; without a plan it exits 2. It reports every result, writes `verify.yaml` next to the plan, and exits 1 when any check failed.

For the OpenShell target, verify always runs session checks as well, in the same container and without a flag to skip them. They reproduce the environment OpenShell gives a session (`env -i` with only `HOME`, `USER`, `TERM=xterm-256color` and `PATH=/usr/local/bin:/usr/bin:/bin`) and run each component check again: `session.bash-login.<component>` through `bash -lc`, `session.bash-interactive.<component>` fed on stdin to `bash -i`, and with `shell.program: zsh` also `session.zsh.<component>` through `zsh -i -c`. The startup check of the selected shell program, `session.zsh.startup` for zsh or `session.bash.startup` for bash, fails when the interactive shell writes anything to stderr during startup or does not read the curated rc. Notes a shell prints only because verify runs it without a terminal are ignored: bash's `no job control in this shell` lines and zsh's `can't change option: zle`. Session checks count toward `failed` and the exit status. The [build guide](guides/build.md#the-session-environment) explains the session environment.

The table has one row per component with the columns `COMPONENT`, `RESULT` (`ok`, or `FAIL (<status>)` with the status `missing`, `incompatible` or `timeout`), `VERSION` and `OUTPUT` (the first line of the check output). Session checks follow in a `session checks` group with the columns `CHECK`, `CONDITION`, `RESULT` and `OUTPUT`. A summary line closes the report:

```text
verify localhost/demo:latest: 9 passed, 0 failed
```

A `FAIL (timeout)` row is a check that did not finish within the check timeout; its output starts with `timeout:`, or with `not run:` for a check that never ran because the container was stopped first.

Every result in `verify.yaml` and in the JSON report carries a `condition`: `image` for the component checks with the image environment, and `session:bash-login`, `session:bash-interactive` or `session:zsh` for the session checks.

### sandcast profiles lint

Lint provider profiles with the built-in rules of OpenShell v0.1.3. No OpenShell tooling is needed.

```text
sandcast profiles lint [<file|dir>] [--target <openshell|container>]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--target <openshell\|container>` | the default target | Target whose adapted set is linted when no path is given. |

Without a path, the command lints `.sandcast/build/<target>/profiles/`. A directory argument lints every `.yaml` and `.yml` file in it except `index.yaml`. Each diagnostic is printed as `<file>: <profile id>: <field>: <message>`, followed by a summary line. Any diagnostic exits 2.

```console
$ sandcast profiles lint profiles/overlay
4 profile file(s), 0 diagnostic(s)
```

### sandcast profiles export

Re-write the adapted profile set from an existing image without rebuilding it. The command reads `/usr/share/sandcast/profiles` from the image in a throwaway container and writes `.sandcast/build/<target>/profiles/`.

```text
sandcast profiles export [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--target <openshell\|container>` | the default target | Target whose build directory receives the profiles. |
| `--image <ref>` | the target's `image:tag` | Image to read the profiles from. |
| `--suffix` | off | Suffix the profile ids with the image label. |

The output lists each mapping as `<upstream id> -> <adapted id> (<n> binaries)`. The JSON output is the profile set index, with the same keys as `index.yaml`.

### sandcast profiles import

Import the adapted profile set into an OpenShell gateway through the `openshell` CLI.

```text
sandcast profiles import [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--scope <workspace\|platform>` | `workspace` | Gateway scope. `platform` imports with `openshell profile import --global` and implies `--suffix`. |
| `--workspace <name>` | the CLI's current workspace | Workspace for workspace scope. |
| `--gateway <name>` | the CLI's own selection | OpenShell gateway name, passed to every `openshell` call as `--gateway <name>`. |
| `--suffix` | off | Suffix the profile ids with the image label. |
| `--update` | off | Replace profiles whose id already exists at the scope. |
| `--dry-run` | off | List what would be imported or updated without changing the gateway. |
| `--target <openshell\|container>` | the default target | Target whose adapted set is imported. |

The command checks `openshell --version` first and exits 3 unless it reports a release on the pinned minor line (any 0.1.x for the v0.1.3 pin), before any gateway call. It then lists the profiles at the chosen scope, imports every adapted profile whose id does not exist yet, one file at a time, and reports existing ids as `exists: <id> (left unchanged)`. Existing ids make the command exit 1 with the choices `--update`, `--suffix` or the other scope. `--dry-run` still checks the CLI version and lists the gateway's profiles, so it needs a reachable gateway.

After an import that placed the whole set at the gateway, sandcast rewrites `index.yaml` in `.sandcast/build/<target>/profiles/` so that it records each profile under the id it was imported with and the scope. A dry run, a run stopped by existing ids, or a failed `openshell` call leaves `index.yaml` unchanged.

The gateway is the one `--gateway` names. Without the flag, the `openshell` CLI picks it from its environment (`OPENSHELL_GATEWAY`, `OPENSHELL_GATEWAY_ENDPOINT`) or its active gateway.

```console
$ sandcast profiles import --scope workspace
imported: claude-code (workspace scope)
imported: go-proxy (workspace scope)
```

The JSON report has `scope`, `imported`, `updated`, `conflicts`, `dry_run`, `mapping` (`upstream_id` and `adapted_id` per profile) and `index` (the path of the rewritten `index.yaml`, omitted when the import changed nothing). When an `openshell` call fails part way, the command still prints the profiles the gateway already accepted, sets `error` in the JSON report, and exits 1.

### sandcast version

Print the binary version and the pinned upstream releases.

```console
$ sandcast version
sandcast:            1f31df9-dirty
openshell:           v0.1.3 (e1f3c82c)
mise:                v2026.10.4
component registry:  v0.1.0
skill bundle:        0.1.0
manifest schema:     1
plan schema:         1
```

## Capture and harness

These commands give the capture skill facts instead of guesses. They are also useful by hand.

### sandcast components match

Evaluate the match conditions of every component against a project directory and the host, and print proposals with their evidence.

```text
sandcast components match [<dir>] [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--host` | on | Detect host tools on the `PATH` and read their versions. |
| `--no-host` | off | Do not detect host tools. |
| `--harness <name>` | none | Selected harness. Proposes the components that match it. Repeatable. |

`<dir>` defaults to `--project`, else the current directory. Each proposal has a source: `project` (ecosystem files such as `go.mod`), `host` (a tool on the host `PATH`), `harness` (a selected harness) or `always` (base system components). Components that do not match are listed as available. An unknown harness name exits 2.

Project proposals carry `pinned_versions`: every version the project pins for the component's mise tool, each with its `value`, its `file` and its `kind` (`exact` for a full version without an operator, `channel` for a word such as `stable`, `nightly` or `lts/*`, `range` otherwise). The files read are `go.mod` and `go.work` (`go` and `toolchain` directives, the highest version first), `rust-toolchain.toml` and `rust-toolchain` (`channel`), `.python-version`, `pyproject.toml` (`requires-python`), `.nvmrc`, `.node-version`, `package.json` (`engines.node`), `.tool-versions` and `mise.toml` (`[tools]`). An entry in `.tool-versions` or `mise.toml` is evidence for the component that installs the tool. A file that cannot be parsed adds a line to `notes` (printed as `note:` lines) and the match still succeeds. The table's `PINNED` column shows the first exact value, else the first value with its kind.

```console
$ sandcast components match tests/fixtures/reference-project --no-host
COMPONENT        SOURCE   VERSION  PINNED          EVIDENCE
ca-certificates  always   -        -               always included
curl             always   -        -               always included
git              always   -        -               always included
go               project  -        1.25 (range)    go.mod
iproute2         always   -        -               always included
nftables         always   -        -               always included
python           project  -        >=3.12 (range)  pyproject.toml; uv.lock
uv               project  -        -               uv.lock

available: bash, bat, claude-code, codex, fzf, gh, jq, lsd, node, opencode, pnpm, ripgrep, rust, starship, yq, zoxide, zsh
```

### sandcast harness list

Show the known harness adapters, the component that installs each agent, and whether the harness is installed on the host.

```text
sandcast harness list
```

### sandcast harness discover

Run one adapter's discovery and print the locally configured MCP servers, plugins and skills of that harness. Secret values are replaced by `${SANDCAST_SECRET_N}` placeholders before anything is printed.

```text
sandcast harness discover <name>
```

Each replaced value is reported as `redacted: <path> (<kind>) -> <placeholder>; supply it as a provider credential at runtime`, and entries that cannot be baked are reported as `note:` lines. An unknown harness name exits 2. The [capture guide](guides/capture.md#what-discovery-finds) lists the files the Claude Code adapter reads.

### sandcast skills install

Install the sandcast skill bundle where a harness discovers it.

```text
sandcast skills install [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--harness <name>` | `claude-code` | Harness to install into. |
| `--scope <user\|project>` | `user` | `user` installs for all projects, `project` for the current project only. |
| `--force` | off | Overwrite installed skills even when they are the same version or newer. |

Each bundle skill is installed as `sandcast-<name>`, so the skills are invoked as `/sandcast-capture` and `/sandcast-build`. An installed skill with the same version is left unchanged, an older one is replaced, and a newer one is skipped with a hint unless `--force` is given. See the [capture guide](guides/capture.md#installing-the-skills) for the install locations.

### sandcast skills check

Compare the installed skills with the bundle in this binary.

```text
sandcast skills check [--harness <name>]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--harness <name>` | `claude-code` | Harness whose installed skills are checked. |

The project scope is checked first, then the user scope. When a skill is missing, older or newer than the bundle, the command prints which side to update and exits 3. Both skills run this command as their first step.

### sandcast manifest set-shell

Write a curated shell configuration into the manifest.

```text
sandcast manifest set-shell [--program <zsh|bash>] [--file <path>]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--program <zsh\|bash>` | `zsh` | Shell program, written to `shell.program`. |
| `--file <path>` | standard input | Read the configuration from this file. `-` also means standard input. |

The text is stored in `shell.rc`; `shell.components` is left as it is. A configuration with credential-shaped values is refused with exit 2.

```sh
sandcast manifest set-shell --program zsh --file /tmp/curated.zsh
```

## Repair and promotion

The build skill uses these commands, and they work the same way by hand. The [build guide](guides/build.md#fragments) explains fragments, learnings and promotion in detail.

### sandcast fragments list

Show the stored fragments in `.sandcast/fragments/` with their status (`active` or `superseded`) and the date they were written.

```console
$ sandcast fragments list
KEY          STATUS  WRITTEN     PATH
broken-tool  active  2026-10-08  .sandcast/fragments/broken-tool.containerfile
```

### sandcast fragments write

Store a Containerfile fragment for a component, read from standard input or a file.

```text
sandcast fragments write <key> [--file <path>]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--file <path>` | standard input | Read the fragment from this file. `-` also means standard input. |

The key must be a kebab-case component key. The fragment is validated before it is written, and a credential-shaped value is refused. A failed validation lists every problem and exits 2. The stored file replaces an earlier fragment for the same key and starts with a header line such as `# sandcast-fragment: key=mytool written=2026-10-08 status=active`.

```console
$ printf 'USER root\nRUN echo hi\n' | sandcast fragments write mytool
sandcast: invalid fragment:
  the fragment ends as USER root; switch back to the final user after system changes
```

### sandcast promote

Convert a working fragment into a project-local recipe at `.sandcast/recipes/<key>.yaml` and mark the fragment superseded.

```text
sandcast promote <key> [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--name <name>` | the existing component name, else the key | Display name of the recipe. |
| `--bins <bin,...>` | the existing definition's binaries, else the key | Binaries the recipe installs. |
| `--yes` | off | Promote without asking. |

Without `--yes`, the command prints the recipe and asks for confirmation on standard input. Any answer other than `y` or `yes` prints `not promoted` and exits 1. A fragment that cannot be expressed as a recipe exits 2.

```console
$ sandcast promote mytool --bins mytool --yes
promoted mytool to .sandcast/recipes/mytool.yaml; the fragment is marked superseded
```

### sandcast learnings add

Append a structured entry to `.sandcast/learnings.yaml`. The build skill records every failed attempt and every successful fix with it.

```text
sandcast learnings add [flags]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--component <key>` | required | Component key. |
| `--failure <text>` | required | What failed, summarized from the build log. |
| `--change <text>` | required | What changes for the next attempt. |
| `--attempt <n>` | `1` | Attempt number, 1 or more. |
| `--outcome <fixed\|unfixed>` | `unfixed` | Whether the change fixed the failure. |
| `--date <YYYY-MM-DD>` | today (UTC) | Date of the entry. |

An invalid entry, or one whose text contains a credential-shaped value, exits 2.

```console
$ sandcast learnings add --component mytool --attempt 1 --failure "tar: not found in archive" --change "strip one directory level"
recorded attempt 1 for mytool (unfixed) in .sandcast/learnings.yaml
```

## Container target

`sandcast build --target container` builds the same components as a plain container image for running directly under podman or docker. The manifest must declare `targets.container.image`.

```sh
sandcast build --target container --verify
```

The build applies no OpenShell conventions: no policy file, no `iproute2` and `nftables`, no created `sandbox` user and no `/sandbox` directory. The image keeps the base image's user (root on `fedora-42`) and uses that user's home as its working directory. The build writes `allowed-domains.txt` (or the name set in `targets.container.allowed_domains_file`) into `.sandcast/build/container/`, with the sorted, unique endpoint hosts of the referenced profiles. The build report shows the file as `allowed domains: <path>` and in the JSON field `allowed_domains`. `plan`, `verify`, `profiles lint` and `profiles export` accept `--target container` as well. The [build guide](guides/build.md#the-container-target) lists every difference.
