package plan

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/shell"
)

// Output file names inside build/<target>/.
const (
	PlanFile       = "plan.yaml"
	MiseDir        = "mise"
	MiseTOMLFile   = "mise.toml"
	MiseLockCopy   = "mise.lock"
	SessionEnvFile = "sandcast-env.sh"
	PolicyFile     = "policy.yaml"
	ShellDir       = "shell"
)

// SessionEnvPath is where the session environment script lives in the image
// (contracts/image-shell.md).
const SessionEnvPath = "/etc/profile.d/" + SessionEnvFile

// MiseShimsDir is the directory of the mise shims, first on PATH.
const MiseShimsDir = MiseDataDir + "/shims"

// MiseEnv is every MISE_* variable the 30-mise layer sets with ENV. The same
// list feeds the session environment script, so a session started with a
// cleared environment gets the same settings (FR-006). The values are plain
// paths and flags; the script quotes them anyway.
var MiseEnv = []EnvVar{
	{Name: "MISE_DATA_DIR", Value: MiseDataDir},
	{Name: "MISE_CONFIG_DIR", Value: MiseConfigDir},
	{Name: "MISE_CACHE_DIR", Value: MiseRuntimeDir + "/cache"},
	{Name: "MISE_STATE_DIR", Value: MiseRuntimeDir + "/state"},
	{Name: "MISE_TRUSTED_CONFIG_PATHS", Value: MiseConfigDir},
	{Name: "MISE_USE_VERSIONS_HOST", Value: "0"},
}

// defaultSessionEnvScript is /etc/profile.d/sandcast-env.sh (spec 002 R-04,
// R-05). It is POSIX sh, idempotent and safe to source more than once. It
// exports every MISE_* variable of MiseEnv, defines _sandcast_path, which
// puts the mise shims first on PATH, and calls it. In bash it also adds
// _sandcast_path to PROMPT_COMMAND, so an interactive bash puts the shims
// first again before every prompt, and in a login non-interactive bash sets
// a self-removing DEBUG trap that runs it right before the first top-level
// command. Both repairs matter on the container target, where a directly
// started bash still reads the image home's rc files after the system
// files. In OpenShell images no home rc file runs: the login hook execs a
// fresh bash before the first command, so the trap dies with the exec, and
// PROMPT_COMMAND only puts the shims first again after a curated rc that
// changed PATH. The bash-only part sits in eval, so dash and zsh never parse
// bash array syntax.
var defaultSessionEnvScript = sessionEnvScript(MiseEnv, nil, "")

// SessionEnvScriptFor is the session environment script of p: the MISE_*
// variables of MiseEnv followed by the image environment of the plan (the
// components' and harnesses' variables such as RUSTUP_HOME, GOPATH or
// CLAUDE_CONFIG_DIR, and the relocation variables such as ZDOTDIR or
// STARSHIP_CONFIG, whose values win in the image env), which a session
// started with a cleared environment needs as much as the mise settings; the
// shell hooks and launchers of spec 003 source the script, so this is the
// one export list of a session. PATH and names MiseEnv already sets are left
// out. For the OpenShell target, _sandcast_path also puts the base image's
// PATH directories beyond the OpenShell default PATH back on PATH
// (SessionPathDirs), so a tool the base image ships in, for example,
// /usr/sbin or /opt/x/bin resolves in a session as it does in the image,
// except directories under the overmount home, which the mounted workspace
// hides.
func SessionEnvScriptFor(p *Plan) string {
	env := slices.Clone(MiseEnv)
	for _, e := range p.Image.Env {
		if e.Name == "PATH" || slices.ContainsFunc(MiseEnv, func(m EnvVar) bool { return m.Name == e.Name }) {
			continue
		}
		env = append(env, e)
	}
	return sessionEnvScript(env, SessionPathDirsFor(p), OvermountPrefix(p))
}

