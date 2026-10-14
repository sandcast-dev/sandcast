package shell

import "strings"

// The shell hooks close the startup-file vector for shells that a runtime
// starts directly. OpenShell v0.1.x runs /bin/bash (or /bin/sh when the image
// has no bash) with -i under a pty, with -lc or -c for a command, and as -l
// for its default agent, always after clearing the environment. It never uses
// the image CMD, the passwd shell or the image ENV, so neither the launcher
// nor the relocation variables reach these shells. bash reads two kinds of
// startup files before any file in the home: the system bashrc
// (/etc/bash.bashrc on Debian and Ubuntu) for interactive non-login shells,
// and /etc/profile, which sources /etc/profile.d/*.sh, for login shells. The
// hooks live there and replace the shell before bash reaches the workspace
// ~/.bashrc, ~/.bash_profile, ~/.profile or ~/.bash_logout (spec 003 FR-001,
// FR-002, SC-002).

// HookBegin and HookEnd delimit the block sandcast prepends to the system
// bashrc, so the build can recognize an image that already carries it.
const (
	HookBegin = "# >>> sandcast shell hook >>>"
	HookEnd   = "# <<< sandcast shell hook <<<"
)

// Export is one relocation variable the hooks export. The runtime cleared the
// image ENV, so the hooks restore the variables the plan relocates.
type Export struct {
	Name, Value string
}

// HookTargets are the commands an interactive bash is replaced with, each
// rendered as shell words that the hook passes further arguments to.
type HookTargets struct {
	// Top is the curated interactive shell for an interactive non-login bash
	// that does not run inside a curated session: the shell OpenShell opens.
	Top string
	// TopLogin is the curated shell for an interactive login bash outside a
	// curated session (the OpenShell default agent under a pty).
	TopLogin string
	// Nested is the shell for an interactive bash that the user starts inside
	// a curated session (SANDCAST_SESSION is set). It keeps bash, but never
	// reads a startup file from the home.
	Nested string
}

// SessionVar marks a process tree that runs inside a curated shell session.
// The launchers, the global zshenv and the hooks export it; OpenShell clears
// it, which is how the hooks tell its top-level shell from a nested one.
const SessionVar = "SANDCAST_SESSION"

// GuardVar holds the pid of the bash the launcher started. That bash reads
// the system bashrc too; the hook recognizes it by the pid, unsets the guard
// so no child inherits it, and lets it continue to the baked rcfile.
const GuardVar = "SANDCAST_SHELL_PID"

func exportLines(exports []Export, indent string) string {
	var b strings.Builder
	for _, e := range exports {
		b.WriteString(indent + "export " + e.Name + "=" + QuoteIfNeeded(e.Value) + "\n")
	}
	return b.String()
}

// interactiveExec renders the branch that replaces an interactive bash with
// target, keeping a -c command string and its arguments ($0 "$@").
func interactiveExec(target, indent string) string {
	return indent + `if [ -n "${BASH_EXECUTION_STRING+x}" ]; then exec ` + target + ` -i -c "$BASH_EXECUTION_STRING" "$0" "$@"; fi` + "\n" +
		indent + "exec " + target + " -i\n"
}

// redirect renders the interactive branch: a bash inside a curated session
// goes to nested, any other to top, which marks the new session first. When
// both are the same command the session check is left out.
func redirect(top, nested, indent string) string {
	var b strings.Builder
	if nested != top {
		b.WriteString(indent + `if [ -n "${` + SessionVar + `-}" ]; then` + "\n")
		b.WriteString(interactiveExec(nested, indent+"  "))
		b.WriteString(indent + "fi\n")
	}
	b.WriteString(indent + SessionVar + "=1\n")
	b.WriteString(indent + "export " + SessionVar + "\n")
	b.WriteString(interactiveExec(top, indent))
	return b.String()
}

// plainBash replaces a non-interactive bash with one that reads no startup
// file at all, keeping the -c command string or the script and its
// arguments, bash's posix mode, the pid and the exit status.
func plainBash(indent string) string {
	return indent + "_sandcast_posix=\n" +
		indent + "case :${SHELLOPTS-}: in *:posix:*) _sandcast_posix=--posix ;; esac\n" +
		indent + `if [ -n "${BASH_EXECUTION_STRING+x}" ]; then exec "$BASH" $_sandcast_posix --noprofile --norc -c "$BASH_EXECUTION_STRING" "$0" "$@"; fi` + "\n"
}

