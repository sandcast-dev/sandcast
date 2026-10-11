# Research: sandcast Standalone Build

**Date**: 2026-10-08 | **Spec**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

Sources: upstream OpenShell at tag v0.1.2 (files read at the tag), mise documentation and the mise 2026.10.x binary run locally and inside a Debian 13 container, the locally installed `openshell` CLI 0.1.2, and a read-only survey of the cc-deck build package. Each decision lists what was chosen, why, and what else was considered.

## R-01: Go toolchain and libraries

**Decision**: Go 1.25 module (`go 1.25` directive, `toolchain go1.25.x` pinned). Libraries: `spf13/cobra` for the CLI, `goccy/go-yaml` for YAML, `BurntSushi/toml` for mise.toml and mise.lock, `opencontainers/go-digest` for digests, `stretchr/testify` for tests. No container client library and no OpenShell SDK.

**Rationale**: cc-deck already proved cobra plus exec-driven podman. goccy/go-yaml rejects unknown fields and reports line numbers, which the manifest and component loaders need for useful errors, and encodes struct fields in declaration order, which keeps plan output deterministic. The OpenShell gateway is only reached through the upstream CLI (clarification 5), so no gRPC SDK is needed. mise.lock is TOML with nested tables, which BurntSushi handles.

**Alternatives**: `gopkg.in/yaml.v3` (cc-deck's choice; works, but weaker unknown-field diagnostics and in maintenance mode). `google/go-containerregistry` for label stamping (cc-deck uses it; needs a Docker API socket, which breaks the podman-first rule, see R-09). `rhuss/openshell-sdk-go` (does not expose profiles at the local version; the CLI is the stable surface).

## R-02: Container engine abstraction

**Decision**: `internal/engine` wraps `podman` and `docker` through `os/exec` with one interface: `Pull(ref, platform)`, `Digest(ref)`, `Run(image, platform, script) (stdout)`, `Build(contextDir, containerfile, tag, platform, labels)`, `Tag`, `RemoveImage`. Selection order: command-line flag, manifest `engine` field, `podman` if on PATH, otherwise `docker` with a printed notice (FR-005). Flags used are the common subset: `pull`, `image inspect --format`, `run --rm --platform --entrypoint`, `build --platform -f -t --label`, `tag`, `rmi`.

**Rationale**: cc-deck's survey shows every call already goes through exec, and the multi-arch pitfall there came from passing two platforms to `-t`. One platform per build (clarification 1) removes the need for `podman manifest`. Docker and podman agree on every flag listed.

**Alternatives**: podman bindings or the Docker SDK (engine-specific, violates constitution V). `buildah` (podman-only).

## R-03: Base image probe and cache

**Decision**: Keep cc-deck's design: a POSIX `sh` script printing one JSON object per line (`os`, `pkgmgr`, `user`, `shells`, one `tool` line per component probe check), run with `--entrypoint /bin/sh`. Two changes: the probe is per component probe check rather than a fixed tool list, and the cache is keyed as the spec requires. Cache layout under `os.UserCacheDir()/sandcast/probe/`: `facts/<digest>.json` for image facts and `checks/<digest>/<component-key>-<sha256(probe check)[:12]>.json` per component. The digest is obtained after `pull --platform <p>` with `image inspect --format '{{.Digest}}'`; a missing digest is an error, never an empty cache key (cc-deck's silent-miss bug).

**Rationale**: FR-012 demands partial probes on component change; a per-check file makes that a directory listing. Platform is part of the pulled image, so the platform-specific digest already distinguishes architectures.

**Alternatives**: one JSON file per image ref (cc-deck; invalidates everything on any component change). Probing by `podman image mount` without running (needs root on Linux, impossible on macOS).

## R-04: mise as installer

**Decision**: Pin mise `v2026.10.4` (constant `MisePin`). The plan writes `mise.toml` with `[settings] lockfile = true, locked = true, lockfile_platforms = [<target>]` and `[tools]` entries from component installers. The host runs `mise lock --platform <mise platform>` through `sandcast lock` and the result is committed as `.sandcast/mise.lock`. In the image, the mise install layer is:

