# Quickstart: validating sandcast end to end

These scenarios prove the feature against the spec's user stories. They reference [contracts/cli.md](contracts/cli.md) for commands and [data-model.md](data-model.md) for file layouts.

## Prerequisites

| Requirement | Needed for | Check |
|-------------|------------|-------|
| Go 1.25 toolchain | building sandcast | `go version` |
| podman 5.x (or docker 28+) | probe, build, verify | `podman version` |
| mise >= 2026.9.16 on the host | `sandcast lock` | `mise --version` |
| openshell CLI 0.1.2 | profile import, sandbox start | `openshell --version` |
| A reachable OpenShell gateway with the podman driver | profile import, sandbox start | `openshell status` |
| Network access to ghcr.io, github.com, mise-versions.jdx.dev | base image pull, mise installs | |

Build the binary and run the unit suite:

```sh
make verify
```

Expected: tests and lint pass, including golden tests for plan, fragments, policy and adapted profiles, and lint tests over every vendored upstream profile.

## Scenario 1: non-interactive build from a hand-written manifest (US1, SC-001, SC-002, SC-004)

```sh
mkdir demo && cd demo
sandcast init --name demo --target openshell
```

Edit `.sandcast/manifest.yaml` to:

```yaml
schema_version: 1
name: demo
base_image: nvidia-community-base
components:
  - key: go
  - key: starship
  - key: fzf
  - key: claude-code
shell:
  program: zsh
targets:
  openshell:
    image: localhost/demo
```

```sh
sandcast lock --platform linux/arm64      # or linux/amd64 on an amd64 gateway
sandcast plan
sandcast plan                             # second run
```

Expected:

- The first plan pulls and probes the base image, reports the probe delta (`zsh`, `starship`, `fzf`, `claude-code`, `go` missing; `git`, `curl` present), and writes `.sandcast/build/openshell/plan.yaml`.
- The second plan prints "probe cache hit" and starts no container (SC-004).
- `sha256sum` of both plan files is identical (SC-002).

```sh
sandcast build --verify
```

Expected: a two-pass build, `verify.yaml` with every component `ok: true`, exit 0 (SC-001), `.sandcast/build/openshell/profiles/` with `claude-code.yaml`, `go-proxy.yaml`, `github.yaml`, each with `binaries` pointing at `/opt/mise/installs/...` paths, and `index.yaml` with `flagged: false` for all.

```sh
sandcast profiles lint
podman image inspect localhost/demo:latest --format '{{index .Labels "io.sandcast.profiles-index"}}'
```

Expected: lint passes; the label lists the adapted ids.

## Scenario 2: import and start under OpenShell (US1 scenario 7, SC-006, SC-007)

```sh
sandcast profiles import --scope workspace
openshell profile list
openshell provider create --name demo-claude --type claude-code --credential ANTHROPIC_API_KEY
openshell provider create --name demo-go --type go-proxy
openshell provider create --name demo-github --type github --credential GITHUB_TOKEN
openshell sandbox create --name demo --from localhost/demo:latest \
  --provider demo-claude --provider demo-go --provider demo-github -- zsh -l
```

Inside the sandbox:

```sh
go install golang.org/x/tools/cmd/goimports@latest
claude -p 'reply with OK'
```

Expected: the module download succeeds through the go-proxy provider, `claude` answers, and `openshell logs demo` shows no `credential_endpoint_mismatch` and no denied request from the included binaries (SC-007). Re-running the import reports the existing ids and leaves them unchanged.

## Scenario 3: deterministic failure without a skill (US1 scenario 8)

Break a fragment on purpose:

```sh
printf 'RUN false\n' | sandcast fragments write broken-tool
```

Add `- key: broken-tool` with `optional: false` to the manifest (no component definition exists, so it is unresolved and the stored fragment is used), then:

```sh
sandcast plan && sandcast build; echo "exit=$?"
```

Expected: `exit=1`, `.sandcast/build/openshell/Containerfile` and `build.log` present, no retry, the stage image kept as `localhost/demo:sandcast-debug`.

## Scenario 4: capture on the reference fixture (US2, SC-003)

Prerequisites: Claude Code with the skills installed (`sandcast skills install --harness claude-code`), host tools `starship`, `fzf`, `lsd` installed, zsh with the fixture's `.zshrc`.

```sh
cd tests/fixtures/reference-project
claude
> /sandcast-capture
```

Expected: the skill asks which harnesses to include, proposes `go`, `python`, `uv` from project files, `starship`, `fzf`, `lsd` from the host and the shell configuration, and `claude-code` from the harness choice, each with evidence; writes the manifest with a curated zsh configuration that contains `compinit` and the `TERM` guard; `sandcast manifest validate` passes; `sandcast lock` runs. Then `/sandcast-build` succeeds with at most one repair iteration (SC-003). No credential value appears in `.sandcast/` (SC-009): `rg -n 'sk-ant-|ghp_|glpat-' .sandcast` returns nothing.

