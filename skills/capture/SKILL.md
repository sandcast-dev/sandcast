---
name: sandcast-capture
description: Capture the current project and the host environment (languages, host tools, curated shell configuration, agent harnesses) into a sandcast manifest at .sandcast/manifest.yaml.
sandcast_version: 0.1.1
---

# sandcast capture

You turn the current project directory and the developer's host environment into a sandcast manifest (`.sandcast/manifest.yaml`).
The `sandcast` binary provides every fact you need. Your job is judgment: present what the binary found, let the user decide, curate the shell configuration, and write the result.
The build happens later, through `/sandcast-build` or `sandcast build`.

## Rules

Follow these rules in every step. They are part of the sandcast contract and a reviewer checks them.

1. **Facts come from the binary.** Component keys, match evidence, host tool versions, harness discovery and validation results come from `sandcast ... --json`. Never invent a component key, a version, or a configuration value the binary did not report.
2. **Never handle credentials.** Never ask for, print, copy, or write a secret value (tokens, API keys, passwords, private keys). The binary replaces discovered secrets with `${SANDCAST_SECRET_N}` placeholders. Keep each placeholder exactly as returned and tell the user to supply the real value at runtime as a credential of the OpenShell provider attached to the sandbox.
3. **Write only the manifest.** You create or change `.sandcast/manifest.yaml` through `sandcast init`, `sandcast manifest set-shell` and direct edits, and `.sandcast/mise.lock` through `sandcast lock`. Never write a Containerfile or any other image definition (FR-037), never touch `.sandcast/build/`, `.sandcast/fragments/` or `.sandcast/recipes/`. The one exception: with the user's consent, copy a chosen skill or local marketplace from outside the project into the project (Step 2).
4. **Ask before deciding.** Every component, harness extension and shell change ends up in an image the user runs. Present proposals with their evidence and wait for confirmation.
5. **Run commands from the project root** (or pass `--project <dir>`). Exit codes: 0 success, 1 operation failed, 2 usage or validation error, 3 external tool missing or at the wrong version.
6. **Temporary files live outside the project**: create them with `mktemp`, never under a fixed name in a shared directory, and delete them when the step is done.

## Step 1: Check the skill and binary versions

Run:

```sh
sandcast skills check --harness claude-code
```

- Exit 0: continue.
- Exit 3: the installed skills and the binary disagree. Print the message exactly as the command printed it, tell the user which side to update, and **stop**. The message names it: `run "sandcast skills install --harness claude-code"` when the skill is missing or older than the binary, `update the sandcast binary` when the skill is newer.
- Command not found: tell the user to install the `sandcast` binary first and stop.
- Any other failure: show the output and stop.

## Step 2: Choose the harnesses

Ask the user upfront which agent harnesses the image should include (FR-036).
Show what this host has:

```sh
sandcast harness list --json
```

Each entry has `name`, `component`, `installed` and `path`. Offer exactly the harnesses this list reports, marking which are installed on the host. If the user asks for a harness the list does not report, say that its adapter is not available yet and leave it out. The user may also choose no harness at all.

For **each selected harness only**, run discovery with the harness `name` from the list (for example `claude-code`):

```sh
sandcast harness discover <harness> --json
```

Run it once per selected harness and handle each result with the steps below. If the user selected no harness, skip discovery and continue with Step 3.

The JSON has `harness`, `mcp_servers` (each with `name`, `command` or `url`, `env`, `origin`), `plugins` and `skills` (each with `name`, `source`, `origin`), `redacted` (each with `path`, `kind`, `placeholder`, `env_var`) and `notes`.

