# Data Model: Runtime home model and shell initialization

Entities are config-level, not a database. They describe what the manifest records and what the plan emits.

## DotfileSelection (manifest)

The set of non-shell home dotfiles to bake. Lives under the manifest, produced by capture (allowlist, confirmed) and extendable by the user (explicit paths).

| Field | Type | Meaning |
|-------|------|---------|
| `source` | enum `allowlist` \| `manifest` | How the entry was added. |
| `host_path` | string | Path on the host home the content came from (allowlist) or the user-listed path (manifest). Relative to the host home unless absolute. |
| `relocated_to` | string | Image path the content is baked to (for example `/etc/gitconfig`, `/etc/xdg/starship.toml`). Never under the overmount home. |
| `merged_from` | list | Further host paths whose content precedes `host_path`'s, in git's order. Set only when git's XDG file and `~/.gitconfig` are merged into `/etc/gitconfig`. |
| `env` | map | Relocation variables this dotfile needs (for example `STARSHIP_CONFIG`, `INPUTRC`). Empty when the tool reads a system path natively. |

Rules:
- Every entry is passed through `internal/redact`; a detected credential becomes a placeholder, never a baked value.
- `relocated_to` MUST NOT be under the overmount home prefix.
- A symlink `host_path` is resolved; if it escapes the home, the entry is dropped with a notice.
- Deterministic ordering (sorted by `relocated_to`) so builds are reproducible.

## RelocationEnv (plan output)

The environment variables the image sets so programs read baked config. Emitted as Containerfile `ENV`, and exported again by the global zshenv and the shell hooks, because OpenShell starts its shells with a cleared environment.

| Variable | Value (example) | Purpose |
|----------|------------------|---------|
| `ZDOTDIR` | `/opt/sandcast/zdotdir` | zsh reads its startup from here (also set in `/etc/zsh/zshenv` and `/etc/zshenv`). |
| `STARSHIP_CONFIG` | `/etc/xdg/starship.toml` | starship configuration. |
| `INPUTRC` | `/opt/sandcast/inputrc` | readline configuration. |

The original model also listed `XDG_CONFIG_HOME` and `GIT_CONFIG_GLOBAL`. Both were dropped after review: an image-wide `XDG_CONFIG_HOME` breaks tools that write their own configuration, and git reads `/etc/gitconfig` natively.

## HomeLayout (image, baked)

The relocation prefix layout, all outside the overmount home:

```text
/etc/zsh/zshenv, /etc/zshenv     # set ZDOTDIR (Debian and Ubuntu read only the first)
/opt/sandcast/zdotdir/.zshrc     # curated zsh config (moved from /etc/zsh/zshrc.d)
/opt/sandcast/bashrc             # curated bash config (empty without one)
/opt/sandcast/shell-login        # launcher: exec bash --noprofile --rcfile /opt/sandcast/bashrc "$@" (bash case)
/opt/sandcast/sh-login           # launcher: ENV=/etc/profile.d/sandcast.sh, exec a non-login /bin/sh (other shells)
/etc/profile.d/sandcast.sh       # minimal default for the sh launcher
/etc/profile.d/zz-sandcast-shell.sh  # login hook for shells OpenShell starts (OpenShell target)
/etc/bash.bashrc                 # sandcast block prepended: interactive bash hook (OpenShell target)
/opt/sandcast/home/.profile      # restores HOME for other POSIX login shells (OpenShell target)
/etc/gitconfig                   # git system config (both git files merged)
/etc/tmux.conf                   # tmux system config
/etc/xdg/...                     # starship and other configs (default XDG_CONFIG_DIRS)
/opt/sandcast/inputrc            # readline config, includes /etc/inputrc
```

The original layout put the launcher flags as `--noprofile --norc --rcfile`, which made bash ignore the rcfile, and used an `/opt/sandcast/xdg` tree with `XDG_CONFIG_HOME`; both were corrected after review.

## RelocationNotice (plan/build output)

A reported decision made because of the overmount.

| Field | Type | Meaning |
|-------|------|---------|
| `kind` | enum `relocated` \| `reprovisioned` \| `hidden` | Whether a baked path was placed outside the home, a base tool found only under the overmount home was scheduled for install, or such a tool stays unresolved because no definition or stored fragment can install it. |
| `subject` | string | Component key or dotfile path. |
| `from` | string | For `reprovisioned` and `hidden`: the probed paths under the overmount home, comma separated. Omitted for `relocated`, whose source is the `subject`. |
| `to` | string | For `reprovisioned` and `relocated`: the persistent location chosen. Absent for `hidden`. |

Notices appear in the plan output and the build log; they are informational, not errors.

## Probe reclassification (plan delta)

Not a stored entity: a pure function at plan time. Input: a cached `CheckResult{status, Path}` plus the base image's overmount prefix (the registry entry's `overmount_home`, default `/sandbox`, recorded in the plan as `base_image.overmount_home`). Output: if `status == present` and every `PATH` hit is under the prefix, treat as `missing`; emit a `RelocationNotice{kind: reprovisioned}` with the install location when an installer or a stored fragment provisions the tool, and a `RelocationNotice{kind: hidden}` without `to` when nothing can, in which case the component stays unresolved.