## Scenario 5: repair and promote (US3, SC-005)

Add a component with a script installer that has a deliberately wrong archive path to `.sandcast/components/`, run `/sandcast-build`, and observe: a self-written fragment, a failed build, a learnings entry, a fixed fragment, a successful build, and a promotion prompt. Accept it. Expected: `.sandcast/recipes/<key>.yaml` exists, the fragment header shows `status=superseded`, and a subsequent `sandcast build --verify` without Claude Code succeeds (SC-005).

## Scenario 6: container target (US5)

Add to the manifest:

```yaml
targets:
  container:
    image: localhost/demo-plain
```

```sh
sandcast build --target container --verify
cat .sandcast/build/container/allowed-domains.txt
podman run --rm localhost/demo-plain:latest go version
```

Expected: verify passes for the same component set, the allowed-domains file lists `api.anthropic.com`, `proxy.golang.org`, `sum.golang.org`, `github.com` and the other referenced hosts sorted and unique, no policy file in the image, and the image runs under podman alone.

## Scenario 7: integration and gateway test targets

```sh
make test-integration     # podman required: probe, plan, build, verify on the fixture
make test-gateway         # additionally starts a v0.1.2 gateway container, imports profiles, starts a sandbox
```

Expected: both pass; the gateway target skips with a message when podman's socket is not available.

## Outcomes (2026-10-08, macOS arm64, podman 6.1.3 with podman machine, mise 2026.10.3, openshell 0.1.2)

| Scenario | Result | How |
|----------|--------|-----|
| 1 | Passed | By hand on the scenario manifest and in `TestCoreFlow`. The first plan probed 9 components; the second printed "probe cache hit: no container started" in under one second and produced a byte-identical `plan.yaml`. `build --verify` built both passes in about 72 seconds and verified 9 of 9 components. Adapted profiles: `claude-code` resolved to `/usr/local/bin/claude`, `go-proxy` to the mise Go install plus runtime globs; the image label listed both ids. |
| 2 | Passed without credentials; inference not run | `TestGatewayImportAndSandbox` and `TestGatewayHarness` against a throwaway v0.1.2 gateway (`tests/integration/gateway.sh`): import at workspace scope, a second import reported the existing ids and left them unchanged, and sandboxes started with `--from localhost/<image>` ran `go version` and `claude --version` with `CLAUDE_CONFIG_DIR=/opt/sandcast/claude-code`. The `claude -p` call needs an Anthropic credential and remains a manual step (SC-007). |
| 3 | Passed | `TestBrokenFragmentFailsOnce`: exit 1, `Containerfile` and `build.log` left in place, one pass-1 attempt only. |
| 4 | Passed with the skill steps executed in a Claude Code session | `sandcast components match` proposed go, python, uv (project), starship, fzf, lsd (host) and claude-code (harness), plus other host tools that were declined; the curated zsh configuration has `compinit`, the `TERM` guard and the fzf block. The recorded manifest is `tests/fixtures/reference-project/expected-manifest.yaml`; it built and verified 12 of 12 components with no repair iteration (SC-003) and `TestCaptureManifestBuilds` rebuilds it. |
| 5 | Passed through the CLI | `TestPromoteFlow`: a stored fragment is reused without an agent, `sandcast promote` writes the recipe, the plan reports the fragment as stale and uses the recipe, and the next `build --verify` succeeds (SC-005). The interactive repair loop of `/sandcast-build` was not exercised end to end. |
| 6 | Passed | `TestCoreFlow` builds the same manifest for both targets and verifies the same components; `allowed-domains.txt` lists the referenced hosts; the container image has no policy file. |
| 7 | Passed | `make test-integration` and `make test-gateway`. |

### Corrections to the scenarios above

- Scenario 1: the community base image already contains `claude-code` (2.1.156), so the delta shows it as present and skipped. `github.yaml` is only produced when the manifest lists `git` (or `gh`); `git` is an `always` component at capture time, not an implicit plan component.
- Scenario 3: a failure in pass 1 leaves no stage image to keep. `<image>:sandcast-debug` exists only when pass 1 completed and a later step failed.
- Scenario 6: the container target keeps the base image's user and uses that user's home as the working directory (FR-040); it creates no `sandbox` user and no `/sandbox`.
- Scenario 7: the upstream container recipe for the gateway needs several additions under rootless podman (state and data directories, sandbox JWT keys, unauthenticated local users, the callback endpoint); `tests/integration/gateway.sh` carries them and research.md records why.
