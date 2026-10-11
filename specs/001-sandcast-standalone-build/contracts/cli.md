# Contract: sandcast command surface

Binary name `sandcast`. Every command accepts `--project <dir>` (default: nearest ancestor containing `.sandcast/`, else the current directory), `--engine podman|docker`, `--verbose`, `--json` where noted. Exit codes: 0 success, 1 failure of the operation, 2 usage or validation error, 3 external tool missing or wrong version, 130 interrupted by SIGINT or SIGTERM.

## Phase 1: core build

| Command | Purpose | Key flags | Outputs |
|---------|---------|-----------|---------|
| `sandcast init` | Create `.sandcast/` with a manifest skeleton and `.gitignore` | `--name`, `--base-image`, `--target openshell\|container`, `--force` | `.sandcast/manifest.yaml`, `.sandcast/.gitignore` |
| `sandcast manifest validate` | Validate manifest schema, version, component and profile references | `--json` | diagnostics with line numbers |
| `sandcast base-images list` | Show merged registry | `--target`, `--json` | table |
| `sandcast components list` | List components across tiers with winning tier | `--tier shipped\|registry\|project`, `--json` | table |
| `sandcast components show <key>` | Print the effective component definition | `--json` | YAML |
| `sandcast components refresh` | Fetch the remote registry at the pinned or given tag, verify checksums | `--ref <tag>` | cache dir, report of rejected files |
| `sandcast lock` | Generate or update `.sandcast/mise.lock` for the target platform(s) | `--platform` (repeatable), `--bump` | `.sandcast/mise.lock` |
| `sandcast probe [<ref>]` | Probe a base image (default: the manifest's) and print facts plus delta | `--platform`, `--target`, `--no-cache`, `--json` | cache files, table |
| `sandcast plan` | Produce the build plan | `--target`, `--platform`, `--no-cache`, `--out <dir>` | `build/<target>/plan.yaml`, `mise/`, `policy.yaml` |
| `sandcast build` | Assemble the Containerfile from the plan and stored fragments, two-pass build, write adapted profiles | `--target`, `--platform`, `--verify`, `--push`, `--no-cache-layers`, `--keep-stage` | `Containerfile`, `Containerfile.final`, `build.log`, `profiles/`, image |
| `sandcast verify` | Run every component check inside the built image | `--target`, `--image <ref>`, `--json` | `verify.yaml`; exit 1 on any failure |
| `sandcast profiles lint [<file\|dir>]` | Lint profiles (default: the adapted set) with the built-in rules | `--json` | diagnostics |
| `sandcast profiles export` | Re-write the adapted profile set from an existing image without rebuilding | `--image`, `--suffix` | `profiles/` |
| `sandcast profiles import` | Import the adapted set into a gateway via the `openshell` CLI | `--scope workspace\|platform`, `--workspace <name>`, `--suffix`, `--update`, `--dry-run`, `--gateway <name>` | import report, conflicts |
| `sandcast version` | Print binary version, pinned OpenShell and mise releases, skill bundle version | `--json` | |

`sandcast plan` and `sandcast build` fail with exit 2 and no partial output when the manifest schema version is unsupported, when the lockfile lacks the selected platform, or when the plan has conflicts.

Platform precedence (FR-026a): `--platform` flag, then manifest `platform`, then the host's `GOOS/GOARCH` mapped to `linux/<arch>`.

## Phase 2: capture and harness

| Command | Purpose | Key flags | Outputs |
|---------|---------|-----------|---------|
| `sandcast components match [<dir>]` | Evaluate component match conditions against a project and the host; print proposals with evidence | `--host`, `--no-host`, `--harness <name>` (repeatable), `--json` | proposals |
| `sandcast harness list` | Show known adapters and whether the harness is installed locally | `--json` | table |
| `sandcast harness discover <name>` | Run one adapter's discovery with redaction, print MCP servers, plugins, skills | `--json` | discovery |
| `sandcast skills install` | Install the skill bundle into a harness | `--harness claude-code`, `--scope user\|project`, `--force` | skill directories |
| `sandcast skills check` | Compare installed skills with the embedded bundle | `--harness`, `--json` | exit 3 on mismatch with the update hint |
| `sandcast manifest set-shell` | Write a curated shell configuration from stdin or a file into the manifest | `--program zsh\|bash`, `--file` | manifest |

## Phase 3: repair, promotion, container target

| Command | Purpose | Key flags | Outputs |
|---------|---------|-----------|---------|
| `sandcast fragments list` | Show stored fragments and their status | `--json` | table |
| `sandcast fragments write <key>` | Store a fragment from stdin or a file, validated as a Containerfile snippet | `--file` | `.sandcast/fragments/<key>.containerfile` |
| `sandcast promote <key>` | Convert a working fragment into a project-local recipe; mark the fragment superseded | `--name`, `--bins`, `--yes` | `.sandcast/recipes/<key>.yaml` |
| `sandcast learnings add` | Append a structured learnings entry (used by the skill) | `--component`, `--failure`, `--change`, `--attempt`, `--outcome`, `--date` | `.sandcast/learnings.yaml` |
| `sandcast build --target container` | Same build without OpenShell conventions (no policy, no sandbox user, the base image's user and home) plus the allowed-domains file | | `allowed-domains.txt` |

## Environment

`SANDCAST_CACHE_DIR` overrides the cache directory (default `os.UserCacheDir()/sandcast`), `SANDCAST_CONFIG_DIR` the user configuration directory (default `~/.config/sandcast`), and `SANDCAST_COMPONENT_REGISTRY_URL` the remote component registry base URL. The `openshell` CLI keeps its own gateway selection (`OPENSHELL_GATEWAY`, `OPENSHELL_GATEWAY_ENDPOINT`, the active gateway).

## Output conventions

Human output goes to stdout as tables or key-value lines; diagnostics go to stderr. `--json` prints one JSON document to stdout and nothing else. Every file sandcast writes passes the redaction scan; a hit aborts the write and names the key path.
