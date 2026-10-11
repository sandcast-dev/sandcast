# Contract: skill bundle

The skill bundle is executable content and is reviewed like Go source (constitution V). It is embedded in the binary and installed by `sandcast skills install`.

## Layout

```text
skills/
├── VERSION                 # bundle version, equals the binary version at release
├── capture/SKILL.md
└── build/SKILL.md
```

Frontmatter of every SKILL.md:

```yaml
---
name: sandcast-capture        # or sandcast-build
description: <one line>
sandcast_version: <VERSION>
---
```

Claude Code install location: `~/.claude/skills/<name>/SKILL.md` (scope `user`) or `<project>/.claude/skills/<name>/SKILL.md` (scope `project`). Invocation: `/sandcast-capture`, `/sandcast-build`.

## Shared rules (both skills)

1. First step: run `sandcast skills check --harness claude-code`. On exit 3, print the message and stop (FR-004).
2. Never call an LLM-unfriendly guess for facts the binary provides: components, matches, probe facts, plan output and verify results are read from `sandcast ... --json`.
3. Never write credential values anywhere. When a discovered value looks like a secret, keep the placeholder the binary returned and tell the user how to supply it as a provider credential at runtime.
4. Never edit generated files in `.sandcast/build/`. The skill writes only: `.sandcast/manifest.yaml` (capture), `.sandcast/mise.lock` (both skills, through `sandcast lock`, after the user agrees), `.sandcast/fragments/<key>.containerfile` (build, through `sandcast fragments write`), `.sandcast/learnings.yaml` (build, through `sandcast learnings add`), `.sandcast/recipes/<key>.yaml` (build, through `sandcast promote`).

## Capture skill contract

Input: a project directory, access to the host. Steps the skill must perform, in order:

1. Ask which harnesses to include (FR-036). Run `sandcast harness discover <name>` only for the selected ones.
2. Run `sandcast components match . --host --json`; present proposals with evidence; let the user confirm, drop or add components (FR-034, FR-035).
3. Read the host shell configuration for the user's login shell. Curate it: strip plugin managers (antidote, oh-my-zsh, zinit, zplug), Homebrew and macOS paths, host-only tools; add `autoload -Uz compinit && compinit -C` when `compdef` or `zstyle ':completion:*'` remain; guard `starship init` with `[[ "$TERM" != "dumb" ]]`; replace `source <(fzf --zsh)` with the version-aware block; list every command the curated configuration references and add the matching components (brainstorm 072, changes 7, 11, 12, 13).
4. Write the manifest with `sandcast manifest set-shell` and direct edits, then run `sandcast manifest validate`. Stop with the validation output if it fails.
5. Offer `sandcast lock` for the target platform.

Output: `.sandcast/manifest.yaml` and `.sandcast/mise.lock`. The skill writes no image definition (FR-037).

## Build skill contract

Input: a project with a manifest. Steps:

1. `sandcast plan --json`. If `conflicts` is non-empty, report and stop. If `unresolved` is empty, run `sandcast build --verify` and report; done.
2. For each unresolved component: write a Containerfile fragment for it using the plan's `image_facts` (package manager, final user, home) and the component's `check`, and store it with `sandcast fragments write <key>` (FR-028, US3 scenario 1). Fragments follow the rules: `USER root` for system changes and back to the final user afterwards, one `RUN` per fragment, `set -eux` (and `-o pipefail` only when the `RUN` runs bash, `RUN bash -euxo pipefail -c '...'`, because `/bin/sh` may be dash), create and chown parent directories, no network calls to unpinned URLs without a checksum.
3. `sandcast build --verify`. On failure, read `build.log`, record `sandcast learnings add --component <key> --failure "<summary>" --change "<what will change>" --attempt <n>`, fix the fragment, retry. Stop after the configured limit (default 3, `--max-attempts`), leaving the last Containerfile in place (FR-029, edge case).
4. On success with a self-written fragment: offer `sandcast promote <key>`. Only on explicit confirmation run it (FR-018, US3 scenario 3).
5. Offer `sandcast profiles import --scope workspace` when the `openshell` CLI is present.

Output: a built and verified image, adapted profiles, updated learnings, optionally promoted recipes.
