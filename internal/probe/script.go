package probe

import (
	"fmt"
	"strings"
	"time"

	"github.com/rhuss/sandcast/internal/shell"
)

// MaxOutput is the number of characters of check output the probe script
// keeps. It must be a power of two because the script builds its cut
// pattern by doubling.
const MaxOutput = 2048

// ScriptOptions selects the sections of a probe script.
type ScriptOptions struct {
	// Facts adds the os, pkgmgr, user and shells sections. Without it the
	// script only runs the checks (a partial probe).
	Facts bool
}

// CheckTimeout bounds one component check. The probe script enforces it
// with timeout(1) when the image has a working one; the container deadline
// (runDeadline) is the backstop for images without it.
const CheckTimeout = 120 * time.Second

// scriptHelpers defines the shared variables and functions on top of
// shell.JSONHelpers (_jsafe). _check runs one check command in a child shell
// with stdin closed, captures stdout and stderr, cuts the output to $_max
// characters and prints one check line. When the image has a timeout(1)
// that accepts "timeout SECS CMD" (coreutils, current busybox), every check
// runs under it with $_tmax seconds; exit 124 (coreutils) or 143 (busybox
// kills with SIGTERM) then marks the line "timeout":true.
//
// When the image has head(1) with -c and cat(1), the output passes through
// "head -c $_max" before it reaches a shell variable, so a check that prints
// megabytes costs neither memory nor time. cat drains the rest, so the check
// is not stopped by SIGPIPE and keeps its exit status. That status leaves
// the pipeline on fd 3 of an inner command substitution, while head writes
// to fd 4, the outer one; the inner one ends only after head and cat have
// finished, so the status is appended after the whole output and split off
// at the last line break. The check itself gets neither fd 3 nor fd 4.
// Without head or cat the output is captured whole, as before. Apart from
// timeout(1), head(1) and cat(1) only shell builtins are used, so the script
// runs in images without coreutils.
const scriptHelpers = shell.JSONHelpers + `_cut='?'
while [ ${#_cut} -lt "$_max" ]; do _cut=$_cut$_cut; done
_to=
if timeout 10 /bin/sh -c : >/dev/null 2>&1; then _to=1; fi
_hd=
if [ "$(printf abc | head -c 2 2>/dev/null)" = ab ] && command -v cat >/dev/null 2>&1; then _hd=1; fi
_run() {
	if [ -n "$_to" ]; then
		timeout "$_tmax" /bin/sh -c "$1" 2>&1 </dev/null 3>&- 4>&-
	else
		/bin/sh -c "$1" 2>&1 </dev/null 3>&- 4>&-
	fi
}
_check() {
	_p=
	if [ -n "$2" ]; then _p=$(command -v "$2" 2>/dev/null) || _p=; fi
	if [ -n "$_hd" ]; then
		_o=$( { _st=$( { { _run "$3"; echo "$?" >&3; } | { head -c "$_max"; cat >/dev/null; } >&4; } 3>&1 ); printf '\n%s' "$_st"; } 4>&1 )
		_e=${_o##*"$_nl"}
		_o=${_o%"$_nl"*}
		while :; do
			case $_o in *"$_nl") _o=${_o%"$_nl"} ;; *) break ;; esac
		done
	else
		_o=$(_run "$3")
		_e=$?
	fi
	case $_e in '' | *[!0-9]*) _e=-1 ;; esac
	_x=false
	if [ -n "$_to" ]; then
		case $_e in 124 | 143) _x=true ;; esac
	fi
	if [ ${#_o} -gt "$_max" ]; then
		_t=${_o#$_cut}
		if [ "$_t" != "$_o" ]; then _o=${_o%"$_t"}; fi
	fi
	printf '{"type":"check","key":"%s","exit":%d,"path":"%s","out":"%s","timeout":%s}\n' \
		"$(_jsafe "$1")" "$_e" "$(_jsafe "$_p")" "$(_jsafe "$_o")" "$_x"
}
`