```dockerfile
ENV MISE_DATA_DIR=/opt/mise MISE_CONFIG_DIR=/etc/mise MISE_CACHE_DIR=/opt/mise/cache \
    MISE_STATE_DIR=/opt/mise/state MISE_TRUSTED_CONFIG_PATHS=/etc/mise \
    PATH=/opt/mise/shims:$PATH
COPY mise/mise.toml mise/mise.lock /etc/mise/
RUN curl -fsSL https://mise.run | MISE_VERSION=v2026.10.4 MISE_INSTALL_PATH=/usr/local/bin/mise sh \
 && install -d -o <user> -g <group> /opt/mise
USER <user>
RUN mise install --locked --yes && mise reshim
```

Platform mapping: `linux/amd64` → `linux-x64`, `linux/arm64` → `linux-arm64`. Locked installs fail on a missing platform entry or a checksum mismatch (verified), which is the behaviour the edge cases require.

**Rationale**: Verified facts: lockfile v3 carries per-platform `checksum` and `url`; a lockfile generated on macOS arm64 installs cleanly on linux-arm64 with `--locked`; `--locked` makes no GitHub API calls; shims on `PATH` resolve tools for non-interactive child processes (FR-031). `/opt/mise` instead of the user's home because OpenShell's Kubernetes driver mounts a workspace volume over `/sandbox`, which would mask anything installed under a `/sandbox` home. `/opt/mise` is not among OpenShell's reserved paths.

**Alternatives**: `mise bin-paths` baked into `PATH` (no shim overhead, but the paths are only known after install, so they cannot go into `ENV`). Installing as root into `/usr/local` (works, but `mise install` as the final user keeps ownership simple and matches the edge case "installs go into that user's home" in spirit). `aqua` or `ubi` directly (no lockfile across platforms).

**Registry coverage (verified)**: `claude` (aqua:anthropics/claude-code), `codex` (aqua:openai/codex), `opencode` (aqua:anomalyco/opencode), `starship`, `fzf`, `lsd`, `ripgrep`, `uv`, `gh`, `jq`, `yq` (all aqua, checksummed), `go`, `python`, `node` (core, checksummed). Not in mise and therefore recipes: `zsh`, `git`, `curl`, `ca-certificates`, `iproute2`, `nftables`, build toolchains.

**Offline**: a complete lockfile still downloads the artifacts from the locked URLs and contacts `mise-versions.jdx.dev` unless `use_versions_host = false` is set; the plan sets it. True air-gapped builds need a proxy and are out of scope.

## R-05: Shipped component definitions

**Decision**: Ship roughly 30 components as YAML under `components/` (embedded): languages `go`, `python`, `node`, `rust` (mise core or aqua), package tools `uv`, `pnpm`; shell tools `zsh` (apt/dnf package recipe), `starship`, `fzf`, `lsd`, `ripgrep`, `bat`, `zoxide`, `jq`, `yq`, `gh`, `git` (package), `curl` (package), `ca-certificates` (package), `iproute2` and `nftables` (package, required by OpenShell images), agents `claude-code`, `codex`, `opencode`, and `mise` itself as an implicit component. Each component names its probe check (`command -v X && X --version`, expected: exit 0 and optional version constraint), image needs (for example `go` needs `read_write: [/opt/mise]` and env `GOPATH=/opt/go`), and profile ids (`go` → `go-proxy` from the overlay, `python` → `pypi`, `claude-code` → `claude-code`, `codex` → `codex`, `git` → `github` plus `gitlab`).

**Rationale**: cc-deck's failure log (brainstorm 072) shows the tools the reference zsh configuration needs; all are in the mise registry except the base system packages.

**Alternatives**: fewer shipped components with everything else in the remote registry (slows first use; the remote tier exists for updates, not for the baseline).

## R-06: Remote component registry

**Decision**: A separate repository `sandcast-components` (to be created) with `index.yaml` listing `files: [{path, sha256}]` and one component per file. The binary pins a release tag in a constant and fetches `https://raw.githubusercontent.com/<org>/sandcast-components/<tag>/`. `sandcast components refresh [--ref <tag>]` is the only command that touches the network for this tier; it verifies each file's checksum against the index, rejects mismatches and keeps the previous cache. Cache under `os.UserCacheDir()/sandcast/components/<tag>/`.

**Rationale**: FR-017 asks for an explicit refresh, a pinned default and per-file checksums. A raw tag URL needs no release tooling. Keeping it outside the sandcast repository lets components change without a binary release.

**Alternatives**: reuse cc-deck's `openshell-policies` catalog (different schema, cc-deck-specific, and would couple the repositories). Release assets of the sandcast repository (couples component updates to binary releases).

