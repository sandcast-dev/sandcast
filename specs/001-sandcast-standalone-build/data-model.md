# Data Model: sandcast Standalone Build

**Spec**: [spec.md](spec.md) | **Research**: [research.md](research.md) | **Contracts**: [contracts/](contracts/)

All entities are files. Field names below are the YAML or TOML keys. Go types live in the package named in each heading. Validation rules reference the functional requirements they implement.

## Manifest (`internal/manifest`)

File: `.sandcast/manifest.yaml`. Schema: [contracts/manifest.schema.yaml](contracts/manifest.schema.yaml).

| Field | Type | Required | Rules |
|-------|------|----------|-------|
| `schema_version` | int | yes | Must equal the binary's supported version (1). Newer refused naming both (FR-009). |
| `name` | string | yes | kebab-case, 1 to 63 chars; source of the image label and default image name. |
| `base_image` | string | yes | Registry entry name or an image reference. A reference outside the registry yields a notice (FR-010). |
| `engine` | enum `podman`, `docker` | no | Overrides auto-detection; the command-line flag overrides this (FR-005). |
| `platform` | string `linux/<arch>` | no | Target platform; the command-line flag overrides this; default host platform (FR-026a). |
| `components` | list of ComponentRef | yes | Keys unique; each must resolve in a tier. |
| `harnesses` | list of HarnessRef | no | Each must name a known adapter. |
| `shell` | Shell | no | Curated shell configuration. |
| `targets` | map `openshell`, `container` → Target | yes, at least one | Image name per target. |

ComponentRef: `key` (string), `version` (optional version selector overriding the component default), `optional` (bool, default false; an optional component that fails to resolve is reported but does not block the plan).

HarnessRef: `name` (`claude-code` in phase 2; `codex`, `opencode`, `pi` later), `mcp_servers` (list of `{name, command|url, env_placeholders}`), `plugins` (list of `{name, source}`), `skills` (list of `{name, source}`). No credential values anywhere (FR-008); env placeholders use `${SANDCAST_SECRET_<N>}`.

Shell: `program` (`zsh` or `bash`), `rc` (multi-line string, the curated configuration), `components` (list of component keys the configuration references, added by capture).

Target: `image` (image name without tag), `tag` (default `latest`), `push` (bool, default false), `allowed_domains_file` (container target only, default `allowed-domains.txt`).

State: written by capture or by hand, read by plan. No transitions.

## Base image registry entry (`internal/registry`)

File: shipped `base-images.yaml`, user `~/.config/sandcast/base-images.yaml`. Map of target type → list of entries.

| Field | Type | Rules |
|-------|------|-------|
| `name` | string | Unique per target type. |
| `ref` | string | Image reference. |
| `default` | bool | Exactly one true per target type after merging (user file wins on name). |
| `notes` | string | Free text shown in the plan notices. |

## Probe result (`internal/probe`)

Cache: `<cache>/sandcast/probe/facts/<digest>.json` and `<cache>/sandcast/probe/checks/<digest>/<component-key>-<check-hash>.json`.

ImageFacts: `digest`, `reference`, `platform`, `os` {`id`, `id_like`, `name`, `version`}, `package_manager` (`apt-get`, `dnf`, `yum`, `apk`, `none`), `user` {`name`, `uid`, `gid`, `home`, `shell`}, `shells` (list), `probed_at`.

ComponentCheck: `component_key`, `check_hash` (sha256 of the probe check command plus expected outcome), `status` (`present`, `missing`, `incompatible`), `path`, `version`, `output`.

ProbeDelta (derived, in the plan): list of `{component_key, status, installed_version, required_version, action}` where action is `skip`, `install`, `shadow`, `unresolvable`.

Lifecycle: facts and checks are immutable per key; `--no-cache` on plan deletes the digest's entries before probing (FR-012).

## Component (`internal/component`)

