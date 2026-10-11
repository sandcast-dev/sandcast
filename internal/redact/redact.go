package redact

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// Kinds of credential shapes a Hit reports (R-18).
const (
	KindAnthropic   = "anthropic-api-key"
	KindOpenAI      = "openai-api-key"
	KindGitHub      = "github-token"
	KindGitHubPAT   = "github-fine-grained-pat"
	KindGitLab      = "gitlab-pat"
	KindAWS         = "aws-access-key-id"
	KindBearer      = "bearer-token"
	KindURLSecret   = "url-password"
	KindPrivateKey  = "private-key"
	KindBasicAuth   = "basic-auth"
	KindAuthHeader  = "authorization-header"
	KindGoogleAPI   = "google-api-key"
	KindKeyValue    = "secret-key-value"
	KindSlack       = "slack-token"
	KindStripe      = "stripe-secret-key"
	KindNPM         = "npm-token"
	KindHuggingFace = "huggingface-token"
	KindNotion      = "notion-token"
	KindJWT         = "jwt"
	// KindURLPath is a credential-like path segment of an MCP server or
	// marketplace url. Scan does not report it; the harness discovery
	// redactor does, because download urls elsewhere carry hashes in paths.
	KindURLPath = "url-path-secret"
)

// MinKeyValueLength is the shortest value under a secret-looking key name
// (token, secret, password, api key) that counts as a credential.
const MinKeyValueLength = 12

// mask replaces the secret part of an excerpt.
const mask = "***"

// Hit is one detected credential. It never carries the secret itself.
type Hit struct {
	// Kind is one of the Kind* constants.
	Kind string `json:"kind"`
	// Path is the YAML key path of the value, when known.
	Path string `json:"path,omitempty"`
	// Line is the 1-based line on which the secret starts.
	Line int `json:"line"`
	// Excerpt shows where the secret was found with the secret masked.
	Excerpt string `json:"excerpt"`
	// Placeholder is the replacement text, set by Replace only.
	Placeholder string `json:"placeholder,omitempty"`
}

var (
	placeholderRe = regexp.MustCompile(`^\$\{SANDCAST_SECRET_[1-9][0-9]*\}$`)
	envRefRe      = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)(?::?[-=?+][^}]*)?\})$`)
	digestRe      = regexp.MustCompile(`^sha(?:256|384|512):[0-9a-fA-F]+$`)
	keyValueRe    = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]*)["']?[ \t]*([:=])[ \t]*(?:"([^"\n]*)"|'([^'\n]*)'|([^\s"',;]+))`)
)

// pattern is one credential shape. Submatch 1 of re is the secret.
type pattern struct {
	kind    string
	re      *regexp.Regexp
	excerpt func(secret string) string
	valid   func(text string, start, end int) bool
}

