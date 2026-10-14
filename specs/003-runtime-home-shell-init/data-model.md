# Data Model: Runtime home model and shell initialization

Entities are config-level, not a database. They describe what the manifest records and what the plan emits.

## DotfileSelection (manifest)

The set of non-shell home dotfiles to bake. Lives under the manifest, produced by capture (allowlist, confirmed) and extendable by the user (explicit paths).

| Field | Type | Meaning |
|-------|------|---------|
| `source` | enum `allowlist` \| `manifest` | How the entry was added. |
| `host_path` | string | Path on the host home the content came from (allowlist) or the user-listed path (manifest). Relative to the host home unless absolute. |
| `relocated_to` | string | Image path the content is baked to (for example `/etc/gitconfig`, `/opt/sandcast/xdg/starship.toml`). Never under the overmount home. |
| `env` | map | Relocation variables this dotfile needs (for example `GIT_CONFIG_GLOBAL`, `STARSHIP_CONFIG`). May be empty when the tool reads a system path natively. |

Rules:
- Every entry is passed through `internal/redact`; a detected credential becomes a placeholder, never a baked value.
- `relocated_to` MUST NOT be under the overmount home prefix.
- A symlink `host_path` is resolved; if it escapes the home, the entry is dropped with a notice.
- Deterministic ordering (sorted by `relocated_to`) so builds are reproducible.

## RelocationEnv (plan output)

The environment variables the image sets so programs read baked config. Emitted as Containerfile `ENV`.

| Variable | Value (example) | Purpose |
|----------|------------------|---------|
| `ZDOTDIR` | `/opt/sandcast/zdotdir` | zsh reads its startup from here (set in `/etc/zshenv`). |
| `XDG_CONFIG_HOME` | `/opt/sandcast/xdg` | XDG-aware tools read config here. |
| `GIT_CONFIG_GLOBAL` | `/etc/gitconfig` (or a baked path) | git global config outside the home (optional when `/etc/gitconfig` is baked). |
| per-tool | tool-specific | e.g. `STARSHIP_CONFIG`. |

## HomeLayout (image, baked)

The relocation prefix layout, all outside the overmount home:

```text
/etc/zshenv                      # sets ZDOTDIR
/opt/sandcast/zdotdir/.zshrc     # curated zsh config (moved from /etc/zsh/zshrc.d)
/opt/sandcast/bashrc             # curated bash config
/opt/sandcast/shell-login        # launcher: exec bash --noprofile --norc --rcfile /opt/sandcast/bashrc (bash case)
/opt/sandcast/xdg/...            # XDG_CONFIG_HOME tree (starship, tmux, ...)
/etc/gitconfig                   # git system config
/etc/profile.d/sandcast.sh       # minimal default for sh logins + env fallback
```

## RelocationNotice (plan/build output)

A reported decision made because of the overmount.

| Field | Type | Meaning |
|-------|------|---------|
| `kind` | enum `relocated` \| `reprovisioned` | Whether a baked path was placed outside the home, or a `/sandbox`-only base tool was scheduled for install. |
| `subject` | string | Component key or dotfile path. |
| `from` | string | The overmount-shadowed location (for `reprovisioned`, the probed `/sandbox` path). |
| `to` | string | The persistent location chosen. |

Notices appear in the plan output and the build log; they are informational, not errors.

## Probe reclassification (plan delta)

Not a stored entity: a pure function at plan time. Input: a cached `CheckResult{status, Path}` plus the base image's overmount prefix. Output: if `status == present` and `Path` is under the prefix, treat as `missing` and schedule install; emit a `RelocationNotice{kind: reprovisioned}`.
