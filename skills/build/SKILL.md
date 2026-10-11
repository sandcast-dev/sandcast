---
name: sandcast-build
description: Build and verify the sandbox image from .sandcast/manifest.yaml, writing and repairing install fragments for components sandcast cannot resolve, then offer promotion to recipes and the OpenShell profile import.
sandcast_version: 0.1.0
---

# sandcast build

You build the sandbox image for the project's `.sandcast/manifest.yaml`.
The `sandcast` binary plans, assembles the Containerfile, builds, verifies and writes the adapted profiles. Your job starts where the binary stops: components it cannot resolve. For those you write a Containerfile fragment, repair it when the build fails, and offer to promote a working fragment into a deterministic recipe.

## Arguments

- `--max-attempts <n>`: the number of builds you run before giving up (default 3).
- `--target <openshell|container>` and `--platform <linux/amd64|linux/arm64>`: pass them unchanged to every `sandcast plan` and `sandcast build` call. Without them the binary builds the `openshell` target when the manifest declares it, else `container`, and selects the platform from the manifest, then the host.

## Rules

Follow these rules in every step. They are part of the sandcast contract and a reviewer checks them.

1. **Facts come from the binary.** The package manager, the final user, the platform, unresolved components, component checks, build errors and verify results come from `sandcast ... --json` and the files it names. Never guess them.
2. **Never handle credentials.** Never put a token, key or password into a fragment, a learnings entry or a command line. Never download with authentication headers. If a tool needs a credential at runtime, tell the user to supply it as a credential of the OpenShell provider attached to the sandbox.
3. **Write only what the contract allows**: fragments through `sandcast fragments write`, learnings through `sandcast learnings add`, recipes through `sandcast promote`. Never edit generated files in `.sandcast/build/` (the Containerfile, plan, mise files, profiles, logs). Never edit `.sandcast/manifest.yaml`; when the manifest needs a change, tell the user and suggest `/sandcast-capture`.
4. **Ask before anything irreversible**: promotion, profile updates at the gateway, and writing fragments for optional components.
5. **Run commands from the project root** (or pass `--project <dir>`). Exit codes: 0 success, 1 operation failed, 2 usage or validation error, 3 external tool missing or at the wrong version.
6. **Temporary files live outside the project**: create them with `mktemp`, never under a fixed name in a shared directory, and delete them when the step is done.
7. **A harness timeout is not a failure.** Run `sandcast plan` and `sandcast build` as described in "Long-running commands".

## Long-running commands

The first `sandcast plan` for a base image pulls the image and probes it, and `sandcast build` runs two image builds and then verify. Both can take many minutes, far longer than the time your harness allows a shell command by default (two minutes in Claude Code).

- Run them with the longest command timeout your harness allows (ten minutes in Claude Code), or start them in the background. When you start a build in the background, launch it with a trailing `&` and capture its process id on the very next line with `pid=$!` in the same shell. That recorded `pid` is the only process you may ever signal. Poll until the process exits. While a build runs, `tail -n 20 .sandcast/build/<target>/build.log` shows its progress.
- If the harness stops a command because of its own timeout, sandcast did not fail. Do not count it as a build attempt, do not record a learning, and do not change a fragment because of it. The sandcast process may still be running; wait for it, then run the command again in the background.
- A build counts as stalled only once it has entered the long image-build phase and then stops making progress. Decide in this order:
  - If `.sandcast/build/<target>/build.log` does not exist yet, the build has not started writing. Treat this as "not started", never as a stall, and keep waiting.
  - sandcast does not yet mark every phase in the log, so measure a stall only from the point the first image build begins. That point is the line `### sandcast pass 1` in `build.log`. Before that line appears the build may sit in plan, image pull, probe, resolve, push, verify, or a bounded download of up to about twenty minutes, none of which write progress to `build.log`. Do not treat that quiet as a stall.
  - After `### sandcast pass 1` has appeared, check every few minutes whether `find .sandcast/build/<target>/build.log -mmin -10` still prints the path. When it prints nothing, the log has not changed for 10 minutes and the build is stalled (often a download behind a stalled network or proxy).
