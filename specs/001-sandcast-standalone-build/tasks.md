# Tasks: sandcast, a Standalone Sandbox Image Builder

**Input**: Design documents from `specs/001-sandcast-standalone-build/`

**Prerequisites**: plan.md, spec.md, research.md (R-01 to R-20), data-model.md, contracts/ (cli.md, manifest.schema.yaml, component.schema.yaml, plan.schema.yaml, adapted-profiles.schema.yaml, skill-bundle.md), quickstart.md

**Tests**: Required by constitution principle I. Every package task includes its unit tests; renderers get golden tests; podman, mise and gateway interactions get build-tagged integration tests.

**Organization**: Phases 3 to 7 are the five user stories in priority order. The spec's three delivery phases map as: Delivery 1 = Phases 1 to 3 (US1), Delivery 2 = Phases 4 and 6 (US2, US4), Delivery 3 = Phases 5 and 7 (US3, US5).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story label (US1 to US5) for story-phase tasks only
- Paths are relative to the repository root

## Path Conventions

Single Go module. `cmd/sandcast/` entry point, `internal/<pkg>/` packages, root-level embedded assets (`components/`, `profiles/`, `policy/`, `skills/`, `base-images.yaml`, `embed.go`), `docs/`, `tests/`. See plan.md "Project Structure".

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Go module, build tooling, repository skeleton