1. Present the MCP servers, plugins and skills as three short groups, each item with its origin file. For MCP servers show the environment variable names, not their values.
2. For each non-empty group, ask which items to bake into the image with a multi-select prompt: a checkbox list the user ticks, not a request to type item numbers. Use the harness's multi-select control when it has one (for example, in Claude Code, `AskUserQuestion` with `multiSelect: true`), and fall back to a numbered list only when the harness has no such control. Offer MCP servers, plugins and skills as three separate multi-select prompts so each is chosen independently. Nothing is baked by default: an empty selection bakes none.
3. Check the `source` of every chosen skill and of every chosen plugin whose marketplace source is a path (`./`, `~/` or `/`). The binary copies such sources into the image only from inside the project, `~/.claude/skills` or `~/.claude/plugins`, and reads them on the machine that runs the plan. A source outside the project, such as `~/.claude/skills/review`, makes the manifest host-specific: `sandcast plan` and `sandcast build` fail in CI and on every other machine that lacks the path. For each such skill, explain this and offer to copy it into the project:
   - Ask first. If the user declines, keep the discovered source and say that only this host can build the image.
   - Copy only the skill directory, to `.claude/skills/<name>` in the project, and never overwrite an existing directory. List its files first and leave out every file that holds a credential (`.env` files, private keys such as `*.pem` or `*.key`, files named like token, secret or credentials); if the skill needs such a file, stop and ask. Show symbolic links and ask before copying them.
   - The name filter misses a token inside an ordinary file, such as a script or a `config.json`, and no sandcast command scans the copied files before a commit. So after copying, show the user the list of copied files (paths only, never their contents) and ask them to confirm that none of them holds a credential. Remove every file they are unsure about, or stop and ask.
   - Record `source: .claude/skills/<name>` for the skill. Only after the user has confirmed the file list, remind them to commit the directory; check that `.gitignore` does not exclude it.

   For a local marketplace outside the project, explain the same limit and offer two remedies: copy the marketplace directory into the project (same rules) and use its `./` path as the plugin source, or publish it as a git repository and use `owner/repo` or its URL.
4. For every entry in `redacted`, explain: the value at `path` looked like a credential (`kind`), the binary replaced it with `placeholder`, and the user supplies the real value at runtime as a provider credential under the environment variable named in `env_var` (the `env` key when the whole env value was the secret, `SANDCAST_SECRET_N` for a secret inside a longer value, an argument or a URL, including an access key in a URL path). Never offer to fill in the value.
   MCP servers reached by `url` carry their bearer credential as one `env` entry, either a placeholder such as `MCP_<NAME>_TOKEN: "${SANDCAST_SECRET_N}"` or a reference such as `VAR: "${VAR}"`. Keep both forms unchanged.
5. Show every `notes` entry.

Do not run discovery for harnesses the user did not select. Unselected harnesses leave no trace in the manifest.

## Step 3: Create the manifest and choose the components

If `.sandcast/manifest.yaml` does not exist, ask for the manifest name (kebab-case, default: the directory name), the target (`openshell` by default, or `container`) and the base image. Show the registry entries for the chosen target:

```sh
sandcast base-images list --target <openshell|container> --json
```

Each entry has `target`, `name`, `ref`, `default` and `notes`. Show the notes; they say which user and tools the image has.

Then create the skeleton:

```sh
sandcast init --name <name> --base-image <registry-entry-or-image-ref> --target <openshell|container>
```

If the manifest already exists, read it, tell the user you will update it, and keep its existing entries unless the user removes them.

Now let the binary match the project and the host against the component tiers. Add one `--harness <harness>` per selected harness so the agent components are proposed too; pass no `--harness` flag when the user selected no harness:

```sh
sandcast components match . --host --harness <harness> --json
```

The JSON shape is:

```json
{"project": "<dir>",
 "proposals": [{"key": "go", "name": "Go", "source": "project", "evidence": ["go.mod"], "detected_version": "1.25.1"}],
 "available": [{"key": "rust", "name": "Rust", "tier": "shipped"}]}
```

`source` is one of `project` (ecosystem files), `host` (a tool found on the host `PATH`), `harness` (a selected harness) or `always` (base system components).

