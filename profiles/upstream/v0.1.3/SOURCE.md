# Vendored OpenShell example provider profiles

The YAML files in this directory are copied byte for byte from the `providers/` directory of the OpenShell repository.
They are not modified, including their header comments and SPDX lines.

| Item | Value |
|------|-------|
| Repository | https://github.com/NVIDIA/OpenShell |
| Tag | `v0.1.3` |
| Commit | `e1f3c82caa3ed3b65de22889ae7ef32a774878ef` |
| Path | `providers/*.yaml` (the `README.md` there is not copied) |
| Files | 17 |
| Vendored | 2026-10-12 |
| License | Apache-2.0 (see the SPDX header of each file) |

sandcast uses these files as the upstream profile tier and as the regression oracle for its built-in lint (`internal/profile`).
Every file here must pass `go test ./internal/profile/...` with zero diagnostics.

## Refreshing to a new OpenShell release

A refresh is a deliberate change (constitution III).
Never update these files implicitly.

1. Check out the new release tag in an OpenShell clone and note the full commit hash (`git rev-parse HEAD`).
2. Create `profiles/upstream/<new tag>/` and copy `providers/*.yaml` into it unchanged (do not copy `README.md`).
3. Write a `SOURCE.md` there with the new tag, commit and date, and remove the old directory.
4. Compare `crates/openshell-providers/src/profiles.rs` (`validate_profile_set`, `is_valid_profile_id`, the profile structs) and `crates/openshell-policy/src/lib.rs` (endpoint host rules) between the two tags, and port every changed rule or field to `internal/profile`.
5. Run `go test ./internal/profile/...`. Every vendored and overlay profile must still lint clean.
6. Update `OpenShellPin` and `OpenShellCommit` in `internal/version/version.go`.
7. Add a CHANGELOG entry naming the old and new pin.