- To stop a stalled build, signal only the `pid` you recorded with `pid=$!` when you started it. Never discover a process by pattern: do not use `pgrep -f` or scan a process list, because such a match also hits the harness wrapper shell, the matching shell itself, and builds of other projects, so it can interrupt the wrong process and leave the real build running. Ask the user before you signal, then send SIGINT to that one process with `kill -INT "$pid"`, never SIGKILL (`kill -9`), so sandcast can stop its containers cleanly. When the process exits the per-target build lock is released, but the `.lock` file itself stays in place by design (sandcast never removes it). Show the user the last lines of the log, report the stall, and stop. Do not change a fragment because of it.
- sandcast runs one build per target at a time. An error saying that another sandcast build is running for the target means an earlier invocation (one of yours that the harness gave up on, or one the user started) is still active. Wait until it finishes, and at least one minute, before you run the command again; this is not an attempt either. Never delete `.sandcast/build/<target>/.lock`.

## Step 1: Check the skill and binary versions

Run:

```sh
sandcast skills check --harness claude-code
```

- Exit 0: continue.
- Exit 3: the installed skills and the binary disagree. Print the message exactly as the command printed it, tell the user which side to update, and **stop**.
- Command not found: tell the user to install the `sandcast` binary first and stop.
- Any other failure: show the output and stop. Never continue without a passing version check.

## Step 2: Plan

The first plan for a base image can take many minutes (see "Long-running commands"):

```sh
sandcast plan --json
```

- Exit 3: the container engine (podman by default, docker through `--engine docker`) is missing. Report and stop.
- Exit 2 with a JSON plan whose `conflicts` is non-empty: report each conflict (`binary` is provided by every key in `components`), tell the user to remove one of the components from the manifest, and stop.
- Exit 2 with a message about the lockfile or a platform: the lockfile lacks the selected platform. Offer `sandcast lock --platform <platform>`, run it only when the user agrees, then plan again. Otherwise report and stop.
- Any other non-zero exit: show the message (for example a base image that cannot be pulled) and stop.

Read these fields of the plan:

- `platform.value`: the platform you build for.
- `image_facts.package_manager` (`apt-get`, `dnf`, `yum`, `apk`, or `none` when the image has none), `image_facts.os` (`id`, `version`), `image_facts.shells`.
- `final_user` (`name`, `uid`, `gid`, `group`, `home`, `created`): the user the image ends with.
- `components[]` with `key`, `installer_kind`, `action` and `check.command`.
- `unresolved[]` with `key`, `reason` and `optional`.
- `notices[]`: show them to the user, for example stale fragments.

Summarize the plan for the user: base image, OS, package manager, final user, the components and their actions, and the unresolved ones with their reasons.

Classify `unresolved`:

- **Blocking** (`optional` is false or missing): you write a fragment for each in Step 3. `sandcast build` refuses to run while any remain.
- **Optional**: ask the user, per component, whether to write a fragment or leave it out of this image.

If nothing needs a fragment, go to Step 4.

## Step 3: Write fragments for unresolved components

Handle one component at a time.

### 3a: Gather the facts

1. If a tier defines the component, read its definition (YAML output):

   ```sh
   sandcast components show <key>
   ```

   Note `check.command` (verify runs it in the built image as the final user), the binaries (`installer.<kind>.bins`, else `provides`, else the key), and why it is unresolved (`reason` from the plan, for example the base image has no package manager the definition supports).
2. If no tier defines it, verify runs `command -v <key>`, so the fragment must put an executable named exactly `<key>` on the final user's `PATH`. Ask the user which upstream project the tool comes from when it is not obvious.
3. Read `.sandcast/learnings.yaml` if it exists and look for earlier entries for this key. A change followed by a `fixed` entry worked; reuse it. A change followed by a later failure for the same key (a higher attempt) did not work; never repeat it.
4. Run `sandcast fragments list --json` to see whether a fragment for this key already exists.