// patterns are tried in order; a later match overlapping an earlier one is
// dropped, so the more specific shapes come first.
var patterns = []pattern{
	{
		kind:    KindPrivateKey,
		re:      regexp.MustCompile(`(-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----)`),
		excerpt: func(string) string { return "-----BEGIN PRIVATE KEY-----" + mask },
	},
	{
		kind:    KindAnthropic,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(sk-ant-[A-Za-z0-9_-]{20,})`),
		excerpt: fixedPrefix("sk-ant-"),
	},
	{
		kind:    KindGitHubPAT,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(github_pat_[A-Za-z0-9_]{22,})`),
		excerpt: fixedPrefix("github_pat_"),
	},
	{
		kind:    KindGitHub,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(gh[pousr]_[A-Za-z0-9]{36,})`),
		excerpt: leadingPrefix(4),
	},
	{
		kind:    KindGitLab,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(glpat-[A-Za-z0-9_-]{20,})`),
		excerpt: fixedPrefix("glpat-"),
	},
	{
		kind:    KindAWS,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9])((?:AKIA|ASIA)[A-Z0-9]{16})`),
		excerpt: leadingPrefix(4),
		valid: func(text string, _, end int) bool {
			return end == len(text) || !isAlnum(text[end])
		},
	},
	{
		kind:    KindOpenAI,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(sk-[A-Za-z0-9_-]{20,})`),
		excerpt: fixedPrefix("sk-"),
		valid: func(text string, start, end int) bool {
			return hasDigit(text[start+len("sk-") : end])
		},
	},
	{
		kind:    KindGoogleAPI,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(AIza[0-9A-Za-z_-]{35})`),
		excerpt: fixedPrefix("AIza"),
	},
	{
		// Slack bot, user, refresh and app-level tokens. The digit check
		// skips documentation samples such as xoxb-your-token-here.
		kind:    KindSlack,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])((?:xox[abeprs]|xapp)-[A-Za-z0-9-]{10,})`),
		excerpt: leadingPrefix(5),
		valid: func(text string, start, end int) bool {
			return hasDigit(text[start+5 : end])
		},
	},
	{
		// Stripe secret and restricted keys; publishable pk_ keys are public.
		kind:    KindStripe,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])((?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,})`),
		excerpt: leadingPrefix(len("sk_live_")),
	},
	{
		kind:    KindNPM,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(npm_[A-Za-z0-9]{36,})`),
		excerpt: fixedPrefix("npm_"),
		valid:   randomAfter("npm_"),
	},
	{
		kind:    KindHuggingFace,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(hf_[A-Za-z0-9]{30,})`),
		excerpt: fixedPrefix("hf_"),
		valid:   randomAfter("hf_"),
	},
	{
		// Notion integration tokens: secret_ (legacy) and ntn_.
		kind:    KindNotion,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])((?:secret|ntn)_[A-Za-z0-9]{32,})`),
		excerpt: func(secret string) string { return secret[:strings.IndexByte(secret, '_')+1] + mask },
		valid: func(text string, start, end int) bool {
			s := text[start:end]
			return mixedClasses(s[strings.IndexByte(s, '_')+1:])
		},
	},
	{
		// A JSON Web Token: base64url header and payload, both JSON objects
		// (eyJ is the encoding of `{"`), and an optional signature. It comes
		// before Bearer so that a bearer JWT keeps this kind.
		kind:    KindJWT,
		re:      regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*)`),
		excerpt: fixedPrefix("eyJ"),
	},
	{
		kind:    KindBearer,
		re:      regexp.MustCompile(`(?i)\bbearer[ \t]+([A-Za-z0-9._~+/-]{16,}=*)`),
		excerpt: fixedPrefix("Bearer "),
		valid: func(text string, start, end int) bool {
			return hasDigit(text[start:end])
		},
	},
	{
		// HTTP Basic credentials: base64 of user:password.
		kind:    KindBasicAuth,
		re:      regexp.MustCompile(`(?i)\bbasic[ \t]+([A-Za-z0-9+/]{8,}={0,2})`),
		excerpt: fixedPrefix("Basic "),
		valid: func(text string, start, end int) bool {
			if end < len(text) && (isAlnum(text[end]) || text[end] == '+' || text[end] == '/' || text[end] == '=') {
				return false
			}
			return isBasicCredential(text[start:end])
		},
	},
	{
		// Any other Authorization value: "Authorization: Token <opaque>".
		// Bearer and Basic come first and win where they overlap.
		kind:    KindAuthHeader,
		re:      regexp.MustCompile(`(?i)\b(?:(?:proxy-)?authorization|(?:set-)?cookie)["']?[ \t]*[:=][ \t]*["']?(?:[A-Za-z][A-Za-z0-9_-]*[ \t]+)?([^\s"',;]{8,})`),
		excerpt: fixedPrefix("Authorization: "),
		valid: func(text string, start, end int) bool {
			secret := text[start:end]
			return hasDigit(secret) && !strings.HasPrefix(secret, "$") && !strings.ContainsAny(secret, "{<") && strings.Trim(secret, "*") != ""
		},
	},
	{
		// The password of URL user information: scheme://user:secret@host.
		kind:    KindURLSecret,
		re:      regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/:@"']*:([^\s/@"']+)@`),
		excerpt: func(string) string { return "://user:" + mask + "@" },
		valid: func(text string, start, end int) bool {
			secret := text[start:end]
			return !strings.HasPrefix(secret, "$") && strings.Trim(secret, "*") != ""
		},
	},
}

func fixedPrefix(p string) func(string) string {
	return func(string) string { return p + mask }
}

func leadingPrefix(n int) func(string) string {
	return func(secret string) string { return secret[:n] + mask }
}

// match is a detected secret as byte offsets into the scanned text.
type match struct {
	kind    string
	start   int
	end     int
	excerpt string
}

