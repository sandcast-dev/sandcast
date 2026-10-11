package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/plan"
)

// PromoteOptions control promotion of a fragment into a recipe.
type PromoteOptions struct {
	// Name is the display name; defaults to the key.
	Name string
	// Bins are the binaries the recipe installs; defaults to the existing
	// definition's binaries, else the key.
	Bins []string
	// Existing is the current definition of the key in the tiers, if any;
	// its check, provides, profiles and image needs are carried over.
	Existing *component.Component
	// FinalUser is the plan's final user; RUN instructions as this user are
	// accepted when the fragment has no root steps.
	FinalUser string
}

// Promotion is a recipe produced from a fragment.
type Promotion struct {
	Recipe *component.Component
	Data   []byte
	Path   string
}

// BuildRecipe converts the stored fragment of key into a project-local
// component recipe (FR-018): the RUN steps become installer.script.run,
// ENV becomes image.env, WORKDIR becomes a cd. Instructions a script cannot
// express (COPY, ADD, ...) and fragments that mix root and non-root RUN
// steps are refused.
func BuildRecipe(fragmentsDir, recipesDir, key string, o PromoteOptions) (*Promotion, error) {
	path := FragmentPath(fragmentsDir, key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fragment for %s: %w", key, err)
	}
	f, err := ParseFragment(key, path, data)
	if err != nil {
		return nil, err
	}
	script, env, asRoot, err := fragmentScript(f.Body, o.FinalUser)
	if err != nil {
		return nil, fmt.Errorf("promote %s: %w", key, err)
	}
	c := &component.Component{
		Key:          key,
		Name:         o.Name,
		Description:  "Promoted from a working build skill fragment.",
		PromotedFrom: filepath.ToSlash(filepath.Join("fragments", key+FragmentExt)),
		Installer: component.Installer{Script: &component.ScriptInstaller{
			Run: script, AsRoot: asRoot, Bins: o.Bins,
		}},
	}
	if c.Name == "" {
		c.Name = key
	}
	if e := o.Existing; e != nil {
		c.Check = e.Check
		c.Provides = e.Provides
		c.Profiles = e.Profiles
		c.Targets = e.Targets
		c.Match = e.Match
		if e.Image != nil {
			c.Image = e.Image.Clone()
		}
		if len(c.Installer.Script.Bins) == 0 {
			c.Installer.Script.Bins = e.Bins()
		}
	}
	if len(c.Installer.Script.Bins) == 0 {
		c.Installer.Script.Bins = []string{key}
	}
	// The build skill unpacks trees into /opt/<key>; the image policy grants
	// only declared /opt paths, so the recipe declares it like the plan does
	// for the fragment.
	if c.Image == nil {
		c.Image = &component.ImageNeeds{}
	}
	if opt := plan.OptDir(key); !slices.Contains(c.Image.ReadOnly, opt) && !slices.Contains(c.Image.ReadWrite, opt) {
		c.Image.ReadOnly = append(c.Image.ReadOnly, opt)
	}
	if c.Check.Command == "" {
		c.Check.Command = "command -v " + c.Installer.Script.Bins[0]
	}
	if len(env) > 0 {
		if c.Image == nil {
			c.Image = &component.ImageNeeds{}
		}
		if c.Image.Env == nil {
			c.Image.Env = map[string]string{}
		}
		for k, v := range env {
			c.Image.Env[k] = v
		}
	}
	out, err := component.Marshal(c)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("# Recipe promoted by sandcast from %s. Review it like code; it is used by\n# every non-interactive build of this project.\n", c.PromotedFrom)
	out = append([]byte(header), out...)
	if _, err := component.Parse(out, filepath.Join(recipesDir, key+".yaml"), component.TierProject); err != nil {
		return nil, fmt.Errorf("promoted recipe does not validate: %w", err)
	}
	return &Promotion{Recipe: c, Data: out, Path: filepath.Join(recipesDir, key+".yaml")}, nil
}

// Promote writes the recipe and marks the fragment superseded.
func Promote(fragmentsDir string, p *Promotion) error {
	if err := fsutil.AtomicWriteFile(p.Path, p.Data, 0o644); err != nil {
		return err
	}
	return MarkSuperseded(fragmentsDir, p.Recipe.Key)
}

