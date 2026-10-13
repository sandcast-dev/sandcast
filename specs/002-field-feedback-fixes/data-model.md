# Data Model: Field Feedback Fixes

**Feature**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

Only changed or new entities are listed. Everything else is as in `specs/001-sandcast-standalone-build/data-model.md`. Go types are named where they exist today; field names in backticks are the YAML or JSON keys.

## Probe check result (`probe.CheckResult`), changed

| Field | Type | Change | Notes |
|-------|------|--------|-------|
| `status` | present, missing, incompatible | unchanged | |
| `path` | string | unchanged | First `PATH` hit, kept for compatibility. |
| `paths` | []string | **new** | Every `PATH` hit of every requested binary, in `PATH` order (R-02). |
| `version` | string | unchanged | Empty when unparsable. |

Cache: stored under a directory whose name includes `probe.FormatVersion = 2`. Entries of format 1 are never read.

## Image facts (`probe.Facts`, `plan.ImageFacts`), changed

| Field | Type | Change | Notes |
|-------|------|--------|-------|
| `home_rc_resets_path` | []string | **new** | Files under the default user's home that assign `PATH` without referencing `$PATH` (for example `.bashrc`). Empty when none (R-05). |

## Delta entry (`plan.DeltaEntry`), changed

| Field | Type | Change | Notes |
|-------|------|--------|-------|
| `action` | skip, install, shadow, unresolvable | unchanged values | Decision rules below. |
| `present_version` | string | unchanged | |
| `locked_version` | string | **new** | Locked version for the target platform, mise components only. |
| `ignored_paths` | []string | **new** | Paths discarded by the workspace rule. |

Decision rules for a mise component with a lock entry (R-01, R-03, FR-001):

| Probe | Paths | Present vs locked | Action | Notice |
|-------|-------|-------------------|--------|--------|
| missing | none | n/a | install | none |
| present or incompatible | all under the workspace path (OpenShell) | n/a | install | `workspace-path` |
| present or incompatible | first under the workspace path, another outside | version treated as unknown | shadow | `workspace-path`, `unknown-version` |
| present or incompatible | outside | equal | skip | none |
| present or incompatible | outside | different | shadow | `lockfile-shadow` |
| present or incompatible | outside | present version empty | shadow | `unknown-version` |

Without a lock entry, today's rules apply (present: skip; incompatible: shadow for mise). For the container target, the workspace rule is off.

## Notice

Represented as a string with a stable prefix (R-15): `<kind>: <component or image>: <message>`.

| Kind | Source | Written to plan | Written to build report |
|------|--------|-----------------|-------------------------|
| `lockfile-shadow` | plan | yes | yes |
| `unknown-version` | plan | yes | yes |
| `workspace-path` | plan | yes | yes |
| `home-rc-path` | plan (image facts) | yes | yes |
| `reference` | plan (manifest) | yes | yes |
| `emulation` | engine host architecture | no | yes |
| `gateway` | `openshell gateway list` | no | yes (build); import report for import |
| `docker-hub` | push result | no | yes |

## Manifest component entry (`manifest.ComponentRef`), changed

| Field | Type | Change | Validation |
|-------|------|--------|------------|
| `key` | string | unchanged | |
| `version` | string | unchanged | |
| `optional` | bool | unchanged | |
| `bins` | []string | **new** | Each matches the binary name pattern; no duplicates. Rejected (validate and plan) when a component tier defines `key` (FR-018). |

Manifest schema version stays 1.

## Manifest skeleton (`sandcast init`), changed inputs

| Input | Maps to | Default |
|-------|---------|---------|
| `--image name[:tag]` | `targets.<target>.image`, `targets.<target>.tag` | `localhost/<name>`, tag `latest` |
| `--platform` | `platform` | unset (host at plan time) |
| `--push` | `targets.<target>.push` | false |

## Component definition (`component.Component`), changed

| Field | Type | Change | Notes |
|-------|------|--------|-------|
| `emulation_unsafe` | bool | **new**, optional | True when the tool is known to fail under user-mode emulation. Set in `components/rust.yaml`. |

## Build result and report, changed

`build.Result` gains `Pushed bool`, `Digest string`. The CLI report (JSON) gains:

| Field | Type | Notes |
|-------|------|-------|
| `image_ref` | string | Normalized, fully qualified reference including tag (R-12). |
| `sandbox_create` | string | `openshell sandbox create --from <image_ref>`, OpenShell target only. |
| `pushed` | bool | Always present. |
| `digest` | string | Registry digest when pushed, omitted otherwise. |
| `notices` | []string | Plan notices followed by runtime notices. |

## Verify result (`verify.Result`), changed

| Field | Type | Change | Notes |
|-------|------|--------|-------|
| `component_key` | string | unchanged | For session entries, `session.<shell-mode>.<component>` or `session.zsh.startup`. |
| `condition` | `image`, `session:bash-login`, `session:bash-interactive`, `session:zsh` | **new** | |

Session entries count toward `failed` and the exit status.

## Pinned version (`capture.PinnedVersion`), new

| Field | Type | Notes |
|-------|------|-------|
| `value` | string | Raw value from the file. |
| `file` | string | Project-relative path. |
| `kind` | `exact`, `range`, `channel` | Classification rules in R-09. |

`capture.Proposal` gains `pinned_versions []PinnedVersion`, only for `source: project` proposals.

## Discovered extension (`harness.Extension`), changed

| Field | Type | Change | Notes |
|-------|------|--------|-------|
| `name`, `source`, `origin` | string | unchanged | `source` may now be a proposed `owner/repo` or remote URL. |
| `portable` | bool | **new** | |
| `reason` | string | **new** | Why not portable; empty when portable. |
| `remote` | object | **new**, optional | For proposed local marketplaces: `url` (credentials stripped), `unpushed_commits` int, `uncommitted_changes` bool, `name_match` `match`, `mismatch` or `unknown`. |

## Image session environment files

See [contracts/image-shell.md](contracts/image-shell.md). Rendered from the plan's mise environment and the manifest's shell section; no new plan input.
