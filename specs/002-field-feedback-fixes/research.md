# Research: Field Feedback Fixes

**Feature**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md) | **Date**: 2026-10-13

Research covered the current code paths for every requirement and the upstream OpenShell v0.1.3 sources (`../OpenShell`, tag `v0.1.3`). File references are to the state of `main` at commit `203fea0`.

## R-01 Lockfile comparison in the delta (FR-001)

- **Decision**: Export `func (l *Lockfile) Locked(t Tool, platform string) (string, bool)` in `internal/mise/lock.go`, built on the unexported `entry` (`lock.go:274`) and accepting version-only backends (rust). `DeltaFor` (`internal/plan/delta.go:18`) takes the locked version as a new argument. For a mise component with a probe status of present or incompatible: an equal version gives `skip`, a different version or an empty `check.Version` gives `shadow` with the locked version. Move `checkLock` (`plan.go:356`) ahead of the component loop so a missing platform entry fails first, as the 001 edge case requires. `sandcast probe` (`pipeline/probe.go:151`) passes no lock and keeps today's output.
- **Rationale**: `plan.Build` already holds `in.Lock` and the mise platform (`plan.go:52`, `:96`), so the comparison needs no new input and the plan stays a pure function of manifest, lockfile, definitions, digest and platform. Shadowing already means "install through the shared mise fragment"; the shims win through `PATH` order (`fragments.go:57-64`).
- **Alternatives considered**: Comparing in the probe (rejected: the probe cache is keyed by digest and check, not by lockfile content, so cached results would go stale on every relock). Using `Lockfile.Version(tool)` (rejected: it ignores the platform).

## R-02 Every binary path in the probe (FR-003)

- **Decision**: The probe script records, per check, every `PATH` hit of each requested binary (walk `$PATH` with `IFS=:` and `[ -x "$d/$bin" ]`), not only `command -v`. `rawCheck` (`internal/probe/parse.go:24`) and `CheckResult` (`types.go:105`) get `Paths []string`. A new constant `probe.FormatVersion = 2` is mixed into the cache directory name (`cache.go:17`) so old cache entries without paths are not reused.
- **Rationale**: Today only the first hit is recorded (`script.go`, `_p=$(command -v "$2")`), which cannot tell "only under the workspace" from "also elsewhere". A format bump is the only safe invalidation, since `CheckHash` does not cover the script.
- **Alternatives considered**: Rerunning the check with the home directory removed from `PATH` (rejected: doubles probe time and couples the probe to the target).

## R-03 Workspace path rule (FR-003, OpenShell target only)