- [X] T001 Initialize the Go module in `go.mod` (module path from `git remote get-url origin`, fallback `github.com/rhuss/sandcast`; `go 1.25` with a pinned `toolchain`) and add dependencies `github.com/spf13/cobra`, `github.com/goccy/go-yaml`, `github.com/BurntSushi/toml`, `github.com/opencontainers/go-digest`, `github.com/stretchr/testify`; run `go mod tidy`
- [X] T002 Create `Makefile` with targets `build` (go build to `./sandcast`), `test` (`go test ./...`), `lint` (`golangci-lint run`), `verify` (test plus lint), `fmt`, `test-integration` (`go test -tags integration ./tests/integration/...`), `test-gateway` (`go test -tags gateway ./tests/integration/...`), `cross` (linux and darwin, amd64 and arm64 into `dist/`)
- [X] T003 [P] Create `.golangci.yml` (v2 format) enabling `govet`, `staticcheck`, `errcheck`, `gocritic`, `revive`, `misspell`, `gosec`, with `tests/` excluded from `gosec`
- [X] T004 [P] Create the directory skeleton with placeholder `doc.go` files: `cmd/sandcast/`, `internal/{cli,manifest,registry,engine,probe,component,mise,profile,policy,plan,build,verify,gateway,harness,skills,redact,capture,fsutil,version}/`, `components/`, `profiles/upstream/v0.1.2/`, `profiles/overlay/`, `policy/`, `skills/`, `docs/guides/`, `tests/{fixtures,golden,integration}/`
- [X] T005 [P] Create `.github/workflows/ci.yml` running `make verify` on ubuntu and macos with the pinned Go toolchain, and `make test-integration` on ubuntu with podman installed
- [X] T006 [P] Create `CHANGELOG.md` with an "Unreleased" section and a "Pins" subsection listing OpenShell v0.1.2 (commit 6648bd0c) and mise v2026.10.4
- [X] T007 [P] Create `internal/version/version.go` with `Version` (ldflags-settable), `OpenShellPin = "v0.1.2"`, `MisePin = "v2026.10.4"`, `ManifestSchemaVersion = 1`, `PlanSchemaVersion = 1`, `ComponentRegistryPin` (tag string), and `internal/version/version_test.go` asserting the pins are non-empty semver-like strings

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared packages every story depends on: file layout, redaction, engine abstraction, manifest, registry, component and profile schemas, embed, CLI root

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T008 Implement `internal/fsutil/layout.go`: project root discovery (nearest ancestor with `.sandcast/`, else cwd), `SandcastDir`, `BuildDir(target)`, `CacheDir()` (`os.UserCacheDir()/sandcast`), `AtomicWriteFile`, `EnsureGitignore` writing `build/` into `.sandcast/.gitignore`; tests in `internal/fsutil/layout_test.go` using `t.TempDir()`; also implement the shared test helper `internal/testutil/golden.go` (`Golden(t, path, got []byte)` comparing bytes and rewriting the file when `UPDATE_GOLDEN=1`) used by every golden test from Phase 3 on, with `internal/testutil/golden_test.go`
- [X] T009 [P] Implement `internal/redact/redact.go`: pattern set for `sk-ant-`, `sk-`, `ghp_`, `github_pat_`, `glpat-`, `AKIA`, bearer headers, and long values under keys matching `(?i)(token|secret|password|api[_-]?key)`; `Scan(text) []Hit`, `Replace(text) (string, []Hit)` producing `${SANDCAST_SECRET_N}` placeholders; `ScanYAML(doc) []Hit` reporting key paths; corpus tests in `internal/redact/redact_test.go` with one positive and one negative case per pattern (R-18)
- [X] T010 [P] Implement `internal/engine/engine.go` (interface `Engine` with `Pull(ctx, ref, platform)`, `Digest(ctx, ref)`, `Run(ctx, RunSpec) (stdout, stderr, error)`, `Build(ctx, BuildSpec)`, `Tag`, `RemoveImage`, `ImageExists`, `Labels(ctx, ref)`), `internal/engine/exec.go` (podman and docker via `os/exec` using only the shared flag subset from R-02, build log streamed to an `io.Writer`), `internal/engine/detect.go` (flag, manifest, `podman` on PATH, `docker` fallback with printed notice; returns the source), `internal/engine/fake.go` (scripted fake for tests); tests in `internal/engine/detect_test.go` and `internal/engine/exec_test.go` (argument construction only)
- [X] T011 [P] Implement `internal/manifest/manifest.go` with the types from data-model.md (Manifest, ComponentRef, HarnessRef, Shell, Target) using goccy/go-yaml strict decoding, `Load(path)`, `Save(path)`, `Validate()` (name pattern, platform pattern, unique component keys, at least one target, engine enum), and `internal/manifest/version.go` refusing `schema_version` greater than `version.ManifestSchemaVersion` with a message naming both (FR-009); tests in `internal/manifest/manifest_test.go` covering each rule and unknown-field rejection, with fixtures in `internal/manifest/testdata/`
- [X] T012 [P] Create the shipped registry `base-images.yaml` per R-20 (`openshell`: `nvidia-community-base` default, `nvidia-ubuntu`; `container`: `fedora-42` default, `ubuntu-24.04`; each with `notes`) and implement `internal/registry/registry.go` (types, `LoadShipped(fs.FS)`, `LoadUser(path)` from `~/.config/sandcast/base-images.yaml`, `Merge` by name with user precedence, `Resolve(input, target) (Entry, notice)` where a non-registry input returns a notice, exactly-one-default validation); tests in `internal/registry/registry_test.go`
- [X] T013 [P] Implement `internal/component/types.go` and `internal/component/load.go` per contracts/component.schema.yaml: strict decoding, exactly one installer, forbidden `endpoints` key reported by name (FR-013), `check.expect` defaults, `LoadDir(fs.FS, tier) ([]Component, error)`; tests in `internal/component/load_test.go` with `fstest.MapFS`
- [X] T014 [P] Implement `internal/profile/types.go` mirroring the OpenShell v0.1.2 provider profile (id, resource_version, annotations, display_name, description, category, inference_capable, credentials with refresh and token_grant kept as `yaml.MapSlice` pass-through, discovery, endpoints with every documented field, binaries) with strict decoding and `UnmarshalYAML` that normalizes `binaries` entries of the form `{path: X}` to scalars; `internal/profile/load.go` with `LoadDir(fs.FS, tier)`; round-trip tests in `internal/profile/types_test.go` proving `Load` then `Save` reproduces the upstream `pypi.yaml`, `claude-code.yaml` and `github.yaml` bodies
- [X] T015 Create `embed.go` at the repository root (package `sandcast`) with `//go:embed components profiles policy skills base-images.yaml` exposing `Components`, `Profiles`, `Policy`, `Skills`, `BaseImages` as `fs.FS` sub-filesystems, plus `embed_test.go` asserting each directory is non-empty once assets exist (placeholder `.keep` files until Phase 3)
- [X] T016 Implement `internal/cli/root.go` (cobra root, global flags `--project`, `--engine`, `--verbose`, `--json`, exit code mapping 0/1/2/3 per contracts/cli.md, stderr diagnostics), `internal/cli/output.go` (table and JSON writers), `internal/cli/version.go` (`sandcast version [--json]` printing binary version and pins), and `cmd/sandcast/main.go`; tests in `internal/cli/root_test.go` executing the command tree with captured output
- [X] T017 Implement `internal/cli/init.go` (`sandcast init --name --base-image --target` writing the manifest skeleton and `.sandcast/.gitignore`) and `internal/cli/manifest.go` (`sandcast manifest validate [--json]` with line-numbered diagnostics); tests in `internal/cli/init_test.go` and `internal/cli/manifest_test.go`
- [X] T018 [P] Create the reference fixture `tests/fixtures/reference-project/` with `go.mod`, `main.go`, `pyproject.toml`, `uv.lock` (minimal), `.zshrc` that uses antidote, `starship init zsh`, `source <(fzf --zsh)`, `alias ls=lsd`, `zstyle ':completion:*'`, and a `README.md` describing the expected capture result (SC-003)

**Checkpoint**: Foundation ready. `make verify` passes with the skeleton.

---

## Phase 3: User Story 1 - Build a sandbox image from a manifest (Priority: P1) 🎯 MVP

**Goal**: From a hand-written manifest, plan (probe, delta, lockfile, fragments, profiles, policy), build in two passes, verify, write and lint the adapted profile set, and import it into a gateway. No agent involved.

**Independent Test**: quickstart.md scenarios 1, 2, 3 and 7. Plan twice yields byte-identical plans; second plan starts no container; build and verify exit 0; adapted profiles lint and import; a broken stored fragment fails the build once with exit 1 and no retry.

### Assets for User Story 1

