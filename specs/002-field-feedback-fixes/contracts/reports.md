# Report Shapes

JSON shapes added or changed by this feature. Fields not shown are unchanged.

## Plan notices (plan file, `notices`)

```yaml
notices:
  - "lockfile-shadow: gh: base image has 2.93.0, mise.lock pins 2.102.0 for linux/arm64; installing 2.102.0"
  - "unknown-version: codex: base image version could not be read; installing locked 0.162.1"
  - "workspace-path: python: found only under /sandbox (workspace path): /sandbox/.uv/python/cpython-3.14.3-linux-aarch64-gnu/bin/python3.14; installing it"
  - "home-rc-path: image: /sandbox/.bashrc resets PATH; sandcast re-applies the mise shims after it"
  - "reference: targets.openshell.image: docker.io/openshell-demo resolves to docker.io/library/openshell-demo"
```

Notices are sorted by kind, then component, so the plan stays deterministic.

## Build report (`sandcast build --json`)

```json
{
  "image": "docker.io/rhuss/openshell-demo:latest",
  "image_ref": "docker.io/rhuss/openshell-demo:latest",
  "sandbox_create": "openshell sandbox create --from docker.io/rhuss/openshell-demo:latest",
  "pushed": true,
  "digest": "sha256:4f1c...",
  "notices": [
    "lockfile-shadow: gh: base image has 2.93.0, mise.lock pins 2.102.0 for linux/amd64; installing 2.102.0",
    "emulation: rust: building linux/amd64 on an arm64 engine host; rust is known to fail under QEMU user emulation, see docs/guides/cross-arch.md",
    "docker-hub: image: anonymous pulls from cluster nodes are rate limited; give the gateway's sandbox service account a pull secret for docker.io or push to a registry the cluster already authenticates to"
  ]
}
```

## Verify report (`verify.yaml`, `sandcast verify --json`)

```yaml
results:
  - component_key: starship
    condition: image
    ok: true
  - component_key: session.bash-login.starship
    condition: session:bash-login
    ok: true
  - component_key: session.bash-interactive.starship
    condition: session:bash-interactive
    ok: true
  - component_key: session.zsh.starship
    condition: session:zsh
    ok: true
  - component_key: session.zsh.startup
    condition: session:zsh
    ok: false
    output: "/etc/zsh/zshrc.d/sandcast.zsh:41: command not found: zoxide"
passed: 4
failed: 1
```

## `components match --json` proposal

```json
{
  "key": "rust",
  "source": "project",
  "evidence": ["rust-toolchain.toml"],
  "pinned_versions": [
    {"value": "1.95.0", "file": "rust-toolchain.toml", "kind": "exact"}
  ]
}
```

## `harness discover --json` plugin entry

```json
{
  "name": "local-tool@dev-marketplace",
  "source": "rhuss/dev-marketplace",
  "origin": "plugin",
  "portable": true,
  "remote": {
    "url": "https://github.com/rhuss/dev-marketplace.git",
    "unpushed_commits": 2,
    "uncommitted_changes": true,
    "name_match": "match"
  }
}
```

Skill entry under `~/.claude/skills`:

```json
{"name": "my-skill", "source": "~/.claude/skills/my-skill", "origin": "skill", "portable": false, "reason": "source under ~/.claude/skills is read on the machine that runs the plan"}
```
