# Draft: upstream OpenShell issue (not filed)

Status: draft for review. Do not file without the user's approval of the text (spec Assumptions, research R-20).
Target repository: NVIDIA/OpenShell.
Basis: OpenShell v0.1.3 (commit e1f3c82c).

---

**Title:** Interactive sessions should honor the image's login shell instead of always starting bash

**Summary**

`openshell sandbox connect` and `openshell sandbox exec --tty` always start bash (or `/bin/sh` when bash is missing), whatever the sandbox image configures as the user's login shell. An image whose user has `/bin/zsh` in `/etc/passwd`, or a sandbox spec that sets `SHELL=/bin/zsh`, still opens a bash session. Image builders who want zsh for interactive sessions have to add a hook to the system bash rc that replaces the interactive bash with zsh, which is fragile and surprising.

**Current behavior (v0.1.3)**

- `crates/openshell-core/src/shell.rs`: `find_login_shell()` (and `detect_login_shell()`) return the first executable of `SHELL_CANDIDATES` (`/bin/bash`, `/usr/bin/bash`, `/bin/sh`). The doc comment explains that `$SHELL` is intentionally not consulted, because the result is later run with `-lc` and an incompatible executable such as `/bin/false` would break command execution.
- `crates/openshell-sandbox/src/boundary_exec.rs`, `command()` (around line 124): for a shell exec spec the program is `find_login_shell()`, with `-i` for an interactive PTY session and `-lc <command>` (or `-c`) for a command. The environment is cleared (`env_clear()`, around line 161) and set to `HOME`, `USER`, `PATH`, `TERM`, `SHELL` (again the detected bash) and the sandbox spec variables.
- `crates/openshell-sandbox/src/process.rs`, `session_user_and_home()`: the session user and home come from the policy's `run_as_user` and its passwd entry, so the passwd entry is already read, but its shell field is not.

**Requested behavior**

For interactive sessions without a command (`sandbox connect`, `sandbox exec --tty` without arguments), start the session user's login shell from the passwd entry, as a login shell, when it is an executable listed in `/etc/shells`; otherwise fall back to the current candidates. Optionally accept `SHELL` from the sandbox spec under the same check.

Command execution (`-lc <command>`) can keep using the POSIX-compatible candidates, so the `SHELL=/bin/false` concern from the doc comment does not apply to it.

**Why**

- Images built for developers (for example with sandcast, which captures a user's curated zsh configuration into the image) set the final user's login shell to zsh. Today that setting has no effect in OpenShell sessions.
- The workaround is a hook in `/etc/bash.bashrc` that runs `exec /bin/zsh -l` for interactive terminal sessions. It must carefully avoid non-interactive shells, it runs bash startup files first, and it cannot be turned off without an extra environment variable.
- The passwd entry is already read for `HOME` in `session_user_and_home()`, and `/etc/shells` gives a standard allow list that addresses the safety concern.

**Compatibility**

Images whose user has `/bin/bash` (or no passwd shell) behave as today. Images that set another login shell get it only for interactive sessions without a command. An image that lists a non-shell in `/etc/shells` would be misconfigured for ordinary logins as well.

**Workaround in use**

sandcast images with `shell.program: zsh` carry this hook in the system bash rc, plus `/bin/zsh` as the user's login shell so that a release honoring the login shell needs no hook:

```sh
case $- in *i*) [ -t 0 ] && [ -t 1 ] && [ -z "$ZSH_VERSION" ] && [ -z "$SANDCAST_NO_SHELL_SWITCH" ] && [ -x /bin/zsh ] && exec /bin/zsh -l ;; esac
```