- [X] T019 [P] [US1] Vendor the 16 upstream example profiles from OpenShell tag v0.1.2 (`providers/*.yaml`, unmodified, including header comments) into `profiles/upstream/v0.1.2/` plus `profiles/upstream/v0.1.2/SOURCE.md` recording the tag, commit `6648bd0c290efbc41ba131ee9831ee45cd431f94` and the refresh procedure
- [X] T020 [P] [US1] Write overlay profiles in `profiles/overlay/`: `npm.yaml` (registry.npmjs.org), `crates-io.yaml` (crates.io, index.crates.io, static.crates.io), `go-proxy.yaml` (proxy.golang.org, sum.golang.org, storage.googleapis.com), `gitlab.yaml` (gitlab.com REST read-only, git transport with the explicit `git-upload-pack` rules pattern of upstream `github.yaml`, credential `GITLAB_TOKEN` as `bearer`), each with the upstream header comment convention (R-07, SC-010)
- [X] T021 [P] [US1] Write `policy/default-v0.1.2.yaml` reproducing `restrictive_default_policy()` (filesystem read-only and read-write lists, landlock best_effort, empty network_policies) with a comment naming the source file and line range
- [X] T022 [P] [US1] Write the shipped components in `components/` per R-05: `go.yaml`, `python.yaml`, `node.yaml`, `rust.yaml`, `uv.yaml`, `pnpm.yaml`, `zsh.yaml` (package), `bash.yaml` (package), `git.yaml` (package), `curl.yaml` (package), `ca-certificates.yaml` (package), `iproute2.yaml` (package, openshell target), `nftables.yaml` (package, openshell target), `starship.yaml`, `fzf.yaml`, `lsd.yaml`, `ripgrep.yaml`, `bat.yaml`, `zoxide.yaml`, `jq.yaml`, `yq.yaml`, `gh.yaml`, `claude-code.yaml`, `codex.yaml`, `opencode.yaml`, each with `match`, `installer`, `check`, `image` (including `runtime_globs` for go and node), `provides`, `profiles`; plus `components/README.md` describing the format
- [X] T023 [US1] Add `internal/component/shipped_test.go` that loads every file in `components/` through `embed.go`, validates it, and asserts every referenced profile id exists in `profiles/upstream/v0.1.2` or `profiles/overlay`

### Probe and cache