// Scan reports every credential found in text, in order of appearance.
func Scan(text string) []Hit {
	ms := findMatches(text)
	hits := make([]Hit, 0, len(ms))
	for _, m := range ms {
		hits = append(hits, Hit{Kind: m.kind, Line: lineAt(text, m.start), Excerpt: m.excerpt})
	}
	return hits
}

// Replace replaces every credential in text with a ${SANDCAST_SECRET_N}
// placeholder, numbered from 1 in order of first appearance. The same secret
// gets the same number.
func Replace(text string) (string, []Hit) {
	out, hits, _ := ReplaceFrom(text, 1)
	return out, hits
}

// ReplaceFrom is Replace with numbering that starts at start, so that several
// values can be redacted in sequence. It returns the next free number.
func ReplaceFrom(text string, start int) (string, []Hit, int) {
	r := NewReplacer(start)
	out, hits := r.Replace(text)
	return out, hits, r.Next()
}

// Replacer numbers placeholders across several Replace calls and gives the
// same secret the same placeholder in every call.
type Replacer struct {
	next int
	seen map[string]int
}

// NewReplacer returns a Replacer whose first placeholder number is start
// (at least 1).
func NewReplacer(start int) *Replacer {
	return &Replacer{next: max(start, 1), seen: map[string]int{}}
}

// Next returns the number the next new secret will get.
func (r *Replacer) Next() int { return r.next }

// Replace replaces every credential in text with its placeholder.
func (r *Replacer) Replace(text string) (string, []Hit) {
	ms := findMatches(text)
	if len(ms) == 0 {
		return text, nil
	}
	var b strings.Builder
	hits := make([]Hit, 0, len(ms))
	prev := 0
	for _, m := range ms {
		secret := text[m.start:m.end]
		n, ok := r.seen[secret]
		if !ok {
			n = r.next
			r.next++
			r.seen[secret] = n
		}
		ph := Placeholder(n)
		b.WriteString(text[prev:m.start])
		b.WriteString(ph)
		prev = m.end
		hits = append(hits, Hit{Kind: m.kind, Line: lineAt(text, m.start), Excerpt: m.excerpt, Placeholder: ph})
	}
	b.WriteString(text[prev:])
	return b.String(), hits
}

// Placeholder returns the placeholder for secret number n.
func Placeholder(n int) string {
	return "${SANDCAST_SECRET_" + strconv.Itoa(n) + "}"
}

// IsPlaceholder reports whether s is exactly a sandcast secret placeholder.
func IsPlaceholder(s string) bool {
	return placeholderRe.MatchString(s)
}

// EnvRef returns the variable name when v is only a reference to an
// environment variable: $NAME, ${NAME}, or ${NAME:-default} and the other
// shell forms.
func EnvRef(v string) (string, bool) {
	m := envRefRe.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		return m[1], true
	}
	return m[2], true
}

// ScanYAML walks a YAML document and reports credentials in its values with
// their key paths (for example harnesses[0].mcp_servers[1].env.API_KEY).
// Secrets outside values, such as in comments, are reported without a path.
func ScanYAML(doc []byte) ([]Hit, error) {
	file, err := parser.ParseBytes(doc, 0)
	if err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	var hits []Hit
	for _, d := range file.Docs {
		walkYAML(d.Body, "", "", &hits)
	}
	for _, th := range Scan(string(doc)) {
		covered := slices.ContainsFunc(hits, func(h Hit) bool {
			return h.Line == th.Line && h.Kind == th.Kind
		})
		if !covered {
			hits = append(hits, th)
		}
	}
	slices.SortStableFunc(hits, func(a, b Hit) int { return a.Line - b.Line })
	return hits, nil
}

func walkYAML(node ast.Node, path, key string, hits *[]Hit) {
	switch n := node.(type) {
	case nil:
	case *ast.DocumentNode:
		walkYAML(n.Body, path, key, hits)
	case *ast.MappingNode:
		for _, mv := range n.Values {
			walkYAML(mv, path, key, hits)
		}
	case *ast.MappingValueNode:
		if n.Key == nil || n.Key.IsMergeKey() {
			return
		}
		k := keyString(n.Key)
		walkYAML(n.Value, joinPath(path, k), k, hits)
	case *ast.SequenceNode:
		for i, v := range n.Values {
			walkYAML(v, fmt.Sprintf("%s[%d]", path, i), key, hits)
		}
	case *ast.AnchorNode:
		walkYAML(n.Value, path, key, hits)
	case *ast.TagNode:
		walkYAML(n.Value, path, key, hits)
	case *ast.StringNode:
		scanScalar(n.Value, tokenLine(n), path, key, hits)
	case *ast.LiteralNode:
		// The block content starts on the line after the | or > header.
		if n.Value != nil && n.Start != nil {
			scanScalar(n.Value.Value, n.Start.Position.Line+1, path, key, hits)
		}
	}
}

