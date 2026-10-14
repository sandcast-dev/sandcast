// Package shell holds the POSIX sh building blocks that sandcast scripts run
// inside user images share: word quoting and a JSON string escaper.
package shell

import (
	"regexp"
	"strings"
)

var safeRe = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

// Quote returns s as one single-quoted sh word. A single quote inside s ends
// the quoted part, adds an escaped quote and opens a new quoted part.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteIfNeeded returns s unchanged when it has only shell-safe characters,
// so generated Containerfiles stay readable, and Quote(s) otherwise.
func QuoteIfNeeded(s string) string {
	if safeRe.MatchString(s) {
		return s
	}
	return Quote(s)
}

// ZshEnvExport returns the single line sandcast adds to the global zshenv
// (/etc/zsh/zshenv on Debian and Ubuntu, /etc/zshenv elsewhere) to relocate
// zsh's startup files out of the overmounted home. zsh reads its global zshenv
// first and unconditionally, before any $HOME file, so setting ZDOTDIR there
// means zsh never sources a workspace $HOME/.zshrc, even when the image ENV
// was cleared (spec 003 research R1, FR-002). The caller quotes the line for
// the Containerfile.
//
// The line also exports SessionVar, so a bash the user starts from a curated
// zsh session is recognized as nested by the bashrc hook (see BashrcHook).
func ZshEnvExport(zdotdir string) string {
	return "export ZDOTDIR=" + zdotdir + " " + SessionVar + "=1"
}

// LoginLauncher returns the /opt/sandcast/shell-login script: a POSIX sh
// launcher that execs bash with no profile files and the baked rcfile in place
// of ~/.bashrc, so no $HOME startup file in the mounted workspace is ever
// sourced (spec 003 research R1, FR-002). --noprofile keeps ~/.bash_profile,
// ~/.bash_login and ~/.profile out, and --rcfile replaces ~/.bashrc. It must
// not pass --norc: bash ignores --rcfile whenever --norc is set, which would
// leave the shell unconfigured. The system bashrc (/etc/bash.bashrc on
// Debian and Ubuntu) lies outside the home and is still read; the launcher
// exports GuardVar with its own pid, which bash keeps across exec, so the
// bashrc hook lets this bash continue to the rcfile instead of replacing it
// again. It also exports SessionVar (see BashrcHook). It is the user's login
// shell and the image CMD for the bash case, and the target of the shell
// hooks; "$@" forwards any arguments (for example -i or a -c command) to bash.
func LoginLauncher(rcfile string) string {
	return "#!/bin/sh\n" +
		"# Written by sandcast: bash under the OpenShell home overmount must never\n" +
		"# read a startup file from the workspace home. Launch bash with no profile\n" +
		"# files and the baked rcfile in place of the home .bashrc.\n" +
		GuardVar + "=$$\n" +
		SessionVar + "=1\n" +
		"export " + GuardVar + " " + SessionVar + "\n" +
		"exec bash --noprofile --rcfile " + QuoteIfNeeded(rcfile) + ` "$@"` + "\n"
}

// ShLoginLauncher returns the POSIX sh fallback launcher used when the login
// shell is neither zsh nor bash. A POSIX login shell reads $HOME/.profile, and
// the final user's $HOME is the overmounted workspace, so launching /bin/sh as
// a login shell would auto-source a workspace .profile (spec 003 FR-002,
// SC-002). The launcher is the created user's login shell instead: it points
// $ENV at the baked profile script and execs a NON-login /bin/sh, which reads
// $ENV for interactive shells and never $HOME/.profile. "$@" forwards any
// arguments (for example a -c command).
func ShLoginLauncher(profile string) string {
	return "#!/bin/sh\n" +
		"# Written by sandcast: a login shell under the OpenShell home overmount\n" +
		"# would read a profile file from the workspace home. Point $ENV at the\n" +
		"# baked profile and exec a non-login shell so that workspace file is\n" +
		"# never sourced.\n" +
		"ENV=" + QuoteIfNeeded(profile) + "\n" +
		SessionVar + "=1\n" +
		"export ENV " + SessionVar + "\n" +
		`exec /bin/sh "$@"` + "\n"
}

// JSONHelpers defines _jsafe, which prints its argument escaped for a JSON
// string: backslash, double quote, tab, newline and carriage return get
// their short escapes, other control characters become \u00XX. It also sets
// _nl, _tab and _cr. Only shell builtins are used, so scripts that include
// it run in images without coreutils, sed or tr. _jsafe sets LC_ALL=C and
// uses the globals _s, _r, _t and _c.
const JSONHelpers = `_nl='
'
_tab=$(printf '\t')
_cr=$(printf '\r')
_jsafe() {
	LC_ALL=C
	case $1 in
	*\"* | *\\* | *[[:cntrl:]]*) ;;
	*) printf '%s' "$1"; return 0 ;;
	esac
	_s=$1 _r=
	while [ -n "$_s" ]; do
		_t=${_s#?}
		_c=${_s%"$_t"}
		[ -n "$_c" ] || break
		_s=$_t
		case $_c in
		\\) _r="$_r\\\\" ;;
		\") _r="$_r\\\"" ;;
		"$_tab") _r="$_r\\t" ;;
		"$_nl") _r="$_r\\n" ;;
		"$_cr") _r="$_r\\r" ;;
		[[:cntrl:]]) _r="$_r$(printf '\\u%04x' "'$_c")" ;;
		*) _r="$_r$_c" ;;
		esac
	done
	printf '%s' "$_r"
}
`
