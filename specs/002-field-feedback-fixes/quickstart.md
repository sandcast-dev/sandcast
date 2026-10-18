# Quickstart: Validating the Field Feedback Fixes

Runnable checks that prove each phase works end to end. Expected report shapes are in [contracts/reports.md](contracts/reports.md); image files in [contracts/image-shell.md](contracts/image-shell.md).

## Prerequisites

- `make build` produced `./sandcast`.
- podman with a running machine (macOS) or podman on Linux.
- For gateway scenarios: `make test-gateway` prerequisites (OpenShell v0.1.3 gateway container, `openshell` CLI v0.1.3).
- sh, bash and zsh on the host for the snippet test.
- A scratch project directory; examples use `$P`.

## Automated suites

```sh
make verify             # unit, golden, structure and snippet tests, lint
make test-integration   # podman: session environment on nvidia-community-base
make test-gateway       # podman plus a v0.1.3 gateway: session via openshell sandbox exec
```

All three must pass before a phase is considered done.

## Phase 1: image correctness

### Lockfile decides versions (#1, SC-001)

```sh
cd "$P" && sandcast init --name lockdemo
# add unpinned gh, node, python, codex components to .sandcast/manifest.yaml
sandcast lock --platform linux/arm64 --platform linux/amd64
sandcast plan --json | jq '.probe_delta[] | select(.component_key|IN("gh","node","python","codex")) | {component_key, action, installed_version, locked_version}'
sandcast build --verify --json | jq '.notices'
```

Expected: every differing component has `action: shadow` and a `lockfile-shadow` or `unknown-version` notice; verify reports the locked versions.

### Workspace path (#2, SC-002)

Expected from the same plan: `python` has a `workspace-path` notice and is installed; after the build, no resolved path in `.sandcast/build/openshell/profiles/` starts with `/sandbox/`, and the adapted `pypi` profile is not flagged.

### Session environment (#11, SC-003, SC-004)

```sh
# shell.program: zsh with a curated rc in the manifest
IMG=$(sandcast build --verify --json | jq -r .image_ref)
podman run --rm --entrypoint /usr/bin/env "$IMG" -i HOME=/sandbox USER=sandbox TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin /bin/bash -lc 'command -v starship mise'
podman run --rm -it --entrypoint /usr/bin/env "$IMG" -i HOME=/sandbox USER=sandbox TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin /bin/bash -i
```

Expected: the first command prints both paths. The second opens zsh with the curated prompt and prints no error lines and no new-user wizard. `echo $0` shows zsh, `bash -lc 'echo $0'` inside it still runs bash.

Negative check (SC-004): remove the zshenv block from a derived image (`FROM $IMG` plus `RUN sed -i '/sandcast/d' /etc/zsh/zshenv`) and run `sandcast verify` against it; expect `session.zsh.*` failures and exit status 1.

Gateway check: `make test-gateway` creates a sandbox from the built image and runs `openshell sandbox exec -n <name> -- command -v starship`, plus `openshell sandbox exec --tty -n <name> -- bash -i -c 'echo $0'`.

### Pinned versions, binary side (#3)

```sh
sandcast components match tests/fixtures/pinned-project --json | jq '.proposals[] | {key, pinned_versions}'
```

Expected: `rust` 1.95.0 from `rust-toolchain.toml`, `python` 3.14.5 from `.python-version`, `go` 1.26.7 from `go.mod`, each `kind: exact`.

## Phase 2: delivery to the gateway

### Reference, push and digest (#10, #9, SC-006)

```sh
podman run -d --rm -p 5000:5000 --name reg docker.io/library/registry:2
sandcast init --name pushdemo --image localhost:5000/demo/pushdemo --push --force
sandcast build --json | jq '{image_ref, sandbox_create, pushed, digest}'
grep -n 'sandcast push' .sandcast/build/openshell/build.log
```

Expected: `pushed: true`, a `sha256:` digest, a `sandbox_create` line with the same reference, and a push section in the log.

### Reference warnings (#10, #8, SC-007)

```sh
sandcast init --name hubdemo --image docker.io/hubdemo --force
sandcast manifest validate          # warning about docker.io/library/hubdemo, exit 0
sandcast init --name localdemo --force   # prints the localhost/ note
```

With a remote gateway registered in `openshell gateway list` as active, `sandcast build` prints a `gateway` notice for the `localhost/` reference.

### Emulation notice (#9)

On an arm64 host with a manifest containing `rust` and `platform: linux/amd64`, `sandcast plan` prints an `emulation` notice, and `sandcast plan --json` output does not contain it. With `CONTAINER_CONNECTION` pointing at an amd64 podman host, no notice appears.

### Guide

`docs/guides/cross-arch.md` exists and is linked from the README, `docs/cli.md` and `docs/guides/build.md`.

## Phase 3: capture facts

### Declared binaries (#4)

Manifest entry `{key: helix, optional: true, bins: [hx]}` with a stored fragment that installs only `hx`: `sandcast build --verify` passes without a `helix` symlink; `sandcast promote helix` proposes `bins: [hx]`. Adding `bins` to `go` fails `sandcast manifest validate`.

### Discovery (#5, #7, SC-008)

`go test ./internal/harness/claudecode/ -run TestDiscoverGitMarketplaces` builds throwaway repos (GitHub remote, other remote, dirty tree, name mismatch, no remote) and asserts proposals, notes and portability markers.

### Skill snippets (#6, SC-009)

`go test ./internal/skills/ -run TestSnippets` runs every `sh` block under sh, bash and zsh with stub exit codes 0 and 2 and asserts the exit code and an empty `TMPDIR`.

### Capture end to end

Run `/sandcast-capture` on `tests/fixtures/pinned-project` with Claude Code: the skill proposes `version:` from the pinned values, asks where the image will run, passes `--image`, `--platform` and `--push` to init for a remote gateway, and offers to copy non-portable sources into the project based on the `portable` marker.
