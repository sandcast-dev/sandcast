# Idea Inbox

Ideas captured from code reviews for future brainstorming.

### deny-by-default-credentials

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Credential detection by name and shape heuristics did not converge over four review passes; a deny-by-default rule for MCP env values, header values and secret-flag arguments (placeholder or `${VAR}` reference only, unless allow-listed) would replace most heuristics with one checkable rule.

> Every widening of the heuristics produced false positives on shipped content, and every narrowing reopened a leak. The scans would remain a backstop.

### registry-trust-anchor

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: The component registry index checksums come from the same moveable tag as the files; compile the pinned index sha256 into the binary or pin to a commit.

> The mise binary download is already anchored to compiled-in checksums; the registry is not.

### container-init-on-cancel

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Probe, resolve and verify containers run `/bin/sh` as PID 1 without `--init`, so forwarded signals are ignored and a hung check can leave an orphaned container.

> Name each run and remove it on cancellation, or add `--init`.

### archives-in-staged-trees

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Archives in staged skill or marketplace trees reach the image without a scan that can see inside them.

> VCS directories are already skipped for the same reason; archives could be refused or reported by magic bytes.

### gateway-tests-in-ci

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: CI does not run `make test-gateway`, and the gateway tests skip silently without the openshell CLI.

> An optional or nightly job with the pinned openshell CLI would catch drift between `internal/gateway` and the real gateway.

### path-containment-helper

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: The "path inside root" check is hand-written in several packages with two different rules.

> One `fsutil.Within` helper would make the containment guarantees uniform.

### adapter-registration

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Harness adapters are registered only through a blank import in the CLI package.

> A library user of `pipeline.Plan` gets "no adapter" without a compile-time signal.

### index-digest-source

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: The profile index digest is the local stage image ID, not a registry digest.

> Consumers that compare it with a pushed image will not find a match.

### test-gateway-network

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: The throwaway test gateway runs on the sandbox network.

> Isolation between the test gateway and sandboxes is weaker than in a real deployment.

### mise-plugins-on-host

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: `sandcast lock` runs mise, whose plugin backends execute on the host.

> Locking an untrusted project runs its tool backends with the developer's privileges.

### build-logs-untrusted

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: The build skill should treat build logs as untrusted data, not instructions.

> Image build output can contain text written by the packages being installed.

### probe-cache-eviction

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: The probe cache is never evicted.

> Entries accumulate per base image digest and platform.

### engine-platform-source

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: The host platform comes from the binary architecture, not from the container engine.

> A Rosetta-translated binary or a remote engine reports a different platform than the one builds run on.

### sorted-keys-helpers

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Several packages carry their own sorted-keys helper.

> `slices.Sorted(maps.Keys(m))` covers all of them.

### build-phase-markers

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Have sandcast write explicit phase markers to build.log (plan, pull, probe, resolve, build, push, verify) so the build skill's stall rule can tell a bounded download from a true compile stall without keying off the single `### sandcast pass 1` line.

> The round-3 fix of the stall rule (R3-4) had to approximate the long-compile phase from one marker because no phase markers exist; real markers would make the skill's progress detection exact and remove the approximation.

### sandbox-only-tools-vanish-at-runtime

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Base-image tools that exist only under `/sandbox` count as present at probe time, but the Kubernetes driver mounts the workspace over `/sandbox`, so they disappear at runtime.

> The probe sees the base image filesystem, the runtime sees the mounted workspace; a tool found only under `/sandbox` passes the probe and then is missing in the running sandbox.

### workspace-writable-rc-exec

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Startup files in a workspace-writable home (`.zshenv`, `.zprofile`, `.bashrc`) are the same code-execution vector as `.zcompdump`, which R3-era fixes addressed only for `compinit`.

> When the home is the writable `/sandbox`, any rc file sourced at shell start runs as code; the compinit hardening closed one instance of a broader class.

### child-process-pdeathsig

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: Child processes sandcast spawns have no `Pdeathsig`, so a killed sandcast can leave engine or mise children running.

> Combined with the unbounded caches note, process and resource lifecycle on abnormal exit deserves one pass.

### build-run-decomposition

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: `build.Run` is long, the harness redaction code would fit better in `redact`, and the `Fake` engine sits in a production package.

> These are structural clarity items that accumulated as the build path grew; a focused refactor would separate production from test seams and move redaction to its owner.

### gid0-run-as-group

- **Source**: deep-review
- **Date**: 2026-10-09
- **Reference**: 001-sandcast-standalone-build
- **Summary**: A base user with GID 0 gets its UID as `run_as_group`, which may coincide with a privileged group.

> The round-1 GID 0 policy fix handles the policy failure, but the derived `run_as_group` value itself can still land on a sensitive group id.

### live-gateway-profile-source

- **Source**: discussion
- **Date**: 2026-10-12
- **Reference**: OpenShell v0.1.3 pin refresh
- **Summary**: Add an optional mode that reads provider profiles from a reachable gateway instead of only the vendored set, and a drift check that compares the vendored profiles against what a live gateway reports. The vendored set stays the default so offline, deterministic, no-gateway builds keep working; the live source is opt-in for when a gateway is available.

> Today refreshing the pin means a manual re-vendor on every OpenShell release. A live source would let a build pick up the running gateway's profiles directly, and a drift check would flag when the vendored set has fallen behind the gateway, turning the re-vendor into a signalled action rather than a periodic manual chore.

### openshell-0.1.3-deferred-validation

