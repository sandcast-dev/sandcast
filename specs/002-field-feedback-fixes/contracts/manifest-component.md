# Manifest and Component Schema Changes

Additive changes to `specs/001-sandcast-standalone-build/contracts/manifest.schema.yaml` and `component.schema.yaml`. Schema versions stay at 1.

## Manifest: `components[].bins`

```yaml
components:
  - key: helix
    optional: true
    bins: [hx]
```

```yaml
# manifest.schema.yaml, components.items.properties
bins:
  type: array
  items:
    type: string
    pattern: "^[A-Za-z0-9._+-]+$"
  uniqueItems: true
  description: >
    Executables of a component that no tier defines. verify checks each
    with command -v; promote defaults --bins to this list. Rejected when a
    component tier defines the key.
```

Diagnostics:

| Condition | Path | Message |
|-----------|------|---------|
| `bins` on a tier-defined key | `$.components[i].bins` | `<key> is defined by the <tier> tier; its binaries come from the definition, remove bins` |
| invalid name | `$.components[i].bins[j]` | `invalid binary name <name>` |
| declared binary provided by another component | `$.components[i].bins[j]` | existing conflict message |

## Component: `emulation_unsafe`

```yaml
# component.schema.yaml, properties
emulation_unsafe:
  type: boolean
  default: false
  description: >
    The tool is known to fail under user-mode emulation (QEMU). plan and
    build print a notice when the build platform differs from the engine
    host's architecture.
```

`components/rust.yaml` sets `emulation_unsafe: true`.
