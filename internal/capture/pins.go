package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Pinned version kinds (R-09).
const (
	PinExact   = "exact"
	PinRange   = "range"
	PinChannel = "channel"
)

// PinnedVersion is a tool version a project declares in a well-known file
// (spec "Pinned version"), reported with the file it came from.
type PinnedVersion struct {
	// Value is the value from the file. A leading "go" of a go toolchain
	// directive and a leading "v" of a Node.js version are dropped.
	Value string `json:"value"`
	// File is the project-relative path of the file.
	File string `json:"file"`
	// Kind is exact, range or channel.
	Kind string `json:"kind"`
}

// toolPins are the pinned versions of one project, keyed by mise tool name
// (go, rust, python, node, or a name from .tool-versions or mise.toml).
type toolPins map[string][]PinnedVersion

// Tool names the pin readers use. Components map to them through their
// installer.mise.tool.
const (
	toolGo     = "go"
	toolRust   = "rust"
	toolPython = "python"
	toolNode   = "node"
)

// toolVersionsAliases maps asdf plugin names in .tool-versions to mise tool
// names.
var toolVersionsAliases = map[string]string{"golang": toolGo, "nodejs": toolNode}

// declaringFiles are the files whose tool entries count as evidence for the
// component that installs the tool, even when the component's match files
// do not name them (R-09).
var declaringFiles = []string{".tool-versions", "mise.toml", ".mise.toml"}

