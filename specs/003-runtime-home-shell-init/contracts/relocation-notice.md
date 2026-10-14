# Contract: relocation notices in the plan

The plan surfaces, as human-readable notices (and in the structured plan output), every decision it made because of the home overmount. This mirrors the existing plan-notice mechanism (for example the GID 0 notice).

## When a notice is emitted

1. **reprovisioned**: a base-image tool whose probe status is `present` but whose binary path is under the overmount home prefix. The plan treats it as missing and schedules installation to a persistent location.
2. **relocated**: a baked configuration or dotfile whose natural home-relative location would fall under the overmount; the plan places it outside the home instead.

## Shape (structured)

```yaml
notices:
  - kind: reprovisioned
    subject: <component key>
    from: /sandbox/.local/bin/<tool>     # the shadowed probe path
    to: /opt/mise/...                    # persistent install location
  - kind: relocated
    subject: <dotfile path or config>
    from: ~/.gitconfig
    to: /etc/gitconfig
```

## Human-readable form

Each notice prints one line in the plan output and the build log, for example:

```text
notice: tool 'ripgrep' was found only under /sandbox (hidden at runtime); it will be installed to /opt/mise instead.
notice: git config relocated to /etc/gitconfig so it survives the workspace mount over the home.
```

## Rules

- Notices are informational; they never fail the plan.
- Notices are deterministic: the same inputs produce the same notices in the same order (sorted by `subject`).
- Every reprovision and every relocation produces exactly one notice (FR-010, SC-004, SC-006).
