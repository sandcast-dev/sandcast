package harness

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/redact"
)

// Redactor replaces the credentials an adapter finds during discovery with
// ${SANDCAST_SECRET_N} placeholders. Numbers start at 1 and run across every
// value redacted through the same Redactor, so one discovery never reuses a
// number. Values are redacted in call order; callers sort first to keep the
// numbering deterministic.
type Redactor struct {
	next  int
	texts map[string]redactedText
	whole map[string]string
}

// redactedText is the memoized result of redacting one input text.
type redactedText struct {
	out  string
	hits []redact.Hit
}

// NewRedactor returns a Redactor whose first placeholder is
// ${SANDCAST_SECRET_1}.
func NewRedactor() *Redactor {
	return &Redactor{next: 1, texts: map[string]redactedText{}, whole: map[string]string{}}
}

// Text replaces every credential shape redact recognizes in s. The same
// input text always yields the same output and placeholders.
func (d *Redactor) Text(s string) (string, []redact.Hit) {
	if r, ok := d.texts[s]; ok {
		return r.out, r.hits
	}
	rep := redact.NewReplacer(d.next)
	out, hits := rep.Replace(s)
	d.next = rep.Next()
	d.texts[s] = redactedText{out: out, hits: hits}
	return out, hits
}

// Secret returns the placeholder for a value that is a credential as a
// whole, such as a short token under a key named API_KEY that no credential
// shape matches. The same value always gets the same placeholder.
func (d *Redactor) Secret(v string) string {
	if ph, ok := d.whole[v]; ok {
		return ph
	}
	ph := redact.Placeholder(d.next)
	d.next++
	d.whole[v] = ph
	return ph
}

