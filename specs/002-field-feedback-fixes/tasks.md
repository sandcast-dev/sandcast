# Tasks: Field Feedback Fixes from First Real-World Use

**Input**: Design documents from `specs/002-field-feedback-fixes/`

**Prerequisites**: plan.md, spec.md (clarified), research.md (R-01 to R-20), data-model.md, contracts/ (cli.md, manifest-component.md, reports.md, image-shell.md), quickstart.md

**Tests**: Required by constitution principle I. Every task that changes Go code includes its unit tests in the same task; renderers update their golden files (`UPDATE_GOLDEN=1 go test ./...`); podman and gateway behavior get build-tagged integration tests (FR-025).

**Organization**: Phases 3 to 6 are the four user stories in priority order. The spec's delivery phases map as: Delivery 1 (image correctness) = Phases 3 and 4 (US1, US2); Delivery 2 (delivery to the gateway) = Phase 5 (US3); Delivery 3 (capture facts) = Phase 6 (US4).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story label (US1 to US4) for story-phase tasks only
- Paths are relative to the repository root

## Path Conventions

Single Go module. `internal/<pkg>/` packages, root-level embedded assets (`components/`, `skills/`), `docs/`, `tests/`. See plan.md "Project Structure". Build and test only through make targets (`make test`, `make lint`, `make verify`, `make test-integration`, `make test-gateway`).

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Changelog and CI prerequisites for the new tests