1. Present the proposals grouped by source as a table with key, name, evidence and detected version.
2. If no proposal has the source `project`, say explicitly: "No recognizable ecosystem files were found in this project." Then still offer the host tools and the `available` components as optional additions.
3. Offer the `available` components as optional additions.
4. Let the user confirm, drop or add components. Keep `always` proposals unless the user explicitly removes them.
5. Additions must be keys from `proposals`, `available`, or `sandcast components list --json`. If the user wants a tool that no tier defines, explain the two choices: add it with `optional: true` so that `/sandcast-build` offers to write an install fragment for it, or let the user define it in `.sandcast/components/<key>.yaml` first. The user writes that file; you may show the shape from `sandcast components show <similar-key>`, but do not write it yourself.
6. Set `version` on a component only when the user asks for a specific version or the project pins one (for example the `go` directive in `go.mod`, `.python-version`, `.nvmrc`). Otherwise leave it out; the lockfile pins exact versions.

Keep the confirmed list in mind; you write it in Step 5.

## Step 4: Curate the shell configuration

The image gets a curated copy of the user's interactive shell configuration, installed under `/etc` for the final user.
Host configurations carry plugin managers, macOS paths and host-only tools that break inside a Linux sandbox. The rules below encode the failures recorded in earlier image builds (brainstorm 072, changes 7, 11, 12 and 13).

Find the login shell with `basename "$SHELL"`.

- `zsh`: read `${ZDOTDIR:-$HOME}/.zshrc`.
- `bash`: read `~/.bashrc` (or `~/.bash_profile` when there is no `.bashrc`).
- Any other shell: say that sandcast curates only zsh and bash. Ask whether to skip the shell configuration or to write a minimal zsh or bash configuration with the user. If the user skips it, continue with Step 5.

If the file sources other files from the home directory that hold aliases or functions, ask before reading them. Never print a line that sets a credential; refer to it by line number and variable name only.

### 4a: Strip

Remove these lines, and tell the user what you removed and why:

1. **Plugin managers and everything they load**: antidote (`source .../antidote.zsh`, `antidote load`, `antidote bundle`), oh-my-zsh (`export ZSH=...`, `ZSH_THEME=...`, `plugins=(...)`, `source $ZSH/oh-my-zsh.sh`), zinit (`source .../zinit.zsh`, every `zinit ...` line), zplug (`source ~/.zplug/init.zsh`, every `zplug ...` line). Treat antigen, prezto, zgenom, sheldon and znap the same way. For bash: oh-my-bash (`export OSH=...`, `OSH_THEME=...`, the `plugins=(...)`, `completions=(...)` and `aliases=(...)` arrays, `source "$OSH"/oh-my-bash.sh`), bash-it (`export BASH_IT=...`, `BASH_IT_THEME=...`, `source "$BASH_IT"/bash_it.sh`) and ble.sh (`source .../ble.sh`, `ble-attach`). List the plugins that are lost, so the user knows.
2. **Homebrew, Linuxbrew and host paths**: anything under `/opt/homebrew`, `/usr/local/Homebrew`, `/usr/local/opt`, `/usr/local/Cellar`, `/home/linuxbrew/.linuxbrew`, `~/.linuxbrew`, `/Applications`, `/Library`, `~/Library` or `/System`, plus `brew shellenv`, `$(brew --prefix ...)`, `HOMEBREW_*` and `DYLD_*`. In a `PATH` assignment remove only the host entries and keep the portable ones, for example `export PATH="/opt/homebrew/bin:$HOME/.local/bin:$PATH"` becomes `export PATH="$HOME/.local/bin:$PATH"`. Absolute paths into the host home (`/Users/<name>/...`, `/home/<name>/...`): rewrite to `$HOME/...` only when the image provides the target or a missing target is harmless (a `PATH` entry); otherwise drop the line.
3. **Host-only tools and integrations**: macOS commands (`pbcopy`, `pbpaste`, `open`, `osascript`, `defaults`, `security`, `launchctl`), terminal integrations (iTerm2 shell integration and similar), agent setup (`ssh-agent`, `ssh-add`, `gpg-agent`, `keychain`), SDK loaders from host paths, and version managers (`mise activate`, `asdf`, `nvm`, `pyenv init`, `rbenv init`, `conda` init blocks, `sdkman`). The image installs tools with mise and puts them on `PATH` without activation.
4. **Sourced host files** that do not exist in the image (`source ~/.zsh_local`, `source ~/.config/zsh/aliases.zsh`). If the user wants their content, inline the relevant parts after applying these same rules.
5. **Credentials**: every line that exports a token, key, password or secret, and every export whose value comes from a command or file that yields a credential: keychains and password managers (`security find-generic-password`, `op read`, `pass`, `gopass`), CLI token commands (`$(gh auth token)`, `$(aws configure get ...)`), and secret files (`$(cat ~/.secrets/...)`). Do not repeat the value. Tell the user to supply the variable as a provider credential at runtime. `sandcast manifest set-shell` refuses configurations with credential-shaped values.