// Server replaces every credential in s in place and returns what it
// replaced. It covers credential shapes in the url, the command arguments
// and the env values, and additionally treats as credentials, whatever their
// length: env values under secret-looking keys, URL query values under
// secret-looking parameter names, URL passwords, credential-like URL path
// segments, and command arguments that follow or are assigned to a
// secret-looking flag. The URL rules apply to the url and to every http and
// https url inside a command argument or an env value, such as the url in
// sh -c "npx mcp-remote https://...". Placeholders and environment variable
// references are left alone. An env redaction records in Redaction.EnvVar
// the variable that carries the credential at runtime: the env key when the
// whole value became the placeholder, else the placeholder's own
// SANDCAST_SECRET_N.
func (d *Redactor) Server(s *MCPServer) []Redaction {
	base := fmt.Sprintf("mcp_servers[%s]", s.Name)
	var out []Redaction
	record := func(path, kind, placeholder, envVar string) {
		out = append(out, Redaction{Path: path, Kind: kind, Placeholder: placeholder, EnvVar: envVar})
	}
	recordHits := func(path string, hits []redact.Hit) {
		for _, h := range hits {
			record(path, h.Kind, h.Placeholder, "")
		}
	}

	if s.URL != "" {
		u, reds := d.URL(base+".url", s.URL)
		s.URL = u
		out = append(out, reds...)
	}

	prevSecretFlag := false
	pendingHeader := ""
	for i, arg := range s.Command {
		path := fmt.Sprintf("%s.command[%d]", base, i)
		a, hits := d.Text(arg)
		recordHits(path, hits)
		argURL := func(kind, placeholder string) { record(path, kind, placeholder, "") }
		switch {
		case pendingHeader != "" && !strings.HasPrefix(a, "-"):
			// -H NAME VALUE: the value of a credential header.
			prefix, token := redact.HeaderSecret(pendingHeader, a)
			if Forceable(token) {
				ph := d.Secret(token)
				record(path, redact.KindKeyValue, ph, "")
				a = prefix + ph
			}
		case prevSecretFlag && !strings.HasPrefix(a, "-") && Forceable(a):
			ph := d.Secret(a)
			record(path, redact.KindKeyValue, ph, "")
			a = ph
		case assignment(a):
			// KEY=value or --flag=value: the whole value is the credential,
			// unless the value is itself a header (--header=Authorization: ...).
			k, v, _ := strings.Cut(a, "=")
			if redact.SecretParam(strings.TrimLeft(k, "-")) && Forceable(v) {
				ph := d.Secret(v)
				record(path, redact.KindKeyValue, ph, "")
				a = k + "=" + ph
			} else if ik, iv, ok := strings.Cut(v, "="); ok && assignment(v) && redact.SecretParam(ik) && Forceable(iv) {
				// --env=KEY=value
				ph := d.Secret(iv)
				record(path, redact.KindKeyValue, ph, "")
				a = k + "=" + ik + "=" + ph
			} else if isCredentialHeader(v) {
				if h, ph, ok := d.redactHeader(v); ok {
					record(path, redact.KindKeyValue, ph, "")
					a = k + "=" + h
				}
			}
		case isCredentialHeader(a):
			if h, ph, ok := d.redactHeader(a); ok {
				record(path, redact.KindKeyValue, ph, "")
				a = h
			}
		}
		// mcp-remote https://host/<key>/mcp, --url=https://..., and urls
		// inside a shell command line.
		a = d.redactURLs(a, argURL)
		pendingHeader = ""
		if i > 0 && strings.HasPrefix(s.Command[i-1], "-") && !strings.Contains(s.Command[i-1], "=") &&
			headerNameRe.MatchString(a) && redact.IsCredentialHeader(a) {
			pendingHeader = a
		}
		prevSecretFlag = strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && redact.SecretParam(strings.TrimLeft(a, "-"))
		s.Command[i] = a
	}

	for _, k := range manifest.SortedKeys(s.Env) {
		path := base + ".env." + k
		v, hits := d.Text(s.Env[k])
		for _, h := range hits {
			// Only a value that became one placeholder as a whole is read
			// from the env key at runtime (see envReference in the adapter);
			// in a partly replaced value the placeholder names the variable.
			envVar := PlaceholderVar(h.Placeholder)
			if v == h.Placeholder {
				envVar = k
			}
			record(path, h.Kind, h.Placeholder, envVar)
		}
		if len(hits) == 0 && redact.SecretName(k) && Forceable(v) {
			v = d.Secret(v)
			record(path, redact.KindKeyValue, v, k)
		} else {
			v = d.redactURLs(v, func(kind, placeholder string) {
				record(path, kind, placeholder, PlaceholderVar(placeholder))
			})
		}
		s.Env[k] = v
	}
	return out
}

// URL replaces the credentials in a url: every credential shape, the
// password of its user information, credential-like path segments and the
// values of secret-looking query parameters, whatever their length. path is
// recorded in each Redaction.
func (d *Redactor) URL(path, raw string) (string, []Redaction) {
	var out []Redaction
	u, hits := d.Text(raw)
	for _, h := range hits {
		out = append(out, Redaction{Path: path, Kind: h.Kind, Placeholder: h.Placeholder})
	}
	u = d.redactURL(u, func(kind, placeholder string) {
		out = append(out, Redaction{Path: path, Kind: kind, Placeholder: placeholder})
	})
	return u, out
}

// redactURLs runs redactURL on every http and https url inside text and
// keeps the text around them as it is. Other schemes, such as
// postgres://admin@host/db, keep their user.
func (d *Redactor) redactURLs(text string, found func(kind, placeholder string)) string {
	return urlInTextRe.ReplaceAllStringFunc(text, func(m string) string {
		u, tail := trimURL(m)
		return d.redactURL(u, found) + tail
	})
}

// trimURL splits off the end of a url found in text what belongs to the
// text around it: sentence punctuation and closing brackets without an
// opening one in the url, as in "(see https://host/<key>)." or
// "mcp-remote https://host/<key>; wait".
func trimURL(m string) (string, string) {
	end := len(m)
	for ; end > 0; end-- {
		c := m[end-1]
		if strings.IndexByte(".,:;!?", c) >= 0 {
			continue
		}
		if open, ok := closingBrackets[c]; ok && strings.Count(m[:end], string(c)) > strings.Count(m[:end], string(open)) {
			continue
		}
		break
	}
	return m[:end], m[end:]
}

