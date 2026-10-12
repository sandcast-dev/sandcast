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