Keep aliases, functions, `setopt` and `shopt` options, history settings, `bindkey` lines, `zstyle` lines, prompt configuration, and exports of non-secret settings such as `EDITOR`, `PAGER` or `LANG`.

### 4b: Add and rewrite

Apply each rule whenever its trigger is present after stripping:

1. **Completion system (zsh)**: the configuration needs `compinit` when it has no `compinit` call and any of these remain:
   - a `compdef` call or a `zstyle ':completion:*'` line,
   - a sourced or evaluated completion script, such as `source <(<tool> completion zsh)`, `eval "$(<tool> completion zsh)"` or a sourced file that starts with `#compdef` (these scripts call `compdef`),
   - a stripped plugin manager that loaded `compinit`.

   Then insert this line before the first of those lines:

   ```zsh
   autoload -Uz compinit && compinit -i -D
   ```

   Keep both flags and never use `-C`. The home directory can be the workspace (`/sandbox`), which the sandbox and the checked-out project can write. `-D` keeps `compinit` from reading or writing a `.zcompdump` there, because a planted dump file would run as shell code at every shell start. `-i` skips completion directories that fail the ownership check instead of stopping the shell with a prompt.

   Plugin managers load `compinit` transitively. Without it, `zstyle` has no effect, every completion script prints `command not found: compdef` at shell start, and tab completion falls back to plain file globbing.

2. **Starship prompt**: guard the init line so that non-interactive exec sessions with `TERM=dumb` do not print starship errors:

   ```zsh
   if [[ "$TERM" != "dumb" ]]; then
     eval "$(starship init zsh)"
   fi
   ```

   Use `starship init bash` for bash.

3. **fzf key bindings**: replace `source <(fzf --zsh)` (and `eval "$(fzf --zsh)"` or `[ -f ~/.fzf.zsh ] && source ~/.fzf.zsh`) with this version-aware block. `fzf --zsh` needs fzf 0.48 or newer, and distribution packages can be older:

   ```zsh
   if fzf --zsh &>/dev/null; then
     source <(fzf --zsh)
   elif [ -f /usr/share/doc/fzf/examples/key-bindings.zsh ]; then
     source /usr/share/doc/fzf/examples/key-bindings.zsh
     [ -f /usr/share/doc/fzf/examples/completion.zsh ] && source /usr/share/doc/fzf/examples/completion.zsh
   fi
   ```

   For bash, replace `eval "$(fzf --bash)"` (or `~/.fzf.bash`) with:

   ```bash
   if fzf --bash >/dev/null 2>&1; then
     eval "$(fzf --bash)"
   elif [ -f /usr/share/doc/fzf/examples/key-bindings.bash ]; then
     . /usr/share/doc/fzf/examples/key-bindings.bash
   fi
   ```

### 4c: List the referenced commands

List every external command the curated configuration references: the first word of each alias value, every `eval "$(<cmd> ...)"` and `source <(<cmd> ...)`, commands called in functions, and the values of `EDITOR`, `VISUAL`, `PAGER` and `MANPAGER`. Ignore shell builtins and the base utilities every image ships (`ls`, `cat`, `grep`, `sed`, `awk`, `find` and similar).

For each referenced command, find its component with `sandcast components list --json`: a component whose `key` equals the command or whose `match.host_tools` contains it (for example `rg` belongs to `ripgrep`). Confirm with `sandcast components show <key>`; its binaries are `installer.<kind>.bins`, else `provides`, else the key.

- A component exists: add its key to `shell.components`. A missing prompt or listing tool is a runtime failure the build cannot detect, so do not skip this.
- No component exists: ask the user whether to drop the line or to keep it and add the command as an optional component (Step 3, item 5).

