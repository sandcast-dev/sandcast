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

// ZshEnvExport returns the single line sandcast adds to /etc/zshenv to
// relocate zsh's startup files out of the overmounted home. zsh reads
// /etc/zshenv first and unconditionally, before any $HOME file, so setting
// ZDOTDIR there means zsh never sources a workspace $HOME/.zshrc (spec 003
// research R1, FR-002). The caller quotes the line for the Containerfile.
func ZshEnvExport(zdotdir string) string {
	return "export ZDOTDIR=" + zdotdir
}

// LoginLauncher returns the /opt/sandcast/shell-login script: a POSIX sh
// launcher that execs bash with neither profile nor rc files and an explicit
// baked rcfile, so no $HOME startup file in the mounted workspace is ever
// sourced (spec 003 research R1, FR-002). It is the created user's login
// shell and the image CMD for the bash case; "$@" forwards any arguments (for
// example a -c command) to bash.
func LoginLauncher(rcfile string) string {
	return "#!/bin/sh\n" +
		"# Written by sandcast: bash under the OpenShell home overmount must never\n" +
		"# read a startup file from the workspace home. Launch bash with no profile\n" +
		"# and no rc, and an explicit baked rcfile.\n" +
		"exec bash --noprofile --norc --rcfile " + QuoteIfNeeded(rcfile) + ` "$@"` + "\n"
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