## R-07: Upstream profiles, overlay and lint

**Decision**: Vendor the 16 upstream example profiles from OpenShell v0.1.2 unmodified under `profiles/upstream/v0.1.2/`. Overlay under `profiles/overlay/`: `npm` (registry.npmjs.org), `crates-io` (crates.io, index.crates.io, static.crates.io), `go-proxy` (proxy.golang.org, sum.golang.org, storage.googleapis.com for the module cache), `gitlab` (gitlab.com API and git transport with the same explicit rules pattern as upstream `github`), `pypi` is upstream. Profile loader: three tiers by id, later wins, report the winning file. Lint implemented in `internal/profile/lint.go` mirroring `validate_profile_set` in `crates/openshell-providers/src/profiles.rs` at the tag:

- `id` lowercase kebab-case per `is_valid_profile_id`, unique in a set.
- `category` in `other, inference, agent, source_control, messaging, data, knowledge`.
- credentials: names unique and non-empty, env vars unique and not prefixed `v<digits>_`, `auth_style` rules (`bearer`/`header` need `header_name`, `path` needs `path_template` with one `{credential}`, `query` needs `query_param`).
- endpoints: host non-empty, `port` 1 to 65535 or `ports` each in range, `rules` and `access` mutually exclusive, empty `rules` or `deny_rules` rejected, `deny_rules` need `protocol`, `tls` in `""|skip`, `enforcement` in `""|enforce|audit`, `access` in `""|read-only|read-write|full`, `protocol: tcp` rejects request-level fields, credentialed profiles need `protocol` on every endpoint unless `allow_uninspected_credentials`.
- binaries: non-empty strings.
- unknown fields rejected (upstream uses `deny_unknown_fields`).
- Additionally the policy host rules from `crates/openshell-policy/src/lib.rs` (wildcard placement, three-label minimum for wildcards, no TLD wildcard, no loopback, link-local or metadata hosts, control-plane ports blocked), because profile lint skips them but sandbox start rejects them.
- Pass-through: `refresh` and `token_grant` blocks are carried unchanged and only checked for the enum values; their deep rules are left to the gateway, which re-validates on import. sandcast never writes these blocks.

Lint tests run every vendored upstream profile (must pass) and a corpus of negative cases taken from the upstream rule list.

**Rationale**: Clarification 5. The vendored set is the regression oracle. Overlay profiles follow the upstream header comment convention so they can be contributed as is (SC-010).

**Alternatives**: shelling out to `openshell profile lint` (requires a reachable gateway even for lint, since the CLI parses locally and asks the gateway to validate; verified in the CLI help and docs).

## R-08: Profile adaptation and id mapping

**Decision**: `binaries` are rewritten to resolved paths as scalar strings. Resolution runs once per build in a container of the built image: for mise-installed components `mise which <bin>` (real install path, not the shim, since OpenShell resolves symlinks and the shim execs the real binary), for recipe components `command -v <bin>` followed by `readlink -f`. Components may add `runtime_globs` (for example `/opt/mise/installs/go/*/bin/*` for Go, `/opt/mise/installs/node/*/bin/node` for node-based CLIs) that are appended. An adapted profile with no resolved binary keeps an empty list and is flagged in the set index (FR-022). Ids: unchanged for workspace scope; with `--suffix` or `--scope platform`, `<id>-<image label>` where the image label is the manifest `name` sanitized to kebab-case. The set index `profiles/index.yaml` records `upstream_id`, `adapted_id`, `file`, `binaries_resolved`, `flagged`.

**Rationale**: Clarification 3. Upstream states that scripts such as `pip` are matched through their interpreter, so the Python component resolves `python3` and `uv`, never `pip`.

**Alternatives**: deterministic paths computed from the lockfile (per-tool bin layouts vary, so a probe is needed anyway).

## R-09: Discoverability of the adapted profile set from the image (FR-024)

**Decision**: Two-pass build. Pass 1 builds the full image under a temporary tag `<image>:sandcast-stage`. Path resolution and verification run against it. Pass 2 builds `FROM <stage image id>` and adds `COPY profiles/ /usr/share/sandcast/profiles/` plus labels `io.sandcast.version`, `io.sandcast.manifest-name`, `io.sandcast.profiles-path=/usr/share/sandcast/profiles`, `io.sandcast.profiles-index` (JSON: list of adapted ids and flagged ids). The final tag is applied to pass 2; the stage tag is removed on success and kept as `<image>:sandcast-debug` on failure.

