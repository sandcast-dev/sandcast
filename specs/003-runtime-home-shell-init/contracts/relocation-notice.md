# Contract: relocation notices in the plan

The plan surfaces, as human-readable notices (and in the structured plan output), every decision it made because of the home overmount. This mirrors the existing plan-notice mechanism (for example the GID 0 notice).

## When a notice is emitted

1. **reprovisioned**: a base-image tool whose probe status is `present` but whose every binary path is under the overmount home prefix. The plan treats it as missing and schedules installation to a persistent location: its installer's, or the `/opt` tree of a stored fragment.
2. **hidden**: the same tool when no definition and no stored fragment can install it. The plan treats it as missing and lists it as unresolved; the notice has no destination.
3. **relocated**: a baked configuration or dotfile whose natural home-relative location would fall under the overmount; the plan places it outside the home instead.

## Shape (structured)

```yaml
notices:
  - kind: reprovisioned
    subject: <component key>
    from: /sandbox/.local/bin/<tool>     # the shadowed probe paths, comma separated
    to: /opt/mise/...                    # persistent install location
  - kind: hidden
    subject: <component key>
    from: /sandbox/.local/bin/<tool>     # no to: nothing can install it
  - kind: relocated
    subject: ~/.gitconfig                # the dotfile path; no from, it would repeat the subject
    to: /etc/gitconfig
```

`from` is set for `reprovisioned` and `hidden` (the shadowed probe paths); `to` is set for `reprovisioned` and `relocated`. A `relocated` notice names its source in `subject`; repeating it in `from` was dropped after review. When git's two configuration files are merged into `/etc/gitconfig`, each source gets its own `relocated` notice.

## Human-readable form

Each notice prints one line in the plan output and the build log, for example:

```text
notice: component ripgrep was found only under /sandbox/.local/bin/rg (hidden once the workspace is mounted over the home); it is provisioned to /opt/mise instead
notice: component mytool was found only under /sandbox/.local/bin/mytool (hidden once the workspace is mounted over the home); no definition or fragment can provision it, so it is unresolved
notice: ~/.gitconfig relocated to /etc/gitconfig so it survives the workspace mount over the home
```

## Rules

- Notices are informational; they never fail the plan.
- Notices are deterministic: the same inputs produce the same notices in the same order (sorted by `subject`).
- Every reprovision, every hidden tool and every relocation produces exactly one notice (FR-010, SC-004, SC-006).