// scanScalar reports the credentials inside one string value, and the value
// itself when it sits under a secret-looking key.
func scanScalar(value string, line int, path, key string, hits *[]Hit) {
	ms := findMatches(value)
	for _, m := range ms {
		*hits = append(*hits, Hit{
			Kind:    m.kind,
			Path:    path,
			Line:    line + strings.Count(value[:m.start], "\n"),
			Excerpt: m.excerpt,
		})
	}
	if len(ms) == 0 && key != "" && secretPair(key, strings.TrimSpace(value)) {
		*hits = append(*hits, Hit{Kind: KindKeyValue, Path: path, Line: line, Excerpt: key + ": " + mask})
	}
}

func tokenLine(n ast.Node) int {
	if tk := n.GetToken(); tk != nil && tk.Position != nil {
		return tk.Position.Line
	}
	return 0
}

func keyString(k ast.Node) string {
	switch n := k.(type) {
	case *ast.MappingKeyNode:
		return keyString(n.Value)
	case *ast.StringNode:
		return n.Value
	case ast.ScalarNode:
		return fmt.Sprint(n.GetValue())
	default:
		return k.String()
	}
}

// joinPath appends key to path, bracket-quoting keys that would be ambiguous
// in dotted form.
func joinPath(path, key string) string {
	if key == "" || strings.ContainsAny(key, ".[]\"' \t") {
		return path + "[" + strconv.Quote(key) + "]"
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

// findMatches returns non-overlapping matches sorted by position.
func findMatches(text string) []match {
	var out []match
	overlaps := func(s, e int) bool {
		return slices.ContainsFunc(out, func(m match) bool { return s < m.end && m.start < e })
	}
	for _, p := range patterns {
		for _, idx := range p.re.FindAllStringSubmatchIndex(text, -1) {
			s, e := idx[2], idx[3]
			if s < 0 || overlaps(s, e) || (p.valid != nil && !p.valid(text, s, e)) {
				continue
			}
			out = append(out, match{kind: p.kind, start: s, end: e, excerpt: p.excerpt(text[s:e])})
		}
	}
	// A pair whose key is not a secret resumes the search right after its
	// separator, so that https://host/?access_token=... is still examined.
	for pos := 0; pos < len(text); {
		idx := keyValueRe.FindStringSubmatchIndex(text[pos:])
		if idx == nil {
			break
		}
		for i := range idx {
			if idx[i] >= 0 {
				idx[i] += pos
			}
		}
		key := text[idx[2]:idx[3]]
		s, e := -1, -1
		for g := 3; g <= 5; g++ {
			if idx[2*g] >= 0 {
				s, e = idx[2*g], idx[2*g+1]
				break
			}
		}
		sep := text[idx[4]:idx[5]]
		// In a URL query or fragment the value ends at the next & or #, so
		// the parameters after it survive Replace.
		if sep == "=" && idx[2] > 0 && strings.IndexByte("?&#", text[idx[2]-1]) >= 0 {
			if i := strings.IndexAny(text[s:e], "&#"); i >= 0 {
				e = s + i
			}
		}
		named := SecretName
		if sep == "=" && idx[2] > 0 && strings.IndexByte("?&#-", text[idx[2]-1]) >= 0 {
			// A URL query parameter or a --flag=value pair.
			named = SecretParam
		}
		// Judge and replace only the item of an inline [..] or {..}, so the
		// brackets survive Replace.
		v := text[s:e]
		item := flowItem(v)
		lead := len(v) - len(strings.TrimLeft(v, "[{"))
		s, e = s+lead, s+lead+len(item)
		if !secretPairWith(named, key, item) || overlaps(s, e) {
			pos = idx[5]
			continue
		}
		pos = e
		if sep == ":" {
			sep = ": "
		}
		out = append(out, match{kind: KindKeyValue, start: s, end: e, excerpt: key + sep + mask})
	}
	slices.SortFunc(out, func(a, b match) int { return a.start - b.start })
	return out
}

// secretSuffixes mark a word of a name as naming a credential.
var secretSuffixes = []string{"token", "secret", "password", "passwd", "passphrase", "apikey"}

// credentialWords name a credential as the last word of a name of two or
// more words (aws_credentials, dbCredentials), in an upper-case env var name,
// or after a qualifier (client_credentials_json). A bare credential or
// credentials field, as in OpenShell provider profiles, holds credential
// names, not values.
var credentialWords = map[string]bool{"credential": true, "credentials": true}

// secretLastWords name a credential when they end a name of two or more
// words that is an upper-case env var name (ACCESS_KEY, GITHUB_PAT) or whose
// previous word is a credential qualifier (access_key, privateKey,
// basic-auth). In other field names such as component_key or cache_key, and
// as a bare "key", they are plain fields; SecretParam also accepts the bare
// word for flags and query parameters.
var secretLastWords = map[string]bool{"key": true, "auth": true, "pat": true, "pass": true, "pwd": true}

// metaWords, as the last word of a key, mean the key describes a secret
// (where it lives, what it is called) rather than holding one.
var metaWords = map[string]bool{
	"cmd": true, "command": true, "count": true, "dir": true, "endpoint": true,
	"env": true, "expiry": true, "expires": true, "field": true, "file": true,
	"format": true, "header": true, "helper": true, "host": true, "id": true,
	"kind": true, "length": true, "limit": true, "name": true, "path": true,
	"prefix": true, "provider": true, "ref": true, "size": true, "source": true,
	"timeout": true, "ttl": true, "type": true, "uri": true, "url": true,
	"var": true,
}

// credentialQualifiers, as the word before a final key, auth or pat, make
// the name a credential in any letter case.
var credentialQualifiers = map[string]bool{
	"access": true, "app": true, "auth": true, "basic": true, "client": true,
	"deploy": true, "encryption": true, "github": true, "gitlab": true,
	"license": true, "master": true, "personal": true, "private": true,
	"secret": true, "service": true, "signing": true, "ssh": true,
}

// IsCredentialHeader reports whether an HTTP header name carries a
// credential: Authorization, Proxy-Authorization, Cookie, or a
// secret-looking name such as X-Api-Key.
func IsCredentialHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie":
		return true
	}
	// A header name is a parameter: X-Subscription-Key, X-Auth.
	return SecretParam(name)
}