// fragmentScript turns fragment instructions into a POSIX script.
func fragmentScript(body, finalUser string) (string, map[string]string, bool, error) {
	if err := ValidateFragment(body); err != nil {
		return "", nil, false, err
	}
	var steps []string
	env := map[string]string{}
	user := ""
	users := map[bool]bool{}
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		word, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch strings.ToUpper(word) {
		case "USER":
			f := strings.Fields(rest)
			if len(f) == 0 {
				return "", nil, false, errors.New("USER without a user name")
			}
			user = f[0]
		case "ENV":
			vars, err := parseEnv(rest)
			if err != nil {
				return "", nil, false, err
			}
			for k, v := range vars {
				env[k] = v
			}
		case "WORKDIR":
			steps = append(steps, "mkdir -p "+rest+" && cd "+rest)
		case "RUN":
			users[isRoot(user, finalUser)] = true
			if m := heredocRe.FindStringSubmatch(rest); m != nil {
				var body []string
				for i++; i < len(lines) && strings.TrimSpace(lines[i]) != m[1]; i++ {
					body = append(body, lines[i])
				}
				if strings.TrimSpace(heredocRe.ReplaceAllString(rest, "")) == "" {
					// RUN <<EOF: the body is the script itself.
					steps = append(steps, strings.Join(body, "\n"))
				} else {
					// RUN cmd <<EOF: a shell heredoc feeding cmd; keep it
					// whole, including the terminator.
					steps = append(steps, rest+"\n"+strings.Join(append(body, m[1]), "\n"))
				}
				continue
			}
			// Keep line continuations as they are: backslash-newline is
			// valid shell inside the recipe script too. Comment and blank
			// lines inside a continuation are dropped, as the Containerfile
			// parser does; in a shell script they would end the command.
			cmd := []string{rest}
			continued := strings.HasSuffix(rest, "\\")
			for continued && i+1 < len(lines) {
				i++
				next := strings.TrimSpace(lines[i])
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				cmd = append(cmd, lines[i])
				continued = strings.HasSuffix(next, "\\")
			}
			steps = append(steps, strings.Join(cmd, "\n"))
		default:
			return "", nil, false, fmt.Errorf("%s cannot be expressed in a recipe script; keep the fragment or rewrite it with RUN steps", strings.ToUpper(word))
		}
	}
	if len(users) > 1 {
		return "", nil, false, fmt.Errorf("the fragment runs steps both as root and as the final user; split it or run every step as root")
	}
	if len(steps) == 0 {
		return "", nil, false, fmt.Errorf("the fragment has no RUN step")
	}
	return strings.Join(steps, "\n") + "\n", env, users[true], nil
}

// isRoot reports whether user (empty means the section default, root) is
// root rather than the final user.
func isRoot(user, finalUser string) bool {
	if user == "" || user == "root" || user == "0" || strings.HasPrefix(user, "0:") || strings.HasPrefix(user, "root:") {
		return true
	}
	return finalUser != "" && user != finalUser && !strings.HasPrefix(user, finalUser+":")
}

// parseEnv parses the arguments of an ENV instruction with Containerfile
// rules: "K=V K2=V2" (values may be quoted with "..." or '...', with
// backslash escapes) or the legacy "K V". A value that references a
// variable is refused: image.env values are literals, so promotion cannot
// keep a reference such as PATH=/opt/x/bin:$PATH.
func parseEnv(rest string) (map[string]string, error) {
	out := map[string]string{}
	words, err := envWords(rest)
	if err != nil {
		return nil, err
	}
	if len(words) > 0 && !strings.Contains(words[0], "=") {
		k, v, _ := strings.Cut(strings.TrimSpace(rest), " ")
		words = []string{k + "=" + strings.TrimSpace(v)}
		if vs, err := envWords(strings.TrimSpace(v)); err == nil && len(vs) == 1 {
			words = []string{k + "=" + vs[0]}
		}
	}
	for _, w := range words {
		k, v, ok := strings.Cut(w, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("ENV %q is not KEY=VALUE", w)
		}
		if strings.Contains(v, "$") {
			return nil, fmt.Errorf("ENV %s=%s references a variable; promotion keeps only literal values: install into /usr/local/bin or keep the fragment", k, v)
		}
		out[k] = v
	}
	return out, nil
}

// envWords splits on unquoted whitespace and removes the quoting.
func envWords(s string) ([]string, error) {
	var words []string
	var b strings.Builder
	inWord := false
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			inWord = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, b.String())
				b.Reset()
				inWord = false
			}
		default:
			b.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in ENV %q", s)
	}
	if inWord {
		words = append(words, b.String())
	}
	return words, nil
}
