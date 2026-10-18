# sandcast

sandcast turns a project directory into a sandbox image.

Sand casting pours metal into a pre-formed mold. sandcast pours a project and its developer's tooling into a pre-configured base image. A guided capture examines the project and the host and writes a manifest. A build probes the chosen base image, adds only what is missing, and produces the OpenShell provider profiles adapted to that image, so a sandbox started from it with those profiles attached gets the right network policy at runtime.

## How it works

sandcast has two halves.

The **capture** runs inside an agent harness (Claude Code in this release) as the `/sandcast-capture` skill. It looks at the project files, the tools installed on the host and the host shell configuration, proposes components with the evidence for each one, and writes `.sandcast/manifest.yaml` once you have confirmed the list.

The **build** is a Go binary that needs no agent. `sandcast plan` probes the base image inside a throwaway container (the result is cached by image digest and platform), compares it with the components the manifest asks for, and writes a deterministic build plan. `sandcast build` turns the plan into a Containerfile that installs only what the base image lacks: tools through mise with a lockfile, base system packages through the image's package manager, and scripts for the rest. It builds the image in two passes and writes one OpenShell provider profile per network dependency, with the profile's `binaries` rewritten to the paths inside the image. `sandcast verify` runs every component check inside the built image.

Because the binary runs plan, build, verify and profile export from committed files alone, a CI job rebuilds the same image from the same manifest and lockfile without a human or an LLM in the loop. The `/sandcast-build` skill handles what the binary cannot: it writes install fragments for tools that no component definition covers, repairs failed builds, and offers to promote a working fragment into a deterministic recipe.

## Installation

| Requirement | Version | Needed for |
|-------------|---------|------------|
| Go | 1.25 or newer | building sandcast |
| podman (or docker) | podman 5 or newer | `probe`, `plan`, `build`, `verify`, `profiles export` |
| mise | 2026.9.16 or newer on the host | `sandcast lock` |
| openshell CLI | 0.1.x (pinned to v0.1.3) | `sandcast profiles import` only |
| Claude Code | | the `/sandcast-capture` and `/sandcast-build` skills |

Build the binary from a checkout of this repository:

```sh
make build
```

This writes `./sandcast`. Copy it to a directory on your `PATH` and check the pinned releases it was built with:

```sh
sandcast version
```

`make cross` builds release binaries for Linux and macOS on amd64 and arm64 into `dist/`.

sandcast uses podman by default. When podman is not on the `PATH`, it falls back to docker and prints a notice. Select an engine explicitly with `--engine docker` or the manifest field `engine`.

## The capture and build flow

Install the skills into Claude Code once per user:

```sh
sandcast skills install
```

Then, in the project directory:

1. Run `/sandcast-capture` in Claude Code. The skill asks which harnesses to include, proposes components, curates your shell configuration, proposes home dotfiles to bake (git, prompt, tmux), writes `.sandcast/manifest.yaml`, and offers to create the lockfile.
2. Lock the mise tools for the platform you build for:

   ```sh
   sandcast lock --platform linux/arm64
   ```

3. Inspect the plan:

   ```sh
   sandcast plan
   ```

4. Build and verify the image. Use `/sandcast-build` instead when the plan lists unresolved components:

   ```sh
   sandcast build --verify
   ```

5. Import the adapted profiles into an OpenShell gateway:

   ```sh
   sandcast profiles import --scope workspace
   ```

6. Create providers from the imported profiles and start a sandbox from the image. The [profiles guide](docs/guides/profiles.md) walks through this step.

Without an agent, start with `sandcast init` and edit the manifest by hand. Steps 2 to 6 stay the same. For a gateway on a cluster, give init the registry reference, the cluster's platform and push, for example `sandcast init --image quay.io/<user>/<name> --platform linux/amd64 --push`; the default `localhost/<name>` works only with a local gateway that shares the build engine's image storage.

## An example manifest

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
dotfiles:
  - path: ~/.gitconfig
targets:
  openshell:
    image: localhost/demo