### 3b: Verify the download before writing it

Wrong asset names and unexpected archive layouts caused most failed builds in the past (brainstorm 072, changes 2 and 3). Check on the host before writing any download command:

1. Pick an exact release. List its assets:

   ```sh
   curl -fsSL https://api.github.com/repos/<owner>/<repo>/releases/tags/<tag> | jq -r '.assets[].name'
   ```

2. Choose the asset for every architecture you build for (`linux/amd64` is `x86_64` or `amd64`, `linux/arm64` is `aarch64` or `arm64`). Naming often differs per architecture (`gnu` versus `musl`, `.tar.gz` versus `.tar.xz`). Confirm each URL resolves:

   ```sh
   curl -fsSLI https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>
   ```

3. Take the sha256 from the upstream checksum file of the release. If there is none, download the asset on the host and compute it with `shasum -a 256` (macOS) or `sha256sum`.
4. Check the archive layout, because many archives nest the binary in a directory:

   ```sh
   curl -fsSL https://github.com/<owner>/<repo>/releases/download/<tag>/<asset> | tar -tzf - | head
   ```

Prefer the image's package manager when its package is recent enough for the component's check; then no download is needed.

### 3c: Fragment rules

Write every fragment so that `sandcast promote` can turn it into a recipe later. Promotion converts only `RUN`, `USER`, `ENV` and `WORKDIR`, and it refuses a fragment that mixes `RUN` steps as root with `RUN` steps as the final user.

- **Shape**: `USER root`, then the `RUN`, then `USER <final_user.name>` as the last line. Every fragment starts as root anyway (the assembler adds `USER root` before it), and the validator only catches an explicit `USER root` at the end, so never omit the final `USER` line. The one exception: when `final_user.name` is `root` (the container target keeps a base image that runs as root), end after the `RUN` without a `USER` line, because the validator refuses a trailing `USER root` and the image footer sets the final user.
- **Exactly one `RUN`, as root.** Use only `RUN`, `USER`, `ENV` and `WORKDIR`. Never `FROM`, `ADD` or `SHELL` (the validator refuses them; podman ignores `SHELL` for OCI images). Never `ARG`, `LABEL` or `COPY`: promotion refuses them, and `COPY` has no usable source because the build context holds only files sandcast generates.
- **`RUN` forms**: use one of these two.
  - A plain `RUN` with backslash continuation, starting with `set -eux;`, as in the example below.
  - A script heredoc with nothing else on the `RUN` line; `/bin/sh` runs the whole body:

    ```dockerfile
    RUN <<'EOF'
    set -eux
    # commands, one per line
    EOF
    ```

  Never put a command on the heredoc `RUN` line (`RUN cat <<'EOF' > file`, `RUN bash <<'EOF'`). To write a file, use `printf` inside the script.
- **Shell options**: `/bin/sh` gets `set -eux`; it may be dash, which rejects `-o pipefail`. Use bash only when `image_facts.shells` lists it, and then only as `RUN bash -euxo pipefail -c '...'` with no single quotes inside the script. Prefer `/bin/sh` and avoid pipes: download to a file, verify it, then extract.
- **Package manager**: use exactly `image_facts.package_manager`.
  - `apt-get`: `mkdir -p /var/lib/apt/lists/partial && apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends <pkg> && rm -rf /var/lib/apt/lists/*`
  - `dnf`: `dnf install -y --setopt=install_weak_deps=False <pkg> && dnf clean all`
  - `yum`: `yum install -y <pkg> && yum clean all`
  - `apk`: `apk add --no-cache <pkg>`
  - `none`: there is no package manager; use a release binary.
