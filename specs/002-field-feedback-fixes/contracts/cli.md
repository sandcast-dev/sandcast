# CLI Contract Changes

Changes to `specs/001-sandcast-standalone-build/contracts/cli.md`. Unchanged commands are not listed. Exit codes are unchanged unless stated.

## `sandcast init`

New flags:

| Flag | Value | Effect |
|------|-------|--------|
| `--image` | `name[:tag]` | Sets `targets.<target>.image` (and `tag` when given). |
| `--platform` | `linux/amd64` or `linux/arm64` | Sets the manifest's `platform`. Same help text as on plan and build. |
| `--push` | bool | Sets `targets.<target>.push: true`. |

Without `--image` and with `--target openshell`, init prints after the `created` lines:

```text
note: targets.openshell.image is localhost/<name>; this works only with a local OpenShell gateway that shares this engine's image storage. For a remote gateway, rerun with --image <registry>/<name> --platform <os/arch> --push.
```

## `sandcast manifest validate`

- Console: `warning: <path>: <message>` lines after the diagnostics.
- JSON: new `warnings` array with the same shape as `diagnostics`. `valid` and the exit code ignore warnings.
- Warning: `$.targets.<target>.image: docker.io/<name> resolves to docker.io/library/<name>; add your namespace (docker.io/<user>/<name>)`. The same check applies to a bare `<name>`.

## `sandcast plan`

- New notice kinds in `notices` (plan file) and as `notice:` lines: `lockfile-shadow`, `unknown-version`, `workspace-path`, `home-rc-path`, `reference`.
- Runtime notices (not in the plan file, printed to stderr after the summary): `emulation`.

## `sandcast build`

- Console report adds `image ref`, `sandbox create` (OpenShell target), `pushed`, `digest` and every notice.
- JSON report adds `image_ref`, `sandbox_create`, `pushed`, `digest`, `notices` (see [reports.md](reports.md)).
- `build.log` contains a `### sandcast push: <ref>` section with the engine's push output when pushing.
- New runtime notices: `emulation`, `gateway` (remote active gateway with a `localhost/` reference), `docker-hub` (pushed to `docker.io`).

## `sandcast verify` and `sandcast build --verify`

- For the OpenShell target, session entries are always added (no flag). Console groups them under `session checks`. Exit status 1 when any entry fails, as before.

## `sandcast profiles import`

- New `warnings` in the import report: `gateway` when the active gateway is remote and the adapted profile index names a `localhost/` image. Lookup failures produce no warning and never fail the import.

## `sandcast components match`

- JSON proposals with `source: project` gain `pinned_versions` (see [reports.md](reports.md)). Table output adds a `pinned` column showing the first exact value or the first value with its kind.

## `sandcast harness discover <harness>`

- Plugins and skills carry `portable` and `reason`. Local marketplace plugins with a git remote are listed with a proposed `source` and a `remote` object instead of a skip note. The `notes` array keeps skip notes only for directories without a remote.

## `sandcast promote <key>`

- Without `--bins` and without an existing definition, `--bins` defaults to the manifest entry's `bins`, then to the key.
