# Building for another architecture

An OpenShell gateway runs sandboxes on its own nodes, and those nodes pull the image for their own architecture.
A developer on an Apple silicon Mac (arm64) who targets a gateway on an amd64 cluster therefore needs a `linux/amd64` image.
This guide shows three ways to build one and explains the `emulation` notice that sandcast prints when a build is likely to fail.

## The platform and the engine host

The manifest field `platform` (or `--platform` on `plan` and `build`) selects the image platform.
`sandcast init --platform linux/amd64` writes it into a new manifest.
The plan and the lockfile use this platform, so run `sandcast lock --platform linux/amd64` (or lock both platforms) before you plan.

The container engine builds the image wherever it runs.
podman and docker read the usual selection variables, and sandcast passes them through unchanged:

| Variable | Engine | Effect |
|----------|--------|--------|
| `CONTAINER_CONNECTION` | podman | Use a named connection from `podman system connection list`. |
| `CONTAINER_HOST` | podman | Use the podman service at this URL (`ssh://...` or `unix://...`). |
| `DOCKER_HOST` | docker | Use the docker daemon at this URL. |

When the engine host has the same architecture as the target platform, the build runs natively.
When it differs, the engine runs every `RUN` step of the foreign architecture under user-mode emulation (QEMU on a podman machine without Rosetta).
Most tools install fine that way, slowly.
Some do not: rustup and the Rust toolchain installer are known to crash under QEMU user emulation.

## The emulation notice

A component definition can declare `emulation_unsafe: true` (see [configuration](../configuration.md#component-definitions)).
The shipped `rust` component does.
When the plan installs such a component and the target platform's architecture differs from the engine host's architecture, `sandcast plan` prints a notice to stderr and `sandcast build` adds it to its report:

```text
notice: emulation: rust: building linux/amd64 on an arm64 engine host; rust is known to fail under QEMU user emulation, see docs/guides/cross-arch.md
```

sandcast asks the engine for its host architecture (`podman info` or `docker info`), so a remote connection reports the remote host and no notice appears when that host matches the platform.
The notice never enters `plan.yaml`: the engine's architecture is not a plan input, and the plan stays byte-identical across hosts.
When the engine cannot report its architecture, no notice appears and the build continues.

## Option 1: Rosetta in the podman machine

On Apple silicon, a podman machine with the Apple hypervisor (`applehv`) can run amd64 Linux binaries through Rosetta 2 instead of QEMU.
Rosetta translates more instructions correctly and runs faster than QEMU user emulation.

1. Install Rosetta on macOS once: `softwareupdate --install-rosetta --agree-to-license`.
2. Make sure Rosetta is enabled for podman machines: `rosetta = true` in the `[machine]` table of `~/.config/containers/containers.conf`. Recent podman releases enable it by default for `applehv`; check the `containers.conf` documentation of your release.
3. Restart the machine (`podman machine stop` and `podman machine start`) after a change.
4. Check that amd64 containers run: `podman run --rm --platform linux/amd64 docker.io/library/alpine:3 uname -m` prints `x86_64`.

The engine host is still arm64, so sandcast still prints the `emulation` notice: it cannot tell Rosetta from QEMU.
If a component still fails, build on an amd64 host instead.

## Option 2: a remote amd64 podman host

Building on an amd64 Linux machine avoids emulation entirely.
The remote host needs podman with the user socket enabled (`systemctl --user enable --now podman.socket`) and lingering for the build user (`loginctl enable-linger <user>`), so the socket survives logouts.

Add a connection and select it per build:

```sh
podman system connection add amd64-builder \
  --identity ~/.ssh/id_ed25519_builder \
  ssh://builder@build.example.com/run/user/1000/podman/podman.sock

CONTAINER_CONNECTION=amd64-builder sandcast lock --platform linux/amd64
CONTAINER_CONNECTION=amd64-builder sandcast build --platform linux/amd64 --verify --push
```

The image is built and stored on the remote host, so the gateway can only pull it from a registry: set `targets.openshell.push: true` (or pass `--push`) and use a registry reference the cluster can reach.
`sandcast build` prints the pushed reference, the digest and a ready-to-run `openshell sandbox create --from <ref>` line.
Run `sandcast verify` with the same `CONTAINER_CONNECTION`, because the image exists only on that host.

**SSH identity limits.** podman's built-in SSH client uses the identity file of the connection.
It does not read `IdentityFile` entries from `~/.ssh/config`, it cannot ask for the passphrase of an encrypted key, and depending on the podman release it may not offer the keys an `ssh-agent` holds.
A connection that works with plain `ssh` can therefore fail in podman with an authentication error.
Either create a dedicated key without a passphrase that only this host accepts, or let OpenSSH carry the connection as shown below.

**Forwarding the socket with OpenSSH.** OpenSSH uses your normal configuration, agent and passphrase prompts.
Forward the remote podman socket to a local Unix socket and point a connection at it:

```sh
ssh -fNT -o ExitOnForwardFailure=yes -o StreamLocalBindUnlink=yes \
  -L /tmp/sc-amd64.sock:/run/user/1000/podman/podman.sock builder@build.example.com
podman system connection add amd64-fwd unix:///tmp/sc-amd64.sock

CONTAINER_CONNECTION=amd64-fwd sandcast build --platform linux/amd64 --push
```

**Socket path length on macOS.** macOS limits a Unix socket path to 104 characters.
The default temporary directory on macOS (`$TMPDIR`, under `/var/folders/...`) is long enough to break a forwarded socket or podman's own control socket.
Keep the local socket path short, for example directly under `/tmp` as above.

## Option 3: CI

A CI runner with the target architecture builds natively and keeps credentials in the CI secret store:

```yaml
jobs:
  image:
    runs-on: ubuntu-latest          # amd64; use ubuntu-24.04-arm for arm64
    steps:
      - uses: actions/checkout@v4
      - name: Install podman and sandcast
        run: |
          sudo apt-get update && sudo apt-get install -y podman
          # install the sandcast binary of your release here
      - name: Log in to the registry
        run: echo "$REGISTRY_TOKEN" | podman login quay.io -u "$REGISTRY_USER" --password-stdin
        env:
          REGISTRY_USER: ${{ secrets.REGISTRY_USER }}
          REGISTRY_TOKEN: ${{ secrets.REGISTRY_TOKEN }}
      - run: sandcast build --platform linux/amd64 --verify --push
```

Commit `.sandcast/mise.lock` with an entry for `linux/amd64` (`sandcast lock --platform linux/amd64` on any machine), because CI builds from the lockfile and never resolves versions itself.
The registry login stays in the runner; neither the manifest nor the image carries it.

## Choosing the image reference

A gateway whose sandbox driver shares the build engine's local image storage (a local podman gateway) can start `localhost/<name>` images.
Every other gateway pulls from a registry, so give the target a registry reference and push it:

```sh
sandcast init --image quay.io/<user>/<name> --platform linux/amd64 --push
```

A Docker Hub reference needs a namespace (`docker.io/<user>/<name>`): `docker.io/<name>` resolves to the official `docker.io/library/<name>`, and `sandcast manifest validate` warns about it.
Anonymous pulls from cluster nodes are rate limited on Docker Hub, so either give the gateway's sandbox service account a pull secret for `docker.io` or push to a registry the cluster already authenticates to.