// plainQualifiers, as the word before a final key, auth or pat, name a
// field that is not a credential: public_key, CACHE_KEY, sort-key, no-auth.
var plainQualifiers = map[string]bool{
	"allow": true, "cache": true, "component": true, "disable": true,
	"disabled": true, "enable": true, "enabled": true, "foreign": true,
	"idempotency": true, "no": true, "object": true, "partition": true,
	"primary": true, "public": true, "publishable": true, "require": true,
	"required": true, "routing": true, "skip": true, "sort": true,
	"use": true, "with": true, "without": true,
}

// HeaderSecret splits the value of a credential header into the part that
// stays readable and the secret. Only Authorization and Proxy-Authorization
// keep a leading scheme word ("Token", "Bearer"); for every other header,
// such as Cookie or X-Api-Key, the whole value is the secret.
func HeaderSecret(name, value string) (prefix, secret string) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization":
		if scheme, token, ok := strings.Cut(value, " "); ok && isWord(scheme) {
			return scheme + " ", strings.TrimSpace(token)
		}
	}
	return "", value
}

func isWord(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) < 0
}

// SecretParam reports whether a command-line flag or URL query parameter
// name looks like it holds a credential: SecretName, or any name ending in
// key, auth or pat (--key, --account-key, ?subscription-key=, --ado-pat)
// unless a plain qualifier precedes it (--sort-key). In these positions such
// a name usually carries a token.
func SecretParam(name string) bool {
	if SecretName(name) {
		return true
	}
	words := splitWords(name)
	if len(words) == 0 || !secretLastWords[words[len(words)-1]] {
		return false
	}
	return len(words) == 1 || !plainQualifiers[words[len(words)-2]]
}

