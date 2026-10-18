---
runbook_version: 1
feature: 002-field-feedback-fixes
status: drafted
mode: full
steps_total: 9
step_results: pending,pending,pending,pending,pending,pending,pending,pending,pending
drafted_at: b4214da
drafted_fingerprint: d81c786fd0c060fea9b55e2f0ee11789eaf172c934f9b9110ff6832f258f1d10
tested_at:
tested_fingerprint:
updated: 2026-10-13
---

# Try it: Field Feedback Fixes

<!-- runbook-status -->
> **Status:** drafted · 0 of 9 steps · Mode: full · Drafted at b4214da · 2026-10-13
<!-- /runbook-status -->

## What you're testing

This runbook takes one project from an empty directory to a built OpenShell image and checks the field feedback fixes along the way.

1. The binary reads pinned versions from a project's files.
2. A fresh project with unpinned components gets a lockfile.
3. The plan replaces base image tools whose version differs from the lockfile ("shadow"), and it explains why in notices.
4. The build produces an image whose session shells (login bash, interactive bash, zsh) see the mise tools even under OpenShell's cleared environment.
5. Image reference warnings appear at init and validate time.
6. The capture fact tests run: git marketplace discovery and skill snippet hygiene.

Run every command from the worktree root.
The demo project lives in the self-ignoring scratch folder `specs/002-field-feedback-fixes/.walkthrough/`.

## Test environment

No fixtures are started for you.
The steps need a running podman (on macOS, `podman machine start`), `jq` and `yq` on the host.
The build step pulls `nvidia-community-base` and installs tools with mise, so it takes several minutes on the first run.

## Steps

### Step 1 of 9: Build the binary (US1)

**Why this step:** every later step runs `./sandcast` from this branch.

**Run:**

```
make build && ./sandcast version
```

**What you should see:** the build finishes without errors, and `version` prints the binary version plus the pinned OpenShell v0.1.3 and mise v2026.10.4 releases.

**If you see something else:** a compile error means the worktree is not at the branch head; run `git status` and `git log -1`.

**Result:**

### Step 2 of 9: Read pinned versions from project files (US2)