- **Decision**: `plan.WorkspacePath(facts, target)` returns the final user's home for the OpenShell target and empty for the container target. OpenShell sets `HOME` to the workspace root (`openshell-sandbox/src/process.rs:182-201`, `delegated.rs:78`), and the final user's home is `/sandbox` on the community base. In `DeltaFor`: if every recorded path lies under the workspace path, the component is `missing` (installed) with a `workspace-path` notice; if some path lies outside, the component counts as present, but its version is treated as unknown when the first hit is under the workspace path (the version came from that copy). With R-01 and the clarified rule, a locked mise component is then shadowed; a non-mise component keeps `skip`.
- **Rationale**: Deterministic, uses only probe facts, and needs no second probe pass.
- **Alternatives considered**: Hard-coding `/sandbox` (rejected by the spec's edge case on future base images).

## R-04 Session environment files (FR-006)

- **Decision**: One generated script, `/etc/profile.d/sandcast-env.sh`, holds an idempotent `PATH` prepend of `/opt/mise/shims` and every `MISE_*` export the image sets (today `MiseProfileScript`, `internal/plan/encode.go:26`, sets only `PATH`; the old `sandcast-mise.sh` name is replaced). It is sourced from the end of the system bash rc (`/etc/bash.bashrc`, or `/etc/bashrc` per the existing detection in `sections.go:65-78`) and from a guarded block in the system zshenv (`/etc/zsh/zshenv` or `/etc/zshenv`). The image `ENV` lines stay, for processes that do not go through a shell (FR-031 of 001).
- **Rationale**: OpenShell v0.1.3 clears the environment (`boundary_exec.rs:123-170`). An interactive `connect` runs `bash -i`, which is not a login shell and reads only the system bash rc and `~/.bashrc`, never `/etc/profile.d`. `exec` runs `bash -lc`, which reads `/etc/profile` and the profile.d directory. zsh reads zshenv in every mode.
- **Alternatives considered**: Wrapper scripts in `/usr/local/bin` that export `MISE_*` and exec the shim (kept as the fallback in R-05; rejected as primary because they collide with base image files in `/usr/local/bin`).

## R-05 Protecting `PATH` from home-directory rc files (FR-007)

- **Decision**: Two hooks defined in `sandcast-env.sh`. For interactive bash, `PROMPT_COMMAND+=(_sandcast_path)` (bash 5.1+ array form) re-applies the shims before every prompt, so a `~/.bashrc` that resets `PATH` or assigns `PROMPT_COMMAND=` (which replaces element 0 only) is repaired before the user types. For a login non-interactive bash (`bash -lc`), a self-removing `DEBUG` trap set from profile.d re-applies the shims right before the first top-level command: `trap '[ ${#BASH_SOURCE[@]} -eq 0 ] && { _sandcast_path; trap - DEBUG; }' DEBUG`. The probe adds an image fact, `home_rc_resets_path`, that scans `~/.bashrc`, `~/.profile` and `~/.bash_profile` of the final user for a `PATH=` assignment that does not mention `$PATH`; the plan emits a notice when it is true.
- **Rationale**: These are the only hooks bash runs after the user's rc files without editing files in the workspace path.
- **Risk and spike**: The `DEBUG` trap behavior under `bash -lc` must be proven on `nvidia-community-base` (bash 5.2) before the rest of phase 1 builds on it. Task order puts this spike first. If it fails, the fallback is wrapper scripts in `/usr/local/bin` for shadowed and installed mise binaries only, written only when the path is free in the base image; the spike result is recorded here and the spec is evolved if the fallback changes observable behavior.
- **Alternatives considered**: Patching `/sandbox/.bashrc` in the image (rejected: it sits in the workspace path, which a volume may replace, and editing base image user files is surprising). `BASH_ENV` (rejected: the environment is cleared, so it is never set).
- **Spike result (T009, 2026-10-13)**: The `DEBUG` trap works; the `/usr/local/bin` wrapper fallback is not needed and T010 proceeds with the trap.
  - Setup: podman 5 on macOS (arm64 machine), image `FROM ghcr.io/nvidia/openshell-community/sandboxes/base:latest` (GNU bash 5.2.21(1)-release, aarch64; user `sandbox`, uid 998, home `/sandbox`). `/sandbox/.bashrc` sets `export PATH="/sandbox/.venv/bin:/usr/local/bin:/usr/bin:/bin"`, `/sandbox/.profile` sources it, and `/etc/bash.bashrc` returns early when `PS1` is empty. The throwaway image added a fake `/opt/mise/shims/mise`, a draft `/etc/profile.d/sandcast-env.sh` (`_sandcast_path`, `MISE_*` exports, `PROMPT_COMMAND[n]=_sandcast_path` with `n` at least 1 so that a later `PROMPT_COMMAND=...` in `~/.bashrc` replaces element 0 only, and the self-removing trap, both inside `eval` so that dash and zsh never parse bash array syntax), and the session environment block appended to `/etc/bash.bashrc`.
  - Session environment for every run: `env -i HOME=/sandbox USER=sandbox TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin`.
  - (a) `bash -lc 'command -v mise; echo "PATH=$PATH"; trap -p DEBUG'` printed `/opt/mise/shims/mise` and `PATH=/opt/mise/shims:/sandbox/.venv/bin:/usr/local/bin:/usr/bin:/bin`; `trap -p DEBUG` printed nothing, so the trap removed itself. Control run with the trap line deleted: `NOT-FOUND` and `PATH=/sandbox/.venv/bin:/usr/local/bin:/usr/bin:/bin`, which confirms that `~/.bashrc` resets `PATH` after profile.d and that the trap is what repairs it.
  - (b) `echo 'command -v mise; echo "PATH=$PATH"; echo "${PROMPT_COMMAND[*]}"' | bash -i` printed `/opt/mise/shims/mise`, the repaired `PATH`, and `_sandcast_path` as the only `PROMPT_COMMAND` element (index 1). Its stderr carried only bash's own "no job control" lines (no terminal) and the prompts.
  - (c) With a trap instrumented to print `FIRED` to stderr, `bash -lc 'echo a; echo b; for i in 1 2; do echo $i; done'` printed `FIRED` exactly once before `a`, and stdout was exactly the command's output (`od -c` of `bash -lc 'echo one'` shows `one\n` only). A function defined and called later still sees the repaired `PATH`.
  - Non-login `bash -c` reads no startup file and does not find `mise`, as expected; OpenShell runs `bash -lc` for exec and `bash -i` for connect. Sourcing the script from dash (`/bin/sh`) works.

## R-06 zsh startup (FR-006, FR-009)

- **Decision**: The zshenv block sources `sandcast-env.sh` (POSIX syntax, runs under zsh) and defines `zsh-newuser-install() { :; }`. The curated rc file in zshrc.d re-applies `PATH` once more, because a system zprofile that sources `/etc/profile` (Debian) may reset it after zshenv.
- **Rationale**: zshenv is read by every zsh; the no-op function prevents the new-user wizard when `HOME` holds no `.zshrc`.

## R-07 Shell switch and login shell (FR-008)

- **Decision**: For `shell.program: zsh`, the 60-shell section appends to the system bash rc, after the env source line:

  ```sh
  case $- in *i*) [ -t 0 ] && [ -t 1 ] && [ -z "$ZSH_VERSION" ] && [ -z "$SANDCAST_NO_SHELL_SWITCH" ] && [ -x /bin/zsh ] && exec /bin/zsh -l ;; esac
  ```

  and sets the final user's login shell with `usermod -s /bin/zsh <user> || chsh -s /bin/zsh <user> || sed` on `/etc/passwd` (Alpine has no `usermod`). Today the login shell is set only for a created user (`sections.go:43-46`). The 60-shell section renders whenever the program is zsh, not only when the rc is non-empty (`assemble.go:56`). `SANDCAST_NO_SHELL_SWITCH` is an escape hatch a sandbox spec can set.
- **Rationale**: OpenShell ignores `$SHELL` and `/etc/passwd` (`openshell-core/src/shell.rs:29`); the hook is the only lever in v0.1.3. The login shell makes a future release that honors it work without the hook.

## R-08 Session checks in verify (FR-010)

- **Decision**: For the OpenShell target, `verify` adds requests to the same container run that wrap checks in `env -i HOME=<home> USER=<user> TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin`:
  - `session.bash-login.<component>`: `bash -lc '<check>'`.
  - `session.bash-interactive.<component>`: the check piped on stdin to `bash -i` (no `-c`), so `PROMPT_COMMAND` runs as in a real session.
  - with `shell.program: zsh`, `session.zsh.<component>`: `zsh -i -c '<check>'`, plus `session.zsh.startup`, which fails when `zsh -i -c exit` writes anything to stderr.

  `verify.Result` gets a `condition` field (`image` or `session:<shell>`). The exec-zsh hook does not fire in verify because stdin is not a terminal, which is the behavior FR-008 requires.
- **Rationale**: Runs under the container engine only, in line with the 001 rule that verify needs no gateway, and reuses `probe.RunChecks`.

## R-09 Pinned versions in `components match` (FR-004)

- **Decision**: New `internal/capture/pins.go`, called from `Match` with the already listed project files. Readers: `go.mod` and `go.work` through a small line parser for `go` and `toolchain` directives (highest across `go.work` `use` modules); `rust-toolchain.toml` (TOML, `toolchain.channel`) and `rust-toolchain` (plain); `.python-version`; `pyproject.toml` `project.requires-python`; `.nvmrc`; `.node-version`; `package.json` `engines.node`; `.tool-versions`; `mise.toml` `[tools]`. Tool names in `.tool-versions` and `mise.toml` map to components through `Installer.Mise.Tool`; such entries also count as evidence and create a proposal. Kind: `exact` when the value parses as a full version with no operator, `channel` for words (`stable`, `nightly`, `lts/*`, `system`), `range` otherwise.
- **Rationale**: `BurntSushi/toml` and `encoding/json` are already available; a 20-line `go.mod` directive reader avoids a new dependency on `golang.org/x/mod`.

## R-10 Declared binaries on a manifest entry (FR-018)

- **Decision**: `ComponentRef` gets `Bins []string` (`bins,omitempty`). No manifest schema version bump: the field is additive and version-1 manifests stay valid. `pipeline.ValidateReferences` and the plan reject `bins` on a tier-defined component. `fragmentComponent` (`plan.go:274-286`) uses the declared list for `Bins` and builds the check as `command -v a && command -v b`. Undefined entries with declared binaries join the conflict check as synthetic definitions. `sandcast promote` loads the manifest and defaults `--bins` to the declared list when neither a flag nor an existing definition supplies one.
- **Rationale**: Exact-match schema versioning would reject every existing manifest on a bump. An older binary reading `bins` fails with an unknown-field error, which is acceptable for an additive field.

## R-11 Push result and digest (FR-011)

- **Decision**: The engine method becomes `Push(ctx, ref string, log io.Writer) (digest string, err error)`. podman uses `push --digestfile <tmp>`; docker parses the `digest: sha256:...` line of its push output and falls back to `image inspect` `RepoDigests` filtered to the pushed repository. The fake gets a `PushDigests` map. `build.Run` writes the push command and output to `build.log`. `build.Result` gets `Pushed` and `Digest`.
- **Rationale**: Both engines expose the digest through the common command surface; no engine-specific feature is required for the build itself.

## R-12 Image reference normalization (FR-011, FR-012)

- **Decision**: New package `internal/imageref` with `Normalize(ref) (Ref, error)` and `Ref.String()`, `Ref.Registry`, `Ref.DockerHubNoNamespace()`. Rule: the first path component is a registry if it contains `.` or `:` or equals `localhost`; otherwise `docker.io/` is prefixed; `docker.io/<name>` without a slash gets `library/`; the tag defaults to `latest`.
- **Rationale**: go.mod has no reference parser, and pulling in a container library for one function is not worth it.

## R-13 Engine host architecture (FR-016)

- **Decision**: Engine interface method `HostArch(ctx) (string, error)`: `podman info --format '{{.Host.Arch}}'` (already GOARCH form) or `docker info --format '{{.Architecture}}'` mapped from `x86_64`/`aarch64`. podman and docker inherit `CONTAINER_CONNECTION`, `CONTAINER_HOST` and `DOCKER_HOST`, so a remote connection reports the remote host. Component schema field `emulation_unsafe: true`, set on `components/rust.yaml`. The plan step collects the keys into `pipeline.Planned` (not serialized), and a `RuntimeNotices` function produces the notice for plan and build output and for the build report.
- **Rationale**: Keeps the plan file free of host facts (FR-016, determinism).

## R-14 Remote gateway detection (FR-014)

- **Decision**: `gateway.Client.ActiveGateway(ctx)` runs `openshell gateway list -o json` and returns the entry with `active: true`; `type` is `local`, `remote` or `cloud`, and `is_remote` is set (`openshell-cli/src/commands/gateway.rs:1316`, `:1436-1449`). It reads local metadata only and needs no admin rights. Any failure means "unknown, no warning". Build calls it after planning; `profiles import` calls it after `CheckVersion` and takes the image reference from the adapted profile index.
- **Finding**: OpenShell v0.1.3 exposes no node architecture anywhere (no field in `GetGatewayInfoResponse` or `ComputeDriverCapabilities`, `proto/openshell.proto:908-967`). `gateway info` exposes driver names but needs platform-admin rights. The architecture warning of FR-014 therefore never fires against v0.1.3; it is implemented behind the same `ActiveGateway` lookup only when a later pin exposes the field. The spec's assumption on best-effort detection covers this, and the init and capture guidance (FR-015) carries the message instead.
- **Alternatives considered**: `openshell gateway info` (rejected: permission denied for ordinary users). Querying the cluster with `kubectl` (rejected: out of scope and needs cluster credentials).

## R-15 Notice routing (FR-002, FR-013, FR-014, FR-016)

- **Decision**: Plan notices stay `[]string` in the plan (`types.go:49`), with a stable `<kind>: <component>: <message>` prefix, so the plan schema version stays 1. The build report gets a `notices` list that repeats the plan notices and appends runtime notices (emulation, gateway, Docker Hub rate limits). Kinds: `lockfile-shadow`, `workspace-path`, `unknown-version`, `home-rc-path`, `reference`, `emulation`, `gateway`, `docker-hub`.
- **Rationale**: Avoids a plan schema bump for a disposable output while keeping notices grep-able and testable.

## R-16 Manifest warnings (FR-012)

- **Decision**: `Manifest.Warnings() []Diagnostic` runs reference checks on `targets.*.image`. `sandcast manifest validate` adds `warnings` to its JSON report and prints `warning:` lines without changing `valid` or the exit status. Plan repeats the warning as a `reference` notice.

## R-17 `sandcast init` flags (FR-015)

- **Decision**: `--image <name[:tag]>`, `--platform linux/amd64|linux/arm64` and `--push` on `init`, mapping to `targets.<target>.image` and `tag`, the top-level `platform`, and `targets.<target>.push`. A tag in `--image` is split into `tag`. Without `--image`, init prints the `localhost/` note. Flag names and help text match the existing `--platform` on plan and build.

## R-18 Discovery: git remotes and portability (FR-019, FR-020)

- **Decision**: `harness.Extension` gets `Portable bool` and `Reason string`. A `Git` runner (interface plus `os/exec` default, injectable for tests) joins `harness.Env`. In `marketplaceSource` (`discover.go:374-417`), before skipping an out-of-roots directory: `git -C <dir> remote` (prefer `origin`), `remote get-url`, credentials stripped with `harness.SplitUserinfo`; a GitHub URL maps to `owner/repo`; `git status --porcelain` and `git rev-list --count <remote>/HEAD..HEAD` (falling back to `@{u}`) report divergence; `git show <remote>/HEAD:.claude-plugin/marketplace.json` gives the remote marketplace name. No fetch. A missing ref produces "name match could not be checked".
- **Rationale**: Offline, deterministic for a given checkout, and reuses the existing credential helpers.

## R-19 Skill snippets (FR-022, FR-023)

- **Decision**: Fix both snippets with `rc=$?` and `(exit "$rc")`, guarded so the body runs only when `mktemp` succeeded. New `internal/skills/snippets_test.go` extracts every `sh` fence, substitutes placeholders (`<key>`, `<zsh|bash>`, `<harness>`), puts stubs for `sandcast`, `curl`, `jq`, `tar` and `openshell` on `PATH`, and runs each block under sh, bash and zsh with stub exit codes 0 and 2, asserting exit-code propagation and an empty `TMPDIR`. `status=$?` becomes a forbidden phrase in `structure_test.go`. CI installs zsh on `ubuntu-latest`; the test fails, not skips, when a shell is missing. Rule 4b of the capture skill gets the guarded completion-load item. `skills/VERSION` goes from `0.1.0` to `0.2.0` in VERSION and both frontmatters, so installed skills report the update.

## R-20 Upstream OpenShell issue

- **Decision**: Draft an issue for NVIDIA/OpenShell asking interactive sessions to honor the login shell from `/etc/passwd` (or `$SHELL` from the sandbox spec). The draft is shown to the user and filed only after approval. Not a code task and not a dependency.