**Rationale**: OpenShell reads no image labels, so the label is for runtimes such as cc-deck and the file copy is for humans and scripts inside the sandbox. Pass 2 is a one-layer build served from cache. This avoids cc-deck's label stamping through a Docker API socket.

**Alternatives**: single pass with deterministic paths (rejected in R-08). Sidecar file only (not discoverable from the image).

## R-10: Image conventions for the OpenShell target

**Decision**: From the upstream v0.1.2 image contract and the community base image:

- Final `USER` non-root with a passwd entry. If the probe reports root as default user, sandcast creates `sandbox` (uid 1000, gid 1000, home `/home/sandbox`); otherwise the probed user is kept and its home is used.
- `/sandbox` exists, is owned by the final user, and is the `WORKDIR`.
- `iproute2` and `nftables` are always included as package components for this target.
- Policy file at `/etc/openshell/policy.yaml` and nothing else under `/etc/openshell`. Reserved paths never written: `/opt/openshell`, `/etc/openshell-tls`, `/run/openshell`, `/run/openshell-sidecar`, `/run/netns`, `/var/run/netns`.
- No `ENTRYPOINT` or `CMD` requirement (the runtime replaces them); the Containerfile sets `CMD ["/bin/zsh","-l"]` or bash when zsh is absent, purely for the container target and manual runs.
- Everything sandcast installs lives outside `/sandbox`: tools under `/opt/mise`, the curated shell configuration under `/etc/zsh/zshrc.d/sandcast.zsh` sourced from `/etc/zsh/zshrc` (and the bash equivalent under `/etc/profile.d/`), because the Kubernetes driver mounts the workspace volume over `/sandbox`. Agent configuration scaffolding under the home directory is a known limitation on Kubernetes and is recorded for the phase 2 adapter design.

**Image-embedded policy (FR-033)**: `version: 1`, `filesystem_policy` with the upstream default read-only and read-write lists plus component image needs (for example `/opt/mise` read-write), `landlock: {compatibility: best_effort}`, `process: {run_as_user, run_as_group}` from the final user, and `network_policies: {}`. The pinned v0.1.2 default policy defines no network rules, so the network section is empty by requirement.

**Rationale**: all items confirmed in the upstream sources at the tag (`container_paths.rs`, `driver_mounts.rs`, `restrictive_default_policy`, `examples/bring-your-own-container`).

## R-11: Gateway import step

**Decision**: `sandcast profiles import --scope workspace|platform [--workspace <name>] [--suffix]` runs `openshell profile import --from <build dir>/profiles [--global]` after `openshell profile list -o json` to detect existing ids. Existing ids are reported with the choice `--update` (runs `openshell profile export` to obtain `resource_version`, then `openshell profile update <id> -f`), `--suffix`, or another scope. The CLI must be v0.1.2; `openshell --version` is checked first and a mismatch fails before any gateway call. Gateway selection is left to the CLI's own flags and environment (`OPENSHELL_GATEWAY`, `OPENSHELL_GATEWAY_ENDPOINT`, active gateway file), passed through unchanged.

**Rationale**: verified command surface of the installed CLI. Import is create-only upstream, which matches "never overwrite unless asked".

## R-12: Project layout and file lifecycle

**Decision** (clarification 2): `.sandcast/` holds `manifest.yaml`, `mise.lock`, `fragments/<component>.containerfile`, `recipes/<component>.yaml` (promoted project-local components), `learnings.yaml`, and optional `profiles/` and `components/` for project-local tiers. `.sandcast/build/<target>/` holds `plan.yaml`, `Containerfile`, `Containerfile.final`, `mise/mise.toml`, `mise/mise.lock` (copy for the build context), `policy.yaml`, `build.log`, `profiles/*.yaml` and `profiles/index.yaml`, `verify.yaml`. `sandcast init` writes `.sandcast/.gitignore` containing `build/`. The existing repository `.gitignore` already ignores `**/.sandcast/build/` for fixtures.

## R-13: Build plan format and determinism

