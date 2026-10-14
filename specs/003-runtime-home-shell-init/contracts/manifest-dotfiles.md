# Contract: manifest `dotfiles` field

Adds an optional `dotfiles` section to `.sandcast/manifest.yaml`. Backward compatible: absent means "no extra dotfiles", preserving current behavior. The manifest schema version does not change unless validation requires it; if it does, the version gate is bumped per the manifest package rules.

## Shape

```yaml
dotfiles:
  # Entries capture proposed from the host allowlist and the user confirmed,
  # plus entries the user added explicitly. Order does not matter on input;
  # the plan sorts deterministically.
  - path: ~/.gitconfig          # host path; ~ is the host home
  - path: ~/.config/starship.toml
  - path: ~/.tmux.conf
  - path: /abs/or/project/relative/extra.conf   # explicit manifest path (FR-006)
```

## Rules

- `path` is required per entry. A `~`-prefixed or home-relative path resolves against the host home; a project-relative or absolute path is taken as given.
- Capture only writes entries from the bounded allowlist (R4). The user may add any `path` by hand (FR-006).
- Credentials never appear here: `dotfiles` records only source paths, never contents. Contents are redacted when baked (FR-007).
- A `path` that does not exist at build time is reported and skipped, not fatal.
- Validation rejects a `dotfiles` entry whose resolved relocation would land under the overmount home (defense in depth; the plan also enforces this).

## Non-goals

- No per-entry target override in v1 (the plan picks the relocation target per R5). A future revision may add an explicit `relocated_to`.
- No runtime-seeding directive (not implemented; FR-013).
