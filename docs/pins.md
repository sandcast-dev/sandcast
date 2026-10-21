# Refreshing pins

sandcast depends on fixed releases of OpenShell, mise and its remote component registry. A pin never moves by itself: no build, plan or refresh updates it implicitly. Changing a pin is a deliberate change with a CHANGELOG entry that names the old and the new value.

| Pin | Value | Defined in |
|-----|-------|------------|
| OpenShell | v0.1.3, commit `e1f3c82caa3ed3b65de22889ae7ef32a774878ef` | `OpenShellPin`, `OpenShellCommit` in `internal/version/version.go`; `Pin` in `internal/policy/policy.go` |
| mise in images | v2026.10.4 | `MisePin` in `internal/version/version.go` |
| mise on the host | 2026.9.16 or newer | `MiseMinHost` in `internal/version/version.go` |
| Component registry | v0.1.0 | `ComponentRegistryPin` and `ComponentRegistryURL` in `internal/version/version.go` |

`sandcast version` prints the pins a binary was built with.

## OpenShell

The OpenShell pin covers four things that must stay consistent with each other: the vendored example profiles, the profile schema and lint rules in `internal/profile`, the default sandbox policy, and the `openshell` CLI release that `sandcast profiles import` requires.

1. **Prepare the source.** Check out the new release tag in an OpenShell clone and note the full commit hash with `git rev-parse HEAD`. Read the release notes for changes to provider profiles, policy validation and the sandbox image contract.

2. **Re-vendor the profiles.** Create `profiles/upstream/<new tag>/` and copy `providers/*.yaml` into it byte for byte; do not copy `providers/README.md`. Write a `SOURCE.md` there with the tag, the commit, the date and the file count, following the existing [SOURCE.md](../profiles/upstream/v0.1.3/SOURCE.md). Remove the old directory.

3. **Update the version constants.** Set `OpenShellPin` and `OpenShellCommit` in `internal/version/version.go`. `embed.go` and the profile tier prefix in `internal/profile/tiers.go` derive the vendored directory from `OpenShellPin`, so they follow without an edit; `embed_test.go` confirms the new directory is embedded.

4. **Check the profile schema.** Compare `ProviderTypeProfile` and its nested structs in `crates/openshell-providers/src/profiles.rs` between the two tags with the Go types in `internal/profile/types.go`. Add new fields and new enum values. The loader rejects unknown fields like upstream does, so a missed field shows up as a load error on the newly vendored files.

5. **Port the lint rules.** Compare `validate_profile_set` and `is_valid_profile_id` in `crates/openshell-providers/src/profiles.rs`, and the host, address and port rules that `internal/profile/hostrules.go` lists with their source files (`crates/openshell-policy/src/lib.rs`, `crates/openshell-core/src/net.rs`, `crates/openshell-server/src/grpc/policy.rs` and `crates/openshell-supervisor-network/src/proxy.rs`). Port every changed rule and message to `internal/profile/lint.go` and `internal/profile/hostrules.go`, including the supported MCP protocol revisions in `supportedMCPVersions`. Add a negative case to `internal/profile/testdata/lint/` for each new rule.

6. **Re-run the lint corpus.**

   ```sh
   go test ./internal/profile/...
   ```

   Every vendored upstream profile and every overlay profile must lint clean, and every negative case must still fail with its expected message. An overlay profile that fails the new rules must be fixed, since the overlay is meant to be contributed upstream unchanged.

7. **Refresh the default policy.** Compare `restrictive_default_policy()` in `crates/openshell-policy/src/lib.rs` with `policy/default-v0.1.3.yaml`. Write `policy/default-<new tag>.yaml` with the new filesystem and landlock defaults, keep the header comment with the source line numbers and the commit, and delete the old file. Set `Pin` in `internal/policy/policy.go` (the defaults file name derives from it) and check the limits next to it (`MaxFilesystemPaths`, `MaxPathLength`, `MaxDocumentBytes`) against `crates/openshell-policy/src/lib.rs` and `crates/openshell-policy-schema/src/lib.rs`. The network section must stay empty unless the new default policy defines network entries.

8. **Check the image contract.** Compare the reserved paths and the policy location in the upstream sources (`container_paths.rs`, `driver_mounts.rs`) with the image paths in `internal/build/sections.go` (`PolicyPath`, `WorkDir`) and with the reserved paths the build skill avoids in `skills/build/SKILL.md`.

9. **Update the remaining mentions.** `rg -n 'v0\.1\.3'` finds them: the help text of `sandcast profiles lint` in `internal/cli/profiles.go`, the gateway version check in `internal/gateway/openshell.go`, test fixtures, the default gateway image `ghcr.io/nvidia/openshell/gateway:0.1.3` in `tests/integration/gateway.sh` (overridable with `OPENSHELL_GATEWAY_IMAGE`), the README and the pages under `docs/`. The import version check accepts any release on the pinned minor line, so a new patch release on the same line needs no code change; a new minor or major does.