File: one YAML per component under `components/` (shipped), the remote cache, `.sandcast/components/` and `.sandcast/recipes/` (project-local). Schema: [contracts/component.schema.yaml](contracts/component.schema.yaml).

| Field | Type | Required | Rules |
|-------|------|----------|-------|
| `key` | string | yes | kebab-case, unique within a tier; later tier overrides (FR-016). |
| `name` | string | yes | Display name. |
| `match` | Match | no | `files` (glob list), `host_tools` (binary names), `harnesses` (names), `always` (bool). |
| `installer` | Installer | yes | Exactly one of `mise`, `package`, `script`. |
| `check` | Check | yes | `command` (sh), `expect` {`exit` (int, default 0), `version` (optional selector), `stdout_contains` (optional)}. |
| `image` | ImageNeeds | no | `read_only` (paths), `read_write` (paths), `env` (map), `dirs` (list of `{path, owner}`), `runtime_globs` (paths added to adapted profile binaries). |
| `provides` | list of binary names | no | Used for conflict detection (edge case "two components provide the same binary"). |
| `profiles` | list of profile ids | no | Each must resolve in a profile tier at plan time. |
| `targets` | list of `openshell`, `container` | no | Default both. |

Installer `mise`: `tool` (registry short name or `backend:name`), `version` (selector, default `latest`), `bins` (binary names the tool provides). Installer `package`: `packages` (map of package manager → list), `bins`. Installer `script`: `run` (multi-line sh), `bins`, `as_root` (bool). A component MUST NOT contain `endpoints` (FR-013); the loader rejects the key.

Promoted recipe: a component file written by the promote step into `.sandcast/recipes/<key>.yaml` with `installer.script.run` taken from the fragment and `promoted_from: fragments/<key>.containerfile`.

## Component registry index (`internal/component`)

File: `index.yaml` at the remote root. `schema_version` (1), `release` (tag), `files` (list of `{path, sha256}`). A file whose digest differs is rejected and the previous cache kept (FR-017).

## Tool lockfile (`internal/mise`)

File: `.sandcast/mise.lock`, format mise lockfile version 3 (TOML). Read for: presence of each mise component's tool, version, and a `platforms.<mise-platform>` table with `checksum` and `url` for the selected platform. Missing platform entry → plan error naming the platform (edge case). Written only by `sandcast lock` through `mise lock`.

Derived file `build/<target>/mise/mise.toml`: `[settings]` (`lockfile = true`, `locked = true`, `lockfile_platforms`, `use_versions_host = false`, `trusted_config_paths`), `[tools]` one entry per mise component.

## Profile (`internal/profile`)

Upstream OpenShell provider profile, loaded verbatim into a Go struct that mirrors the v0.1.2 schema with `deny_unknown_fields` semantics. Key fields used by sandcast: `id`, `display_name`, `description`, `category`, `inference_capable`, `credentials[]`, `discovery`, `endpoints[]`, `binaries[]` (scalar strings; the transitional `{path}` form is normalized to scalars on load). Tiers: upstream (`profiles/upstream/v0.1.2/`), overlay (`profiles/overlay/`), project (`.sandcast/profiles/`). Later wins by id; the plan records the winning file (edge case).

## Adapted profile set (`internal/profile`)

Directory: `build/<target>/profiles/`. One `<adapted id>.yaml` per referenced profile, plus `index.yaml` ([contracts/adapted-profiles.schema.yaml](contracts/adapted-profiles.schema.yaml)):

| Field | Type |
|-------|------|
| `schema_version` | int |
| `image` | `{name, tag, digest, platform}` |
| `image_label` | string |
| `scope_hint` | `workspace` or `platform` |
| `profiles[]` | `{upstream_id, adapted_id, file, source_tier, source_file, binaries[], flagged (bool), flag_reason}` |

Written after pass 1, copied into the image in pass 2 (R-09), and the label `io.sandcast.profiles-index` carries `{ids: [...], flagged: [...]}` as JSON.

## Build plan (`internal/plan`)