// SessionPathDirsFor returns the base image PATH directories the session
// script of p puts back on PATH (sessionPathDirs): none off the OpenShell
// target, and never a directory under the plan's overmount home. It is the
// one entry point for the build and the script, so no caller can leave the
// overmount prefix out.
func SessionPathDirsFor(p *Plan) []string {
	if p.Target != manifest.TargetOpenShell {
		return nil
	}
	kept, _ := sessionPathDirs(p.ImageFacts.Path, OvermountPrefix(p))
	return kept
}

// OpenShellDefaultPath is the PATH OpenShell v0.1.3 gives a session.
var OpenShellDefaultPath = []string{"/usr/local/bin", "/usr/bin", "/bin"}

// plainDirRe matches a directory the session script can name without
// quoting: an absolute path of plain characters.
var plainDirRe = regexp.MustCompile(`^/[A-Za-z0-9._+@/-]*$`)

// sessionPathDirs returns the directories of the base image's PATH that a
// session needs beyond the OpenShell default PATH and the mise shims (kept),
// in the base image's order and without duplicates, and the ones left out
// because they lie under the overmount home (hidden; overmount is the plan's
// OvermountPrefix, "" for none). Also left out, and not reported, is a
// directory with characters beyond [A-Za-z0-9._+@/-], so the generated
// script never quotes a probed value. The workspace mounted over the home
// may come from an untrusted project, and a command looked up in it during
// shell startup (Ubuntu's system bashrc calls groups) would run project
// code, so no session PATH and no image PATH ever names such a directory
// (spec 003 FR-002, SC-002); a tool the base image installed there is
// re-provisioned by the workspace rule instead, and the plan reports each
// hidden directory once. Callers go through SessionPathDirsFor and
// imagePath, which supply the prefix.
func sessionPathDirs(basePath []string, overmount string) (kept, hidden []string) {
	for _, d := range basePath {
		d = path.Clean(d)
		if !plainDirRe.MatchString(d) || d == MiseShimsDir || slices.Contains(OpenShellDefaultPath, d) || slices.Contains(kept, d) || slices.Contains(hidden, d) {
			continue
		}
		if UnderOvermount(overmount, d) {
			hidden = append(hidden, d)
			continue
		}
		kept = append(kept, d)
	}
	return kept, hidden
}