Show the user the final curated configuration and the list of referenced components. Apply their corrections.

### 4d: Write it

Write the curated text to a private temporary file created with `mktemp`, store it in the manifest, and delete the file. Run it as one shell command so that `$tmp` survives, and set `--program` to the shell you curated (`zsh` or `bash`):

```sh
tmp="$(mktemp)"
cat > "$tmp" <<'SANDCAST_RC'
<the curated configuration>
SANDCAST_RC
sandcast manifest set-shell --program <zsh|bash> --file "$tmp"; status=$?
rm -f "$tmp"
[ "$status" -eq 0 ]
```

The command also accepts the configuration on standard input. If it exits 2 because of a credential-shaped value, remove that line and write again; never weaken the value to get past the check.
Do not edit `shell.rc` in the manifest by hand; rerun `sandcast manifest set-shell` to change it.

### Example

The host `.zshrc`:

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

The curated configuration:

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

The referenced components are `fzf`, `lsd` and `starship`, so `shell.components` is `[fzf, lsd, starship]`.

## Step 5: Write the components and harnesses, then validate

Edit `.sandcast/manifest.yaml` directly for the parts `sandcast manifest set-shell` does not write. The schema rejects unknown keys everywhere. The shape:

```yaml
schema_version: 1
name: my-project
base_image: nvidia-community-base
components:
  - key: git
  - key: go
  - key: python
    version: "3.13"
  - key: my-internal-tool
    optional: true
harnesses:
  - name: claude-code
    mcp_servers:
      - name: docs
        command: [npx, -y, "@example/docs-mcp"]
        env:
          DOCS_API_KEY: "${SANDCAST_SECRET_1}"
      - name: tracker
        url: https://mcp.example.com/sse
        env:
          MCP_TRACKER_TOKEN: "${SANDCAST_SECRET_2}"
    plugins:
      - name: clangd-lsp@claude-plugins-official
        source: anthropics/claude-plugins-official
    skills:
      - name: review
        source: .claude/skills/review
shell:
  program: zsh
  rc: |
    # written by sandcast manifest set-shell
  components: [fzf, lsd, starship]
targets:
  openshell:
    image: localhost/my-project
```

- `components`: the confirmed list from Step 3. Leave out components already listed in `shell.components`, the shell program itself (`zsh` or `bash`) and the agent component of each harness (`claude-code`); the plan adds those implicitly.
- `harnesses`: one entry per selected harness with only the MCP servers, plugins and skills the user chose. Copy `name`, `command`, `url`, `env`, and `source` exactly as discovery printed them, placeholders included, except the `source` of a skill or marketplace you moved into the project in Step 2. Drop the `origin` fields. Omit `harnesses` entirely when the user selected none.
- `shell.components`: the referenced components from Step 4c.
- `platform`: set it (`linux/amd64` or `linux/arm64`) only when the user builds for a platform other than the host's.

Then validate:

```sh
sandcast manifest validate --json
```

The JSON has `valid`, `manifest` and `diagnostics` (each with `path`, `line`, `message`).
If `valid` is false, show every diagnostic with its line, fix what is clearly yours to fix (a typo in a key, a wrong field name), and validate again. If a diagnostic needs a user decision or reports a credential, stop and show the diagnostics; never write a credential to make validation pass.

## Step 6: Offer the lockfile

Builds install mise tools in locked mode, and `sandcast plan` fails for a platform the lockfile lacks. Offer to generate the lockfile for the target platform (default: the manifest `platform`, else the host architecture). Repeat `--platform` for more than one:

```sh
sandcast lock --platform linux/arm64
```

Exit 3 means the host has no `mise` or one older than the minimum release named in the error message. Report every tool the command could not lock, with its platform and reason.

## Finish

Summarize for the user:

- the components, harness extensions and shell components in the manifest,
- every secret placeholder and how to supply it at runtime,
- what the shell curation removed,
- the next step: `/sandcast-build` (or `sandcast build --verify` when no component is unresolved).

Suggest committing `.sandcast/manifest.yaml` and `.sandcast/mise.lock`.
