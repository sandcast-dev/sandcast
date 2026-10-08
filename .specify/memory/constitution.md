# sandcast Constitution

## Core Principles

### I. Every feature MUST include tests and documentation

A feature is NOT complete until:
1. Tests exist for the new code (unit tests at minimum, integration tests when touching podman, mise, or an OpenShell gateway)
2. README.md is updated with user-facing changes
3. The command reference (`docs/cli.md`) covers new commands and flags
4. The configuration reference (`docs/configuration.md`) covers new manifest fields, registry entries, component or profile tiers, and file locations
5. A guide page exists for substantial features (capture, build, profiles)

Documentation updates MUST happen in the same branch or commit that delivers the change, never as a follow-up.
This applies to every user-visible change: new subcommands, new flags, changed defaults, new component or profile tiers, and bug fixes that alter observable behavior.

### II. Determinism before convenience

The Go core (plan, build, verify, profile export) MUST produce identical output for identical inputs: the same manifest, lockfile, component and profile definitions, and base image digest.
Judgment calls, discovery, and repair belong in the skill bundle, never in the binary.
Anything the skill learns that can be made deterministic (a working install layer, a verified asset) MUST have a promotion path into a recipe or a lockfile.

### III. Upstream formats over home-grown ones

Network policy uses the upstream OpenShell provider profile schema without extension.
Tool installation uses mise and its lockfile; recipes exist only for what mise cannot install.
Overlay profiles MUST pass the upstream lint unchanged so they can be contributed upstream.
Pinned upstream releases (OpenShell, mise) are refreshed deliberately, with a changelog entry, never implicitly.

### IV. No credentials, ever

No credential value may appear in the manifest, the build plan, stored fragments, adapted profiles, the image, or the logs.
Discovered secrets are replaced by placeholders and reported.
Credentials are the responsibility of whatever starts the sandbox.

### V. Build and tool rules

- Use make targets (`make test`, `make lint`, `make verify`). Never run `go build` directly.
- Podman is the default container engine. docker is accepted through the same command surface. No engine-specific features.
- Skill files in the skill bundle are executable code and are reviewed with the same rigor as Go source.

## Governance

Constitution principles are enforced in CLAUDE.md and apply to ALL code changes, whether from a spec workflow or ad-hoc.
Amendments require updating both this file and the Design Decisions section of CLAUDE.md.

**Version**: 1.0 | **Ratified**: 2026-10-08 | **Last Amended**: 2026-10-08