// sessionEnvScript renders the script: the exports, _sandcast_path, which
// puts dirs and the shims first on PATH, and the call. With an overmount
// home, the script first drops every inherited PATH entry equal to or under
// it, once, in the body: a session that keeps the image environment (the
// OpenShell default agent) or a runtime that sets PATH itself must not look
// commands up in the mounted workspace either. That step stays out of
// _sandcast_path, so the bash PROMPT_COMMAND repair never strips a directory
// a curated rc or the user put on PATH later (a venv activated by hand).
func sessionEnvScript(env []EnvVar, dirs []string, overmount string) string {
	var b strings.Builder
	b.WriteString(`# Generated by sandcast: session environment for OpenShell sessions, whether
# started with a cleared environment or with the image environment. Safe to
# source more than once.
`)
	for _, e := range env {
		fmt.Fprintf(&b, "export %s=%s\n", e.Name, shell.Quote(e.Value))
	}
	if overmount != "" {
		fmt.Fprintf(&b, `# Drop inherited PATH entries under the overmount home %[1]s: the workspace
# mounted there must never be searched for commands.
_sandcast_prest=${PATH:-} _sandcast_pnew=
while [ -n "$_sandcast_prest" ]; do
	case $_sandcast_prest in *:*) _sandcast_pd=${_sandcast_prest%%%%:*} _sandcast_prest=${_sandcast_prest#*:} ;; *) _sandcast_pd=$_sandcast_prest _sandcast_prest= ;; esac
	case $_sandcast_pd in "" | %[1]s | %[1]s/*) ;; *) _sandcast_pnew="${_sandcast_pnew:+$_sandcast_pnew:}$_sandcast_pd" ;; esac
done
PATH=$_sandcast_pnew
export PATH
unset _sandcast_prest _sandcast_pnew _sandcast_pd
`, overmount)
	}
	b.WriteString("_sandcast_path() {\n")
	// Each missing base image directory is prepended, last first, so they
	// end up in the base image's order between the shims and the rest.
	for i := len(dirs) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "\tcase \":${PATH:-}:\" in *:%[1]s:*) ;; *) PATH=\"%[1]s${PATH:+:$PATH}\" ;; esac\n", dirs[i])
	}
	b.WriteString(`	case "${PATH:-}" in
	` + MiseShimsDir + ` | ` + MiseShimsDir + `:*) ;;
	*) PATH="` + MiseShimsDir + `${PATH:+:$PATH}" ;;
	esac
	export PATH
}
_sandcast_path
if [ -n "${BASH_VERSION:-}" ]; then
	eval '
	if [ "${BASH_VERSINFO[0]}" -gt 5 ] || { [ "${BASH_VERSINFO[0]}" -eq 5 ] && [ "${BASH_VERSINFO[1]}" -ge 1 ]; }; then
		case " ${PROMPT_COMMAND[*]-} " in
		*" _sandcast_path "*) ;;
		*)
			_sandcast_i=1
			for _sandcast_k in "${!PROMPT_COMMAND[@]}"; do
				[ "$_sandcast_k" -lt "$_sandcast_i" ] || _sandcast_i=$((_sandcast_k + 1))
			done
			PROMPT_COMMAND[_sandcast_i]=_sandcast_path
			unset _sandcast_i _sandcast_k
			;;
		esac
	else
		case ";${PROMPT_COMMAND-};" in
		*";_sandcast_path;"*) ;;
		*) PROMPT_COMMAND="_sandcast_path${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
		esac
	fi
	case $- in
	*i*) ;;
	*)
		if shopt -q login_shell; then
			trap '\''[ ${#BASH_SOURCE[@]} -eq 0 ] && { _sandcast_path; trap - DEBUG; }'\'' DEBUG
		fi
		;;
	esac
	'
fi
`)
	return b.String()
}

// Encode renders the plan with fixed encoder settings: two-space indent,
// block style, literal blocks for multi-line strings, struct field order.
func Encode(p *Plan) ([]byte, error) {
	data, err := yaml.MarshalWithOptions(p,
		yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}
	return data, nil
}

// Load reads and validates build/<target>/plan.yaml.
func Load(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	return Decode(data, path)
}

// Decode parses and validates plan bytes.
func Decode(data []byte, source string) (*Plan, error) {
	var p Plan
	if err := yaml.UnmarshalWithOptions(data, &p, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %s", source, yaml.FormatError(err, false, false))
	}
	if err := Validate(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &p, nil
}

// WriteOutputs writes plan.yaml, mise/ (mise.toml and the lockfile copy) and
// policy.yaml into dir. lock is the content of
// .sandcast/mise.lock, nil when no mise tools are planned. Stale outputs from
// an earlier plan are removed.
func WriteOutputs(dir string, p *Plan, lock []byte) error {
	data, err := Encode(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	miseDir := filepath.Join(dir, MiseDir)
	if err := os.RemoveAll(miseDir); err != nil {
		return err
	}
	if p.Mise.TOML != "" {
		files := map[string][]byte{
			MiseTOMLFile: []byte(p.Mise.TOML),
			MiseLockCopy: lock,
		}
		for name, content := range files {
			if err := fsutil.AtomicWriteFile(filepath.Join(miseDir, name), content, 0o644); err != nil {
				return err
			}
		}
	}
	policyPath := filepath.Join(dir, PolicyFile)
	if p.Policy != "" {
		if err := fsutil.AtomicWriteFile(policyPath, []byte(p.Policy), 0o644); err != nil {
			return err
		}
	} else if err := os.Remove(policyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return fsutil.AtomicWriteFile(filepath.Join(dir, PlanFile), data, 0o644)
}