**Decision**: `plan.yaml` is one YAML document with sections `target_platform` (value and source), `base_image` (ref, digest, registry entry or notice), `engine`, `image_facts`, `probe_delta` (sorted by component key), `components` (resolved, sorted, each with installer kind, fragment name and the fragment text), `mise` (toml text and lock path), `profiles` (ids, tier file, adapted id), `policy` (embedded policy text), `unresolved` (keys with reasons), `conflicts`, `notices`. All maps are sorted, timestamps are excluded, and the encoder is fixed (2-space indent, no flow style). Golden tests compare bytes.

**Rationale**: FR-025 and FR-026. Embedding fragment text in the plan makes the plan self-contained for the skill and for diffs.

**Alternatives**: a plan directory with one file per fragment (harder to diff as a unit, two sources of truth).

## R-14: Image definition assembly

**Decision**: The Containerfile is assembled by the binary from fixed-order sections, each rendered from a Go `text/template` with plan data: `00-header` (FROM with digest, ARG TARGETPLATFORM, LABEL base), `10-system-packages` (USER root, package manager update plus install of every package-recipe component in one layer, mkdir for apt lists), `20-user` (create user if root default; ensure `/sandbox`, `/opt/mise`), `30-mise` (R-04), `40-recipes` (one layer per script recipe, in key order), `50-fragments` (stored or skill-written fragments for unresolved components, in key order), `60-shell` (curated shell configuration under `/etc`), `70-harness` (adapter scaffolding, phase 2), `80-policy` (COPY policy.yaml, OpenShell target only), `90-footer` (ENV, WORKDIR, USER, CMD). The skill never edits generated sections; it writes fragments into `.sandcast/fragments/` and reruns `sandcast build`.

**Rationale**: cc-deck's lessons: `USER root` before packages, `mkdir -p /var/lib/apt/lists/partial`, chown parents not children, one package layer. Keeping the Containerfile fully generated keeps the non-interactive path deterministic (constitution II) and limits the skill to fragment files.

## R-15: Verification

**Decision**: `sandcast verify` runs one container (`run --rm --entrypoint /bin/sh <image>`) with a script executing each component's probe check as the final user, printing JSON lines `{key, ok, output, version}`. Results go to `build/<target>/verify.yaml`; exit code 1 if any check failed. `sandcast build --verify` chains both.

## R-16: Skill bundle and Claude Code installation

**Decision**: Skills are authored once as `skills/<name>/SKILL.md` with YAML frontmatter (`name`, `description`, `sandcast_version`). `sandcast skills install [--harness claude-code] [--scope user|project]` copies them to `~/.claude/skills/sandcast-capture/` and `~/.claude/skills/sandcast-build/` (or `.claude/skills/` in the project), invoked as `/sandcast-capture` and `/sandcast-build`. `sandcast skills check` compares the installed frontmatter version with the embedded bundle version and prints which to update; each skill's first step runs `sandcast skills check` (FR-004). The adapter interface exposes `SkillInstallDir(scope)` and `SkillFileName()` so Codex, OpenCode and pi adapters only add paths and frontmatter tweaks.

**Rationale**: personal skills under `~/.claude/skills/<name>/SKILL.md` are the documented Claude Code mechanism and need no marketplace registration. A plugin marketplace would give `/sandcast:capture` but requires registering a marketplace entry in Claude Code's plugin state, which is not a stable public surface.

## R-17: Capture inputs the skill needs from the binary

**Decision**: The binary provides read-only helpers so the capture skill does not guess: `sandcast components list [--json]` (all tiers with match conditions), `sandcast components match <dir>` (evaluates project file patterns and host tool names, prints proposals with evidence), `sandcast manifest validate`, `sandcast init` (skeleton). Host tool detection is `exec.LookPath` on the component's host tool names plus a version call. Shell configuration curation (strip plugin managers, add `compinit` when `compdef` or `zstyle` are present, guard `starship init` behind `TERM != dumb`, replace `fzf --zsh` with the version-aware block) is skill logic with the rules written in the skill, following brainstorm 072.

## R-18: Secret redaction