- [X] T024 [P] [US1] Implement `internal/probe/script.go` generating the POSIX sh probe script (JSON lines for `os`, `pkgmgr`, `user`, `shells`, and one `check` line per component running its `check.command` as the image's default user with exit code, first stdout line, and parsed version) and `internal/probe/parse.go`; tests in `internal/probe/script_test.go` (shell-quoting of check commands, `_jsafe` escaping) and `internal/probe/parse_test.go`
- [X] T025 [US1] Implement `internal/probe/cache.go` (facts at `<cache>/probe/facts/<digest>.json`, checks at `<cache>/probe/checks/<digest>/<key>-<sha256(check)[:12]>.json`, `Lookup`, `Store`, `Invalidate(digest)`) and `internal/probe/probe.go` (`Probe(ctx, engine, ref, platform, components, opts)`: pull with platform, digest via inspect (error on empty), facts from cache or full run, per-component checks from cache or a partial run for the missing keys only, `NoCache` forcing invalidation); tests in `internal/probe/probe_test.go` with the fake engine asserting the partial-probe behaviour (FR-012, SC-004)

### mise, tiers, profiles, policy

- [X] T026 [P] [US1] Implement `internal/mise/platform.go` (`linux/amd64` to `linux-x64`, `linux/arm64` to `linux-arm64`, host detection from `runtime.GOARCH`), `internal/mise/config.go` rendering `mise.toml` (`[settings] lockfile=true, locked=true, lockfile_platforms, use_versions_host=false, trusted_config_paths=["/etc/mise"]`, `[tools]` sorted by key) and `internal/mise/lock.go` reading lockfile v3 with `BurntSushi/toml` and `Check(components, platform)` returning per-component errors naming the platform; golden test `internal/mise/testdata/mise.toml.golden` and lock fixtures in `internal/mise/testdata/`
- [X] T027 [P] [US1] Implement `internal/mise/lockcmd.go` running the host `mise lock --platform <list>` in a temp dir seeded with the rendered `mise.toml` and the existing `.sandcast/mise.lock`, copying the result back, with a version check that the host mise is at least 2026.9.16; when `mise lock` fails for some tools (offline host, unreachable release) the per-tool errors from its output are reported per component with the reason while successful entries are kept (edge case "host is offline"); tests in `internal/mise/lockcmd_test.go` with a fake exec covering success, partial failure and offline
- [X] T028 [US1] Implement `internal/component/tiers.go` (`Resolve(shipped, registry, project fs.FS) (Set, error)`: later tier wins by key, each component records `Tier` and `SourceFile`, report of overrides) and `internal/component/conflicts.go` (two components providing the same binary); tests in `internal/component/tiers_test.go`
- [X] T029 [US1] Implement `internal/component/remote.go`: `Refresh(ctx, baseURL, tag, cacheDir)` fetching `index.yaml`, verifying each file's sha256, rejecting mismatches and keeping the previous cache, `LoadCached(cacheDir, tag)`; tests in `internal/component/remote_test.go` with `httptest.Server` covering good, tampered and missing index (FR-017)
- [X] T030 [US1] Implement `internal/profile/tiers.go` (`Resolve(upstream, overlay, project fs.FS) (Set, error)`, later wins by id, winning file recorded); tests in `internal/profile/tiers_test.go`
- [X] T031 [US1] Implement `internal/profile/lint.go` with every rule in R-07 (id predicate, category enum, credential name and env var rules, auth_style requirements, endpoint host and port rules, rules versus access exclusivity, empty rules and deny_rules rejection, tls and enforcement and access enums, tcp request-level field rejection, credentialed endpoints need protocol unless `allow_uninspected_credentials`, non-empty binaries, unknown fields) plus `internal/profile/hostrules.go` (wildcard placement, three-label minimum, no TLD wildcard, no loopback, link-local or metadata hosts, control-plane ports); tests in `internal/profile/lint_test.go` asserting every vendored upstream and overlay profile passes and a negative corpus in `internal/profile/testdata/lint/` fails with the expected message
- [X] T032 [US1] Implement `internal/profile/adapt.go` (`Adapt(set, resolved map[component]paths, runtimeGlobs, opts{Suffix bool, ImageLabel}) (AdaptedSet)`: binaries rewritten to resolved paths plus runtime globs, flagged when empty, id suffixing, lint before write), `internal/profile/index.go` writing `index.yaml` per contracts/adapted-profiles.schema.yaml and the `io.sandcast.profiles-index` JSON; golden tests in `internal/profile/adapt_test.go` with `internal/profile/testdata/adapted/` (FR-022, FR-022a)
- [X] T033 [P] [US1] Implement `internal/policy/policy.go` rendering the image-embedded policy (upstream defaults from `policy/default-v0.1.2.yaml` merged with component `image.read_only` and `image.read_write`, sorted and deduplicated, `process` from the final user, `network_policies: {}`, upstream limits enforced); golden test `internal/policy/testdata/policy.yaml.golden` in `internal/policy/policy_test.go` (FR-032, FR-033)

### Plan

- [X] T034 [US1] Implement `internal/plan/delta.go` (probe result to ProbeDelta: present/missing/incompatible via version selector comparison, actions skip/install/shadow/unresolvable; shadow only for mise installers; package-manager `none` makes package components unresolvable) and `internal/plan/user.go` (final user: probed user kept, or `sandbox` uid 1000 created when root); tests in `internal/plan/delta_test.go`
- [X] T035 [US1] Implement `internal/plan/fragments.go` with `text/template` fragments for `mise` (one aggregated `30-mise` layer per R-04), `package` (aggregated into `10-system-packages` per package manager), `script` (one layer per component, `as_root` handling); golden tests in `internal/plan/fragments_test.go` with `internal/plan/testdata/fragments/`
- [X] T036 [US1] Implement `internal/plan/plan.go` (`Build(inputs) (*Plan, error)` assembling every section of contracts/plan.schema.yaml, platform and engine sources, registry notice, winning profile files, unresolved and conflicts, stored-fragment detection for unresolved keys from `.sandcast/fragments/`), `internal/plan/encode.go` (fixed goccy encoder settings, sorted lists, no timestamps) and `internal/plan/validate.go` (schema check on load); determinism test in `internal/plan/plan_test.go` building twice and comparing bytes, golden plan in `internal/plan/testdata/plan.yaml.golden` (FR-025, FR-026, SC-002)

### Build and verify

- [X] T037 [US1] Implement `internal/build/sections.go` with the Containerfile section templates `00-header`, `10-system-packages`, `20-user`, `30-mise`, `40-recipes`, `50-fragments`, `60-shell`, `80-policy`, `90-footer` per R-14 (USER root before packages, `mkdir -p /var/lib/apt/lists/partial`, `/sandbox` and `/opt/mise` ownership, shell config under `/etc/zsh/zshrc.d/sandcast.zsh` and `/etc/profile.d/sandcast.sh`, CMD login shell) and `internal/build/assemble.go` (plan plus stored fragments to `Containerfile`); golden tests in `internal/build/assemble_test.go` with `internal/build/testdata/Containerfile.golden`
- [X] T038 [US1] Implement `internal/build/resolve.go`: script run in the stage image that prints JSON lines `{component, bin, path, method}` using `mise which` for mise tools, `command -v` plus `readlink -f` for the rest; parser; tests in `internal/build/resolve_test.go`
- [X] T039 [US1] Implement `internal/build/build.go`: two-pass build (`<image>:sandcast-stage`, resolve, adapt profiles, write `profiles/`, `Containerfile.final` with `COPY profiles/ /usr/share/sandcast/profiles/` and the four `io.sandcast.*` labels, final tag, stage tag removal, `:sandcast-debug` retag on failure), `build.log` capture, non-zero exit and no retry on failure (FR-027), `--keep-stage`, `--push`; tests in `internal/build/build_test.go` with the fake engine covering success, pass-1 failure and pass-2 failure
- [X] T040 [US1] Implement `internal/build/redactpass.go` running `redact.Scan` on every file under the build directory and on `build.log` after the build, failing with the key paths on a hit; test in `internal/build/redactpass_test.go`
- [X] T041 [P] [US1] Implement `internal/verify/verify.go`: script executing each component check as the final user in `run --rm --entrypoint /bin/sh <image>`, JSON-line results, `verify.yaml` writer, exit status semantics (report all, then fail if any failed); tests in `internal/verify/verify_test.go` with the fake engine (FR-030)
- [X] T042 [P] [US1] Implement `internal/gateway/openshell.go`: `CheckVersion()` (must be 0.1.2, exit 3 otherwise), `ListProfiles(scope, workspace) []id`, `Import(dir, scope, workspace)`, `Export(id, scope)`, `Update(id, file, scope)`, passing gateway selection flags and environment through; tests in `internal/gateway/openshell_test.go` with a fake exec recording arguments (R-11)

### CLI for User Story 1

- [X] T043 [US1] Implement `internal/cli/baseimages.go` (`base-images list`), `internal/cli/components.go` (`components list|show|refresh`), `internal/cli/lock.go` (`lock --platform --bump`), `internal/cli/probe.go` (`probe [ref] --platform --no-cache --json`); tests in `internal/cli/components_test.go` and `internal/cli/probe_test.go`
- [X] T044 [US1] Implement `internal/cli/plan.go` (`plan --target --platform --no-cache --out`, platform precedence flag > manifest > host, writes `plan.yaml`, `mise/`, `policy.yaml`, exit 2 on conflicts or lockfile gaps with no partial output), `internal/cli/build.go` (`build --target --platform --verify --push --no-cache-layers --keep-stage`) and `internal/cli/verify.go` (`verify --target --image --json`); tests in `internal/cli/plan_test.go` and `internal/cli/build_test.go` using the fake engine end to end on a temp project (FR-026a)
- [X] T045 [US1] Implement `internal/cli/profiles.go` (`profiles lint [path]`, `profiles export --image --suffix`, `profiles import --scope --workspace --suffix --update --dry-run` with existing-id conflict report offering update, suffix or other scope); tests in `internal/cli/profiles_test.go` with the fake gateway (FR-023)

### Integration tests and documentation for User Story 1

- [X] T046 [US1] Write `tests/integration/core_test.go` (build tag `integration`): probe, plan twice (byte-identical), build, verify on `tests/fixtures/reference-project` against the default base image for the host platform; measures and asserts the plan's performance goals: the second (cached) plan completes in under 2 seconds and the first full probe of the already-pulled image in under 60 seconds, printing both durations; skips with a message when podman and docker are both absent
- [X] T047 [US1] Write `tests/integration/gateway_test.go` (build tag `gateway`) and `tests/integration/gateway.sh` starting the v0.1.2 gateway container with the podman driver per R-19, registering it as `sandcast-test`, importing the adapted profiles, creating a sandbox with `--from localhost/<image>` and running `go version` inside; skips when the podman socket is unavailable
- [X] T048 [P] [US1] Write `README.md` (installation, the capture and build flow, pins, project layout) replacing the status-only content
- [X] T049 [P] [US1] Write `docs/cli.md` covering every Phase 1 command and flag from contracts/cli.md
- [X] T050 [P] [US1] Write `docs/configuration.md` covering the manifest, the base image registry, the component definition format, the profile tiers, the lockfile, the `.sandcast/` layout and the cache directory
- [X] T051 [P] [US1] Write `docs/guides/build.md` (manifest to image walkthrough, two-pass build, failure handling) and `docs/guides/profiles.md` (tiers, adaptation, id strategy, import, provider creation, smoke test)
- [X] T052 [US1] Run quickstart.md scenarios 1, 2, 3 and 7 manually against a reachable gateway, record outcomes and any corrections in `specs/001-sandcast-standalone-build/quickstart.md`, and update `CHANGELOG.md`

**Checkpoint**: User Story 1 is fully functional. A checked-in manifest and lockfile rebuild and verify without an agent (SC-001, SC-002, SC-004, SC-006, SC-007 manual).

---

## Phase 4: User Story 2 - Capture a project into a manifest (Priority: P2)

**Goal**: The capture skill, installed into Claude Code by the binary, examines the project and the host, asks for harnesses, proposes components with evidence, curates the shell configuration and writes a manifest that User Story 1 builds.

**Independent Test**: quickstart.md scenario 4 on the reference fixture: the manifest lists go, python, uv, starship, fzf, lsd, claude-code with evidence, a curated zsh configuration with `compinit` and the `TERM` guard, `sandcast manifest validate` passes, and `sandcast build --verify` succeeds.

### Implementation for User Story 2

- [X] T053 [P] [US2] Implement `internal/capture/match.go`: evaluate component `match.files` globs against a project tree (respecting `.gitignore`), `match.host_tools` via `exec.LookPath` plus a version call, `match.harnesses` against selected names, `match.always`; returns proposals with evidence strings and the detected version; tests in `internal/capture/match_test.go` on `tests/fixtures/reference-project` with a fake `LookPath`
- [X] T054 [P] [US2] Implement `internal/harness/adapter.go` (interface from data-model.md: `Name`, `Discover`, `Component`, `Scaffold`, `SkillInstallDir`, `SkillFileName`), `internal/harness/registry.go` (static map, `claude-code` registered), `internal/harness/discovery.go` (Discovery types with redaction applied on construction); tests in `internal/harness/registry_test.go`
- [X] T055 [US2] Implement `internal/harness/claudecode/discover.go`: read `~/.claude.json` and `<project>/.mcp.json` for MCP servers, `~/.claude/plugins/installed_plugins.json` for plugins, `~/.claude/skills/` and `<project>/.claude/skills/` for skills, replace secrets through `redact`, report placeholders; tests in `internal/harness/claudecode/discover_test.go` with fixture files in `internal/harness/claudecode/testdata/` including an embedded token (FR-038, SC-009)
- [X] T056 [US2] Implement `internal/harness/claudecode/skills.go` (`SkillInstallDir(user)` = `~/.claude/skills`, `SkillInstallDir(project)` = `<project>/.claude/skills`, `SkillFileName` = `SKILL.md`) and `internal/harness/claudecode/component.go` returning the shipped `claude-code` component; tests in `internal/harness/claudecode/skills_test.go`
- [X] T057 [US2] Implement `internal/skills/bundle.go` (embedded `skills/` access, `VERSION` reading, frontmatter parsing), `internal/skills/install.go` (`Install(harness, scope, force)` copying `skills/<name>/` to `<dir>/sandcast-<name>/`), `internal/skills/check.go` (compare installed `sandcast_version` with bundle version, message naming which to update); tests in `internal/skills/install_test.go` and `internal/skills/check_test.go` with `t.TempDir()` homes (FR-003, FR-004, SC-008)
- [X] T058 [US2] Implement `internal/cli/capture.go` (`components match [dir] --host --no-host --json`, `manifest set-shell --program --file`), `internal/cli/harness.go` (`harness list`, `harness discover <name> --json`), `internal/cli/skills.go` (`skills install --harness --scope --force`, `skills check --harness --json` exiting 3 on mismatch); tests in `internal/cli/capture_test.go` and `internal/cli/skills_test.go`
- [X] T059 [US2] Write `skills/VERSION` and `skills/capture/SKILL.md` implementing the capture contract in contracts/skill-bundle.md (version check first, harness question upfront, `sandcast components match --json`, confirmation loop, shell curation rules from brainstorm 072 changes 7, 11, 12, 13, `manifest set-shell`, `manifest validate`, offer `sandcast lock`); when `components match` returns no project proposals the skill states that nothing was detected and still offers the tier components and detected host tools as optional additions (edge case "no recognizable ecosystem files") (FR-034 to FR-037)
- [X] T060 [US2] Write the initial `skills/build/SKILL.md` implementing steps 1, 2 and the success path of the build contract (plan, fragment writing for unresolved components through `sandcast fragments write`, `sandcast build --verify`, offer import); the repair loop and promotion are added in Phase 5
- [X] T061 [US2] Write `internal/skills/structure_test.go` asserting every `skills/*/SKILL.md` has the required frontmatter keys, that `sandcast_version` equals `skills/VERSION`, and that the mandatory step phrases from contracts/skill-bundle.md ("sandcast skills check", "sandcast manifest validate" for capture; "sandcast plan --json", "sandcast build --verify" for build) are present
- [X] T062 [P] [US2] Write `docs/guides/capture.md` (installing the skills, running a capture, what gets proposed and why, shell curation rules, secrets handling) and extend `docs/cli.md` with the Phase 2 commands
- [X] T063 [US2] Run quickstart.md scenario 4 on the reference fixture with Claude Code, record the manifest produced under `tests/fixtures/reference-project/expected-manifest.yaml`, and add `tests/integration/capture_test.go` (tag `integration`) that builds and verifies that expected manifest

**Checkpoint**: User Stories 1 and 2 work independently. A capture produces a manifest that builds with at most one repair iteration (SC-003) and contains no secrets (SC-009).

---

## Phase 5: User Story 3 - Repair a failed build and promote the fix (Priority: P3)

**Goal**: The build skill writes fragments for unresolved components, repairs failures within a retry limit while recording learnings, and promotes a working fragment into a project-local recipe that the non-interactive build then uses.

**Independent Test**: quickstart.md scenario 5: a component with no installer produces a self-written fragment, a forced failure, a learnings entry, a fix, a success, a promotion prompt, and after promotion `sandcast build --verify` succeeds without Claude Code (SC-005).

### Implementation for User Story 3

- [X] T064 [P] [US3] Implement `internal/build/fragments.go`: stored fragment file format with the `# sandcast-fragment:` header (key, written, status active or superseded), `List`, `Write(key, text)` validating the text as a Containerfile snippet (no `FROM`, no `USER root` left open at the end), `MarkSuperseded`, stale detection when a recipe now covers the key (used by `plan` to report "stale"); tests in `internal/build/fragments_test.go` (FR-028, edge case)
- [X] T065 [P] [US3] Implement `internal/build/learnings.go`: `.sandcast/learnings.yaml` schema (attempt, component, failure, change, outcome, date), `Append`, `Load` with validation; tests in `internal/build/learnings_test.go`
- [X] T066 [US3] Implement `internal/build/promote.go`: convert a stored fragment into `.sandcast/recipes/<key>.yaml` (component with `installer.script.run` from the fragment body, `bins` from flags or the component's `provides`, `check` copied from the manifest component or a default `command -v`, `promoted_from`), validate it through `component.Load`, mark the fragment superseded; tests in `internal/build/promote_test.go` (FR-018)
- [X] T067 [US3] Extend `internal/component/tiers.go` so the project tier also loads `.sandcast/recipes/` and extend `internal/plan/plan.go` so a promoted recipe takes precedence over a stored fragment with a "stale fragment" notice; tests added to `internal/plan/plan_test.go`
- [X] T068 [US3] Implement `internal/cli/fragments.go` (`fragments list`, `fragments write <key> --file`), `internal/cli/promote.go` (`promote <key> --name --bins --yes`), `internal/cli/learnings.go` (`learnings add --component --failure --change --attempt --outcome`); tests in `internal/cli/fragments_test.go` and `internal/cli/promote_test.go`
- [X] T069 [US3] Extend `skills/build/SKILL.md` with the repair loop (read `build.log`, `sandcast learnings add`, fix fragment, retry up to `--max-attempts` default 3, stop leaving the last Containerfile), the fragment rules (USER root and back, one RUN, `set -eux -o pipefail`, parent directory ownership, pinned URLs with checksums), and the promotion step on explicit confirmation (FR-029, US3 scenarios 2 and 3); update `internal/skills/structure_test.go` with the new mandatory phrases
- [X] T070 [US3] Write `tests/integration/promote_test.go` (tag `integration`): seed a stored fragment for a fake tool, build non-interactively (fragment reused), promote through the CLI, build again and assert the recipe path was used and the fragment is reported stale (US3 scenario 4, SC-005)
- [X] T071 [P] [US3] Document fragments, learnings and promotion in `docs/guides/build.md` and the new commands in `docs/cli.md` and `docs/configuration.md`

**Checkpoint**: A build with an unknown tool converges to a deterministic recipe; CI rebuilds without an agent.

---

## Phase 6: User Story 4 - Bake a harness with its local extensions (Priority: P4)

**Goal**: Selected harnesses are installed in the image with their MCP servers, plugins and skills, configured for non-interactive use, with the harness's upstream profile adapted and extra project-local profiles for uncovered MCP endpoints.

**Independent Test**: quickstart.md scenario 2 extended: capture with Claude Code selected plus one MCP server and one plugin, build, start the sandbox with the providers attached, run `claude --version` and `claude plugins list`, and confirm the adapted `claude-code.yaml` carries the resolved path. Requires a gateway and an Anthropic credential.

### Implementation for User Story 4

- [X] T072 [US4] Implement `internal/harness/claudecode/scaffold.go` producing the `70-harness` fragment: `claude plugins marketplace add anthropics/claude-plugins-official` before any `claude plugins install`, plugin installs from the manifest, skills copied to the harness skills directory, `settings.json` merge with the exact jq expression from brainstorm 072 change 9, MCP server entries written with `${SANDCAST_SECRET_N}` placeholders, non-interactive flags (onboarding skipped, telemetry prompt disabled), config directories created and owned by the final user before any command runs as that user; golden test in `internal/harness/claudecode/scaffold_test.go` with `internal/harness/claudecode/testdata/70-harness.golden` (FR-038 scaffolding, US4 scenario 2)
- [X] T073 [US4] Implement `internal/profile/mcp.go`: for each manifest MCP server with a `url` whose host is not covered by any referenced profile endpoint, generate a project-local profile in the upstream schema (`id: mcp-<name>`, `category: other`, endpoint host and port with `protocol: rest` when a credential is declared, credential declaration with `env_vars` from the placeholder names and no value, `binaries` bound to the agent's resolved path), lint it, and add it to the adapted set; tests in `internal/profile/mcp_test.go` (FR-039, US4 scenario 3)
- [X] T074 [US4] Wire harness handling into `internal/plan/plan.go` and `internal/build/assemble.go`: harness components added from the adapter, the `70-harness` section rendered only for manifest harnesses, MCP profiles generated after resolution; extend golden tests in `internal/plan/plan_test.go` and `internal/build/assemble_test.go` with a harness manifest fixture
- [X] T075 [US4] Extend `skills/capture/SKILL.md` with the harness discovery step (only selected harnesses, `sandcast harness discover`, user chooses which MCP servers, plugins and skills to bake, secret placeholders explained) and update `internal/skills/structure_test.go` (FR-036, US4 scenario 1)
- [X] T076 [US4] Extend `tests/integration/gateway_test.go` with a harness case: build with `claude-code` and an MCP server fixture, import profiles, assert the sandbox can run `claude --version` and that `openshell profile list` shows `claude-code` and `mcp-<name>`; the inference call is documented as a manual step needing a credential (SC-007)
- [X] T077 [P] [US4] Document harness baking, MCP profiles and credential handling at runtime in `docs/guides/capture.md`, `docs/guides/profiles.md` and `docs/configuration.md`

**Checkpoint**: Two-harness captures are possible in design; Claude Code is proven end to end.

---

## Phase 7: User Story 5 - Build a plain container image (Priority: P5)

**Goal**: The same manifest builds a plain container image without OpenShell conventions, verifies under the engine, and writes an allowed-domains list.

**Independent Test**: quickstart.md scenario 6: `sandcast build --target container --verify` passes, `allowed-domains.txt` lists every referenced endpoint host sorted and unique, no `/etc/openshell/policy.yaml` in the image, `podman run` works.

### Implementation for User Story 5

- [X] T078 [US5] Extend `internal/build/sections.go` and `internal/build/assemble.go` for the container target: skip `80-policy`, skip the `iproute2` and `nftables` target-filtered components, keep the same user logic and `WORKDIR /sandbox`, `CMD` login shell; golden test `internal/build/testdata/Containerfile.container.golden` (FR-040, US5 scenario 1)
- [X] T079 [P] [US5] Implement `internal/profile/domains.go` deriving `allowed-domains.txt` (sorted unique hosts of every referenced and generated profile endpoint, wildcards kept verbatim) and write it from `internal/build/build.go` for the container target; tests in `internal/profile/domains_test.go` (US5 scenario 3)
- [X] T080 [US5] Extend `internal/cli/plan.go`, `internal/cli/build.go` and `internal/cli/verify.go` so `--target container` selects the container registry list, build directory `build/container/`, and skips the policy and the pass-2 labels specific to OpenShell while keeping the profile set copy and labels; tests added to `internal/cli/build_test.go`
- [X] T081 [US5] Extend `tests/integration/core_test.go` with a container-target case building the fixture for both targets and asserting verify passes for the same component set in both (US5 scenario 2)
- [X] T082 [P] [US5] Document the container target in `docs/guides/build.md`, `docs/cli.md` and `docs/configuration.md`

**Checkpoint**: All five user stories are independently functional.

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Release readiness, consistency, and the full validation pass

- [X] T083 [P] Add `tests/golden/README.md` describing how to regenerate golden files with the `internal/testutil` helper (`UPDATE_GOLDEN=1 go test ./...`) and listing every golden directory
- [X] T084 [P] Add `docs/pins.md` (how to refresh the OpenShell and mise pins: re-vendor profiles, re-run the lint corpus, update `policy/default-v0.1.2.yaml`, CHANGELOG entry) and link it from `README.md`
- [X] T085 Review every `skills/*/SKILL.md` against contracts/skill-bundle.md and the constitution's credential rule as a code review, recording findings in `specs/001-sandcast-standalone-build/checklists/skills-review.md`
- [X] T086 Run `make verify`, `make test-integration` and `make test-gateway`, then every quickstart.md scenario; fix regressions; update `CHANGELOG.md` with the release notes for the first tagged version
- [X] T087 [P] Update `CLAUDE.md` "Project Structure" from "planned" to the delivered layout and add the make targets to "Build and Tool Rules"

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies
- **Foundational (Phase 2)**: depends on Phase 1; blocks all stories
- **US1 (Phase 3)**: depends on Phase 2; the MVP and delivery 1
- **US2 (Phase 4)**: depends on Phase 2 for packages and on US1 for the end-to-end test (T063); skills and capture code can start after Phase 2
- **US3 (Phase 5)**: depends on US1 (build, plan) and on the initial build skill from US2 (T060)
- **US4 (Phase 6)**: depends on US1 (adaptation, build sections) and US2 (adapter interface, capture skill)
- **US5 (Phase 7)**: depends on US1 only
- **Polish (Phase 8)**: depends on every story being complete

### Within User Story 1

- Assets (T019 to T023) before lint and plan tests that load them
- T024 before T025; T026 before T035 and T036; T028 and T030 before T036; T031 before T032; T033 before T036
- T036 before T037; T037 and T038 before T039; T039 before T040
- T041 and T042 can run alongside T037 to T040
- T043 to T045 after their packages; T046 and T047 after T044 and T045
- Documentation (T048 to T051) can run alongside the CLI tasks; T052 last

### Parallel Opportunities

- Phase 1: T003 to T007 in parallel after T001 and T002
- Phase 2: T009 to T014 and T018 in parallel after T008
- US1 assets: T019 to T022 in parallel; T024, T026, T027, T033 in parallel once T023 passes
- US1 late: T041, T042 alongside T037 to T040; docs T048 to T051 together
- US2: T053 and T054 in parallel; T062 alongside T059 to T061
- US3: T064 and T065 in parallel; T071 alongside T068 to T070
- US4: T077 alongside T072 to T076
- US5: T079 and T082 alongside T078
- Polish: T083, T084, T087 in parallel

---

## Parallel Example: User Story 1 assets and core packages

```bash
# After Phase 2, launch the asset tasks together:
Task: "Vendor the 16 upstream example profiles into profiles/upstream/v0.1.2/"
Task: "Write overlay profiles in profiles/overlay/"
Task: "Write policy/default-v0.1.2.yaml"
Task: "Write the shipped components in components/"

# Then launch independent packages together:
Task: "Implement internal/probe/script.go and parse.go"
Task: "Implement internal/mise/platform.go, config.go, lock.go"
Task: "Implement internal/mise/lockcmd.go"
Task: "Implement internal/policy/policy.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only, delivery 1)

1. Complete Phase 1 and Phase 2
2. Complete Phase 3 in the order given; stop at the checkpoint
3. Validate with quickstart scenarios 1, 2, 3 and 7 against a real gateway
4. Tag a first release: a checked-in manifest rebuilds in CI without an agent

### Incremental Delivery

1. Delivery 1: US1 (Phase 3). Core build, profiles, import, docs.
2. Delivery 2: US2 then US4 (Phases 4 and 6). Capture, skills installer, Claude Code adapter.
3. Delivery 3: US3 then US5 (Phases 5 and 7). Repair loop, promotion, container target.
4. Polish (Phase 8).

### Parallel Team Strategy

After Phase 2, one developer takes US1 assets and probe, a second takes mise, profiles and lint, a third takes policy and plan. After US1, one developer takes US2 and US4 (skill and adapter track) while another takes US3 and US5 (build track).

---

## Notes

- Every task includes its tests because constitution principle I requires them; integration tests sit behind the `integration` and `gateway` build tags.
- Documentation tasks live inside each story phase so documentation ships in the same branch as the change.
- Pins (`OpenShellPin`, `MisePin`, `ComponentRegistryPin`) are constants; changing one requires a CHANGELOG entry.
- Commit after each task or logical group. Never commit `.mcp.json` or any file with a credential.
