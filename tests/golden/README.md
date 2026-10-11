# Golden files

sandcast promises byte-identical output for identical inputs, so every renderer is tested against a golden file: the exact bytes it must produce. A golden test fails on any difference and prints the expected and the actual output.

The golden files live next to the package that owns the renderer, in its `testdata/` directory, because Go resolves test paths relative to the package. This directory only holds this README.

## The helper

All golden tests use `testutil.Golden` from `internal/testutil`:

```go
testutil.Golden(t, "testdata/plan.yaml.golden", got)
```

Without the environment variable `UPDATE_GOLDEN`, the helper compares `got` with the file and fails on a mismatch or a missing file. With `UPDATE_GOLDEN=1`, it writes `got` to the file, creating the directory if needed, and passes.

## Regenerating golden files

Regenerate after an intended change to a renderer, a template, a shipped asset that feeds a fixture, or a pin that appears in the output (such as the mise version in the Containerfile):

```sh
UPDATE_GOLDEN=1 go test ./...
```

`UPDATE_GOLDEN=1 make test` does the same. To regenerate one package only:

```sh
UPDATE_GOLDEN=1 go test ./internal/plan/...
```

Then:

1. Review the diff with `git diff -- '*/testdata/*'`. Every changed line must follow from your change. A golden file that changes for no reason points to non-deterministic output, which is a bug.
2. Run the tests again without `UPDATE_GOLDEN`, to confirm they pass on the new files.
3. Commit the golden files together with the change that caused them.

The adapted profile test also checks that its golden directories hold no file without matching output. `UPDATE_GOLDEN=1` writes and overwrites files but never deletes them, so when a fixture profile goes away, delete its golden file by hand.

## Golden files in the repository

| Golden file | Test | Content |
|-------------|------|---------|
| `internal/build/testdata/Containerfile.golden` | `internal/build/assemble_test.go` | Containerfile for the OpenShell target, all sections |
| `internal/build/testdata/Containerfile.container.golden` | `internal/build/assemble_test.go` | Containerfile for the container target (no `80-policy`, the base image's user, its home as working directory) |
| `internal/harness/claudecode/testdata/70-harness.golden` | `internal/harness/claudecode/scaffold_test.go` | The Claude Code `70-harness` section |
| `internal/mise/testdata/mise.toml.golden` | `internal/mise/config_test.go` | Generated `mise.toml` |
| `internal/plan/testdata/plan.yaml.golden` | `internal/plan/plan_test.go` | A complete `plan.yaml` |
| `internal/plan/testdata/fragments/packages-apt-get.golden` | `internal/plan/fragments_test.go` | `10-system-packages` layer for apt-get |
| `internal/plan/testdata/fragments/packages-dnf.golden` | `internal/plan/fragments_test.go` | `10-system-packages` layer for dnf |
| `internal/plan/testdata/fragments/packages-yum.golden` | `internal/plan/fragments_test.go` | `10-system-packages` layer for yum |
| `internal/plan/testdata/fragments/packages-apk.golden` | `internal/plan/fragments_test.go` | `10-system-packages` layer for apk |
| `internal/plan/testdata/fragments/mise.golden` | `internal/plan/fragments_test.go` | `30-mise` layer |
| `internal/plan/testdata/fragments/script-root.golden` | `internal/plan/fragments_test.go` | `40-recipes` layer of a root script recipe |
| `internal/policy/testdata/policy.yaml.golden` | `internal/policy/policy_test.go` | Image-embedded OpenShell policy |

The adapted profile test in `internal/profile/adapt_test.go` writes its golden files through the same helper, one directory per id scope. These files have no `.golden` extension because each one is a complete adapted profile set:

| Golden directory | Files |
|------------------|-------|
| `internal/profile/testdata/adapted/workspace/` | `index.yaml`, `label.json`, `github.yaml`, `internal-api.yaml`, `npm.yaml`, `pypi.yaml` |
| `internal/profile/testdata/adapted/platform/` | `index.yaml`, `label.json`, `github-demo-sandbox.yaml`, `internal-api-demo-sandbox.yaml`, `npm-demo-sandbox.yaml`, `pypi-demo-sandbox.yaml` |

To find golden files added since this list was written:

```sh
rg --files -g '*.golden'
rg -l 'testutil.Golden' --glob '*_test.go'
```

Other files under `testdata/` directories are inputs, not golden files: manifests, lockfiles, and the lint corpus in `internal/profile/testdata/lint/`.
