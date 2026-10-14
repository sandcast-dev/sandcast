# Capturing a project

The capture turns a project directory and the developer's own environment into a sandcast manifest. It looks at the project files, the tools installed on the host and the host shell configuration, asks which agent harnesses to include, and proposes a component list with the evidence for each entry. You confirm or edit the proposals, and the capture writes `.sandcast/manifest.yaml`.

The capture is the `/sandcast-capture` skill. The judgment calls (what to include, how to curate the shell configuration) happen in the skill, together with you. Component keys, match evidence, host tool versions, harness discovery and validation results come from the `sandcast` binary, so the skill never invents a component key or a version. The capture writes the manifest and, if you agree, the lockfile. It never writes an image definition; building is the job of `sandcast build` or `/sandcast-build`.

You can also write a manifest by hand. The [configuration reference](../configuration.md#the-manifest) describes every field.

## Installing the skills

The skill bundle is embedded in the `sandcast` binary. Install it into Claude Code once:

```sh
sandcast skills install
```

| Scope | Command | Location |
|-------|---------|----------|
| user (default) | `sandcast skills install --scope user` | `~/.claude/skills/sandcast-capture/` and `~/.claude/skills/sandcast-build/` |
| project | `sandcast skills install --scope project` | `.claude/skills/sandcast-capture/` and `.claude/skills/sandcast-build/` in the project |

The skills are then available as `/sandcast-capture` and `/sandcast-build`. The command prints one line per skill, for example `installed sandcast-capture (0.1.0) at <path>`. Each skill carries the version of the bundle it came from. Running `sandcast skills install` again leaves skills of the same version alone, replaces older ones, and skips newer ones with a hint. `--force` overwrites in every case.

The binary and the installed skills must match, because the skills rely on the binary's commands and JSON output. Check them with:

```sh
sandcast skills check
```

The check looks at the project scope first, then the user scope. A missing, older or newer skill makes it print which side to update and exit 3. Both skills run this check as their first step and stop when it fails. After upgrading the binary, run `sandcast skills install` again.

`--harness` selects the harness for both commands. `claude-code` is the default and, in this release, the only harness with an adapter; `sandcast harness list` shows the adapters and whether each harness is installed on the host. Codex, OpenCode and pi adapters are planned.

## Running a capture

Open the project in Claude Code and run:

```text
/sandcast-capture
```

The skill works through these steps and asks before it decides anything:

1. **Version check.** It runs `sandcast skills check --harness claude-code` and stops on a mismatch.
2. **Harnesses.** It asks which agent harnesses the image should include, using `sandcast harness list` to show what the host has. For each selected harness, and only for those, it runs `sandcast harness discover <name>` and shows the locally configured MCP servers, plugins and skills. You pick which ones to bake into the image; nothing is included by default. Unselected harnesses leave no trace in the manifest.
3. **Manifest and components.** When there is no manifest yet, it asks for a name, a target (`openshell` by default, or `container`) and a base image from `sandcast base-images list`, and runs `sandcast init`. It then runs `sandcast components match . --host --harness <name> --json` and presents the proposals grouped by source, with their evidence and detected versions. You confirm, drop or add components.
4. **Shell configuration.** It reads your zsh or bash configuration, curates it for the image, shows you the result and the components it references, and stores it with `sandcast manifest set-shell`.
5. **Validation.** It writes the confirmed components and harness extensions into the manifest and runs `sandcast manifest validate`. It shows every diagnostic with its line and never works around a credential finding.
6. **Lockfile.** It offers `sandcast lock` for the target platform.

At the end it summarizes the components, the harness extensions, every secret placeholder and how to supply it, and what the shell curation removed. The next step is `sandcast build --verify`, or `/sandcast-build` when the plan lists unresolved components. Commit `.sandcast/manifest.yaml` and `.sandcast/mise.lock`.

## What gets proposed and why

`sandcast components match` evaluates the `match` conditions of every component in every tier against the project and the host. Each proposal names its source and its evidence:

| Source | Proposed when | Evidence |
|--------|---------------|----------|
| `harness` | the component's `match.harnesses` contains a selected harness (for example `claude-code`) | `harness claude-code selected` |
| `project` | a file in the project matches one of the component's `match.files` globs | the matching file paths, for example `go.mod` |
| `host` | an executable from `match.host_tools` is on the host `PATH` | `host: <path> (<version>)` |
| `always` | the component sets `match.always: true` (base system components such as `git`, `curl` and `ca-certificates`) | `always included` |

When several conditions match, the source is the first one in this table, and the evidence lists all of them. For host tools, the binary runs `<tool> --version` with a short timeout and reports the version as `detected_version`. Components that match nothing are listed as available additions.

The project walk goes four directory levels deep, honours the root `.gitignore`, and skips `.git`, `.sandcast`, `node_modules`, `vendor`, `.venv`, `venv`, `target`, `dist`, `build`, `__pycache__`, `.tox`, `.idea` and `.vscode`. A glob without a slash, such as `go.mod`, matches that file name at any depth.

On the reference fixture in `tests/fixtures/reference-project`, without host detection:

```console
$ sandcast components match tests/fixtures/reference-project --no-host
COMPONENT        SOURCE   VERSION  EVIDENCE
ca-certificates  always   -        always included
curl             always   -        always included
git              always   -        always included
go               project  -        go.mod
iproute2         always   -        always included
nftables         always   -        always included
python           project  -        pyproject.toml; uv.lock
uv               project  -        uv.lock

available: bash, bat, claude-code, codex, fzf, gh, jq, lsd, node, opencode, pnpm, ripgrep, rust, starship, yq, zoxide, zsh
```

The skill keeps `always` proposals unless you remove them, sets a `version` only when you ask for one or the project pins one (the `go` directive in `go.mod`, `.python-version`, `.nvmrc`), and leaves out components that the plan adds by itself: the shell program, the components in `shell.components`, and the agent component of each harness.

When you want a tool that no tier defines, there are two ways forward. Add it with `optional: true`, and `/sandcast-build` offers to write an install fragment for it. Or define it first in `.sandcast/components/<key>.yaml` (see [component definitions](../configuration.md#component-definitions)).

### Managing the detected host-tool list

Host detection is a closed set, not a scan of your `PATH`. A host tool is proposed only when some component defines it in `match.host_tools`, so the detectable set is exactly the union of the `host_tools` of every component across the three tiers (shipped, registry, project). A binary that no component names is never proposed from the host, even when it is installed. A binary that resolves only through a shell alias or function is also not detected: `host_tools` uses an absolute `PATH` lookup, not the shell. Detect the real tool behind the alias by giving it a component instead.

The shipped components that declare `host_tools` today are `bash`, `bat`, `fzf`, `gh`, `jq`, `lsd`, `ripgrep` (binary `rg`), `starship`, `yq`, `zoxide` and `zsh`. Language toolchains such as `go`, `python`, `node` and `rust` are proposed from project files rather than the host, and base tools such as `git`, `curl` and `ca-certificates` use `match.always`.

Inspect the current set:

```sh
sandcast components list --json          # every key and its winning tier
sandcast components show <key>           # the effective definition, including host_tools
sandcast components match . --host       # what matched in this project and on this host
```

Extend or change the set through the component tiers; a later tier overrides an earlier one with the same key (see [component tiers](../configuration.md#component-tiers)).

- **Detect a new host binary.** Add a project component at `.sandcast/components/<key>.yaml` with `match.host_tools: [<binary>]`, plus an `installer` and a `check` so the tool can also be built into the image. For example, to detect and install `shellcheck`:

  ```yaml
  key: shellcheck
  name: ShellCheck
  match:
    host_tools: [shellcheck]
  installer:
    mise:
      tool: shellcheck
  check:
    command: shellcheck --version
  ```

- **Change detection of an existing tool.** Give a project component the same `key` as a shipped or registry component and set the `host_tools` you want. Because the project tier wins, your definition replaces the one that shipped (the plan records a notice naming the winning and losing file).

- **Share across projects.** Publish the component to the [remote component registry](../configuration.md#remote-component-registry) so a whole team picks it up with `sandcast components refresh`, without a new sandcast release; or contribute it to the shipped set in `components/`.

The `match.host_tools` field is defined in the [match reference](../configuration.md#match); the [component README](../../components/README.md) covers writing a full definition.

## Shell curation

The image gets a curated copy of your interactive shell configuration, baked outside the home so it survives the OpenShell workspace mount over the home (see [the home model](build.md#the-home-model-and-shell-initialization)). Host configurations carry plugin managers, macOS paths and host-only tools that break inside a Linux sandbox, so the skill applies fixed rules. They encode the failures recorded while the image build skill of cc-deck was developed. sandcast curates zsh and bash; for any other login shell, the skill asks whether to skip the shell configuration or write a minimal one with you.

### What is removed

- **Plugin managers and everything they load**: antidote, antigen, oh-my-zsh, prezto, sheldon, zgenom, zinit, znap and zplug. The skill lists the plugins that are lost.
- **Homebrew and macOS paths**: anything under `/opt/homebrew`, `/usr/local/Homebrew`, `/usr/local/opt`, `/usr/local/Cellar`, `/Applications`, `/Library`, `~/Library`, `/System` or `/Users/<name>`, plus `brew shellenv`, `$(brew --prefix ...)`, `HOMEBREW_*` and `DYLD_*`. In a `PATH` assignment only the host entries are removed, so `export PATH="/opt/homebrew/bin:$HOME/.local/bin:$PATH"` becomes `export PATH="$HOME/.local/bin:$PATH"`.
- **Host-only tools and integrations**: macOS commands such as `pbcopy`, `open` or `osascript`, terminal integrations, agent setup (`ssh-agent`, `gpg-agent`, `keychain`), and version managers (`mise activate`, `asdf`, `nvm`, `pyenv init`, `rbenv init`, conda, sdkman). The image installs tools with mise and puts them on the `PATH` without activation.
- **Sourced host files** that do not exist in the image. Their content can be inlined after the same rules are applied.
- **Credentials**: every line that exports a token, key, password or secret, or reads one from a keychain or password manager. The skill refers to such lines by line number and variable name only.

Aliases, functions, shell options, history settings, key bindings, `zstyle` lines, prompt configuration and non-secret exports such as `EDITOR` or `LANG` stay.

### What is added or rewritten

- **Completion (zsh)**: when `compdef` or `zstyle ':completion:*'` remain but nothing calls `compinit`, the skill inserts `autoload -Uz compinit && compinit -i -D` before the first of them. Plugin managers load `compinit` as a side effect; without it, tab completion falls back to file names.
- **Starship**: `eval "$(starship init zsh)"` is guarded with `if [[ "$TERM" != "dumb" ]]`, so non-interactive sessions do not print starship errors.
- **fzf**: `source <(fzf --zsh)` is replaced with a block that falls back to the key bindings shipped in `/usr/share/doc/fzf/examples/` when the installed fzf is older than 0.48. The bash variant does the same for `fzf --bash`.

### Referenced commands become components

The skill lists every external command the curated configuration uses (alias targets, `eval "$(<cmd> ...)"`, commands called in functions, and `EDITOR`, `VISUAL`, `PAGER` and `MANPAGER`) and looks up the component for each one. Matching components go into `shell.components`, so the image contains the prompt and the listing tool the configuration calls. A missing prompt or listing tool is a runtime failure that no build check detects. A command without a component is either dropped from the configuration or added as an optional component, as you decide.

A host `.zshrc` like this:

```zsh
export PATH="/opt/homebrew/bin:$HOME/.local/bin:$PATH"
source /opt/homebrew/opt/antidote/share/antidote/antidote.zsh
antidote load
zstyle ':completion:*' menu select
eval "$(starship init zsh)"
source <(fzf --zsh)
alias ls=lsd
setopt HIST_IGNORE_DUPS SHARE_HISTORY
```

becomes this curated configuration, with `shell.components` set to `[fzf, lsd, starship]`:

```zsh
export PATH="$HOME/.local/bin:$PATH"

autoload -Uz compinit && compinit -i -D
zstyle ':completion:*' menu select

if [[ "$TERM" != "dumb" ]]; then
  eval "$(starship init zsh)"
fi

if fzf --zsh &>/dev/null; then
  source <(fzf --zsh)
elif [ -f /usr/share/doc/fzf/examples/key-bindings.zsh ]; then
  source /usr/share/doc/fzf/examples/key-bindings.zsh
  [ -f /usr/share/doc/fzf/examples/completion.zsh ] && source /usr/share/doc/fzf/examples/completion.zsh
fi

alias ls=lsd
setopt HIST_IGNORE_DUPS SHARE_HISTORY
```

The skill stores the text with `sandcast manifest set-shell --program zsh --file <file>`. Change it later the same way rather than by editing `shell.rc` in the manifest.

## Dotfiles beyond the shell

Besides the shell, the skill proposes a bounded set of home dotfiles found on the host: `~/.gitconfig` and `~/.config/git/config` (git), `~/.config/starship.toml` (the prompt tool), `~/.tmux.conf` and `~/.inputrc`. You confirm which to bake with a multi-select, the same flow as harness extensions. You may also add explicit paths the allowlist omits, within fixed bounds: from the home only files under `~/.config`, otherwise only files inside the project. Credential stores (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.docker`, `~/.config/gh`), `.netrc`, history files and credential-looking names are never baked, and only regular files up to 256 KiB are read. The bounds exist because the manifest is committed with the project; copy a file into the project to bake one from elsewhere.

Each selected dotfile is read at build time, passed through the same credential redaction as everything else (a detected token becomes a `${SANDCAST_SECRET_N}` placeholder), and baked to a persistent location outside the home so it survives the overmount: git goes to `/etc/gitconfig`, tmux to `/etc/tmux.conf`, starship to `/etc/xdg/starship.toml` (with `STARSHIP_CONFIG`), readline to `/opt/sandcast/inputrc` (with `INPUTRC`), and the rest under `/etc/xdg`. The manifest records only the source path under `dotfiles`, never contents. A dotfile outside the bounds, a symlink out of its root, or a path that does not exist is skipped with a notice rather than failing the plan. See the [configuration reference](../configuration.md#dotfiles) for the manifest field.

## Secrets

No credential value may appear in the manifest, the plan, a stored fragment, an adapted profile, the image or a log. Credentials belong to whatever starts the sandbox, which supplies them as OpenShell provider credentials at runtime.

sandcast enforces this at several points:

- `sandcast harness discover` replaces every value that looks like a credential with a `${SANDCAST_SECRET_N}` placeholder before printing anything, and reports where it was (`path`), what it looked like (`kind`), the placeholder, and the environment variable it was assigned to (`env_var`).
- `sandcast manifest validate` reports every credential-shaped value in the manifest with its line, and fails.
- `sandcast manifest set-shell`, `sandcast fragments write` and `sandcast learnings add` refuse input that contains one.
- Baked dotfile contents are run through the redactor before they enter the image, so a credential in a captured `.gitconfig` becomes a placeholder and is never written.
- `sandcast plan` refuses a manifest or a stored fragment that contains one.
- Before the first build pass, sandcast scans the build context (including skill and marketplace files copied from the host, without their `.git` directories) and refuses to build on a finding; after the build it scans every output and removes the image on a finding.
- The harness scaffold refuses an MCP server or a marketplace source that discovery would have redacted, so a hand-edited manifest meets the same rule.

The detected shapes are Anthropic and OpenAI API keys, GitHub tokens and fine-grained personal access tokens, GitLab personal access tokens, AWS access key ids, bearer tokens, HTTP Basic credentials, passwords embedded in a URL (the password of `scheme://user:secret@host`), secrets in a URL query or fragment parameter whose name suggests a secret (such as `?access_token=` or `#apikey=`, where the value runs to the next `&` or `#` so that the rest of the URL survives), private keys (including PGP private key blocks), and long values assigned to keys whose names suggest a secret (such as `token`, `secret`, `password`, `credentials`, `access_key`, `privateKey` or an upper-case `GITHUB_PAT`). Discovery and the scaffold additionally treat values of any length as credentials when they sit under such a name, follow a flag such as `--api-key` or `--key`, sit in an `Authorization` header argument, or are the user name of a URL other than the `git` SSH user.

Discovery and the harness scaffold also redact a credential-like path segment of a URL, which the general manifest scan does not. This applies to the `url` of an MCP server, to every `http` or `https` URL inside a command argument or an environment value, and to a plugin marketplace source. After percent-decoding, a path segment becomes a `${SANDCAST_SECRET_N}` placeholder when it is a UUID, or a token of at least twenty base64, base64url or hex characters whose letters and digits interleave, as hosted MCP servers issue in their paths. A forty-character hexadecimal segment counts as well, because it has the shape of a classic GitHub token and of many hex API keys, even though it is also the form of a git commit name. The placeholder stands for the segment exactly as written, percent-encoding included, so the runtime value restores the URL.

A placeholder stays in the manifest exactly as discovery returned it:

```yaml
mcp_servers:
  - name: docs
    command: [npx, -y, "@example/docs-mcp"]
    env:
      DOCS_API_KEY: "${SANDCAST_SECRET_1}"
```

The build writes the server into the image as `"DOCS_API_KEY": "${DOCS_API_KEY}"`: a reference to the variable the placeholder was assigned to, never a value. A placeholder inside a command argument or a URL stays as it is and refers to the variable `SANDCAST_SECRET_N`; discovery reports that name as `env_var`.

At runtime, supply the real value as a credential of an OpenShell provider attached to the sandbox, under the variable discovery reported (`DOCS_API_KEY` here). Create the provider with `openshell provider create --credential <ENV_VAR>`, which reads the value from your environment so that it never appears on a command line or in a file, and attach it with `openshell sandbox create --provider <name>`. For a URL server, the profile that the build generates for it (`mcp-<name>`, see [harness MCP servers](profiles.md#harness-mcp-servers)) already declares the variable as its credential. The [profiles guide](profiles.md#creating-providers-and-starting-a-sandbox) shows the full sequence.

When `/sandcast-capture` hits a credential finding, it removes the line or keeps the placeholder. It never splits, encodes or otherwise disguises a value to get it past the check.

## Baking harness extensions

When you select a harness, the image gets the agent, its configuration for non-interactive use, and the MCP servers, plugins and skills you chose. This section describes the Claude Code adapter, the only one in this release.

### What discovery finds

`sandcast harness discover claude-code` reads the Claude Code configuration on the host. It never changes it.

| Kind | Read from | Notes |
|------|-----------|-------|
| MCP servers | `~/.claude.json` (user servers and the entry for this project) and `.mcp.json` in the project | When a name repeats, Claude Code's precedence applies: the project entry in `~/.claude.json` wins over `.mcp.json`, which wins over the user servers. The shadowed entries are reported as notes. |
| Plugins | `~/.claude/plugins/installed_plugins.json` | Plugins installed for the user or for this project, as `name@marketplace`, with the marketplace source from `known_marketplaces.json`. Plugins disabled in `~/.claude/settings.json` are skipped with a note. |
| Skills | `~/.claude/skills/` and `.claude/skills/` in the project | Every directory with a `SKILL.md`. A project skill shadows a user skill with the same name. sandcast's own `sandcast-*` skills are never listed. |

A stdio server becomes a `command` entry and an HTTP or SSE server becomes a `url` entry. The manifest has no header field, so a URL server keeps only its bearer `Authorization` header, as one `env` entry. When the header references a variable, the entry keeps that variable. When it holds a literal token, the entry key becomes `MCP_<NAME>_TOKEN` and its value becomes a placeholder; the token itself is never written. Other headers and other transports are not baked, and each gets a note.

### What the image gets

The `70-harness` section of the Containerfile sets up Claude Code after the agent itself has been installed:

- **Configuration outside the home directory.** The configuration lives in `/opt/sandcast/claude-code`, and `CLAUDE_CONFIG_DIR` points Claude Code at it. The final user's home can be `/sandbox`, which the OpenShell Kubernetes driver masks with the workspace volume, so anything written there at build time would disappear. The directory belongs to the final user and is read-write in the image policy.
- **Non-interactive settings.** `settings.json` sets `DISABLE_AUTOUPDATER`, `DISABLE_ERROR_REPORTING` and `DISABLE_TELEMETRY` to `1`, and `.claude.json` marks onboarding as complete and lists the MCP servers. When the base image already has these files, sandcast merges into them with `jq` and keeps their arrays and hooks.
- **MCP servers.** Each server is written with references, never values. See [Secrets](#secrets) for how the variables reach the agent at runtime.
- **Skills.** Each selected skill directory is copied from the host into the build context and from there to `/opt/sandcast/claude-code/skills/<name>/`.
- **Plugins.** When the manifest lists plugins, the section adds the official marketplace (`anthropics/claude-plugins-official`), then every other marketplace the plugins come from, and installs each plugin with `claude plugin install <name> --scope user`. A marketplace on the host is copied to `/opt/sandcast/claude-code-marketplaces/<name>/` (read-only in the image policy) and added from there. Plugin installs clone their marketplaces, so sandcast adds the `git` component implicitly.

For example, a URL server with a placeholder:

```yaml
mcp_servers:
  - name: docs
    url: https://docs.example.com/sse
    env:
      DOCS_TOKEN: "${SANDCAST_SECRET_2}"
```

ends up in the image's `.claude.json` as:

```json
"docs": {
  "type": "sse",
  "url": "https://docs.example.com/sse",
  "headers": {
    "Authorization": "Bearer ${DOCS_TOKEN}"
  }
}
```

The harness can also add network profiles. When a URL server's endpoint is not covered by a profile the agent component references, the build generates a project-local profile `mcp-<name>` bound to the agent's binary. The [profiles guide](profiles.md#harness-mcp-servers) describes these profiles and how to supply their credentials, and its [smoke test](profiles.md#smoke-test) shows how to check the result in a sandbox.