- **Source**: discussion
- **Date**: 2026-10-12
- **Reference**: OpenShell v0.1.3 pin refresh
- **Summary**: The v0.1.3 refresh ported the stable profile validation but deferred two pieces. (1) The experimental `ProviderFileProfile` (`files`) feature: a `files` list on a provider profile with `path`/`content`/`env_var` and a `{{config.KEY}}` template, which upstream marks "may change or be removed". Porting means a new struct in `internal/profile/types.go`, lint rules (at most 16 files, path/content/env_var checks, the `files.env_var`-vs-credential conflict, the `{{config.KEY}}` validator and render, `valid_file_config_key`), and `files.*` in the null-object path list. (2) The token_grant audience-override to endpoint binding-conflict checks (the rest of `validate_token_grant_audience_overrides`): detect two overrides that bind to the same endpoint host and port with the same match score. Porting means typing the `token_grant` pass-through block and reimplementing the scoring and binding logic.

> Neither feature is used by any vendored or overlay profile, token_grant is a pass-through block that the gateway re-validates on import (R-07), and `files` is upstream-unstable, so both were left out of the stable-parity refresh. Pick this up when a profile actually uses `files` or `token_grant`, or when upstream marks `files` stable.

### skeleton-options-signature

- **Source**: deep-review
- **Date**: 2026-10-13
- **Reference**: 002-field-feedback-fixes
- **Summary**: `manifest.Skeleton` takes a variadic options struct and silently ignores extra arguments.

> A plain `opts SkeletonOptions` parameter makes the decision explicit; harmless today.

### root-shell-mise-shims

- **Source**: deep-review
- **Date**: 2026-10-13
- **Reference**: 002-field-feedback-fixes
- **Summary**: Root shells in the image also prepend `/opt/mise/shims`, a directory the final user owns.

> Not new (the image ENV PATH did the same), but the startup files make it sticky; revisit if images are used with root sessions.

### streaming-push-log

- **Source**: deep-review
- **Date**: 2026-10-13
- **Reference**: 002-field-feedback-fixes
- **Summary**: Push output reaches build.log only after the push finishes, so long pushes look stuck.

> A line-buffered redacting writer could stream the push into the log and the progress output.

### lockfile-test-moving-latest

- **Source**: deep-review
- **Date**: 2026-10-13
- **Reference**: 002-field-feedback-fixes
- **Summary**: `TestLockfileDecidesVersions` relies on `latest` differing from the base image's tool versions.

> A base image refresh can make the correct `skip` decision fail the test; pin selectors or accept skip when versions match.

### plugins-from-local-marketplaces

- **Source**: discussion
- **Date**: 2026-10-12
- **Reference**: openshell-demo capture
- **Summary**: The Claude Code adapter only copies plugin sources that live inside the project, `~/.claude/skills` or `~/.claude/plugins`. A plugin whose marketplace is a local filesystem path (for example a sibling checkout under `~/Development/...`) is enabled in the user's config but silently skipped at capture, so the built image is missing it. Find a way to include plugins from local marketplaces. Options: resolve the local marketplace directory's git remote and record that as the marketplace source in the manifest (works when the repo is reachable from the build, for example a public GitHub repo or with build-time credentials); or copy the local marketplace sources into the project or image for repos on internal git hosts behind a VPN that the build cannot reach. The manifest and capture flow would need a way to represent a marketplace by remote URL and/or by vendored copy, and to decide per plugin which route to take.

> In the openshell-demo capture, `installed_plugins.json` had 35 entries: 9 were offered (enabled, user-scoped, GitHub marketplaces), 13 were enabled but skipped because their marketplace is a local path, 10 were disabled, and 3 were scoped to other projects. The 13 skipped plugins are the gap this idea closes; most have a GitHub remote already, a few are only on internal hosts and would need the vendored-copy route.

### dotfiles-in-masked-home

- **Source**: discussion
- **Date**: 2026-10-12
- **Reference**: openshell-demo capture
- **Summary**: Capture bakes the curated shell configuration into `/etc` (`/etc/zsh/zshrc.d/sandcast.zsh`, `/etc/profile.d/sandcast.sh`, sourced from the system rc), so it survives the OpenShell home overmount: the Kubernetes driver masks the user's home (`/sandbox`) with the workspace volume, but `/etc` and `/opt` are untouched. General home dotfiles (`.gitconfig`, tool configs under `~/.config`, `.tmux.conf`, and similar) are not captured, so a user's wider environment is missing in the sandbox. Add a way to bake selected home dotfiles into the image so they survive the masked home, using relocation rather than home symlinks: a baked `ZDOTDIR` tree (set `ZDOTDIR` in `/etc/zshenv`, which zsh reads first and is not under the home, pointing at for example `/opt/sandcast/zdotdir`) for zsh; `XDG_CONFIG_HOME`/`XDG_DATA_HOME` or per-tool env vars (`GIT_CONFIG_GLOBAL=/etc/gitconfig`, `STARSHIP_CONFIG`, and so on) for XDG-aware apps; and `/etc` system files for bash. Reuse the same credential-redaction and copy-into-project rules the shell and skill flows already apply.

> Baked home symlinks do not help: a symlink at `/sandbox/.zshrc` lives under the masked home and is hidden by the overmount. Runtime copy-into-home is a weak fallback: it needs the workspace writable, writes into the project tree, and `/etc/skel` does not apply to a pre-existing overmounted home (skel seeds only at user creation). The robust pattern, already proven for the shell config, is to place the content outside the home and point the program at it with an environment variable; this idea generalizes that from the shell to the rest of the user's dotfiles. Related: [[plugins-from-local-marketplaces]] and the still-unfiled "persist the harness credential store across sandbox recreations" gap.