// closingBrackets maps each closing bracket to its opening one.
var closingBrackets = map[byte]byte{')': '(', ']': '[', '}': '{'}

// redactURL replaces the password of the URL authority, a user name that is
// not the plain "git" SSH user (a token used as the user, as GitHub, Gitea
// and Azure DevOps accept), credential-like path segments (an access key
// in the path, as hosted MCP servers issue them), and the values of
// secret-looking query parameters, working on the raw text so that the
// placeholders stay readable. A placeholder stands for the segment as
// written, percent-encoding included, so that the runtime value restores
// the url exactly. found is called for every placeholder.
func (d *Redactor) redactURL(raw string, found func(kind, placeholder string)) string {
	rest, fragment, hasFragment := strings.Cut(raw, "#")
	rest, query, hasQuery := strings.Cut(rest, "?")
	if scheme, after, ok := strings.Cut(rest, "://"); ok {
		authority, pathPart := after, ""
		if i := strings.Index(after, "/"); i >= 0 {
			authority, pathPart = after[:i], after[i:]
		}
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			user, pass, hasPass := strings.Cut(authority[:at], ":")
			switch {
			case hasPass && Forceable(pass):
				ph := d.Secret(pass)
				found(redact.KindURLSecret, ph)
				authority = user + ":" + ph + authority[at:]
			case !hasPass && user != "git" && Forceable(user):
				ph := d.Secret(user)
				found(redact.KindURLSecret, ph)
				authority = ph + authority[at:]
			}
		}
		segments := strings.Split(pathPart, "/")
		for i, seg := range segments {
			if credentialSegment(seg) {
				ph := d.Secret(seg)
				found(redact.KindURLPath, ph)
				segments[i] = ph
			}
		}
		rest = scheme + "://" + authority + strings.Join(segments, "/")
	}
	if hasQuery {
		pairs := strings.Split(query, "&")
		for i, pair := range pairs {
			k, v, ok := strings.Cut(pair, "=")
			if ok && redact.SecretParam(k) && Forceable(v) {
				ph := d.Secret(v)
				found(redact.KindKeyValue, ph)
				pairs[i] = k + "=" + ph
			}
		}
		rest += "?" + strings.Join(pairs, "&")
	}
	if hasFragment {
		rest += "#" + fragment
	}
	return rest
}

var (
	// uuidRe matches a UUID, which hosted MCP servers issue as a path access
	// key (https://mcp.pipedream.net/<uuid>/<app>).
	uuidRe = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	// tokenSegmentRe matches a percent-decoded path segment of at least 20
	// characters of the base64, base64url and hex alphabets ("/" only
	// through %2F). Dots, as in file names and versions, never occur in it.
	tokenSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_+/=-]{20,}$`)
	// urlInTextRe matches an http or https url inside a longer text, such as
	// a shell command line or an env value that holds several arguments.
	urlInTextRe = regexp.MustCompile("(?i)\\bhttps?://[^\\s\"'<>`]+")
)

// minWordLen is the length from which a word of a path segment (a run of
// letters and digits) counts towards its letter and digit changes. Shorter
// words such as k8s, v2, e2e, ipv6 or run42 mix letters and digits in
// ordinary names.
const minWordLen = 6