**Decision**: `internal/redact` scans strings for known credential shapes and replaces them with `${SANDCAST_SECRET_<N>}` placeholders while reporting the key path. Hardening widened the corpus beyond the original provider prefixes. The detected shapes (the `Kind*` constants) are: Anthropic `sk-ant-`, OpenAI `sk-`, GitHub `ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_` and fine-grained `github_pat_`, GitLab `glpat-`, AWS access key id `AKIA`/`ASIA`, Google API key `AIza`, Slack `xox`/`xapp` tokens, Stripe secret and restricted keys `sk_live_`/`rk_test_`, npm `npm_`, Hugging Face `hf_`, Notion `secret_`/`ntn_`, JSON Web Tokens (`eyJ...` header and payload), bearer tokens, HTTP basic credentials, any other `Authorization`, `Proxy-Authorization` or `Cookie` header value, the password in URL user information (`scheme://user:secret@host`), PEM private key blocks, and a secret-looking value under a secret-looking key name, flag or URL query parameter (`token`, `secret`, `password`, `credential`, api key, and the `key`/`auth`/`pat` suffixes with a credential qualifier). The harness discovery redactor (`internal/harness/discovery.go`) additionally replaces credential-like URL path segments, reported as `url-path-secret`: UUIDs, long base64, base64url or hex tokens whose character classes interleave, and a 40-hex path segment (the shape of a classic GitHub token or a hex API key). The redaction runs on the manifest before write, on every file in the build directory after the build, and on the build log. A unit corpus covers each shape.

## R-19: Integration test environment

**Decision**: `make test-integration` (tag `integration`) needs podman and pulls the registry default base image; it covers probe, plan, build, verify on the reference fixture. `make test-gateway` (tag `gateway`) additionally starts the OpenShell gateway container pinned to v0.1.2 with the podman driver (upstream `container-deployment.mdx` recipe), registers it with `openshell gateway add http://127.0.0.1:8080 --local --name sandcast-test`, imports the adapted profiles and creates a sandbox with `--from localhost/<image>`. SC-007 (inference reachability) needs provider credentials and is a manual smoke test documented in quickstart.md.

## R-20: Base image registry default

