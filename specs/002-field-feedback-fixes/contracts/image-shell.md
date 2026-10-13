# Image Session Environment Contract

Files an OpenShell-target image carries so that sessions started with a cleared environment get the tools and the curated shell (FR-006 to FR-009). Paths follow the base image's layout as detected today (`/etc/bash.bashrc` or `/etc/bashrc`; `/etc/zsh/zshenv` or `/etc/zshenv`).

## `/etc/profile.d/sandcast-env.sh` (all images with mise components)

POSIX sh, idempotent, safe to source more than once. Replaces `/etc/profile.d/sandcast-mise.sh`.

Responsibilities:

1. Export every `MISE_*` variable the image `ENV` sets, with the same values.
2. Define `_sandcast_path`, which prepends `/opt/mise/shims` to `PATH` unless it is already the first element.
3. Call `_sandcast_path`.
4. In bash only: append `_sandcast_path` to the `PROMPT_COMMAND` array, and in a login non-interactive bash set the self-removing `DEBUG` trap (R-05, subject to the phase 1 spike).

## System bash rc (appended block)

```sh
# sandcast: session environment
[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh
```

With `shell.program: zsh`, followed by:

```sh
# sandcast: switch interactive terminal sessions to zsh
case $- in *i*) [ -t 0 ] && [ -t 1 ] && [ -z "$ZSH_VERSION" ] && [ -z "$SANDCAST_NO_SHELL_SWITCH" ] && [ -x /bin/zsh ] && exec /bin/zsh -l ;; esac
```

## System zshenv (appended block, `shell.program: zsh` or zsh present)

```sh
# sandcast: session environment
[ -r /etc/profile.d/sandcast-env.sh ] && . /etc/profile.d/sandcast-env.sh
zsh-newuser-install() { :; }
```

## Curated rc

Unchanged location (`/etc/zsh/zshrc.d/sandcast.zsh` or `/etc/profile.d/sandcast.sh`); gains a first line `_sandcast_path 2>/dev/null || true`.

## Final user

With `shell.program: zsh`, the final user's login shell is `/bin/zsh`, for created and kept users.

## Invariants (checked by verify, FR-010)

- `env -i HOME=<home> USER=<user> TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin bash -lc 'command -v <bin>'` resolves every component binary.
- The same with `bash -i` reading the check from stdin resolves every component binary.
- With zsh: `zsh -i -c 'command -v <bin>'` resolves every binary and `zsh -i -c exit` writes nothing to stderr.