```

On a linux/arm64 podman host, this manifest produced the following results. The first `sandcast plan` probed nine components in `ghcr.io/nvidia/openshell-community/sandboxes/base:latest`. `claude-code` (2.1.156), `curl`, `ca-certificates`, `iproute2` and `nftables` were already present, and `go`, `starship`, `fzf` and `zsh` were missing. sandcast added `zsh` to the plan because the manifest names it as the shell program, `iproute2` and `nftables` because OpenShell images need them, and `curl` and `ca-certificates` because the mise installer needs them; the last four were already in the base image, so nothing was installed for them. The second `sandcast plan` printed `probe cache hit: no container started`, finished in under a second, and wrote a byte-identical `plan.yaml`.

`sandcast build --verify` built both passes in about 72 seconds and verified all nine components. The adapted `claude-code` profile pointed at `/usr/local/bin/claude`, the binary the base image already had, and the adapted `go-proxy` profile pointed at `/opt/mise/installs/go/<version>/bin/go` plus the runtime globs of the `go` component. The image label `io.sandcast.profiles-index` read `{"ids":["claude-code","go-proxy"],"flagged":[]}`.

## Project layout

Everything sandcast keeps in a project lives in one directory:

```text
.sandcast/
├── manifest.yaml          # committed: the manifest
├── mise.lock              # committed: exact tool versions and per-platform checksums
├── fragments/             # committed: install fragments for unresolved components
├── recipes/               # committed: recipes promoted from working fragments
├── learnings.yaml         # committed: build failures and fixes recorded by the build skill
├── components/            # committed, optional: project-local component definitions
├── profiles/              # committed, optional: project-local provider profiles
├── .gitignore             # contains build/
└── build/<target>/        # git-ignored: plan, Containerfile, build log, adapted profiles
```

A checkout with only the committed files is enough for `sandcast plan`, `sandcast build` and `sandcast verify`. The [configuration reference](docs/configuration.md) lists every file.

## Pinned upstream releases

| Pin | Value | Used for |
|-----|-------|----------|
| OpenShell | v0.1.3 (commit e1f3c82c) | vendored example profiles, the built-in profile lint, the default sandbox policy, the `openshell` CLI for the import step |
| mise | v2026.10.4 inside images | installing tools in the image; the host needs mise 2026.9.16 or newer for `sandcast lock` |
| Component registry | v0.1.0 | default tag of the remote component registry |
| Manifest schema | 1 | `.sandcast/manifest.yaml` |
| Plan schema | 1 | `.sandcast/build/<target>/plan.yaml` |

Pins change only on purpose and always with a [CHANGELOG](CHANGELOG.md) entry. [docs/pins.md](docs/pins.md) describes how to refresh each one.

## Documentation

- [Command reference](docs/cli.md): every command, flag and exit code.
- [Configuration reference](docs/configuration.md): the manifest, the base image registry, component and profile tiers, the lockfile, the project layout, the cache and environment variables.
- [Capture guide](docs/guides/capture.md): installing the skills, running a capture, shell curation and secrets.
- [Build guide](docs/guides/build.md): from manifest to image, the Containerfile layout, failure handling, fragments, promotion and the container target.
- [Profiles guide](docs/guides/profiles.md): profile tiers, lint, adaptation, the import step and starting a sandbox.
- [Cross-architecture guide](docs/guides/cross-arch.md): building a `linux/amd64` image on an arm64 Mac through Rosetta, a remote podman host or CI, and the `emulation` notice.
- [Refreshing pins](docs/pins.md): updating the OpenShell, mise and component registry pins.
- [Shipped components](components/README.md): the component file format and the components that ship with the binary.
- [Golden files](tests/golden/README.md): regenerating the golden test files.

## Development

| Target | What it does |
|--------|--------------|
| `make build` | builds `./sandcast` |
| `make test` | runs the unit tests, including the golden tests and the profile lint corpus |
| `make lint` | runs golangci-lint |
| `make verify` | runs `test` and `lint` |
| `make test-integration` | builds the binary and runs the podman integration tests (tag `integration`) |
| `make test-gateway` | builds the binary and runs the tests against an OpenShell gateway container (tag `gateway`) |

The design is specified in [specs/001-sandcast-standalone-build/spec.md](specs/001-sandcast-standalone-build/spec.md), with the research and decisions in [research.md](specs/001-sandcast-standalone-build/research.md) next to it.

## Origin

The capture-to-manifest-to-image flow was built inside [cc-deck](https://github.com/cc-deck/cc-deck). sandcast extracts it into a standalone tool for any project and any supported agent harness, drops the home-grown policy catalog in favor of upstream [OpenShell](https://github.com/NVIDIA/OpenShell) provider profiles, and uses [mise](https://mise.jdx.dev) as the installer.

## License

Apache License 2.0. See [LICENSE](LICENSE).