**Decision**: Shipped `base-images.yaml` lists for `openshell`: `nvidia-community-base` (`ghcr.io/nvidia/openshell-community/sandboxes/base:latest`, default, notes: Ubuntu 24.04, user `sandbox`, home `/sandbox`, no zsh, starship, lsd) and `nvidia-ubuntu` (`nvcr.io/nvidia/base/ubuntu:24.04`, OpenShell's own default workload image, runs as root, sandcast creates the user). For `container`: `fedora-42` (default) and `ubuntu-24.04`. A user registry file at `~/.config/sandcast/base-images.yaml` merges over it.

**Open items carried to tasks**: whether `/etc/openshell` being listed as reserved conflicts with the policy file location (the upstream docs instruct `COPY policy.yaml /etc/openshell/policy.yaml`; the first integration test confirms the behaviour). Agent configuration under a masked `/sandbox` on Kubernetes is a phase 2 design item.

## Implementation notes (2026-10-08)

Facts verified or decisions adjusted while implementing. Each note names the research item it refines.

- **R-03, probe cache key**: entries are keyed by digest and platform (`facts/<digest-hex>_<os>-<arch>.json`, `checks/<digest-hex>_<os>-<arch>/<key>-<hash>.json`). docker reports only the index digest, which is the same for every architecture, so the digest alone is not a safe key. A failed pull falls back to a local image of the right platform with a notice on stderr; the notice is not written into the plan, so the plan stays byte-identical.
- **R-04, mise layout in the image**: verified with mise v2026.10.4 in an ubuntu:24.04 build. mise reads `/etc/mise/mise.toml` with `/etc/mise/mise.lock` when `MISE_CONFIG_DIR=/etc/mise`; `mise install --locked` succeeds and `mise which jq` returns `/opt/mise/installs/jq/<version>/jq`. Two fixes were needed: the copied files must be made world-readable (`chmod 0644`, the host lockfile is written 0600) and `MISE_USE_VERSIONS_HOST=0` is set as an environment variable as well. mise cache and state live under `/tmp/sandcast-mise` and are removed after the install, because `/opt/mise` is read-only under the sandbox policy at runtime. `/etc/profile.d/sandcast-mise.sh` puts the shims back on `PATH` for login shells, because Debian's `/etc/profile` resets `PATH`. The OpenShell supervisor starts sandbox processes with the container environment, so `ENV PATH` reaches agent child processes (FR-031).
- **R-08, adapted set index**: `image.digest` in `profiles/index.yaml` is the pass-1 image ID. The final image cannot record its own digest because the index is copied into it.
- **R-09, debug image**: a failure in pass 1 leaves no image to keep; `<image>:sandcast-debug` exists only when pass 1 completed and a later step (resolution, adaptation, pass 2) failed.
- **R-10, OpenShell policy**: `/opt/mise` is added to the read-only list when mise tools are installed, since the pinned default policy does not cover `/opt`.
- **R-11, import**: profiles are imported one file at a time with `openshell profile import --file`, from a staging directory without `index.yaml`, so existing ids can be reported per profile and the rest still imported. Tests reach a gateway through `OPENSHELL_GATEWAY_ENDPOINT` instead of `openshell gateway add`, which leaves the user's gateway configuration unchanged. The gateway image tag is `ghcr.io/nvidia/openshell/gateway:0.1.2` (no `v` prefix).
- **R-14, sections**: every section starts with an explicit `USER`, including each stored fragment in `50-fragments`, so a fragment always starts as root regardless of the preceding section. Script recipes and the user setup use `RUN <<'SANDCAST_EOF'` heredocs, which buildah and BuildKit support.
- **R-20, base images across targets**: a manifest that names a registry entry of the other target (for example `nvidia-community-base` with `--target container`) uses that entry with a notice, so one manifest builds both targets from the same base image.
- **Plan contents**: the plan carries everything the build and verify steps need (per-component binaries, runtime globs and checks, the merged image environment and directories, the shell configuration text, harness sections), so `build` and `verify` read only the plan. The additions are recorded in contracts/plan.schema.yaml.
- **Package layout**: `internal/pipeline` gathers the plan inputs and runs build and verify. It sits above `internal/plan` and `internal/build`, because the plan needs the stored fragments that `internal/build` owns.
- **R-04, rust**: `core:rust` installs through rustup, so `mise lock` records only the exact version and no per-platform artifacts; mise accepts that with `--locked` and rustup verifies the toolchain against its signed channel manifest. The lockfile check exempts this backend (`mise.VersionOnlyBackends`). `CARGO_HOME=/opt/cargo` and `RUSTUP_HOME=/opt/rustup` come from the component's image env, which the `20-user` section sets before the `30-mise` layer.
- **R-10, policy identities**: OpenShell v0.1.2 accepts only the literal `sandbox` or a numeric id for `run_as_user` and `run_as_group` (`crates/openshell-policy/src/lib.rs:825-860`). A probed non-root user with another name is written as its numeric uid and gid.
- **R-07, strict decoding**: profiles are decoded strictly although upstream tolerates unknown top-level and endpoint fields at v0.1.2. Nothing is dropped silently; a profile with fields from a newer OpenShell release is rejected until the pin is refreshed.
- **R-19, gateway container**: the upstream v0.1.2 container recipe does not start as written under rootless podman. `tests/integration/gateway.sh` adds `XDG_STATE_HOME=/var/openshell/state` (the image home `/app` is not writable, so the gateway cannot create its credential key), `--user 0:0` (container root maps to the invoking user, who owns the podman socket) and `--security-opt label=disable` for the socket mount. The podman socket path comes from `podman info --format '{{.Host.RemoteSocket.Path}}'`, which also works with a podman machine on macOS. Starting sandboxes needs more than the recipe shows: the podman driver extracts the supervisor binary under `$XDG_DATA_HOME` and bind-mounts it into each sandbox, so that directory is mounted at the same path on the podman host (a directory in the home directory works on Linux and with podman machine); sandbox launches need the sandbox JWT signing keys, generated with the image's `generate-certs` and handed over through `OPENSHELL_LOCAL_TLS_DIR` (only the `jwt/` part, so TLS stays off); with JWT keys present, user calls need authentication, so the throwaway gateway runs with a config file that sets `[openshell.gateway.auth] allow_unauthenticated_users = true` (it listens on 127.0.0.1 only); passing `--config` replaces the image's default arguments, so `OPENSHELL_BIND_ADDRESS=0.0.0.0` and `OPENSHELL_SERVER_PORT=8080` are set explicitly; and sandbox supervisors run in the host network namespace, so `[openshell.drivers.podman] grpc_endpoint` points at the gateway's published loopback port. With these, a sandbox started with `--from localhost/<image>` runs the mise-installed Go toolchain through `PATH` from the image environment (FR-031).
- **R-04, download retries**: a validation run failed once with curl exit 18 (partial transfer) inside the `mise.run` installer. The `30-mise` layer now downloads the installer to a file and runs it, up to three attempts, then checks that `/usr/local/bin/mise` exists, and retries `mise install --locked` once. These are retries of a download inside one layer; a failed build is still never retried (FR-027).
