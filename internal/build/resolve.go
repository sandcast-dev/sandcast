package build

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/plan"
	"github.com/rhuss/sandcast/internal/shell"
)

// Resolution methods.
const (
	MethodMiseWhich = "mise-which"
	MethodCommandV  = "command-v"
	MethodMissing   = "missing"
)

// Resolution is the resolved absolute path of one component binary inside
// the built image (spec "Resolved path").
type Resolution struct {
	Component string `json:"component" yaml:"component"`
	Bin       string `json:"bin" yaml:"bin"`
	Path      string `json:"path" yaml:"path"`
	Method    string `json:"method" yaml:"method"`
}

// resolveHelpers prints one JSON line per binary in POSIX sh. _jsafe comes
// from shell.JSONHelpers and uses only builtins, so resolution works in
// images without sed or tr.
const resolveHelpers = shell.JSONHelpers + `_emit() { printf '{"component":"%s","bin":"%s","path":"%s","method":"%s"}\n' "$(_jsafe "$1")" "$(_jsafe "$2")" "$(_jsafe "$3")" "$4"; }
_resolve() {
  c="$1"; b="$2"; usemise="$3"
  if [ "$usemise" = 1 ] && command -v mise >/dev/null 2>&1; then
    p="$(mise which "$b" 2>/dev/null || true)"
    if [ -n "$p" ]; then p="$(readlink -f "$p" 2>/dev/null || printf '%s' "$p")"; _emit "$c" "$b" "$p" ` + MethodMiseWhich + `; return; fi
  fi
  p="$(command -v "$b" 2>/dev/null || true)"
  case "$p" in
    /*) p="$(readlink -f "$p" 2>/dev/null || printf '%s' "$p")"; _emit "$c" "$b" "$p" ` + MethodCommandV + ` ;;
    *) _emit "$c" "$b" "" ` + MethodMissing + ` ;;
  esac
}
`

// ResolveScript builds the resolution script for the plan's components:
// mise-installed components use mise which (the real install path, not the
// shim), everything else command -v plus readlink -f (R-08).
func ResolveScript(p *plan.Plan) string {
	var b strings.Builder
	b.WriteString(resolveHelpers)
	for _, c := range p.Components {
		usemise := "0"
		if c.InstallerKind == "mise" {
			usemise = "1"
		}
		for _, bin := range c.Bins {
			fmt.Fprintf(&b, "_resolve %s %s %s\n", shell.Quote(c.Key), shell.Quote(bin), usemise)
		}
	}
	return b.String()
}

// ParseResolutions reads the JSON lines of the resolution script, sorted by
// component and binary.
func ParseResolutions(out []byte) ([]Resolution, error) {
	var res []Resolution
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var r Resolution
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("parse resolution line %q: %w", line, err)
		}
		res = append(res, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Component != res[j].Component {
			return res[i].Component < res[j].Component
		}
		return res[i].Bin < res[j].Bin
	})
	return res, nil
}

// Resolve runs the resolution script in image as its default (final) user,
// stopped after engine.HelperTimeout.
func Resolve(ctx context.Context, eng engine.Engine, image, platform string, p *plan.Plan) ([]Resolution, error) {
	stdout, stderr, err := engine.RunWithin(ctx, eng, engine.RunSpec{
		Image: image, Platform: platform, Entrypoint: "/bin/sh", Script: ResolveScript(p),
	}, engine.HelperTimeout)
	if err != nil {
		return nil, fmt.Errorf("resolve binary paths in %s: %w", image, engine.WithStderr(err, stderr))
	}
	return ParseResolutions(stdout)
}

// ProfileBinaries maps every profile id referenced by the plan to the
// resolved paths of the referencing components plus their runtime globs.
func ProfileBinaries(p *plan.Plan, res []Resolution) map[string][]string {
	paths := map[string][]string{}
	for _, r := range res {
		if r.Path != "" {
			paths[r.Component] = append(paths[r.Component], r.Path)
		}
	}
	out := map[string][]string{}
	for _, pr := range p.Profiles {
		out[pr.ID] = []string{}
	}
	for _, c := range p.Components {
		for _, id := range c.Profiles {
			if _, ok := out[id]; !ok {
				continue
			}
			out[id] = append(out[id], paths[c.Key]...)
			out[id] = append(out[id], c.RuntimeGlobs...)
		}
	}
	for id, list := range out {
		sort.Strings(list)
		out[id] = compact(list)
	}
	return out
}

// WritableBinaries returns, per profile id of binaries, the entries
// (resolved paths and runtime globs) that lie under a location the sandbox
// can write at runtime: a read_write path of the image policy, the workdir
// (when the policy has include_workdir, and always for the OpenShell target,
// where sandcast creates /sandbox owned by the final user; on Kubernetes it
// is the writable workspace volume), or a directory the plan creates owned
// by the final user. Any process can put an executable there and get the
// profile's network access, so the build flags such profiles. Ids without
// such entries are left out.
//
// /opt/mise is deliberately not a writable root, although the mise fragment
// installs into it as the final user (R-04), so that user owns the files.
// The image policy lists /opt/mise read-only, so its integrity at runtime
// rests on landlock, under the pinned compatibility best_effort. Treating
// it as writable would flag every mise profile; making it root-owned needs
// either a second copy of the tool tree in a chown layer or a root install.
func WritableBinaries(p *plan.Plan, binaries map[string][]string) (map[string][]string, error) {
	var doc struct {
		FilesystemPolicy struct {
			IncludeWorkdir bool     `yaml:"include_workdir"`
			ReadWrite      []string `yaml:"read_write"`
		} `yaml:"filesystem_policy"`
	}
	if err := yaml.Unmarshal([]byte(p.Policy), &doc); err != nil {
		return nil, fmt.Errorf("read the plan's image policy: %w", err)
	}
	roots := slices.Clone(doc.FilesystemPolicy.ReadWrite)
	if doc.FilesystemPolicy.IncludeWorkdir || p.Target == manifest.TargetOpenShell {
		roots = append(roots, workDir(p))
	}
	for _, d := range p.Image.Dirs {
		if d.Owner != "root" {
			roots = append(roots, d.Path)
		}
	}
	out := map[string][]string{}
	for id, list := range binaries {
		for _, b := range list {
			if slices.ContainsFunc(roots, func(root string) bool { return mayMatchUnder(b, root) }) {
				out[id] = append(out[id], b)
			}
		}
	}
	return out, nil
}

// mayMatchUnder reports whether entry, an absolute path or glob, can name
// root itself or a path below it. Glob segments are matched against the
// segments of root; a "**" segment matches anything below.
func mayMatchUnder(entry, root string) bool {
	es := strings.Split(strings.Trim(path.Clean(entry), "/"), "/")
	r := strings.Trim(path.Clean(root), "/")
	if r == "" {
		return true
	}
	for i, seg := range strings.Split(r, "/") {
		if i >= len(es) {
			return false
		}
		if strings.Contains(es[i], "**") {
			return true
		}
		// A malformed pattern counts as a possible match.
		if ok, err := path.Match(es[i], seg); !ok && err == nil {
			return false
		}
	}
	return true
}

func compact(list []string) []string {
	out := list[:0]
	for i, s := range list {
		if i == 0 || s != list[i-1] {
			out = append(out, s)
		}
	}
	return out
}