- **Pinned downloads only**: an exact version in the URL, never `latest`, never an API call at build time, and a sha256 check of every downloaded file before it is used.
- **Bounded downloads**: every download uses `curl -fsSL --connect-timeout 20 --max-time 300 --retry 3`, so a stalled network fails the build step instead of hanging it, and a transient error is retried.
- **Locations**: binaries go to `/usr/local/bin`, unpacked trees to `/opt/<key>`, system configuration to `/etc`. Never put anything under `/sandbox`; on Kubernetes the workspace volume is mounted over it. Never write the paths sandcast manages: `/opt/mise`, `/etc/mise`, `/etc/profile.d/sandcast-mise.sh`, `/tmp/sandcast-mise`, `/usr/share/sandcast` (the adapted profile set) and the curated shell file at `shell.rc_path` in the plan. Never write the OpenShell reserved paths `/etc/openshell`, `/etc/openshell-tls`, `/opt/openshell`, `/run/openshell`, `/run/netns`, `/var/run/netns`.
- **Never the home directory**: never write tool configuration, caches or state into `final_user.home`. On the default OpenShell base image the home is `/sandbox`, and on Kubernetes the workspace volume is mounted over it, so anything written there is lost. Put configuration under `/etc/<key>` or `/opt/<key>` and point the tool there with the environment variable the tool supports, set with `ENV` (for example `<TOOL>_CONFIG_DIR=/etc/<key>`). At runtime the sandbox policy keeps most of the filesystem read-only, so send caches to `/tmp` through the tool's cache variable. If the tool insists on the home directory, do not write it there; tell the user its configuration does not survive on the Kubernetes driver.
- **Directory ownership**: when the final user must write to a directory you create, create it and its parents explicitly and chown the topmost directory you created, not only the leaf (`install -d -o <uid> -g <gid> <dir>` for each level). A root-owned parent blocks every later tool that needs a sibling directory.
- **Post-install commands** (for example a tool's `init`) stay inside the single root `RUN`. Create and chown the directories they write first, then run them as the final user with `su -s /bin/sh -c '<cmd> <non-interactive flags>' <final_user.name>`. They must not prompt; pass the tool's non-interactive flags.
- **Helper tools**: do not assume `curl`. The plan adds `curl` and `ca-certificates` only when a mise tool is planned, and minimal or Alpine images lack them. If `curl` is not in the plan's `components[]`, install `curl` and `ca-certificates` through the package manager at the start of the same `RUN`. If the image has no package manager and no `curl`, tell the user that the fragment cannot download anything. Beyond that, rely only on `tar`, `gzip` and `sha256sum`; if the archive needs `xz` or `unzip`, install it through the package manager in the same `RUN`.
- **Architecture**: select the asset with `case "$(uname -m)" in x86_64) ... ;; aarch64) ... ;; esac` and fail for anything else.
- **Provenance**: the first line is a comment with the tool, version and release URL.

Example for a tool that no tier defines, so the binary must be named after the key. The final user here is `sandbox`; use `final_user.name` from the plan:

```dockerfile
# mytool 1.4.2 from https://github.com/example/mytool/releases/tag/v1.4.2
USER root
RUN set -eux; \
    case "$(uname -m)" in \
      x86_64) asset=mytool-1.4.2-x86_64-unknown-linux-musl.tar.gz; sum=SHA256_OF_THE_X86_64_ASSET;; \
      aarch64) asset=mytool-1.4.2-aarch64-unknown-linux-gnu.tar.gz; sum=SHA256_OF_THE_AARCH64_ASSET;; \
      *) echo "unsupported architecture: $(uname -m)" >&2; exit 1;; \
    esac; \
    curl -fsSL --connect-timeout 20 --max-time 300 --retry 3 -o /tmp/mytool.tar.gz "https://github.com/example/mytool/releases/download/v1.4.2/${asset}"; \
    echo "${sum}  /tmp/mytool.tar.gz" | sha256sum -c -; \
    install -d /opt/mytool; \
    tar -xzf /tmp/mytool.tar.gz -C /opt/mytool --strip-components=1; \
    install -m 0755 /opt/mytool/mytool /usr/local/bin/mytool; \
    rm -f /tmp/mytool.tar.gz
USER sandbox
```

### 3d: Store the fragment

Write the fragment to a private temporary file created with `mktemp`, store it, and delete the file. Run it as one shell command so that `$tmp` survives. The binary validates the fragment and writes `.sandcast/fragments/<key>.containerfile`:

```sh
tmp="$(mktemp)"
cat > "$tmp" <<'SANDCAST_FRAGMENT'
<the fragment>
SANDCAST_FRAGMENT
sandcast fragments write <key> --file "$tmp"; status=$?
rm -f "$tmp"
[ "$status" -eq 0 ]
```

The command also reads the fragment from standard input. If it exits 2, fix what the validation message names and write again. If the message reports a credential-shaped value, remove it; never weaken, split or encode it to get past the check.
Stored fragments are committed with the project, so the non-interactive `sandcast build` reuses them without you.

## Step 4: Build, verify and repair

Count every build you run in this session as one attempt, starting at 1. Stop after `--max-attempts` builds (default 3). A build that your harness stopped, or that did not start because another build was running, is not an attempt (see "Long-running commands"). A build takes many minutes; run it with the longest command timeout or in the background:

```sh
sandcast build --verify --json
```

The JSON report has `image`, `containerfile`, `build_log`, `profiles`, `flagged`, `allowed_domains` (container target), `verify` (`results` with `component_key`, `ok`, `status`, `version`, `output`; `passed`; `failed`) and `error` when something failed.

- Exit 0: go to Step 5.
- Exit 2: the plan still has blocking problems (for example an unresolved component without a fragment, or a lockfile gap). Report them and stop; for a lockfile gap offer `sandcast lock` as in Step 2.
- Exit 3: the container engine is missing. Report and stop.
- Exit 1: the build or a verify check failed. Repair as described next.

### Repair

1. **Find the cause.**
   - Build failure (`error` set, no `verify`): read the end of the log at `build_log`, for example `tail -n 80 .sandcast/build/<target>/build.log`. Find the failing step and map it to its section through the comments in the Containerfile at `containerfile` (read it, never edit it). Fragments appear as `# 50-fragments: <key> (...)`.
   - Verify failure: each result with `ok: false` names the `component_key` and the check `output`. A `status` of `timeout` means the check did not finish within its time limit (for example a download behind a stalled proxy); report it and check the network before you change a fragment because of it. A `timeout` result whose `output` starts with `not run:` was never checked: the verify container was stopped while an earlier check was running. Run the checks again once with `sandcast verify --json` (with the same `--target`); never repair a component because of such a result. Rerun at most once: if the same check still returns `not run:` or `timeout` on that single rerun, do not rerun again. Report the check and its `output` to the user and stop.
2. **Failure outside the fragments** (packages, mise tools, recipes, harness or shell sections, or a check of a component that has no fragment): do not edit anything. When the failure names a component, record it with `sandcast learnings add --component <key> --attempt <n> --failure "<summary>" --change "none: failure outside the skill's fragments, reported to the user" --outcome unfixed`. Show the log excerpt and the likely cause to the user (for example a lockfile gap, a version selector, or a package name), and stop.
3. **Failure in a fragment**: stored fragments are yours to repair, including those from earlier sessions (`installer_kind: fragment` in the plan), for example when an upstream asset was renamed. Ask the user before changing a fragment written in an earlier session. If n equals `--max-attempts`, go to item 5. Otherwise record the failure before changing anything:

   ```sh
   sandcast learnings add --component <key> --attempt <n> --failure "<one-line summary of the error>" --change "<what you will change>" --outcome unfixed
   ```

   Then fix the fragment, store it again as in 3d, and run `sandcast build --verify --json` as attempt n+1. Typical fixes: the asset name per architecture, `--strip-components` for a nested binary, a missing parent directory or chown, a missing helper package.
4. **When a change works**, record it with the number of the attempt that showed it. A change works when the build completes the component's `# 50-fragments: <key>` step and verify reports its check as `ok`. Record it even when another component fails in the same build. If the build fails after the step, so that verify does not run, record it at the first later attempt whose verify passes the check.

   ```sh
   sandcast learnings add --component <key> --attempt <n> --failure "<the failure that was fixed>" --change "<the change that fixed it>" --outcome fixed
   ```

   If `sandcast learnings add` exits 2, the entry is invalid or contains a credential-shaped value. Shorten the text, remove paths and values, and record it again.

5. **Retry limit reached**: when attempt `--max-attempts` fails, record it with `--outcome unfixed` and `--change "none: retry limit reached"`, then stop. Leave the last Containerfile and build log in place for inspection; never delete them. Tell the user the paths of the Containerfile, the build log and `.sandcast/learnings.yaml`, and the image named in `stage_image` when the report includes it (the binary keeps the failed stage for debugging).

Learnings entries never contain credentials, full logs, or host paths with user names; keep them to one line each.

### Success report

Report the image reference, the verify results (`passed` and `failed`), the profiles directory, and every `flagged` profile with the `flag_reason` from `profiles/index.yaml` in the profiles directory. A profile is flagged when no binary resolves in the image, so it would deny all traffic, or when some of its binaries lie under paths the sandbox can write, so any process placed there would get the profile's network access. The user decides whether to keep the component that references it.

## Step 5: Offer promotion

For each component whose fragment you wrote or repaired in this session and that now builds and verifies, ask the user explicitly:

> The fragment for `<key>` works. Promote it into the project-local recipe `.sandcast/recipes/<key>.yaml`? Future builds then install it deterministically without this skill.

Run the promotion only after the user says yes, and pass the executables the fragment installs:

```sh
sandcast promote <key> --bins <bin1,bin2> --yes
```

`--yes` confirms the user's answer you already have; never pass it without that answer. Explain the result: the recipe lives in `.sandcast/recipes/<key>.yaml`, the fragment `.sandcast/fragments/<key>.containerfile` is marked superseded and no longer used, and the next `sandcast plan` resolves the component through the recipe. Suggest committing the recipe, the fragment and `.sandcast/learnings.yaml`.

Confirm that the recipe path works, because the recipe runs as a script in the recipes section instead of the fragment's `RUN`, and its `ENV` moves to the user section:

```sh
sandcast plan --json
```

The component must now have `installer_kind: script` and a `source_file` of `.sandcast/recipes/<key>.yaml`. Offer one more `sandcast build --verify --json` to prove that the non-interactive build works with the recipe. If that build fails, report it and do not edit the recipe; the user decides whether to fix the recipe by hand or to restore the fragment.

If `sandcast promote` exits 2, the fragment has a shape promotion cannot convert (see 3c). Show the message to the user and keep the fragment: it stays active and the non-interactive build keeps using it. Do not retry the promotion blindly. Only if the user asks, rewrite the fragment in the promotable shape, build and verify it again, and then offer promotion once more.

If the user declines, the fragment stays active and the non-interactive build keeps using it.

## Step 6: Offer the profile import

Only for the `openshell` target. Check whether the OpenShell CLI is present:

```sh
openshell --version
```

If it works, offer to import the adapted profile set into the gateway, optionally previewing with `--dry-run` first:

```sh
sandcast profiles import --scope workspace
```

- Exit 3: the `openshell` CLI is missing or not at the pinned release; the message names the required release. Report it; the image itself is fine.
- Exit 1 with existing profile ids: sandcast never overwrites them on its own. Explain the choices and let the user pick one:
  - `--update` replaces the existing profiles with the same ids, which affects every sandbox that uses them;
  - `--suffix` imports the profiles under image-specific ids;
  - `--scope platform` imports at platform scope instead (it implies `--suffix`).

After an import with `--suffix` or `--scope platform`, `profiles/index.yaml` in the build directory lists each upstream id with the imported id and the scope; tell the user that the runtime that starts the sandbox must use those ids. The `io.sandcast.profiles-index` label of the image keeps the ids of the build, because a label cannot change after the build.

If the `openshell` CLI is absent, tell the user the profiles are in the `profiles` directory of the build report and can be imported later with `sandcast profiles import`.

## Finish

Summarize: the image reference, the verify result, the fragments written or repaired, the learnings recorded, the recipes promoted, and the profile import result.