// SecretName reports whether a key, env var, flag or parameter name looks
// like it holds a credential: one of its words ends in token, secret,
// password, credential or api key, or a name of two or more words ends in
// key, auth or pat and is an upper-case env var name or has a credential
// qualifier before that word (ACCESS_KEY, access_key, privateKey). Names whose last word describes the credential, such as
// TOKEN_FILE or API_KEY_PATH, do not. Every credential check in sandcast
// (discovery, manifest validation, the build output scan) uses this one
// definition.
func SecretName(key string) bool {
	words := splitWords(key)
	if len(words) == 0 {
		return false
	}
	last := words[len(words)-1]
	if metaWords[last] {
		return false
	}
	upper := strings.ToUpper(key) == key
	if len(words) > 1 && secretLastWords[last] {
		prev := words[len(words)-2]
		if !plainQualifiers[prev] && (upper || credentialQualifiers[prev]) {
			return true
		}
	}
	for i, w := range words {
		if credentialWords[w] && i > 0 && (upper || i == len(words)-1 || credentialQualifiers[words[i-1]]) {
			return true
		}
		if w == "api" && i+1 < len(words) && words[i+1] == "key" {
			return true
		}
		for _, suffix := range secretSuffixes {
			if strings.HasSuffix(w, suffix) {
				return true
			}
		}
	}
	return false
}

// secretPair reports whether value, found under key, is a credential: a
// secret-looking value under a secret-looking name, or base64 user:password
// under an auth field (Docker config.json "auth", .npmrc _auth).
func secretPair(key, value string) bool {
	return secretPairWith(SecretName, key, value)
}

func secretPairWith(named func(string) bool, key, value string) bool {
	if named(key) && secretValue(value) {
		return true
	}
	words := splitWords(key)
	return len(words) > 0 && words[len(words)-1] == "auth" && isBasicCredential(value)
}

// isBasicCredential reports base64 of user:password.
func isBasicCredential(s string) bool {
	b, err := base64.StdEncoding.DecodeString(s)
	return err == nil && bytes.IndexByte(b, ':') > 0
}

// flowItem strips the brackets of an unquoted inline YAML or JSON
// collection, so that "key: [value]" is judged by its item.
func flowItem(v string) string {
	return strings.TrimRight(strings.TrimLeft(v, "[{"), "]}")
}

// varPathRe matches a path that starts with a variable: $HOME/.config/x.
var varPathRe = regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_]*|\{[A-Za-z_][A-Za-z0-9_]*\})/`)

// IsPathLike reports a value that is a file path rather than a secret: it
// starts with /, ./, ../, ~/ or a variable followed by a slash.
func IsPathLike(v string) bool {
	for _, p := range []string{"/", "./", "../", "~/"} {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return varPathRe.MatchString(v)
}

// secretValue reports whether a value under a secret key is long enough and
// is not a reference, a placeholder, a path, a URL, a digest or a number.
func secretValue(v string) bool {
	switch {
	case len(v) < MinKeyValueLength,
		strings.ContainsFunc(v, unicode.IsSpace),
		strings.Contains(v, "${"),
		envRefRe.MatchString(v),
		strings.Contains(v, "://"),
		digestRe.MatchString(v),
		strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">"),
		strings.HasPrefix(v, "{{"):
		return false
	}
	if IsPathLike(v) {
		return false
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return false
	}
	return strings.Trim(v, v[:1]) != ""
}

// splitWords splits a key on non-alphanumerics and camelCase boundaries and
// lowercases the words: "x-api-key" and "apiKey" both give [.. api key].
func splitWords(key string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(key)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := cur[len(cur)-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

func lineAt(text string, offset int) int {
	return 1 + strings.Count(text[:offset], "\n")
}

func hasDigit(s string) bool {
	return strings.ContainsFunc(s, unicode.IsDigit)
}

// mixedClasses reports a value that mixes at least two of lower-case
// letters, upper-case letters and digits, as random tokens do and long
// identifiers such as npm_packagelockfile usually do not.
func mixedClasses(s string) bool {
	n := 0
	for _, is := range []func(rune) bool{unicode.IsLower, unicode.IsUpper, unicode.IsDigit} {
		if strings.ContainsFunc(s, is) {
			n++
		}
	}
	return n >= 2
}

// randomAfter is a pattern validity check: the part after prefix mixes
// character classes.
func randomAfter(prefix string) func(text string, start, end int) bool {
	return func(text string, start, end int) bool {
		return mixedClasses(text[start+len(prefix) : end])
	}
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