- [X] T001 Add an "Unreleased" entry skeleton to `CHANGELOG.md` with one line per issue (#1 to #11) and a "Compatibility" subsection noting probe cache format 2 (one full re-probe), skill bundle 0.2.0, and that `shell.program: zsh` images now switch interactive sessions to zsh (escape hatch `SANDCAST_NO_SHELL_SWITCH`)
- [X] T002 [P] Install zsh on `ubuntu-latest` in `.github/workflows/ci.yml` before `make verify` (`sudo apt-get install -y zsh`), so the snippet test (T051) runs under sh, bash and zsh on both CI platforms

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Probe, lockfile and notice plumbing that US1 and US2 both build on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T003 Add `FormatVersion = 2` to `internal/probe/cache.go` and mix it into the facts and checks cache directory names (`cacheDirName`), so entries written by format 1 are never read; test in `internal/probe/cache_test.go` that a format 1 directory is ignored and a format 2 directory round-trips (R-02)
- [X] T004 Extend the probe script in `internal/probe/script.go` to record every `PATH` hit of each requested binary (walk `$PATH` with `IFS=:` and `[ -x "$d/$bin" ]`, keep `PATH` order, deduplicate), parse it in `internal/probe/parse.go` (`rawCheck.Paths`) and expose it as `CheckResult.Paths` in `internal/probe/types.go`, keeping `Path` as the first hit; cover with parser tests and the script golden in `internal/probe/testdata/` (R-02, data-model "Probe check result")
- [X] T005 Add the image fact `home_rc_resets_path` to the probe script facts block in `internal/probe/script.go`: for the default user's home, scan `.bashrc`, `.profile` and `.bash_profile` with `while read` plus `case` for a `PATH=` assignment that does not reference `$PATH`; record the matching file names; add the field to `probe.Facts` in `internal/probe/types.go` and to `plan.ImageFacts` in `internal/plan/types.go`; tests for a reset line, an `export PATH="$HOME/bin:$PATH"` line (not a reset) and a missing file (R-05, data-model "Image facts")
- [X] T006 [P] Export `func (l *Lockfile) Locked(t Tool, platform string) (string, bool)` in `internal/mise/lock.go`, built on the unexported `entry` and accepting version-only backends such as rust (no platform table); table tests in `internal/mise/lock_test.go` for a platform hit, a platform miss, a version-only backend and an unknown tool (R-01)
- [X] T007 [P] Create `internal/plan/notices.go` with the notice kinds from data-model "Notice" as constants (`lockfile-shadow`, `unknown-version`, `workspace-path`, `home-rc-path`, `reference`, `emulation`, `gateway`, `docker-hub`), a `Notice(kind, subject, message string) string` formatter producing `<kind>: <subject>: <message>`, and a `SortNotices([]string)` that orders by kind then subject; tests in `internal/plan/notices_test.go` (R-15)
- [X] T008 Add `Notices []string` (`json:"notices"`) to `buildReport` in `internal/cli/build.go`, fill it from `planned.Plan.Notices` in `buildReportOf`, print each as a `notice:` line in `printBuildReport`, and extend `internal/cli/build_test.go` so a plan notice appears in both console and JSON output (FR-002)

**Checkpoint**: Probe records all paths and the home rc fact, the lock answers per-platform queries, and plan notices reach the build report

---

## Phase 3: User Story 1 - An interactive sandbox session gets the curated shell and the tools (Priority: P1) 🎯 MVP

**Goal**: Sessions started with OpenShell's cleared environment get the mise tools, the `MISE_*` settings, zsh and the curated rc; verify proves it (FR-006 to FR-010, FR-025)

**Independent Test**: Build the reference fixture with `shell.program: zsh` on `nvidia-community-base`, then run the session invariants from `contracts/image-shell.md` under `env -i` with podman, and through `openshell sandbox exec` against a v0.1.3 gateway (quickstart "Session environment")

- [X] T009 [US1] Spike R-05 before any rendering change: with podman, build a throwaway image `FROM` the `nvidia-community-base` reference in `base-images.yaml` that adds a draft `/etc/profile.d/sandcast-env.sh` (shims prepend, `PROMPT_COMMAND+=(_sandcast_path)`, the self-removing `DEBUG` trap) and verify under `env -i HOME=/sandbox USER=sandbox TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin` that (a) `bash -lc 'command -v mise'` resolves after `/sandbox/.bashrc` resets `PATH`, (b) `bash -i` fed `command -v mise` on stdin resolves it, (c) the trap does not fire twice or leak into the command's output; record the commands, bash version and outcome under R-05 in `specs/002-field-feedback-fixes/research.md`, and if (a) fails, record the switch to the `/usr/local/bin` wrapper fallback and adjust T010 accordingly
- [X] T010 [US1] Replace `MiseProfileScript` in `internal/plan/encode.go` with the `sandcast-env.sh` content from `contracts/image-shell.md` (POSIX, idempotent `_sandcast_path`, every `MISE_*` export the image `ENV` sets, bash-only `PROMPT_COMMAND` array append and the `DEBUG` trap as validated by T009), write it in `WriteOutputs` as `sandcast-env.sh`, and copy it to `/etc/profile.d/sandcast-env.sh` in the 30-mise fragment in `internal/plan/fragments.go` (removing `sandcast-mise.sh`); update `internal/plan/testdata/fragments/mise.golden` and add a test that every `MISE_*` key in the fragment's `ENV` appears in the script (R-04, FR-006)
- [X] T011 [US1] In `internal/build/sections.go` and `internal/build/assemble.go`, append the "session environment" block from `contracts/image-shell.md` to the system bash rc (`/etc/bash.bashrc`, else `/etc/bashrc`, using the existing detection) for every OpenShell-target image with mise components, guarded so a rebuild does not append twice; update `internal/build/testdata/Containerfile.golden` (container target golden unchanged) (R-04, FR-006)
- [X] T012 [US1] In `internal/build/sections.go`, append the zshenv block (`/etc/zsh/zshenv`, else `/etc/zshenv`) that sources `sandcast-env.sh` and defines `zsh-newuser-install() { :; }` whenever zsh is present in the image facts or `shell.program` is zsh; golden update (R-06, FR-006, FR-009)
- [X] T013 [US1] Render the 60-shell section in `internal/build/assemble.go` whenever `shell.program` is zsh, not only when the curated rc is non-empty, and prefix the curated rc file with `_sandcast_path 2>/dev/null || true` in `internal/build/sections.go`; golden tests for zsh with and without an rc (R-06)
- [X] T014 [US1] Append the guarded shell-switch hook from `contracts/image-shell.md` (interactive, stdin and stdout are terminals, not already zsh, `SANDCAST_NO_SHELL_SWITCH` unset, `/bin/zsh` executable) to the system bash rc after the env source line when `shell.program` is zsh, in `internal/build/sections.go`; golden for zsh and bash programs (bash renders no hook) (R-07, FR-008)
- [X] T015 [US1] Set the final user's login shell to `/bin/zsh` for kept users too (today only `useradd -s` for created users, `internal/build/sections.go:43-46`): render `usermod -s /bin/zsh <user> || chsh -s /bin/zsh <user> || sed -i 's#^\(<user>:.*:\)[^:]*$#\1/bin/zsh#' /etc/passwd` in the user or 60-shell section; goldens for a created and a kept user (R-07, FR-008)
- [X] T016 [US1] Emit a `home-rc-path` notice in `plan.Build` (`internal/plan/plan.go`) when `ImageFacts.HomeRCResetsPath` is non-empty, naming each file and stating that sandcast re-applies the shims after it; extend the plan golden in `internal/plan/testdata/plan.yaml.golden` and keep `TestBuildDeterministicAndGolden` passing (FR-007)
- [X] T017 [US1] Add session-condition requests to `internal/verify/verify.go` for the OpenShell target only, always on (no flag): `session.bash-login.<key>` (`env -i ... bash -lc '<check>'`), `session.bash-interactive.<key>` (check piped on stdin to `env -i ... bash -i`), and for `shell.program: zsh` `session.zsh.<key>` (`zsh -i -c '<check>'`) plus `session.zsh.startup` (fails when `zsh -i -c exit` writes to stderr, output captured); add `Condition` (`condition`) to `verify.Result` with values `image`, `session:bash-login`, `session:bash-interactive`, `session:zsh`; session failures count toward `Failed` and `ErrChecksFailed`; unit tests with the fake engine for the OpenShell and container targets (R-08, FR-010)
- [X] T018 [US1] Show session entries in `internal/cli/verify.go` (console group "session checks", JSON unchanged shape plus `condition`) and in the `build --verify` path of `internal/cli/build.go`; CLI tests for a failing `session.zsh.startup` producing exit status 1 (FR-010)
- [X] T019 [US1] Add a podman integration test in `tests/integration/core_test.go` (tag `integration`) that builds the zsh manifest on `nvidia-community-base` and asserts the invariants of `contracts/image-shell.md`: `env -i` plus `bash -lc 'command -v <bin>'` for every component, `bash -i` from stdin, `zsh -i -c` with empty stderr, `podman run -t ... bash -i -c 'echo $0'` (switches only with a terminal), and a negative case: a derived image with the zshenv block removed fails `sandcast verify` with `session.zsh.*` entries (SC-003, SC-004, FR-025)
- [X] T020 [US1] Add a gateway test in `tests/integration/gateway_test.go` (tag `gateway`) that creates a sandbox from the T019 image on the v0.1.3 gateway and runs `openshell sandbox exec -n <name> -- command -v starship` and `openshell sandbox exec --tty -n <name> -- bash -i -c 'echo $ZSH_VERSION; command -v starship'`, asserting zsh and the tool path; then, for every component binary of the T019 manifest, assert `command -v <bin>` resolves in the session (100% of binaries), and assert the interactive session start writes zero error lines (FR-025, SC-003)
- [X] T021 [US1] Document the session environment: new "Session environment" section in `docs/guides/build.md` (what OpenShell clears, the files from `contracts/image-shell.md`, the switch hook and `SANDCAST_NO_SHELL_SWITCH`, the home rc notice), `shell.program` behavior and login shell in `docs/configuration.md`, and session entries plus `condition` in the `verify` and `build` sections of `docs/cli.md`

**Checkpoint**: A zsh image opens a curated zsh session under OpenShell; verify fails images whose session setup is broken

---

## Phase 4: User Story 2 - The lockfile and the project's pinned versions decide what is in the image (Priority: P1)

**Goal**: Locked versions win over base image versions, nothing relies on the workspace path, and `components match` reports pinned versions (FR-001 to FR-004)

**Independent Test**: The #1 reproduction manifest on `nvidia-community-base`: plan shows `shadow` with notices, verify reports locked versions, no resolved path under `/sandbox`; `components match` on `tests/fixtures/pinned-project` reports the pins (quickstart "Phase 1")

- [X] T022 [US2] Extend `DeltaFor` in `internal/plan/delta.go` with the locked version and the workspace path (options struct), implementing the decision table in data-model "Delta entry" for mise components: equal gives `skip`; different or empty present version gives `shadow` with the locked version; without a lock entry keep today's rules; add `LockedVersion` and `IgnoredPaths` to `DeltaEntry` in `internal/plan/types.go`; table tests in `internal/plan/delta_test.go` covering every row of the table (R-01, R-03, FR-001)
- [X] T023 [US2] Add `WorkspacePath(facts, target)` in `internal/plan/user.go` (final user's home for the OpenShell target, empty for the container target) and apply the workspace rule inside `DeltaFor`: all paths under it gives `install` plus `IgnoredPaths`; first hit under it with another outside gives present with unknown version; tests for both targets (R-03, FR-003)
- [X] T024 [US2] In `plan.Build` (`internal/plan/plan.go`), move `checkLock` ahead of the component loop, look up `in.Lock.Locked(tool, misePlatform)` per mise component, pass it and `WorkspacePath` to `DeltaFor`, and emit `lockfile-shadow`, `unknown-version` and `workspace-path` notices through `internal/plan/notices.go`, sorted; update `internal/plan/testdata/plan.yaml.golden` and assert byte-identical plans for identical inputs (FR-001, FR-002)
- [X] T025 [US2] Keep `sandcast probe` output stable: `Probed.Delta()` in `internal/pipeline/probe.go` passes no lock and no workspace path; test in `internal/pipeline/probe_test.go` that its delta is unchanged for a present component
- [X] T026 [P] [US2] Create `internal/capture/pins.go` with readers for `go.mod` and `go.work` (`go` and `toolchain` directives, highest across `go.work` `use` modules, line parser, no new dependency), `rust-toolchain.toml` (`toolchain.channel`) and `rust-toolchain`, `.python-version`, `pyproject.toml` (`project.requires-python`), `.nvmrc`, `.node-version`, `package.json` (`engines.node`), `.tool-versions` and `mise.toml` (`[tools]`); classify each value as `exact`, `range` or `channel` per R-09; a file that cannot be parsed (invalid `package.json` or `pyproject.toml`, a `go.mod` without a `go` directive) yields no pin and a note `<file>: <reason>` that `Match` adds to the match result's notes (add a `notes` field to `capture.Result` if it has none), never an error; table tests in `internal/capture/pins_test.go` including `stable`, `>=3.12`, `^20`, `lts/*` and each malformed case (R-09, FR-004, spec edge case on unparsable pin files)
- [X] T027 [US2] Create the fixture `tests/fixtures/pinned-project/` (`rust-toolchain.toml` channel 1.95.0, `.python-version` 3.14.5, `go.mod` go 1.26.7, `go.work` with two modules, `package.json` engines.node `^20`, `.tool-versions` with a node line), add `PinnedVersions` (`pinned_versions`) to `capture.Proposal` in `internal/capture/match.go` for `source: project` proposals, map `.tool-versions` and `mise.toml` tool names to components through `Installer.Mise.Tool` (counting as evidence), and add a `pinned` column to the table output in `internal/cli/capture.go`; tests in `internal/capture/match_test.go` against the fixture (FR-004)
- [X] T028 [US2] Add an integration test in `tests/integration/core_test.go` (tag `integration`) reproducing #1 and #2: manifest on `nvidia-community-base` with unpinned `gh`, `node`, `python`, `codex`; `sandcast lock` for the host platform; `sandcast build --verify --json`; assert each verified version equals `mise.lock`, each differing component has a `lockfile-shadow` or `unknown-version` notice, `python` has a `workspace-path` notice, no `binaries` entry in `.sandcast/build/profiles/` starts with `/sandbox/`, and the adapted `pypi` profile is not listed under `flagged` in the `io.sandcast.profiles-index` image label (SC-001, SC-002)
- [X] T029 [US2] Document: lockfile precedence and the new notices in `docs/guides/build.md`, `locked_version` and notice kinds in the `plan` section of `docs/cli.md`, `pinned_versions` in the `components match` section of `docs/cli.md`, and the workspace path rule in `docs/configuration.md` (base images section)

**Checkpoint**: Delivery 1 complete; issues #1, #2, #3 (binary side) and #11 (image side) are closed by Phases 3 and 4

---

## Phase 5: User Story 3 - The built image reaches the gateway that will run it (Priority: P2)

**Goal**: Exact reference, push result and digest in the report; warnings for reference, emulation and remote-gateway pitfalls outside the plan file; init flags; cross-architecture guide (FR-011 to FR-017)

**Independent Test**: quickstart "Phase 2" with a local registry, a fake remote gateway entry and the emulation case

- [ ] T030 [P] [US3] Create `internal/imageref/imageref.go` with `Normalize(ref string) (Ref, error)`, `Ref.String()`, `Ref.Registry`, `Ref.DockerHubNoNamespace()` per R-12 (registry detection by `.`, `:` or `localhost`; `docker.io/` default; `library/` insertion; default tag `latest`; digests preserved); table tests in `internal/imageref/imageref_test.go`
- [ ] T031 [US3] Change the engine `Push` to `Push(ctx, ref string, log io.Writer) (string, error)` in `internal/engine/engine.go`; implement podman with `push --digestfile <tmp>` and docker by parsing `digest: sha256:...` from push output with a `RepoDigests` fallback filtered to the pushed repository in `internal/engine/exec.go`; add `PushDigests` to the fake in `internal/engine/fake.go`; tests with the scripted exec runner for both engines (R-11)
- [ ] T032 [US3] Add `HostArch(ctx) (string, error)` to the engine interface: podman `info --format '{{.Host.Arch}}'`, docker `info --format '{{.Architecture}}'` mapped from `x86_64`/`aarch64` to `amd64`/`arm64`, fake field; tests in `internal/engine/exec_test.go` (R-13)
- [ ] T033 [US3] In `build.Run` (`internal/build/build.go`), write `### sandcast push: <ref>` and the push output to `build.log`, and set `Result.Pushed` and `Result.Digest`; tests with the fake engine for push on, push off, and a failing push (build fails, `Result.Pushed` is false) (FR-011, spec edge case "A push fails")
- [ ] T034 [US3] Add `image_ref`, `sandbox_create` (OpenShell target only), `pushed` and `digest` to `buildReport` in `internal/cli/build.go` using `internal/imageref`, print them in `printBuildReport`, and append a `docker-hub` notice when pushed to `docker.io`; CLI tests for both targets and both push states (FR-011, FR-013, SC-006)
- [ ] T035 [US3] Add `Manifest.Warnings() []Diagnostic` in `internal/manifest/manifest.go` flagging `targets.*.image` references that resolve to `docker.io/library/<name>` (bare name or `docker.io/<name>`); add `warnings` to `validateReport` in `internal/cli/manifest.go` (printed as `warning:` lines, `valid` and exit code unchanged); emit the same as a `reference` notice from the plan step in `internal/pipeline/pipeline.go`; tests in `internal/manifest/manifest_test.go` and `internal/cli/manifest_test.go` (FR-012)
- [ ] T036 [P] [US3] Add `EmulationUnsafe bool` (`emulation_unsafe`) to `component.Component` in `internal/component/types.go`, set it in `components/rust.yaml`, document it in `components/README.md`, and update the strict-decode test in `internal/component/load_test.go` (R-13, FR-016)
- [ ] T037 [US3] Collect `emulation_unsafe` component keys into `pipeline.Planned.EmulationUnsafe` (not serialized) in `internal/pipeline/pipeline.go`, add `RuntimeNotices(ctx, env, planned) []string` that compares the plan platform's architecture with `engine.HostArch` and returns an `emulation` notice pointing to `docs/guides/cross-arch.md`; print runtime notices to stderr in `internal/cli/plan.go` and append them to `buildReport.Notices` in `internal/cli/build.go`; tests that `plan --json` output is byte-identical with and without the notice, that an engine reporting the target architecture yields no notice, and that a `HostArch` error yields no notice and does not fail plan or build (FR-016)
- [ ] T038 [US3] Add `Client.ActiveGateway(ctx) (*GatewayEntry, error)` to `internal/gateway/openshell.go` running `openshell gateway list -o json` and returning the `active: true` entry (`name`, `type`, `is_remote`); treat every error or missing entry as unknown; tests with the fake runner in `internal/gateway/openshell_test.go` (R-14)
- [ ] T039 [US3] Warn about a `localhost/` image with a remote active gateway: in `internal/cli/build.go` after planning (as a `gateway` runtime notice, only when the `openshell` CLI is present) and in `pipeline.Import` in `internal/pipeline/profiles.go` after `CheckVersion` using the adapted profile index image name, adding `Warnings []string` to `ImportReport` and printing them in `internal/cli/profiles.go`; tests that a lookup failure produces no warning and never fails build or import (FR-014)
- [ ] T040 [US3] Add `--image name[:tag]`, `--platform` and `--push` to `sandcast init` in `internal/cli/init.go` (splitting a tag into `targets.<target>.tag`, reusing the plan and build `--platform` help text and validation), extend `manifest.Skeleton` in `internal/manifest/manifest.go` with an options struct, and print the `localhost/` note from `contracts/cli.md` when `--image` is absent for the OpenShell target; tests in `internal/cli/init_test.go` (FR-015)
- [ ] T041 [P] [US3] Write `docs/guides/cross-arch.md`: Rosetta in the podman machine, building through a remote amd64 podman connection with `CONTAINER_CONNECTION` (SSH `IdentityFile` limits with passphrase keys and agents, the `ssh -L` socket forward plus `unix://` connection workaround, the 104-character socket path limit on macOS), CI, the `emulation` notice and `emulation_unsafe`; link it from `README.md` (Documentation list), `docs/cli.md` ("Targets and platforms") and `docs/guides/build.md` (push section) (FR-017)
- [ ] T042 [US3] Document in `docs/cli.md` the init flags and note, `manifest validate` warnings, the build report fields and notices, and `profiles import` warnings; in `docs/configuration.md` the `emulation_unsafe` component field and the inherited engine variables `CONTAINER_CONNECTION`, `CONTAINER_HOST`, `DOCKER_HOST`; in `docs/guides/profiles.md` replace the `localhost/` caveat with a pointer to the printed `sandbox create` command; in `README.md` next to the `sandcast init` line, mention `--image`, `--platform` and `--push` for a remote gateway (FR-024)

**Checkpoint**: Delivery 2 complete; issues #8, #9 and #10 are closed

---

## Phase 6: User Story 4 - Capture works from facts the binary reports (Priority: P3)

**Goal**: Declared binaries, git-remote proposals and portability markers in discovery, shell-agnostic skill snippets, and skill rules that use the new facts (FR-005, FR-018 to FR-023)

**Independent Test**: quickstart "Phase 3": declared `bins` build, discovery tests with throwaway git repos, the snippet test under three shells

- [ ] T043 [US4] Add `Bins []string` (`bins,omitempty`) to `manifest.ComponentRef` in `internal/manifest/manifest.go` with the name pattern and uniqueness checks from `contracts/manifest-component.md` in `Validate()`; tests in `internal/manifest/manifest_test.go`; manifest schema version stays 1 (R-10, FR-018)
- [ ] T044 [US4] Reject `bins` on a tier-defined key in `pipeline.ValidateReferences` (`internal/pipeline/validate.go`) and in plan selection (`Select` in `internal/plan/select.go`), with the diagnostic text from `contracts/manifest-component.md`; tests for a shipped key (`go`) and an undefined key (FR-018)
- [ ] T045 [US4] In `fragmentComponent` (`internal/plan/plan.go:274-286`), use the declared `bins` for `Bins` and build the check as `command -v a && command -v b`, falling back to the key; add synthetic definitions for undefined entries with declared binaries to the conflict checks in `internal/plan/plan.go` and `internal/pipeline/validate.go`; tests including a conflict with a shipped component's binary (FR-018)
- [ ] T046 [US4] Default `sandcast promote --bins` to the manifest entry's `bins` when neither the flag nor an existing definition supplies binaries, in `internal/cli/fragments.go` (load the manifest), keeping `build.BuildRecipe` precedence; test in `internal/cli/fragments_test.go` (FR-018)
- [ ] T047 [US4] Add `Portable bool` (`portable`), `Reason string` (`reason,omitempty`) and `Remote *RemoteInfo` (`remote,omitempty`: `url`, `unpushed_commits`, `uncommitted_changes`, `name_match`) to `harness.Extension` in `internal/harness/adapter.go`; add a `Git` runner interface with an `os/exec` default in a new `internal/harness/git.go` and wire it into `harness.Env`; unit tests for the runner's argument construction (R-18)
- [ ] T048 [US4] In `marketplaceSource` (`internal/harness/claudecode/discover.go:374-417`), before skipping an out-of-roots directory, detect a git remote (prefer `origin`), strip credentials with `harness.SplitUserinfo`, map GitHub URLs to `owner/repo`, count unpushed commits (`<remote>/HEAD..HEAD`, fallback `@{u}`), detect uncommitted changes (`status --porcelain`), and compare the marketplace name from `git show <remote>/HEAD:.claude-plugin/marketplace.json` (`match`, `mismatch`, `unknown`); no fetch; keep the skip note only for directories without a remote; add `TestDiscoverGitMarketplaces` in `internal/harness/claudecode/discover_test.go` building throwaway repos in `t.TempDir()` (GitHub remote, non-GitHub remote with userinfo, dirty tree, name mismatch, missing remote ref, no remote, and a `git` runner that reports git as absent, which keeps today's skip) and update `TestDiscover` expectations (FR-019, SC-008)
- [ ] T049 [US4] Set `portable` and `reason` for every reported skill and plugin in `internal/harness/claudecode/discover.go`: sources under `~/.claude/skills`, `~/.claude/plugins` or another host directory are not portable with the reason text from `contracts/reports.md`; `owner/repo`, URLs and project-relative paths are portable; tests in `discover_test.go` (FR-020)
- [ ] T050 [US4] Fix the two temp-file snippets in `skills/capture/SKILL.md` (step 4d, `sandcast manifest set-shell`) and `skills/build/SKILL.md` (step 3d, `sandcast fragments write`): run the body only when `mktemp` succeeded, capture `rc=$?`, always `rm -f "$tmp"`, and end with `(exit "$rc")` so exit status 2 propagates (FR-022)
- [ ] T051 [US4] Create `internal/skills/snippets_test.go` that extracts every `sh` fenced block from `skills/**/SKILL.md`, substitutes placeholders (`<key>`, `<zsh|bash>`, `<harness>` and any other `<...>` token) with fixed values, puts stub executables for `sandcast`, `curl`, `jq`, `tar` and `openshell` on `PATH` (the `sandcast` stub exits with `$SANDCAST_STUB_RC`), and runs each block under `sh`, `bash` and `zsh` with stub exit codes 0 and 2, asserting the exit status propagates and the test's `TMPDIR` is empty afterwards; fail (not skip) when a shell is missing; add `status=$?` as a forbidden phrase in `internal/skills/structure_test.go` (FR-022, SC-009)
- [ ] T052 [US4] Update `skills/capture/SKILL.md`: propose an exact `pinned_versions` value as the component's `version:` and let the user accept or change it, never turning a range or channel into a pin (FR-005); ask where the image will run and, for a remote gateway, pass `--image`, `--platform` and `--push` to `sandcast init` (FR-015); base the copy-into-project offer on `portable: false` and present proposed marketplace plugins with their `remote` notes as opt-in (FR-019, FR-021); add rule 4b item "before a kept `compdef` line whose completion a removed plugin provided, add `command -v <tool> >/dev/null && source <(<tool> completion zsh)`" (FR-023); add the matching mandatory phrases to `internal/skills/structure_test.go`
- [ ] T053 [US4] Update `skills/build/SKILL.md`: replace the advice to add a symlink for verify with declaring `bins` on the manifest entry, and note that `sandcast promote` defaults `--bins` to that list (FR-018)
- [ ] T054 [US4] Bump the skill bundle to `0.2.0` in `skills/VERSION` and the `sandcast_version` frontmatter of `skills/capture/SKILL.md` and `skills/build/SKILL.md`; confirm `internal/skills/structure_test.go` version checks pass (R-19)
- [ ] T055 [US4] Document `bins` in the manifest section of `docs/configuration.md`; `portable`, `reason` and `remote` plus the no-fetch rule in `docs/guides/capture.md`; `harness discover` and `promote` changes in `docs/cli.md`

**Checkpoint**: Delivery 3 complete; issues #4, #5, #6, #7, #11 (capture side) and #3 (skill side) are closed

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Release notes, full verification, upstream follow-up draft

- [ ] T056 Finalize `CHANGELOG.md` "Unreleased" with every user-visible change from Phases 3 to 6, cross-checked against `docs/cli.md` and `docs/configuration.md` for missing entries (constitution I)
- [ ] T057 Draft the upstream OpenShell issue from R-20 (interactive sessions should honor the login shell from `/etc/passwd` or `$SHELL` from the sandbox spec, with the v0.1.3 code references) in `specs/002-field-feedback-fixes/upstream-openshell-login-shell.md`; do not file it (filing needs the user's approval of the text)
- [ ] T058 Run `make verify`, `make test-integration` and `make test-gateway`; regenerate goldens only where a phase changed rendering on purpose, and walk through `specs/002-field-feedback-fixes/quickstart.md`, recording any deviation as a follow-up task; for SC-005, build an image from a manifest with the pinned versions of `tests/fixtures/pinned-project`, run `rustc --version`, `python --version` and `go version` inside the project directory in a session, and confirm no toolchain download appears in the output

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies
- **Foundational (Phase 2)**: depends on Setup; blocks all user stories
- **US1 (Phase 3)** and **US2 (Phase 4)**: both depend on Phase 2; they touch different files except `internal/plan/plan.go` (T016 in US1, T024 in US2), so run T016 before T024 or serialize those two
- **US3 (Phase 5)**: depends on Phase 2 (T007, T008) only; independent of US1 and US2
- **US4 (Phase 6)**: Go tasks T043 to T051 depend on Phase 2 only; T052 depends on T027 (pinned versions) and T040 (init flags)
- **Polish (Phase 7)**: depends on all stories

### User Story Dependencies

- **US1 (P1)**: T009 (spike) blocks T010; T010 blocks T011 to T015; T017 blocks T018; T019 and T020 need T010 to T018
- **US2 (P1)**: T022 blocks T023 and T024; T026 blocks T027
- **US3 (P2)**: T030 blocks T034 and T035; T031 blocks T033 blocks T034; T032 and T036 block T037; T038 blocks T039
- **US4 (P3)**: T043 blocks T044 to T046; T047 blocks T048 and T049; T050 blocks T051

### Parallel Opportunities

- Phase 2: T006 and T007 in parallel with T003 to T005 (different packages)
- US2: T026 (capture) in parallel with T022 to T025 (plan)
- US3: T030, T036 and T041 in parallel; T031 and T032 serialize on `internal/engine/`
- US4: T043 to T046 (manifest, plan) in parallel with T047 to T049 (harness) and T050 to T051 (skills)
- Across stories: US3 can proceed alongside US1 and US2 once Phase 2 is done

---

## Parallel Example: User Story 3

```text
Task: "T030 [P] [US3] Create internal/imageref/imageref.go with Normalize ..."
Task: "T036 [P] [US3] Add EmulationUnsafe to component.Component ..."
Task: "T041 [P] [US3] Write docs/guides/cross-arch.md ..."
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 and Phase 2
2. Phase 3 (US1), starting with the T009 spike
3. **Stop and validate**: the session invariants under podman and the gateway (T019, T020)

### Incremental Delivery

1. Delivery 1: Phases 3 and 4 (US1 plus US2), then `make verify` and `make test-integration`
2. Delivery 2: Phase 5 (US3)
3. Delivery 3: Phase 6 (US4), including the skill bundle bump
4. Phase 7 polish

Each delivery ships its documentation in the same commits (constitution I).

---

## Notes

- Every Go task includes its tests; a task is done only when `make test` passes.
- Golden files change only in tasks that change rendering (T004, T010 to T016, T024); review golden diffs as part of the task.
- Never use `go build` directly; never require docker-specific features in the build path.
- No credential value may appear in discovery output, reports or logs (T048 strips userinfo).