// scriptFacts prints the os, pkgmgr, user and shells lines. The passwd entry
// of the current uid comes from getent when available, else from
// /etc/passwd; it supplies the user name when id -un fails (an arbitrary
// uid), the home directory when HOME is unset, and the login shell.
const scriptFacts = `_os_id= _os_id_like= _os_name= _os_version=
_osr=$_sysroot/etc/os-release
[ -r "$_osr" ] || _osr=$_sysroot/usr/lib/os-release
if [ -r "$_osr" ]; then
	while IFS='=' read -r _k _v || [ -n "$_k" ]; do
		case $_v in
		\"*\") _v=${_v#\"}; _v=${_v%\"} ;;
		\'*\') _v=${_v#\'}; _v=${_v%\'} ;;
		esac
		case $_k in
		ID) _os_id=$_v ;;
		ID_LIKE) _os_id_like=$_v ;;
		NAME) _os_name=$_v ;;
		VERSION_ID) _os_version=$_v ;;
		esac
	done < "$_osr"
fi
printf '{"type":"os","id":"%s","id_like":"%s","name":"%s","version":"%s"}\n' \
	"$(_jsafe "$_os_id")" "$(_jsafe "$_os_id_like")" "$(_jsafe "$_os_name")" "$(_jsafe "$_os_version")"
_pm=none
for _x in apt-get dnf yum apk; do
	if command -v "$_x" >/dev/null 2>&1; then _pm=$_x; break; fi
done
printf '{"type":"pkgmgr","name":"%s"}\n' "$(_jsafe "$_pm")"
_uname=$(id -un 2>/dev/null) || _uname=
_uid=$(id -u 2>/dev/null) || _uid=
_gid=$(id -g 2>/dev/null) || _gid=
_gname=$(id -gn 2>/dev/null) || _gname=
case $_uid in '' | *[!0-9]*) _uid=-1 ;; esac
case $_gid in '' | *[!0-9]*) _gid=-1 ;; esac
_pwset() { _pw_name=$1 _pw_uid=$3 _pw_home=$6 _pw_shell=$7; }
_pwent() {
	_oifs=$IFS
	IFS=:
	set -f
	_pwset $1
	set +f
	IFS=$_oifs
}
_pw_name= _pw_uid= _pw_home= _pw_shell=
if [ "$_uid" != -1 ]; then
	if command -v getent >/dev/null 2>&1; then
		_line=$(getent passwd "$_uid" 2>/dev/null) || _line=
		_pwent "${_line%%"$_nl"*}"
	fi
	if [ "$_pw_uid" != "$_uid" ] && [ -r "$_sysroot/etc/passwd" ]; then
		while IFS= read -r _line || [ -n "$_line" ]; do
			_pwent "$_line"
			if [ "$_pw_uid" = "$_uid" ]; then break; fi
		done < "$_sysroot/etc/passwd"
	fi
	if [ "$_pw_uid" != "$_uid" ]; then _pw_name= _pw_home= _pw_shell=; fi
fi
[ -n "$_uname" ] || _uname=$_pw_name
_home=${HOME:-$_pw_home}
printf '{"type":"user","name":"%s","uid":%s,"gid":%s,"group":"%s","home":"%s","shell":"%s"}\n' \
	"$(_jsafe "$_uname")" "$_uid" "$_gid" "$(_jsafe "$_gname")" "$(_jsafe "$_home")" "$(_jsafe "$_pw_shell")"
_shells=
if [ -r "$_sysroot/etc/shells" ]; then
	while IFS= read -r _line || [ -n "$_line" ]; do
		case $_line in '' | '#'*) continue ;; esac
		if [ -f "$_line" ]; then _shells="$_shells\"$(_jsafe "$_line")\","; fi
	done < "$_sysroot/etc/shells"
fi
printf '{"type":"shells","list":[%s"/bin/sh"]}\n' "$_shells"
`

// Script returns the POSIX sh probe script for reqs. It prints one JSON
// object per line: with opts.Facts the os, pkgmgr, user and shells lines,
// then one check line per request in the order of reqs. Every request value
// is passed to the script as a single-quoted shell word, so check commands
// may contain any character except NUL. The script always exits 0; a failing
// check only shows in its exit field.
func Script(reqs []Request, opts ScriptOptions) string {
	var b strings.Builder
	b.WriteString("# sandcast probe script\n")
	// _sysroot prefixes the files the facts sections read; tests point it
	// at a fake tree.
	b.WriteString("_sysroot=\n")
	fmt.Fprintf(&b, "_max=%d\n", MaxOutput)
	fmt.Fprintf(&b, "_tmax=%d\n", int(CheckTimeout/time.Second))
	b.WriteString(scriptHelpers)
	if opts.Facts {
		b.WriteString(scriptFacts)
	}
	for _, r := range reqs {
		fmt.Fprintf(&b, "_check %s %s %s\n", shell.Quote(r.Key), shell.Quote(r.Bin), shell.Quote(r.Command))
	}
	b.WriteString("exit 0\n")
	return b.String()
}
