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