10. **Regenerate the golden files** with `UPDATE_GOLDEN=1 go test ./...` and review the diff. The adapted profile headers and the policy header carry the pin (see [tests/golden/README.md](../tests/golden/README.md)).

11. **Test against a gateway.** Install the matching `openshell` CLI and run `make verify` and `make test-gateway`, which starts the new gateway image, imports the adapted profiles and creates a sandbox from a built image.

12. **Record the change** in [CHANGELOG.md](../CHANGELOG.md) under Pins, naming the old and the new tag and commit.

## mise

sandcast uses mise in two places. Inside images, the `30-mise` section installs mise at `MisePin` through the `mise.run` installer and runs `mise install --locked`. On the host, `sandcast lock` runs `mise lock` and refuses a mise older than `MiseMinHost`, the oldest release that writes lockfile version 3 with a checksum and a URL per platform.

1. **Read the mise changelog** between the two releases. Look for changes to the lockfile format (`lockfile_version`), `mise lock --platform`, `mise install --locked`, `mise which`, the settings sandcast writes into `mise.toml` (`lockfile`, `locked`, `lockfile_platforms`, `use_versions_host`, `trusted_config_paths`), and the environment variables the image sets (`MISE_DATA_DIR`, `MISE_CONFIG_DIR`, `MISE_CACHE_DIR`, `MISE_STATE_DIR`, `MISE_TRUSTED_CONFIG_PATHS`, `MISE_USE_VERSIONS_HOST`, `MISE_OVERRIDE_CONFIG_FILENAMES`, `MISE_OVERRIDE_TOOL_VERSIONS_FILENAMES`). The override variables rely on mise joining an absolute config file name to every directory it searches and on `none` turning off `.tool-versions`; the `session.mise.project-config` verify check fails when a new release changes either.

2. **Update the constants.** Set `MisePin` in `internal/version/version.go`. Raise `MiseMinHost` only when the in-image release needs a lockfile that older host releases cannot write. When the lockfile format version changes, update `LockFormatVersion` and the parser in `internal/mise/lock.go`.

3. **Regenerate the golden files** with `UPDATE_GOLDEN=1 go test ./...`. The Containerfile goldens and the `30-mise` fragment golden contain the pin.

4. **Re-run the in-image layout check.** Run `make test-integration`, which locks, plans, builds and verifies the reference fixture project and checks that the adapted profiles carry resolved binaries. Then inspect one built image by hand:

   ```sh
   podman run --rm --entrypoint /bin/sh localhost/demo:latest -c 'mise --version; mise which go; command -v go; echo "$PATH"'
   ```

   `mise --version` must report the new pin, `mise which` must return a path under `/opt/mise/installs/<tool>/<version>/`, `command -v` must find the shim in `/opt/mise/shims`, and `PATH` must start with `/opt/mise/shims` without any shell activation. Run the same command through a login shell (`sh -l -c '...'`) to check `/etc/profile.d/sandcast-env.sh`, and run `sandcast verify` to cover the session checks.

5. **Check the host side** with the oldest supported host mise: `sandcast lock --platform linux/amd64 --platform linux/arm64` must write a lockfile that `sandcast plan` accepts for both platforms.

6. **Update the mentions** in the README, `docs/configuration.md`, `docs/guides/build.md` and this page, and **record the change** in CHANGELOG.md under Pins.

## Component registry

The remote component registry is pinned to a release tag of its own repository. sandcast never refreshes it implicitly: users fetch it with `sandcast components refresh`, which defaults to the pinned tag.

1. **Publish the registry release.** Tag the registry repository. The tag directory must contain `index.yaml` with `schema_version: 1`, `release`, and `files`, each with a `path` and the `sha256` of the file.

2. **Verify the release** with an empty cache:

   ```sh
   SANDCAST_CACHE_DIR="$(mktemp -d)" sandcast components refresh --ref <new tag>
   ```

   The command must report zero rejected files, and `sandcast components list --tier registry` with the same cache directory must show the new definitions.

3. **Update the constant** `ComponentRegistryPin` in `internal/version/version.go`, and `ComponentRegistryURL` if the repository moved.

4. **Record the change** in CHANGELOG.md under Pins.

The registry tier always uses the tag of the last successful refresh, recorded in the `current` file of the cache. A user who refreshed under the old pin keeps the old tag until running `sandcast components refresh` again, which then fetches the new pinned tag.

## Schema versions

The manifest schema version and the plan schema version (`ManifestSchemaVersion`, `PlanSchemaVersion` in `internal/version/version.go`) are listed with the pins in the CHANGELOG. They are not upstream releases; they change when sandcast changes a file format incompatibly. A binary refuses a manifest with any other schema version and names both versions, so a bump needs a CHANGELOG entry that tells users how to update their manifests.