File: `build/<target>/plan.yaml`. Schema: [contracts/plan.schema.yaml](contracts/plan.schema.yaml). Sections in fixed order:

1. `schema_version`, `sandcast_version`
2. `target` (`openshell` or `container`), `platform` `{value, source}` with source in `flag`, `manifest`, `host`
3. `engine` `{name, source}`
4. `base_image` `{input, registry_entry|null, reference, digest, notes[]}`
5. `image_facts` (from ProbeResult)
6. `final_user` `{name, uid, gid, home, created (bool)}`
7. `probe_delta[]`
8. `components[]` `{key, tier, source_file, installer_kind, action, fragment_name, fragment (text), profiles[]}` sorted by key
9. `mise` `{toml (text), lock_path, platform}`
10. `profiles[]` `{id, tier, source_file, adapted_id}` sorted by id
11. `policy` (text; OpenShell target only)
12. `shell` `{program, rc_path}`
13. `unresolved[]` `{key, reason}`; `conflicts[]` `{binary, components[]}`; `notices[]`

Determinism (FR-026): no timestamps, all lists sorted, fixed encoder. Any `conflicts` entry blocks rendering of the Containerfile.

## Fragment (`internal/build`)

Rendered fragments are embedded in the plan. Stored fragments are files `.sandcast/fragments/<component-key>.containerfile` with a header comment `# sandcast-fragment: key=<key> written=<date> status=active|superseded`. A stored fragment for a key that now resolves through a recipe is reported stale and not used (edge case).

## Learnings entry (`internal/build`, written by the skill)

File: `.sandcast/learnings.yaml`, list of `{attempt (int), component, failure (string), change (string), outcome (fixed|unfixed), date}`. The binary validates the schema when reading; the skill appends.

## Verify result (`internal/verify`)

File: `build/<target>/verify.yaml`: `image` `{reference, digest}`, `results[]` `{component_key, ok, version, output}`, `passed` (int), `failed` (int).

## Image policy (`internal/policy`)

OpenShell sandbox policy v1 rendered to `build/openshell/policy.yaml`: `version: 1`, `filesystem_policy` {`include_workdir: true`, `read_only`, `read_write`} (upstream defaults merged with component image needs, sorted, deduplicated), `landlock` {`compatibility: best_effort`}, `process` {`run_as_user`, `run_as_group`}, `network_policies: {}`. Upstream limits enforced: at most 256 paths, absolute, no `..`, `/` never in `read_write`.

## Harness adapter (`internal/harness`)

Go interface, phase 2:

- `Name() string`
- `Discover(ctx, projectDir) (Discovery, error)` returning MCP servers, plugins, skills found locally, with secrets already redacted.
- `Component() Component` the agent's component definition.
- `Scaffold(plan) (fragment text)` configuration layer for non-interactive use.
- `SkillInstallDir(scope) string`, `SkillFileName(skill) string`.

Registry of adapters is a static map; `claude-code` is the only entry in this feature.

## Target (`internal/plan`)

Enum `openshell`, `container`. Selects the registry list, the Containerfile sections (`80-policy` only for openshell), the verify runner (same for both) and the extra container-target output `allowed-domains.txt` (sorted unique endpoint hosts of the referenced profiles, FR-040).

## Relationships

```text
Manifest ──references──▶ Component (by key, tiers) ──references──▶ Profile (by id, tiers)
Manifest ──selects────▶ Base image registry entry ──probed as──▶ Probe result (by digest)
Manifest + Lockfile + Probe result + Components + Profiles ──plan──▶ Build plan
Build plan + Stored fragments ──build──▶ Image (pass 1) ──resolve──▶ Adapted profile set ──build──▶ Image (pass 2)
Image ──verify──▶ Verify result
Adapted profile set ──import──▶ Gateway profiles (workspace or platform scope)
Fragment ──promote──▶ Promoted recipe (project-local Component); fragment marked superseded
```