// BashrcHook returns the block sandcast prepends to the system bashrc. For
// an interactive non-login bash it exports the relocation variables and execs
// the curated shell, so ~/.bashrc is never read; the bash the launcher starts
// is recognized by GuardVar and continues normally. A non-interactive bash
// reads the system bashrc only when sshd or rshd starts it with -c; that one
// is replaced by a bash without startup files. Login shells are left to the
// profile hook, because /etc/profile sources the system bashrc before
// /etc/profile.d. The block is prepended rather than appended because the
// Debian file returns early for non-interactive shells.
func BashrcHook(exports []Export, t HookTargets) string {
	return HookBegin + "\n" +
		"# Written by sandcast: the home is the mounted workspace, so an\n" +
		"# interactive bash must never read a .bashrc there. Replace it with the\n" +
		"# curated shell before bash gets to the home file.\n" +
		`if [ -n "${BASH_VERSION-}" ]; then` + "\n" +
		`  if [ "${` + GuardVar + `-}" = "$$" ]; then` + "\n" +
		"    unset " + GuardVar + "\n" +
		"  elif ! shopt -q login_shell; then\n" +
		"    case $- in\n" +
		"    *i*)\n" +
		exportLines(exports, "      ") +
		redirect(t.Top, t.Nested, "      ") +
		"      ;;\n" +
		"    *)\n" +
		plainBash("      ") +
		"      ;;\n" +
		"    esac\n" +
		"  fi\n" +
		"fi\n" +
		HookEnd + "\n"
}

// LoginHook returns /etc/profile.d/zz-sandcast-shell.sh. /etc/profile sources
// it last (the zz- prefix) for every login shell, before the shell reads a
// profile file from the home. It exports the relocation variables and then:
//
//   - bash, interactive: execs the curated shell (TopLogin, or Nested inside a
//     curated session), which is not a login shell, so neither ~/.bash_profile
//     nor ~/.bash_logout is read.
//   - bash, not interactive (bash -lc 'cmd', bash -l script, bash -l reading
//     stdin): execs a bash with no startup files that runs the same command,
//     script or input, with the same pid and exit status. Returning instead
//     would let bash read ~/.bash_profile next and ~/.bash_logout on exit.
//   - other POSIX shells (dash, busybox ash, ksh): they read $HOME/.profile
//     next and expand $HOME at that moment, and they have no logout file, so
//     HOME points at redirectHome until the baked .profile there restores it.
//   - zsh (sourcing /etc/profile in sh emulation) only gets the exports; its
//     startup files follow ZDOTDIR.
//
// A bash that sources /etc/profile by hand is not a login shell and is left
// alone.
func LoginHook(exports []Export, t HookTargets, redirectHome string) string {
	return "# Written by sandcast: the home is the mounted workspace, so a login\n" +
		"# shell must never read a profile or logout file there. See\n" +
		"# docs/guides/build.md, the home model and shell initialization.\n" +
		exportLines(exports, "") +
		`if [ -n "${BASH_VERSION-}" ]; then` + "\n" +
		"  if shopt -q login_shell 2>/dev/null; then\n" +
		"    case $- in\n" +
		"    *i*)\n" +
		redirect(t.TopLogin, t.Nested, "      ") +
		"      ;;\n" +
		"    *)\n" +
		plainBash("      ") +
		"      case ${0##*/} in\n" +
		`      bash | -bash | sh | -sh) exec "$BASH" $_sandcast_posix --noprofile --norc -s "$@" ;;` + "\n" +
		"      esac\n" +
		`      exec "$BASH" $_sandcast_posix --noprofile --norc "$0" "$@"` + "\n" +
		"      ;;\n" +
		"    esac\n" +
		"  fi\n" +
		`elif [ -z "${ZSH_VERSION-}" ] && [ -n "${HOME-}" ] && [ "$HOME" != ` + QuoteIfNeeded(redirectHome) + ` ]; then` + "\n" +
		"  SANDCAST_HOME=$HOME\n" +
		"  HOME=" + QuoteIfNeeded(redirectHome) + "\n" +
		"fi\n"
}

// RedirectHomeProfile returns the .profile baked in the redirect home. A
// POSIX login shell reads it in place of the workspace ~/.profile and it
// restores HOME, so the session sees its real home again.
func RedirectHomeProfile() string {
	return "# Written by sandcast: the login hook pointed HOME here so the shell\n" +
		"# reads this file instead of a .profile in the mounted workspace.\n" +
		"# Restore the real home.\n" +
		`if [ -n "${SANDCAST_HOME-}" ]; then` + "\n" +
		"  HOME=$SANDCAST_HOME\n" +
		"  unset SANDCAST_HOME\n" +
		"fi\n"
}
