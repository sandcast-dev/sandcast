# Provider profiles

OpenShell controls network access per process. A provider profile names the endpoints a tool may reach and the `binaries` that may reach them, and a sandbox gets that access only through provider instances attached to it at start time. sandcast builds on this model and adds no network format of its own:

- A component never lists endpoints. It references provider profile ids, for example `go` references `go-proxy` and `claude-code` references `claude-code`.
- The image policy that sandcast bakes into OpenShell images has an empty network section.
- After each build, sandcast writes an adapted copy of every referenced profile whose `binaries` match the paths inside that image. You import those copies into a gateway, create providers from them, and attach the providers to the sandbox.

Every profile uses the upstream OpenShell provider profile schema of v0.1.3, unchanged.

## Profile tiers

Profiles come from three tiers. A later tier overrides an earlier one with the same id, and the plan reports which file won.

| Order | Tier | Location | Profiles |
|-------|------|----------|----------|
| 1 | `upstream` | `profiles/upstream/v0.1.3/`, embedded in the binary | The 17 example profiles of OpenShell v0.1.3, byte for byte: `anthropic`, `aws`, `aws-bedrock`, `aws-s3`, `claude-code`, `codex`, `copilot`, `cursor`, `deepinfra`, `github`, `google-cloud`, `google-vertex-ai`, `nvidia`, `oci-genai`, `openai`, `openrouter`, `pypi` |
| 2 | `overlay` | `profiles/overlay/`, embedded in the binary | `npm`, `crates-io`, `go-proxy`, `gitlab` |
| 3 | `project` | `.sandcast/profiles/` in the project | Your own profiles |

Two files with the same id inside one tier are an error.

### The overlay

The overlay covers the package registries and the source host that the upstream examples leave out. Each overlay profile follows the header comment convention of the upstream examples and passes the upstream lint unchanged, so it can be contributed upstream as it is.

| Id | Category | Endpoints | Credential | Referenced by |
|----|----------|-----------|------------|---------------|
| `npm` | `data` | `registry.npmjs.org` | none | `node`, `pnpm` |
| `crates-io` | `data` | `crates.io`, `index.crates.io`, `static.crates.io` | none | `rust` |
| `go-proxy` | `data` | `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` | none | `go` |
| `gitlab` | `source_control` | `gitlab.com` (API and git transport) | `api_token` from `GITLAB_TOKEN` | `git` |

The `binaries` in the vendored and overlay files describe common install locations. sandcast replaces them in every adapted copy, so they do not need to match your image.

### Project profiles

Put a profile into `.sandcast/profiles/<id>.yaml` to cover an endpoint that no shipped profile covers, or to override a shipped profile with the same id. Reference it from a project component in `.sandcast/components/`.

```yaml
id: internal-api
display_name: Internal API
description: The team's internal REST API
category: other
endpoints:
  - host: api.internal.example.com
    port: 443
binaries:
  - /usr/bin/curl
```

Lint new profiles before the first build:

```console
$ sandcast profiles lint .sandcast/profiles
1 profile file(s), 0 diagnostic(s)
```

## Lint

sandcast lints profiles with rules built into the binary, so plan, build and export need no OpenShell tooling. The rules mirror `validate_profile_set` of OpenShell v0.1.3 and keep the upstream messages. They are tested against every vendored upstream profile, which must lint clean, and against a corpus of negative cases.

In summary, the lint checks:

- **Identity**: `id` is present, lowercase kebab-case and unique in the set, and `category` is one of `other`, `inference`, `agent`, `source_control`, `messaging`, `data` and `knowledge`.
- **Credentials**: names are present and unique, environment variable names are unique and not reserved, discovery entries name declared credentials, and `auth_style` has its companion field (`header_name` for `bearer` and `header`, a `path_template` with one `{credential}` for `path`, `query_param` for `query`).
- **Endpoints**: a host is present, ports are in range, `rules` and `access` are not combined, rule lists are not empty, and the `protocol`, `tls`, `enforcement` and `access` values are known. Credentialed profiles need a `protocol` on every endpoint.
- **Binaries**: every entry is a non-empty path.
- **Unknown fields**: rejected everywhere.
- **Policy host rules** that OpenShell enforces only at sandbox start: no top-level-domain wildcards and no malformed wildcards, no loopback, link-local or cloud metadata hosts, no IP literals for `protocol: tcp`, and no control-plane ports (etcd 2379 and 2380, the Kubernetes API 6443, the kubelet 10250 and 10255). A profile that breaks one of them imports cleanly but never carries traffic, so sandcast reports it early.

