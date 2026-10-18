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

var safeAbsPathRe = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)

// SafeAbsPath reports whether p is a clean absolute path whose segments
// consist only of letters, digits, '.', '_' and '-', none of them "." or
// "..". Such a path is inert as a shell word and in a Containerfile
// instruction, so it can be written into either unquoted, and it cannot
// escape the tree it names. "/" itself is not accepted.
func SafeAbsPath(p string) bool {
	if !safeAbsPathRe.MatchString(p) {
		return false
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// ZshProgram is the zsh of the images sandcast builds: the zsh component
// installs the distribution package, which puts it there.
const ZshProgram = "/bin/zsh"

// ZshEnvExport returns the export line of the global zshenv block
// (ZshEnvBlock) that relocates zsh's startup files out of the overmounted
// home. zsh reads its global zshenv first and unconditionally, before any
// $HOME file, so setting ZDOTDIR there means zsh never sources a workspace
// $HOME/.zshrc, even when the image ENV was cleared (spec 003 research R1,
// FR-002).
//
// The line also exports SessionVar, so a bash the user starts from a curated
// zsh session is recognized as nested by the bashrc hook (see BashrcHook).
// With ownShell it exports SHELL as well: OpenShell sets SHELL=/bin/bash for
// its sessions, so a program that starts $SHELL from the curated zsh (tmux,
// an editor's terminal) would otherwise get a login bash that the hooks
// turn into the nested bare bash; the curated zsh owns SHELL instead. An
// image whose curated shell is not zsh keeps the launcher as $SHELL, so a
// zsh the user starts there relocates its startup files and marks the
// session, nothing more.
func ZshEnvExport(zdotdir string, ownShell bool) string {
	line := "export ZDOTDIR=" + zdotdir + " " + SessionVar + "=1"
	if ownShell {
		line += " SHELL=" + ZshProgram
	}
	return line
}

// ZshEnvMode selects what the global zshenv block does for zsh's startup
// files, besides the session environment and the disabled new-user wizard
// that every block carries.
type ZshEnvMode int

const (
	// ZshEnvSessionOnly writes no export: a container image whose curated
	// shell is not zsh, where the home is never overmounted, so a zsh keeps
	// reading the image home's own startup files.
	ZshEnvSessionOnly ZshEnvMode = iota
	// ZshEnvRelocate exports ZDOTDIR and the session mark: an OpenShell
	// image whose curated shell is not zsh, where a zsh the user starts
	// must never read a workspace .zshrc; $SHELL stays the launcher.
	ZshEnvRelocate
	// ZshEnvCurated exports ZDOTDIR, the session mark and SHELL: the zsh
	// kind, on both targets.
	ZshEnvCurated
)

// ZshEnvBegin and ZshEnvEnd delimit the block sandcast writes to the global
// zshenv files (/etc/zsh/zshenv on Debian and Ubuntu, /etc/zshenv
// elsewhere), so a build on an image that already carries one replaces it.
const (
	ZshEnvBegin = "# >>> sandcast zshenv >>>"
	ZshEnvEnd   = "# <<< sandcast zshenv <<<"
)

// ZshEnvBlock returns the block sandcast appends to the global zshenv: the
// export line the mode selects (ZshEnvExport, none for ZshEnvSessionOnly),
// the session environment (spec 002: every zsh gets the MISE_* settings, the
// component environment and the shims first on PATH, also when the runtime
// cleared the environment; sessionEnv is the script path, "" for none) and
// a no-op zsh-newuser-install, so zsh never starts its new-user wizard in a
// home without .zshrc (an empty workspace volume).
func ZshEnvBlock(zdotdir, sessionEnv string, mode ZshEnvMode) string {
	b := ZshEnvBegin + "\n"
	switch mode {
	case ZshEnvRelocate:
		b += ZshEnvExport(zdotdir, false) + "\n"
	case ZshEnvCurated:
		b += ZshEnvExport(zdotdir, true) + "\n"
	}
	return b +
		SessionEnvSource(sessionEnv) +
		"zsh-newuser-install() { :; }\n" +
		ZshEnvEnd + "\n"
}

// LoginLauncher returns the /opt/sandcast/shell-login script: a POSIX sh
// launcher that execs bash with no profile files and the baked rcfile in place
// of ~/.bashrc, so no $HOME startup file in the mounted workspace is ever
// sourced (spec 003 research R1, FR-002). --noprofile keeps ~/.bash_profile,
// ~/.bash_login and ~/.profile out, and --rcfile replaces ~/.bashrc. It must
// not pass --norc: bash ignores --rcfile whenever --norc is set, which would
// leave the shell unconfigured. It is the user's login shell and the image
// CMD for the bash case, and the target of the shell hooks. "$@" forwards
// the arguments (for example -i or a -c command) without -l and --login: a
// login bash reads no rcfile, so the curated configuration would be lost,
// and an interactive login bash runs the workspace ~/.bash_logout on exit
// (see shOptionFilter).
//
// The system bashrc (/etc/bash.bashrc on Debian and Ubuntu) lies outside the
// home and is still read by an interactive bash; the bashrc hook there
// recognizes the launcher's bash by GuardVar, its own pid, which bash keeps
// across exec, and lets it continue to the rcfile instead of replacing it
// again. The guard is exported only when the bash will be interactive (bash's
// own rule: -i, or neither -c nor a script operand with stdin and stderr on a
// terminal), because only that bash reads the hook that unsets it. A
// non-interactive bash would pass the guard on to every descendant, and an
// exec chain from its command that ends in an interactive bash keeps the pid,
// so that bash would be waved through to the workspace ~/.bashrc. The
// launcher also exports SessionVar (see BashrcHook).
//
// sessionEnv names the session environment script (spec 002
// /etc/profile.d/sandcast-env.sh); the launcher sources it first, so a
// bash it starts for a caller with a cleared environment (su -, a $SHELL
// -c command) carries the MISE_* settings, the component environment and
// the shims first on PATH whether or not that bash reads the system
// bashrc. Empty means none.
func LoginLauncher(rcfile, sessionEnv string) string {
	return "#!/bin/sh\n" +
		"# Written by sandcast: bash under the OpenShell home overmount must never\n" +
		"# read a startup file from the workspace home. Launch bash with no profile\n" +
		"# files and the baked rcfile in place of the home .bashrc.\n" +
		SessionEnvSource(sessionEnv) +
		SessionVar + "=1\n" +
		"export " + SessionVar + "\n" +
		shOptionFilter +
		guardExport +
		"exec bash --noprofile --rcfile " + QuoteIfNeeded(rcfile) + ` "$@"` + "\n"
}

// SessionEnvSource returns the line that sources the session environment
// script at path when it is readable, or "" for an empty path. The script
// is POSIX sh and idempotent, so every entry point sources it: the
// launchers and the hooks before they replace the shell, and the global
// zshenv. It comes before anything that sets _sandcast_ variables, because
// the script's bash part uses and unsets _sandcast_i and _sandcast_k.
func SessionEnvSource(path string) string {
	if path == "" {
		return ""
	}
	p := QuoteIfNeeded(path)
	return "[ -r " + p + " ] && . " + p + "\n"
}

// guardExport is the launcher block that exports GuardVar for an interactive
// bash and removes it (also an inherited value) for any other, using the
// flags shOptionFilter recorded. bash itself decides interactivity the same
// way: -i, or no -c and no operand (unless -s) with stdin and stderr both
// terminals.
const guardExport = `# Export the pid guard only for a bash that will be interactive: only that
# bash reads the system bashrc, whose hook unsets the guard again. Any other
# bash would pass it on to every descendant.
if [ -n "$_sandcast_i" ] || { [ -z "$_sandcast_c" ] && { [ -n "$_sandcast_s" ] || [ -z "$_sandcast_arg" ]; } && [ -t 0 ] && [ -t 2 ]; }; then
  ` + GuardVar + `=$$
  export ` + GuardVar + `
else
  unset ` + GuardVar + `
fi
`

// ShLoginLauncher returns the POSIX sh fallback launcher used when the login
// shell is neither zsh nor bash. A POSIX login shell reads $HOME/.profile, and
// the final user's $HOME is the overmounted workspace, so launching /bin/sh as
// a login shell would auto-source a workspace .profile (spec 003 FR-002,
// SC-002). The launcher is the created user's login shell instead: it points
// $ENV at the baked profile script and execs a NON-login /bin/sh, which reads
// $ENV for interactive shells and never $HOME/.profile. "$@" forwards the
// arguments (for example a -c command), without -l and --login: a caller
// that passes them (an editor's environment probe, su -l, a tmux
// default-command) would otherwise make /bin/sh a login shell that reads the
// workspace .profile. See shOptionFilter. sessionEnv is the session
// environment script, sourced first as in LoginLauncher.
func ShLoginLauncher(profile, sessionEnv string) string {
	return "#!/bin/sh\n" +
		"# Written by sandcast: a login shell under the OpenShell home overmount\n" +
		"# would read a profile file from the workspace home. Point $ENV at the\n" +
		"# baked profile and exec a non-login shell so that workspace file is\n" +
		"# never sourced.\n" +
		SessionEnvSource(sessionEnv) +
		"ENV=" + QuoteIfNeeded(profile) + "\n" +
		SessionVar + "=1\n" +
		"export ENV " + SessionVar + "\n" +
		shOptionFilter +
		`exec /bin/sh "$@"` + "\n"
}

// shOptionFilter rewrites the positional parameters without the login
// options: --login, -l, and an l inside an option cluster (-lc becomes -c,
// -il becomes -i). It scans options the way sh and bash do: up to "--", "-"
// or the first operand (the -c command string or a script name); everything
// from there on, including a "-l" passed as an argument to the command, is
// kept. The argument of -o, +o, -O, +O, --rcfile and --init-file is kept as
// is, and long options other than --login (bash's --noprofile, for one) are
// not touched. While scanning it records whether -i, -c and -s were given
// and whether an operand followed the options (_sandcast_i, _sandcast_c,
// _sandcast_s, _sandcast_arg), which the bash launcher's guard needs. It
// uses only shell builtins.
const shOptionFilter = `# Drop -l and --login (also inside a cluster such as -lc) from the options
# before the command string, script name or "--", so the shell never becomes
# a login shell. Arguments from there on are kept as they are. The scan also
# records -i, -c, -s and whether an operand was given.
_sandcast_opts=1 _sandcast_oarg= _sandcast_i= _sandcast_c= _sandcast_s= _sandcast_arg=
for _sandcast_a do
  shift
  if [ -n "$_sandcast_oarg" ]; then
    _sandcast_oarg=
  elif [ -n "$_sandcast_opts" ]; then
    case $_sandcast_a in
    --login) continue ;;
    -- | -) _sandcast_opts= ;;
    --rcfile | --init-file) _sandcast_oarg=1 ;;
    --*) ;;
    [-+]o | [-+]O) _sandcast_oarg=1 ;;
    -*)
      while :; do
        case $_sandcast_a in
        -*l*) _sandcast_a=${_sandcast_a%%l*}${_sandcast_a#*l} ;;
        *) break ;;
        esac
      done
      case $_sandcast_a in -*i*) _sandcast_i=1 ;; esac
      case $_sandcast_a in -*c*) _sandcast_c=1 ;; esac
      case $_sandcast_a in -*s*) _sandcast_s=1 ;; esac
      case $_sandcast_a in
      -) continue ;;
      *[oO]) _sandcast_oarg=1 ;;
      esac
      ;;
    +*) case $_sandcast_a in *[oO]) _sandcast_oarg=1 ;; esac ;;
    *) _sandcast_opts= _sandcast_arg=1 ;;
    esac
  else
    _sandcast_arg=1
  fi
  set -- "$@" "$_sandcast_a"
done
`

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