// credentialSegment reports a URL path segment that looks like a key
// rather than a name, after percent-decoding: a UUID, or a long base64,
// base64url or hex token whose digits and letters interleave. Ordinary
// segments keep their digits together or in short words (v2, 2025-03-26,
// k8s-cluster-production-v2, GetUserV2), so it takes at least three letter
// and digit changes inside words of minWordLen or more characters, or
// digits plus a character class change (lower, upper, digit) at four in ten
// positions, as in random mixed-case tokens. A 40-hex git commit name in a
// path counts as a key too: it has the shape of a classic GitHub token and
// of many hex API keys, so it becomes a placeholder whose variable supplies
// it at runtime.
func credentialSegment(seg string) bool {
	if dec, err := url.PathUnescape(seg); err == nil {
		seg = dec
	}
	if uuidRe.MatchString(seg) {
		return true
	}
	if !tokenSegmentRe.MatchString(seg) {
		return false
	}
	letterDigit, changes := 0, 0
	start, wordLetterDigit := 0, 0
	for i := 0; i <= len(seg); i++ {
		if i == len(seg) || charClass(seg[i]) == 0 {
			if i-start >= minWordLen {
				letterDigit += wordLetterDigit
			}
			start, wordLetterDigit = i+1, 0
			continue
		}
		if i == start {
			continue
		}
		a, b := charClass(seg[i-1]), charClass(seg[i])
		if a == b {
			continue
		}
		changes++
		if a == 'd' || b == 'd' {
			wordLetterDigit++
		}
	}
	hasDigit := strings.ContainsAny(seg, "0123456789")
	return letterDigit >= 3 || (hasDigit && 10*changes >= 4*(len(seg)-1))
}

// charClass is 'd' for a digit, 'l' for a lower-case and 'u' for an
// upper-case ASCII letter, and 0 otherwise.
func charClass(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return 'd'
	case c >= 'a' && c <= 'z':
		return 'l'
	case c >= 'A' && c <= 'Z':
		return 'u'
	}
	return 0
}

// SplitUserinfo splits the user information off a scheme://user:pass@host
// url. It returns the url without it, the user information, and whether
// there was any. Values without a scheme are returned unchanged.
func SplitUserinfo(raw string) (string, string, bool) {
	scheme, after, ok := strings.Cut(raw, "://")
	if !ok {
		return raw, "", false
	}
	end := len(after)
	if i := strings.IndexAny(after, "/?#"); i >= 0 {
		end = i
	}
	at := strings.LastIndex(after[:end], "@")
	if at < 0 {
		return raw, "", false
	}
	return scheme + "://" + after[at+1:], after[:at], true
}

// Forceable reports whether v, found under a secret-looking name, is treated
// as a credential: it is not empty, not a placeholder, does not reference an
// environment variable, and is not a path, a URL or a boolean.
func Forceable(v string) bool {
	t := strings.TrimSpace(v)
	switch strings.ToLower(t) {
	case "", "true", "false", "yes", "no", "on", "off", "0", "1", "none", "null":
		return false
	}
	if redact.IsPlaceholder(t) || strings.Contains(t, "${") || isEnvRef(t) || strings.Contains(t, "://") {
		return false
	}
	return !redact.IsPathLike(t)
}

// headerNameRe matches an HTTP header name.
var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// assignment reports an argument whose first separator is "=", such as
// KEY=value or --flag=value, as opposed to a "Name: value" header.
func assignment(a string) bool {
	eq, colon := strings.IndexByte(a, '='), strings.IndexByte(a, ':')
	return eq >= 0 && (colon < 0 || eq < colon)
}

// redactHeader replaces the credential of a "Name: [scheme ]value" header
// argument, keeping the scheme word, and reports the placeholder.
func (d *Redactor) redactHeader(a string) (string, string, bool) {
	name, value, _ := strings.Cut(a, ":")
	prefix, token := redact.HeaderSecret(name, value)
	if !Forceable(token) {
		return a, "", false
	}
	ph := d.Secret(token)
	return name + ": " + prefix + ph, ph, true
}

// isCredentialHeader reports an argument of the form "Name: value" whose
// header name carries a credential: Authorization, Proxy-Authorization, or a
// secret-looking name such as X-Api-Key.
func isCredentialHeader(a string) bool {
	name, _, ok := strings.Cut(a, ":")
	if !ok || !headerNameRe.MatchString(name) {
		return false
	}
	return redact.IsCredentialHeader(name)
}

func isEnvRef(v string) bool {
	_, ok := redact.EnvRef(v)
	return ok
}

// PlaceholderVar returns the environment variable a placeholder names:
// SANDCAST_SECRET_3 for ${SANDCAST_SECRET_3}.
func PlaceholderVar(placeholder string) string {
	return strings.TrimSuffix(strings.TrimPrefix(placeholder, "${"), "}")
}