**Why this step:** capture should propose the versions a project already pins instead of the latest ones (issue #3).

**Run:**

```
./sandcast components match tests/fixtures/pinned-project --json | jq '.proposals[] | select(.pinned_versions != null) | {key, pinned_versions}'
```

**What you should see:** `rust` with 1.95.0 from `rust-toolchain.toml`, `python` with 3.14.5 from `.python-version` and `go` with 1.26.7 from `go.mod`, each with `kind: exact`.

**If you see something else:** an empty result means the pin readers did not run; a `notes` entry naming a file means that file failed to parse and was skipped by design.

**Result:**

### Step 3 of 9: Create a demo project and lock it (US2)

**Why this step:** unpinned components plus a lockfile are the setup in which the base image's own tool versions used to win (issue #1).

**Needs:** Step 1.

**Run:**

```
mkdir -p specs/002-field-feedback-fixes/.walkthrough/demo && ./sandcast --project specs/002-field-feedback-fixes/.walkthrough/demo init --name lockdemo --force && yq -i '.components = [{"key":"gh"},{"key":"node"},{"key":"python"},{"key":"zsh"},{"key":"starship"}] | .shell.program = "zsh"' specs/002-field-feedback-fixes/.walkthrough/demo/.sandcast/manifest.yaml && ./sandcast --project specs/002-field-feedback-fixes/.walkthrough/demo manifest validate && ./sandcast --project specs/002-field-feedback-fixes/.walkthrough/demo lock --platform linux/arm64 --platform linux/amd64
```

**What you should see:** init prints the `localhost/lockdemo` note about local gateways, validate exits 0, and `lock` writes `.sandcast/mise.lock` with entries for both platforms.

**If you see something else:** a validate error on `shell` means the manifest schema names the field differently; check `sandcast manifest set-shell --help`.

**Result:**

### Step 4 of 9: Plan shows shadowing and notices (US2)

**Why this step:** the lockfile must decide versions, and the plan must say when it overrides a base image tool or skips a workspace copy (issues #1 and #2).

**Needs:** Step 3.

**Run:**

```
./sandcast --project specs/002-field-feedback-fixes/.walkthrough/demo plan --json | jq '{delta: [.probe_delta[] | {component_key, action, installed_version, locked_version}], notices}'
```

**What you should see:** components whose base image version differs from the locked one show `action: shadow`; the notices list contains `lockfile-shadow` or `unknown-version` lines for them, and `python` carries a `workspace-path` notice because the base image's copy lives under `/sandbox/.venv`.

**If you see something else:** `action: skip` with a differing version is the original bug; a missing `notices` key means the JSON shape changed (see `contracts/reports.md`).

**Result:**

### Step 5 of 9: Build and verify the image (US1, US2)

**Why this step:** verify now runs session checks that imitate OpenShell's cleared environment, so a passing build proves the tools reach real sessions (issue #11).

**Needs:** Step 4, podman running.

**Run:**

```
./sandcast --project specs/002-field-feedback-fixes/.walkthrough/demo build --verify --json | jq '{image_ref, notices, verify: (.verify // .verify_report)}'
```

**What you should see:** `image_ref` is `localhost/lockdemo:<tag>`, and the verify part passes every component check plus `session.bash-login.*`, `session.bash-interactive.*`, `session.zsh.*` and `session.zsh.startup`. `nftables` may carry a note that `nft` lives outside the session PATH.

**If you see something else:** a failing `session.*` entry names the shell and the component whose binary did not resolve; `build.log` under `.sandcast/build/openshell/` has the details.

**Result:**

### Step 6 of 9: A cleared login shell finds the tools (US1)

**Why this step:** `openshell sandbox exec` runs `bash -lc` with only HOME, USER, TERM, SHELL and a minimal PATH; this is exactly that environment.

**Needs:** Step 5.

**Run:**

```
podman run --rm --entrypoint /usr/bin/env localhost/lockdemo -i HOME=/sandbox USER=sandbox TERM=xterm-256color SHELL=/bin/bash PATH=/usr/local/bin:/usr/bin:/bin /bin/bash -lc 'command -v mise gh node python starship; echo "PATH=$PATH"'
```

**What you should see:** a path for every tool, with mise shims first, and a PATH that also contains the base image's extra directories such as `/sandbox/.venv/bin`. No error lines.

**If you see something else:** `localhost/lockdemo` not found means the build used a tag; use the `image_ref` from step 5. A missing tool means the session environment script or the DEBUG trap did not run.

**Result:**

### Step 7 of 9: An interactive session switches to the curated zsh (US1)

**Why this step:** OpenShell's connect runs `bash -i` regardless of the configured shell, so the image switches to zsh itself.

**Needs:** Step 5. This step needs a real terminal, so you run it yourself.

**Do:**

1. Run `podman run --rm -it --entrypoint /usr/bin/env localhost/lockdemo -i HOME=/sandbox USER=sandbox TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin /bin/bash -i`
2. Type `echo $0` and then `command -v starship mise`.
3. Type `bash -lc 'echo $0'` to confirm a nested bash stays bash.
4. Type `exit`.

**What you should see:** a zsh prompt with no error lines and no zsh new-user wizard; `echo $0` shows zsh, both tools resolve, and the nested command prints bash.

**If you see something else:** a plain bash prompt means the switch hook did not fire; setting `SANDCAST_NO_SHELL_SWITCH` disables it on purpose.

**Result:**

### Step 8 of 9: Image reference warnings (US3)

**Why this step:** a short Docker Hub name and a `localhost/` reference both fail silently on a remote gateway (issues #8 and #10).

**Needs:** Step 1.

**Run:**

```
mkdir -p specs/002-field-feedback-fixes/.walkthrough/hub && ./sandcast --project specs/002-field-feedback-fixes/.walkthrough/hub init --name hubdemo --image docker.io/hubdemo --force && ./sandcast --project specs/002-field-feedback-fixes/.walkthrough/hub manifest validate; echo "exit=$?"
```

**What you should see:** validate prints a warning that the reference resolves to `docker.io/library/hubdemo`, and `exit=0`.

**If you see something else:** a non-zero exit means the warning became an error, which the spec does not allow.

**Result:**

### Step 9 of 9: Capture fact tests (US4)

**Why this step:** git marketplace discovery (issues #5 and #7) and skill snippet cleanup (issue #6) are covered by focused tests that build throwaway repositories and run every snippet under sh, bash and zsh.

**Run:**

```
go test -count=1 -run 'TestDiscoverGitMarketplaces|TestSnippets' ./internal/harness/claudecode/ ./internal/skills/
```

**What you should see:** `ok` for both packages.

**If you see something else:** a snippet failure names the shell and the stub exit code; a discovery failure names the repository case (GitHub remote, other remote, dirty tree, name mismatch, no remote).

**Result:**

## Not covered here

- **Push and digest (US3, issue #9):** the local registry speaks HTTP and the podman machine requires TLS; trying it needs a registry configuration change on your machine. The failed push path was observed during implementation.
- **Remote gateway warning (US3):** needs a remote OpenShell gateway registered as active.
- **Emulation notice (US3):** needs an arm64 host planning a rust component for `linux/amd64`; not part of this demo manifest.
- **Session through a real gateway (US1):** covered by `make test-gateway`, which starts a throwaway v0.1.3 gateway.
- **Declared binaries and promote (US4, issue #4):** needs a stored fragment for an optional component; covered by unit tests.
- **Capture skill end to end (US4):** needs an agent session running `/sandcast-capture`.

## Coverage

- US1 (session shell and tools): steps 5, 6, 7.
- US2 (lockfile and pinned versions decide): steps 2, 3, 4, 5.
- US3 (image reaches the gateway): step 8 only; push, remote gateway and emulation are listed above.
- US4 (capture from facts): step 9; declared binaries and the capture skill are listed above.

## Results