`refresh` and `token_grant` credential blocks are carried through unchanged and only checked for their enum values; the gateway validates them in full on import.

```console
$ sandcast profiles lint bad.yaml
bad.yaml: Bad_ID: id: provider profile id must be lowercase kebab-case using only a-z, 0-9, and '-'
bad.yaml: Bad_ID: endpoints[0].host: endpoint host 'localhost' is always blocked; the proxy will deny traffic to loopback regardless of policy
...
```

Every adapted set is linted again when it is written, during `build`, `profiles export` and `profiles import`. A diagnostic stops the step.

sandcast reads and lints profile files in place and never follows a symbolic link to one. It refuses a symlinked profile file, a symlinked `index.yaml`, and a symlinked profiles directory. When `profiles lint` runs without a path, it lints the adapted set in the build directory and first checks that directory, and the files sandcast writes there, for symlinks.

## Adaptation

After pass 1 of a build, sandcast resolves the binaries of every component inside the stage image (see [two passes](build.md#two-passes)) and adapts each referenced profile:

1. It copies the profile from its winning tier.
2. It sets `binaries` to the resolved paths of every component that references the profile, plus those components' `runtime_globs`, sorted and deduplicated. mise tools resolve to their real install path under `/opt/mise/installs/`, never to a shim, because OpenShell matches the process that opens the connection.
3. It lints the result.
4. It writes `<adapted id>.yaml` into `.sandcast/build/<target>/profiles/`, with a header comment that names the upstream id, the source tier and file, the OpenShell pin, and what changed.

Runtime globs cover processes that are not the binary a component lists. For example, `python` adds `/opt/mise/installs/python/*/bin/python*` because scripts such as `pip` run through the interpreter, and `go` adds `/opt/mise/installs/go/*/bin/*` so the profile matches the Go tools under any installed version in the mise tree.

sandcast **flags** a profile for either of two reasons. A profile whose binaries resolved to nothing is still written, with an empty `binaries` list, and flagged, because an unflagged profile without binaries would match no process and silently deny all traffic. A profile is also flagged when a resolved binary or runtime glob falls under a path the sandbox can write at runtime (a policy `read_write` path, the working directory, or a directory owned by the final user), because any process placed there would inherit that profile's network access. Flagged profiles appear in the build report, in `index.yaml`, in the image label, and in the header of the profile file; the reason is recorded in the build report, in `index.yaml`, and in the profile header.

Next to the profiles, `index.yaml` maps every upstream id to its adapted file. For the example in the [build guide](build.md#the-example), it looks like this:

```yaml
schema_version: 1
image:
  name: localhost/demo
  tag: latest
  digest: sha256:<pass-1 image id>
  platform: linux/arm64
image_label: demo
scope_hint: workspace
profiles:
  - upstream_id: claude-code
    adapted_id: claude-code
    file: claude-code.yaml
    source_tier: upstream
    source_file: profiles/upstream/v0.1.3/claude-code.yaml
    binaries:
      - /usr/local/bin/claude
    flagged: false
  - upstream_id: go-proxy
    adapted_id: go-proxy
    file: go-proxy.yaml
    source_tier: overlay
    source_file: profiles/overlay/go-proxy.yaml
    binaries:
      - /opt/mise/installs/go/*/bin/*
      - /opt/mise/installs/go/<version>/bin/go
      # ... one entry per resolved binary
    flagged: false
```

`image.digest` is the ID of the pass-1 image. The final image cannot record its own digest, because the index is copied into it.

## Profile ids

An adapted profile keeps its upstream id by default. That suits workspace scope, where one project owns the profiles of its workspace.

Several images in one gateway namespace need distinct ids. With `--scope platform`, or with `--suffix`, each id gets the image label as a suffix: `<id>-<image label>`. The image label is the manifest `name` in lowercase kebab-case, so the manifest `demo` turns `go-proxy` into `go-proxy-demo`.

`sandcast build` always writes the set with upstream ids. `sandcast profiles import` and `sandcast profiles export` apply the suffix when you ask for it, and report the mapping from upstream id to adapted id: the import report lists it under `mapping`, and the export rewrites `index.yaml` with the suffixed ids.

## Finding the profiles from the image

The image carries its adapted profile set, so a runtime that starts sandboxes from it does not need the build directory:

- The profile files and `index.yaml` are copied to `/usr/share/sandcast/profiles/` in pass 2.
- The label `io.sandcast.profiles-path` names that directory.
- The label `io.sandcast.profiles-index` lists the adapted ids and the flagged ids as JSON.

```console
$ podman image inspect localhost/demo:latest --format '{{index .Labels "io.sandcast.profiles-index"}}'
{"ids":["claude-code","go-proxy"],"flagged":[]}
```

OpenShell itself reads no image labels. The label is for runtimes such as cc-deck and for scripts.

## Exporting from an image

`sandcast profiles export` reads `/usr/share/sandcast/profiles/` from an existing image in a throwaway container and writes the set to `.sandcast/build/<target>/profiles/`, without rebuilding. Use it on a fresh checkout or in CI, when the image exists but the build directory does not:

```sh
sandcast profiles export --image localhost/demo:latest
sandcast profiles export --suffix
```

## Importing into a gateway

The import step is the only part of sandcast that needs OpenShell tooling: the `openshell` CLI on the 0.1.x line (pinned to v0.1.3) and a reachable gateway.

```sh
sandcast profiles import --scope workspace
```

The command works in this order:

1. It runs `openshell --version` and stops with exit 3 unless the CLI reports a release on the pinned minor line (any 0.1.x for the v0.1.3 pin; a pre-release suffix such as `0.1.3-dev.1` counts as its base version). Nothing contacts the gateway before this check passes.
2. It applies the id strategy: upstream ids for workspace scope, suffixed ids for platform scope or `--suffix`.
3. It lists the profiles that exist at the scope with `openshell profile list -o json`.
4. It imports every adapted profile whose id does not exist yet, one file at a time with `openshell profile import --file`. Platform scope adds `--global` to each call. At workspace scope, sandcast passes `--workspace <name>` when you give that flag, and otherwise lets the CLI use its current workspace.
5. It reports every id that already exists as `exists: <id> (left unchanged)` and exits 1.

sandcast never deletes gateway profiles and never overwrites one unless you ask. When ids already exist, choose one of these:

| Option | Effect |
|--------|--------|
| `--update` | Replaces the existing profiles with the same ids. sandcast exports each one to read its `resource_version` and runs `openshell profile update`. This affects every sandbox that uses those profiles. |
| `--suffix` | Imports the profiles under image-specific ids such as `go-proxy-demo`. |
| `--scope platform` | Imports at platform scope instead, which implies `--suffix`. |

`--dry-run` lists what would be imported or updated without changing the gateway. It still checks the CLI version and lists the existing profiles, so it needs the gateway too.

To choose the gateway, pass `--gateway <name>`; sandcast passes it through to every `openshell` call. Without it, the `openshell` CLI picks the gateway from `OPENSHELL_GATEWAY` (a gateway name), `OPENSHELL_GATEWAY_ENDPOINT` (an endpoint URL), or its active gateway:

```sh
sandcast profiles import --scope workspace --gateway staging
OPENSHELL_GATEWAY_ENDPOINT=http://127.0.0.1:18180 sandcast profiles import --scope workspace
```

Running the same import twice is safe: the second run reports every id as existing and leaves the gateway unchanged. `make test-gateway` checks this against a v0.1.3 gateway started by `tests/integration/gateway.sh`.

## Harness MCP servers

An agent that calls a remote MCP server opens that connection itself, so the agent's binary needs a profile that allows the server's host. When the manifest bakes a harness with URL servers (see [Harnesses](../configuration.md#harnesses)), the build checks each server against the profiles the agent component references, for Claude Code the upstream `claude-code` profile. When no endpoint in those profiles covers a server's host and port, the build generates a profile for the server and writes it into the adapted set, with the source tier `project` and the source file `generated from harness claude-code mcp_servers`.

For this manifest entry:

```yaml
harnesses:
  - name: claude-code
    mcp_servers:
      - name: docs
        url: https://docs.example.com/sse
        env:
          DOCS_TOKEN: "${SANDCAST_SECRET_2}"
```

the build writes `mcp-docs.yaml` with this content (below the generated header):

```yaml
id: mcp-docs
display_name: MCP server docs
description: MCP server docs at docs.example.com:443, generated by sandcast from the manifest
category: other
credentials:
  - name: api_token
    description: Credential of MCP server docs
    env_vars: [DOCS_TOKEN]
    required: true
    auth_style: bearer
    header_name: authorization
discovery:
  credentials: [api_token]
endpoints:
  - host: docs.example.com
    port: 443
    protocol: rest
    access: full
    enforcement: enforce
binaries:
  - /usr/local/bin/claude
```

The rules:

- The id is `mcp-` followed by the server name in lowercase, with every run of characters other than `a` to `z` and `0` to `9` replaced by a dash. The build stops with an error when two servers map to the same id, or when the agent component already references a profile with that id that does not cover the server.
- The single endpoint is the URL's host and port, 443 for `https` and 80 for `http` when the URL has none.
- `binaries` are the resolved paths of the agent component, so only the agent may reach the server.
- When the server's `env` carries a credential (a placeholder or a variable reference), the profile declares it as a bearer credential named `api_token` with that variable, and the endpoint gets `protocol: rest` with `access: full`, so that the gateway inspects the traffic and injects the credential. The profile never contains a value. A server without a credential gets an endpoint with only the host and port.
- Command (stdio) servers, servers on loopback inside the sandbox, and servers already covered by an agent profile get no profile.

Generated profiles pass the same lint as every other adapted profile, appear in `index.yaml` and the image label, and are imported with the rest of the set.

## Creating providers and starting a sandbox

A profile is a template. To use it, create a provider from it, supply its credentials, and attach the provider to the sandbox. This is the job of whatever starts the sandbox: a script, a runtime such as cc-deck, or you. sandcast never handles credential values.

For the example image, with the profiles imported at workspace scope:

```sh
openshell provider create --name demo-claude --type claude-code --credential ANTHROPIC_API_KEY
openshell provider create --name demo-go --type go-proxy
openshell sandbox create --name demo --from localhost/demo:latest \
  --provider demo-claude --provider demo-go -- zsh -l
```

`--type` is the adapted profile id. `--credential ANTHROPIC_API_KEY`, given as a name without a value, makes the CLI read the value from your environment, so it never appears on the command line. `go-proxy` needs no credential. For profiles imported at platform scope, use the suffixed id (for example `--type claude-code-demo`) and add `--global-profile`.

A generated MCP profile works the same way. If the image also bakes the `docs` server from the previous section, create one more provider that reads `DOCS_TOKEN` from your environment, and use this `sandbox create` command instead of the one above:

```sh
openshell provider create --name demo-docs --type mcp-docs --credential DOCS_TOKEN
openshell sandbox create --name demo --from localhost/demo:latest \
  --provider demo-claude --provider demo-go --provider demo-docs -- zsh -l
```

`--from localhost/demo:latest` works when the gateway uses the same container engine that built the image, for example a gateway with the podman driver on the build host. Otherwise push the image (`sandcast build --push` or `push: true` in the target) and pass the registry reference.

### Smoke test

Inside the sandbox, exercise every provider:

```sh
go list -m golang.org/x/text@latest
claude -p 'reply with OK'
```

The module lookup goes through the `go-proxy` provider and the prompt through the `claude-code` provider. On the test gateway of `make test-gateway`, a sandbox started from a built image ran `go version` and `claude --version`, with Claude Code reading its configuration from `CLAUDE_CONFIG_DIR=/opt/sandcast/claude-code`. Then check the sandbox logs from the host:

```sh
openshell logs demo
```

A working setup shows no `credential_endpoint_mismatch` and no denied request from the included binaries. A denied request from a binary you expected to be allowed usually means its path is missing from the adapted profile: check the `binaries` in `.sandcast/build/openshell/profiles/<id>.yaml`, and add a `runtime_globs` entry to the component when the connecting process is not the binary the component lists.