var (
	exactRe   = regexp.MustCompile(`^v?\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
	channelRe = regexp.MustCompile(`^[A-Za-z]`)
)

// classifyPin returns exact for a full version without an operator, channel
// for a word such as stable, nightly, lts/* or system, and range otherwise
// (R-09).
func classifyPin(v string) string {
	switch {
	case exactRe.MatchString(v):
		return PinExact
	case channelRe.MatchString(v):
		return PinChannel
	}
	return PinRange
}

// readPins reads every supported pin file among the project-relative files
// (FR-004). A file that cannot be parsed yields no pin and a note
// "<file>: <reason>"; it never fails the match. Go pins are ordered highest
// version first, so the first one is the proposal across a go.work's
// modules; other tools keep the file order.
func readPins(root string, files []string) (toolPins, []string) {
	pins := toolPins{}
	var notes []string
	add := func(tool, value, file string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		pins[tool] = append(pins[tool], PinnedVersion{Value: value, File: file, Kind: classifyPin(value)})
	}
	note := func(file, format string, args ...any) {
		notes = append(notes, file+": "+fmt.Sprintf(format, args...))
	}
	goMods := map[string]bool{}
	for _, f := range files {
		goMods[f] = path.Base(f) == "go.mod"
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				note(f, "cannot be read: %v", errors.Unwrap(err))
			}
			continue
		}
		switch path.Base(f) {
		case "go.mod":
			goPins(data, f, add, note, true)
		case "go.work":
			goPins(data, f, add, note, false)
			for _, use := range goWorkUses(data) {
				mod := path.Join(path.Dir(f), use, "go.mod")
				if strings.HasPrefix(mod, "../") || path.IsAbs(use) || goMods[mod] {
					continue
				}
				if modData, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(mod))); err == nil {
					goMods[mod] = true
					goPins(modData, mod, add, note, true)
				}
			}
		case "rust-toolchain.toml":
			rustToolchainTOML(data, f, add, note)
		case "rust-toolchain":
			if bytes.Contains(data, []byte("[toolchain]")) {
				rustToolchainTOML(data, f, add, note)
			} else {
				add(toolRust, firstLine(data), f)
			}
		case ".python-version":
			add(toolPython, firstLine(data), f)
		case "pyproject.toml":
			var py struct {
				Project struct {
					RequiresPython string `toml:"requires-python"`
				} `toml:"project"`
			}
			if _, err := toml.Decode(string(data), &py); err != nil {
				note(f, "invalid TOML: %v", err)
				continue
			}
			add(toolPython, py.Project.RequiresPython, f)
		case ".nvmrc", ".node-version":
			add(toolNode, nodeValue(firstLine(data)), f)
		case "package.json":
			var pkg struct {
				Engines map[string]any `json:"engines"`
			}
			if err := json.Unmarshal(data, &pkg); err != nil {
				note(f, "invalid JSON: %v", err)
				continue
			}
			if v, ok := pkg.Engines["node"].(string); ok {
				add(toolNode, nodeValue(v), f)
			}
		case ".tool-versions":
			sc := bufio.NewScanner(bytes.NewReader(data))
			for sc.Scan() {
				line, _, _ := strings.Cut(sc.Text(), "#")
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				tool := fields[0]
				if alias, ok := toolVersionsAliases[tool]; ok {
					tool = alias
				}
				v := fields[1]
				if tool == toolNode {
					v = nodeValue(v)
				}
				add(tool, v, f)
			}
		case "mise.toml", ".mise.toml":
			var m struct {
				Tools map[string]any `toml:"tools"`
			}
			if _, err := toml.Decode(string(data), &m); err != nil {
				note(f, "invalid TOML: %v", err)
				continue
			}
			for _, tool := range sortedMapKeys(m.Tools) {
				for _, v := range miseToolVersions(m.Tools[tool]) {
					add(tool, v, f)
				}
			}
		}
	}
	sortGoPins(pins[toolGo])
	return pins, notes
}

// goPins reads the go and toolchain directives of a go.mod or go.work. A
// go.mod without a go directive is a note.
func goPins(data []byte, file string, add func(tool, value, file string), note func(file, format string, args ...any), requireGo bool) {
	foundGo := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "//")
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "go":
			foundGo = true
			add(toolGo, fields[1], file)
		case "toolchain":
			if v, ok := strings.CutPrefix(fields[1], "go"); ok {
				add(toolGo, v, file)
			}
		}
	}
	if requireGo && !foundGo {
		note(file, "no go directive")
	}
}

// goWorkUses returns the module directories of the use directives of a
// go.work, single-line and block form.
func goWorkUses(data []byte) []string {
	var uses []string
	inBlock := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "//")
		line = strings.TrimSpace(line)
		switch {
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "":
			uses = append(uses, unquote(line))
		case line == "use (":
			inBlock = true
		case strings.HasPrefix(line, "use "):
			uses = append(uses, unquote(strings.TrimSpace(strings.TrimPrefix(line, "use "))))
		}
	}
	return uses
}

func rustToolchainTOML(data []byte, file string, add func(tool, value, file string), note func(file, format string, args ...any)) {
	var rt struct {
		Toolchain struct {
			Channel string `toml:"channel"`
		} `toml:"toolchain"`
	}
	if _, err := toml.Decode(string(data), &rt); err != nil {
		note(file, "invalid TOML: %v", err)
		return
	}
	add(toolRust, rt.Toolchain.Channel, file)
}

// miseToolVersions returns the versions of one [tools] entry of a
// mise.toml: a string, an array of strings, or a table with "version".
func miseToolVersions(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, miseToolVersions(item)...)
		}
		return out
	case map[string]any:
		if s, ok := t["version"].(string); ok {
			return []string{s}
		}
	}
	return nil
}

// sortGoPins orders go pins by version, highest first; values that do not
// parse as numbers keep their order after the numeric ones.
func sortGoPins(pins []PinnedVersion) {
	sort.SliceStable(pins, func(i, j int) bool {
		a, aok := numericVersion(pins[i].Value)
		b, bok := numericVersion(pins[j].Value)
		if aok != bok {
			return aok
		}
		return aok && slices.Compare(a, b) > 0
	})
}

func numericVersion(v string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	for len(out) < 3 {
		out = append(out, 0)
	}
	return out, true
}

// nodeValue drops the leading v of a Node.js version (v20.11.1).
func nodeValue(v string) string {
	if len(v) > 1 && v[0] == 'v' && v[1] >= '0' && v[1] <= '9' {
		return v[1:]
	}
	return v
}

// firstLine returns the first non-empty line that is not a comment.
func firstLine(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

func unquote(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
