package redact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Synthetic, obviously fake credentials of realistic shape and length. They
// are assembled at run time so no literal credential appears in the source.
var (
	fakeAnthropic = "sk-ant-api03-" + strings.Repeat("x", 93) + "AA"
	fakeOpenAI    = "sk-proj-" + strings.Repeat("x1", 24)
	fakeGitHub    = "ghp_" + strings.Repeat("x", 36)
	fakeGitHubPAT = "github_pat_" + strings.Repeat("x", 22) + "_" + strings.Repeat("y", 59)
	fakeGitLab    = "glpat-" + strings.Repeat("x", 20)
	fakeAWS       = "AKIA" + "IOSFODNN7" + "EXAMPLE" // the AWS documentation example key id
	fakeBearer    = strings.Repeat("x1", 16)
	fakeGeneric   = "fakeSecretValue" + strings.Repeat("x", 9)
	fakePEM       = "-----BEGIN RSA PRIVATE KEY-----\nMIIfakefakefake\n-----END RSA PRIVATE KEY-----"
	fakeDigest    = "sha256:" + strings.Repeat("ab", 32)
	fakeSlack     = "xoxb-" + "123456789012-1234567890123-" + strings.Repeat("x", 24)
	fakeStripe    = "sk_" + "live_" + strings.Repeat("x1", 12)
	fakeNPM       = "npm_" + strings.Repeat("x1", 18)
	fakeHF        = "hf_" + strings.Repeat("xY", 17)
	fakeNotion    = "secret_" + strings.Repeat("x1", 21) + "x"
	fakeJWT       = "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiJmYWtlIn0" + "." + strings.Repeat("x", 43)
)

func TestScanPositive(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		kind    string
		excerpt string
	}{
		{"anthropic env", "ANTHROPIC_API_KEY=" + fakeAnthropic, KindAnthropic, "sk-ant-***"},
		{"anthropic quoted", `"apiKey": "` + fakeAnthropic + `"`, KindAnthropic, "sk-ant-***"},
		{"openai", "export OPENAI_API_KEY=" + fakeOpenAI, KindOpenAI, "sk-***"},
		{"github classic", "token " + fakeGitHub, KindGitHub, "ghp_***"},
		{"github oauth", "gho_" + strings.Repeat("x", 36), KindGitHub, "gho_***"},
		{"github fine grained", "GH=" + fakeGitHubPAT, KindGitHubPAT, "github_pat_***"},
		{"gitlab", "glab auth login --token " + fakeGitLab, KindGitLab, "glpat-***"},
		{"aws", "aws_access_key_id = " + fakeAWS, KindAWS, "AKIA***"},
		{"aws temporary", "ASIA" + "IOSFODNN7" + "EXAMPLE", KindAWS, "ASIA***"},
		{"bearer header", "Authorization: Bearer " + fakeBearer, KindBearer, "Bearer ***"},
		{"bearer lowercase", "curl -H 'authorization: bearer " + fakeBearer + "'", KindBearer, "Bearer ***"},
		{"key value yaml", "client_secret: " + fakeGeneric, KindKeyValue, "client_secret: ***"},
		{"key value env", "DB_PASSWORD=" + fakeGeneric, KindKeyValue, "DB_PASSWORD=***"},
		{"key value json", `{"accessToken": "` + fakeGeneric + `"}`, KindKeyValue, "accessToken: ***"},
		{"key value flag", "--api-key=" + fakeGeneric, KindKeyValue, "api-key=***"},
		{"key value query", "https://example.com/cb?access_token=" + fakeGeneric, KindKeyValue, "access_token=***"},
		{"url password", "https://deploy:hunter2@git.example.com/org/repo.git", KindURLSecret, "://user:***@"},
		{"url password other scheme", "git+https://me:s3cret@example.com/repo", KindURLSecret, "://user:***@"},
		{"private key", fakePEM + "\n", KindPrivateKey, "-----BEGIN PRIVATE KEY-----***"},
		{"slack bot", "slack: " + fakeSlack, KindSlack, "xoxb-***"},
		{"slack user", `"xoxp-` + "1234-5678-" + strings.Repeat("ab", 8) + `"`, KindSlack, "xoxp-***"},
		{"slack app", "xapp-" + "1-A0123456789-" + strings.Repeat("ab12", 4), KindSlack, "xapp-***"},
		{"stripe live", "stripe " + fakeStripe, KindStripe, "sk_live_***"},
		{"stripe restricted test", "rk_" + "test_" + strings.Repeat("Ab1", 8), KindStripe, "rk_test_***"},
		{"npm", "//registry.npmjs.org/:_authToken " + fakeNPM, KindNPM, "npm_***"},
		{"hugging face", "login " + fakeHF, KindHuggingFace, "hf_***"},
		{"notion legacy", "notion " + fakeNotion, KindNotion, "secret_***"},
		{"notion", "ntn_" + strings.Repeat("1", 11) + strings.Repeat("xY", 18), KindNotion, "ntn_***"},
		{"jwt", "id " + fakeJWT, KindJWT, "eyJ***"},
		{"jwt as bearer", "Authorization: Bearer " + fakeJWT, KindJWT, "eyJ***"},
		{"age secret key", "AGE-SECRET-KEY-1" + strings.Repeat("Q", 58) + "\n# public key: age1" + strings.Repeat("q", 58) + "\n", KindAge, "AGE-SECRET-KEY-1***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := Scan(tt.text)
			require.Len(t, hits, 1, "hits: %+v", hits)
			assert.Equal(t, tt.kind, hits[0].Kind)
			assert.Equal(t, tt.excerpt, hits[0].Excerpt)
			assert.Equal(t, 1, hits[0].Line)
		})
	}
}

func TestScanNegative(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"anthropic too short", "sk-ant-short"},
		{"anthropic inside word", "mask-ant-" + strings.Repeat("x", 30)},
		{"openai inside word", "task-runner-for-the-long-build-pipeline-2024"},
		{"openai no digit", "sk-" + strings.Repeat("abcdefgh", 4)},
		{"openai too short", "sk-123456"},
		{"github too short", "ghp_short"},
		{"github inside word", "xghp_" + strings.Repeat("x", 36)},
		{"github pat too short", "github_pat_short"},
		{"gitlab too short", "glpat-short"},
		{"aws too long", fakeAWS + "XYZ"},
		{"aws inside word", "X" + fakeAWS},
		{"bearer env reference", "Authorization: Bearer ${GITHUB_TOKEN}"},
		{"bearer shell variable", "Authorization: Bearer $TOKEN"},
		{"bearer placeholder", "Authorization: Bearer ${SANDCAST_SECRET_1}"},
		{"bearer prose", "uses bearer authentication-without-any-digits"},
		{"key value env reference", "token: ${GITHUB_TOKEN}"},
		{"key value env reference with default", "token: ${GITHUB_TOKEN:-unset}"},
		{"key value shell variable", "token: $GITHUB_TOKEN"},
		{"key value placeholder", "API_KEY=${SANDCAST_SECRET_1}"},
		{"key value short", "password: hunter2"},
		{"key value file", "password_file: /run/secrets/database-password"},
		{"key value path", "token: /run/secrets/github-token-file"},
		{"key value url", "token_endpoint: https://example.com/oauth/token"},
		{"key value url under secret key", "secret: https://vault.example.com/v1/secret/data"},
		{"key value count", "max_tokens: 409600000000000"},
		{"key value number", "token: 409600000000000"},
		{"key value tokenizer", "tokenizer: sentencepiece-model-v2"},
		{"key value name", "secret_name: my-kubernetes-secret-name"},
		{"key value masked", "password: ****************"},
		{"key value template", "token: {{ .Values.github.token }}"},
		{"key value angle placeholder", "API_KEY=<your-api-key-here>"},
		{"key value prose", `token: "use the GITHUB_TOKEN variable"`},
		{"key value digest", "token: " + fakeDigest},
		{"digest", "image@" + fakeDigest},
		{"digest field", "digest: " + fakeDigest},
		{"public key", "-----BEGIN PUBLIC KEY-----\nMIIfake\n-----END PUBLIC KEY-----"},
		{"certificate", "-----BEGIN CERTIFICATE-----\nMIIfake\n-----END CERTIFICATE-----"},
		{"ordinary text", "Build the sandbox image and export the provider profiles."},
		{"url user without password", "https://deploy@git.example.com/org/repo.git"},
		{"url port", "https://git.example.com:8443/org/repo.git"},
		{"url password reference", "https://deploy:${GIT_TOKEN}@git.example.com/repo"},
		{"url password placeholder", "https://deploy:${SANDCAST_SECRET_1}@git.example.com/repo"},
		{"url password masked", "https://deploy:****@git.example.com/repo"},
		{"slack placeholder", "use a bot token such as xoxb-your-token-goes-here"},
		{"slack inside word", "abcxoxb-123456789012-1234567890123"},
		{"stripe publishable", "pk_" + "live_" + strings.Repeat("x1", 12)},
		{"stripe too short", "sk_" + "live_abc123"},
		{"npm config", "npm_config_registry=https://registry.npmjs.org/"},
		{"npm lowercase identifier", "npm_" + strings.Repeat("packagelock", 4)},
		{"hugging face env name", "HF_HUB_ENABLE_HF_TRANSFER=1"},
		{"hugging face identifier", "hf_" + strings.Repeat("transformers", 3)},
		{"secret identifier", "secret_access_key_id_for_the_service_account"},
		{"secret lowercase identifier", "secret_" + strings.Repeat("kubernetesconfig", 3)},
		{"jwt header only", "eyJhbGciOiJIUzI1NiJ9"},
		{"age public key", "# public key: age1" + strings.Repeat("q", 58)},
		{"age secret key too short", "AGE-SECRET-KEY-1" + strings.Repeat("Q", 57)},
		{"age secret key too long", "AGE-SECRET-KEY-1" + strings.Repeat("Q", 59)},
		{"jwt without payload", "eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnop.signature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Empty(t, Scan(tt.text))
		})
	}
}

func TestScanLinesAndOrder(t *testing.T) {
	text := "first line\nGITHUB=" + fakeGitHub + "\n\nANTHROPIC=" + fakeAnthropic + " and " + fakeGitLab + "\n"
	hits := Scan(text)
	require.Len(t, hits, 3)
	assert.Equal(t, []Hit{
		{Kind: KindGitHub, Line: 2, Excerpt: "ghp_***"},
		{Kind: KindAnthropic, Line: 4, Excerpt: "sk-ant-***"},
		{Kind: KindGitLab, Line: 4, Excerpt: "glpat-***"},
	}, hits)
}

func TestScanSpecificWinsOverKeyValue(t *testing.T) {
	hits := Scan("ANTHROPIC_API_KEY: " + fakeAnthropic)
	require.Len(t, hits, 1)
	assert.Equal(t, KindAnthropic, hits[0].Kind)
}

func TestExcerptNeverContainsSecret(t *testing.T) {
	for _, secret := range []string{
		fakeAnthropic, fakeOpenAI, fakeGitHub, fakeGitHubPAT, fakeGitLab, fakeAWS, fakeBearer, fakeGeneric,
		fakeSlack, fakeStripe, fakeNPM, fakeHF, fakeNotion, fakeJWT,
	} {
		hits := Scan("Authorization: Bearer " + secret + "\nAPI_KEY=" + secret)
		require.NotEmpty(t, hits, secret)
		for _, h := range hits {
			assert.NotContains(t, h.Excerpt, secret)
		}
	}
}

func TestReplaceNumbering(t *testing.T) {
	text := "a=" + fakeGitHub + "\nb=" + fakeAnthropic + "\nc=" + fakeGitHub + "\nDB_PASSWORD=" + fakeGeneric + "\n"
	out, hits := Replace(text)
	assert.Equal(t, "a=${SANDCAST_SECRET_1}\nb=${SANDCAST_SECRET_2}\nc=${SANDCAST_SECRET_1}\nDB_PASSWORD=${SANDCAST_SECRET_3}\n", out)
	require.Len(t, hits, 4)
	assert.Equal(t, []string{"${SANDCAST_SECRET_1}", "${SANDCAST_SECRET_2}", "${SANDCAST_SECRET_1}", "${SANDCAST_SECRET_3}"},
		[]string{hits[0].Placeholder, hits[1].Placeholder, hits[2].Placeholder, hits[3].Placeholder})
	assert.Equal(t, []int{1, 2, 3, 4}, []int{hits[0].Line, hits[1].Line, hits[2].Line, hits[3].Line})
	assert.Empty(t, Scan(out), "placeholders are not hits")

	again, hits := Replace(out)
	assert.Equal(t, out, again, "replace is idempotent")
	assert.Empty(t, hits)
}

func TestReplaceQuotedAndPEM(t *testing.T) {
	out, hits := Replace(`{"token": "` + fakeGeneric + `"}` + "\n" + fakePEM + "\n")
	assert.Equal(t, `{"token": "${SANDCAST_SECRET_1}"}`+"\n${SANDCAST_SECRET_2}\n", out)
	require.Len(t, hits, 2)
	assert.Equal(t, KindPrivateKey, hits[1].Kind)
	assert.Equal(t, 2, hits[1].Line)
}

func TestReplaceURLQuery(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"middle parameter with fragment",
			"https://x/mcp?token=" + fakeGeneric + "&mode=ro#frag",
			"https://x/mcp?token=${SANDCAST_SECRET_1}&mode=ro#frag",
		},
		{
			"last parameter",
			"https://x/mcp?mode=ro&apikey=" + fakeGeneric,
			"https://x/mcp?mode=ro&apikey=${SANDCAST_SECRET_1}",
		},
		{
			"only parameter before fragment",
			"https://x/mcp?apikey=" + fakeGeneric + "#frag",
			"https://x/mcp?apikey=${SANDCAST_SECRET_1}#frag",
		},
		{
			"fragment parameters",
			"https://x/cb#access_token=" + fakeGeneric + "&token_type=bearer",
			"https://x/cb#access_token=${SANDCAST_SECRET_1}&token_type=bearer",
		},
		{
			"two secrets in one query",
			"url: https://x/mcp?token=" + fakeGeneric + "&api_key=other" + fakeGeneric + "&mode=ro",
			"url: https://x/mcp?token=${SANDCAST_SECRET_1}&api_key=${SANDCAST_SECRET_2}&mode=ro",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, hits := Replace(tt.in)
			assert.Equal(t, tt.want, out)
			require.NotEmpty(t, hits)
			assert.Equal(t, KindKeyValue, hits[0].Kind)
		})
	}

	// Outside a URL query an unquoted value keeps & and # characters.
	out, _ := Replace("DB_PASSWORD=" + fakeGeneric + "&more#chars")
	assert.Equal(t, "DB_PASSWORD=${SANDCAST_SECRET_1}", out)
}

func TestReplaceURLPassword(t *testing.T) {
	out, hits := Replace("source: https://deploy:hunter2@git.example.com/org/repo.git#main")
	assert.Equal(t, "source: https://deploy:${SANDCAST_SECRET_1}@git.example.com/org/repo.git#main", out)
	require.Len(t, hits, 1)
	assert.Equal(t, KindURLSecret, hits[0].Kind)

	// A token shape in the password keeps its specific kind.
	out, hits = Replace("https://x:" + fakeGitHub + "@github.com/org/repo")
	assert.Equal(t, "https://x:${SANDCAST_SECRET_1}@github.com/org/repo", out)
	require.Len(t, hits, 1)
	assert.Equal(t, KindGitHub, hits[0].Kind)
}

func TestReplaceNoSecrets(t *testing.T) {
	text := "image: " + fakeDigest + "\ntoken: ${GITHUB_TOKEN}\n"
	out, hits, next := ReplaceFrom(text, 4)
	assert.Equal(t, text, out)
	assert.Empty(t, hits)
	assert.Equal(t, 4, next)
}

func TestReplaceFrom(t *testing.T) {
	out, hits, next := ReplaceFrom(fakeGitHub+" "+fakeGitLab+" "+fakeGitHub, 5)
	assert.Equal(t, "${SANDCAST_SECRET_5} ${SANDCAST_SECRET_6} ${SANDCAST_SECRET_5}", out)
	assert.Len(t, hits, 3)
	assert.Equal(t, 7, next)

	out, _, next = ReplaceFrom(fakeAWS, next)
	assert.Equal(t, "${SANDCAST_SECRET_7}", out)
	assert.Equal(t, 8, next)

	out, _, next = ReplaceFrom(fakeAWS, 0)
	assert.Equal(t, "${SANDCAST_SECRET_1}", out, "numbering starts at 1 at the least")
	assert.Equal(t, 2, next)
}

func TestReplacerSharesNumbersAcrossValues(t *testing.T) {
	r := NewReplacer(1)
	a, _ := r.Replace(fakeGitHub)
	b, _ := r.Replace(fakeAnthropic)
	c, _ := r.Replace("Bearer " + fakeBearer + " " + fakeGitHub)
	assert.Equal(t, "${SANDCAST_SECRET_1}", a)
	assert.Equal(t, "${SANDCAST_SECRET_2}", b)
	assert.Equal(t, "Bearer ${SANDCAST_SECRET_3} ${SANDCAST_SECRET_1}", c)
	assert.Equal(t, 4, r.Next())
}

func TestIsPlaceholder(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"${SANDCAST_SECRET_1}", true},
		{"${SANDCAST_SECRET_42}", true},
		{Placeholder(7), true},
		{"${SANDCAST_SECRET_0}", false},
		{"${SANDCAST_SECRET_}", false},
		{"${SANDCAST_SECRET_1} ", false},
		{"$SANDCAST_SECRET_1", false},
		{"${GITHUB_TOKEN}", false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, IsPlaceholder(tt.in), tt.in)
	}
}

func TestScanYAMLKeyPaths(t *testing.T) {
	doc := `harnesses:
  - name: claude
    mcp_servers:
      - name: fs
        env:
          HOME_DIR: /home/user
      - name: gh
        env:
          GITHUB_TOKEN: ` + fakeGitHub + `
          API_KEY: "` + fakeGeneric + `"
          OTHER_TOKEN: ${SANDCAST_SECRET_1}
          PASSWORD_FILE: /run/secrets/database-password
          "my.secret": ` + fakeGeneric + `
        headers:
          Authorization: Bearer ` + fakeBearer + `
        args: ["--verbose", "--api-key=` + fakeGeneric + `"]
# leftover from a copy and paste: ` + fakeGitLab + `
notes: |
  first line
  token=` + fakeGeneric + `
`
	hits, err := ScanYAML([]byte(doc))
	require.NoError(t, err)
	assert.Equal(t, []Hit{
		{Kind: KindGitHub, Path: "harnesses[0].mcp_servers[1].env.GITHUB_TOKEN", Line: 9, Excerpt: "ghp_***"},
		{Kind: KindKeyValue, Path: "harnesses[0].mcp_servers[1].env.API_KEY", Line: 10, Excerpt: "API_KEY: ***"},
		{Kind: KindKeyValue, Path: `harnesses[0].mcp_servers[1].env["my.secret"]`, Line: 13, Excerpt: "my.secret: ***"},
		{Kind: KindBearer, Path: "harnesses[0].mcp_servers[1].headers.Authorization", Line: 15, Excerpt: "Bearer ***"},
		{Kind: KindKeyValue, Path: "harnesses[0].mcp_servers[1].args[1]", Line: 16, Excerpt: "api-key=***"},
		{Kind: KindGitLab, Line: 17, Excerpt: "glpat-***"},
		{Kind: KindKeyValue, Path: "notes", Line: 20, Excerpt: "token=***"},
	}, hits)
}

func TestScanYAMLBlockValueUnderSecretKey(t *testing.T) {
	doc := "creds:\n  password: |\n    " + fakeGeneric + "\n"
	hits, err := ScanYAML([]byte(doc))
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "creds.password", hits[0].Path)
	assert.Equal(t, KindKeyValue, hits[0].Kind)
}

func TestScanYAMLAnchorsAndAliases(t *testing.T) {
	doc := "base: &b\n  token: " + fakeGeneric + "\nother:\n  <<: *b\nalias: *b\n"
	hits, err := ScanYAML([]byte(doc))
	require.NoError(t, err)
	require.Len(t, hits, 1, "the anchored value is reported once")
	assert.Equal(t, "base.token", hits[0].Path)
}

func TestScanYAMLClean(t *testing.T) {
	doc := `schema_version: 1
name: demo
base_image: ` + fakeDigest + `
env:
  GITHUB_TOKEN: ${GITHUB_TOKEN}
  API_KEY: ${SANDCAST_SECRET_2}
  password_file: /run/secrets/password
  max_tokens: 4096
`
	hits, err := ScanYAML([]byte(doc))
	require.NoError(t, err)
	assert.Empty(t, hits)
}

func TestScanYAMLInvalid(t *testing.T) {
	_, err := ScanYAML([]byte("a: [unclosed\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse YAML")
}

func TestSecretKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"GITHUB_TOKEN", true},
		{"token", true},
		{"accessToken", true},
		{"ACCESSTOKEN", true},
		{"client_secret", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"password", true},
		{"DB_PASSWD", true},
		{"API_KEY", true},
		{"apiKey", true},
		{"APIKey", true},
		{"x-api-key", true},
		{"apikey", true},
		{"github_token_value", true},
		{"tokenizer", false},
		{"max_tokens", false},
		{"password_file", false},
		{"secretName", false},
		{"token_url", false},
		{"TOKEN_ENV", false},
		{"api_key_id", false},
		{"key", false},
		{"monkey", false},
		{"ACCESS_KEY", true},
		{"GITHUB_PAT", true},
		{"BASIC_AUTH", true},
		{"GOOGLE_APPLICATION_CREDENTIALS", true},
		{"X-API-Key", true},
		{"--api-key", true},
		{"SECRET_NAME", false},
		{"LOG_LEVEL", false},
		{"KEYBOARD", false},
		{"auth", false},
		{"OPENAI_KEY", true},
		{"client_credentials_json", true},
		{"AWS_CREDENTIALS_B64", true},
		{"aws_credentials_b64", false},
		{"ROUTING_KEY", false},
		{"ENABLED_AUTH", false},
		{"REQUIRED_AUTH", false},
		{"component_key", false},
		{"cache_key", false},
		{"auth_key", true},
		{"basic_auth", true},
		{"access_key", true},
		{"access-key", true},
		{"privateKey", true},
		{"accessKey", true},
		{"basicAuth", true},
		{"X-Auth-Key", true},
		{"license-key", true},
		{"sort_key", false},
		{"primary_key", false},
		{"public_key", false},
		{"credential", false},
		{"credentials", false},
		{"GOOGLE_APPLICATION_CREDENTIALS", true},
		{"AWS_CREDENTIALS", true},
		{"client_credentials", true},
		{"clientCredential", true},
		{"aws_credentials", true},
		{"dbCredentials", true},
		{"credential_signing", false},
		{"SMTP_PASS", true},
		{"MYSQL_PWD", true},
		{"db_pass", false},
		{"bypass", false},
		{"PWD", false},
		{"PUBLIC_KEY", false},
		{"SSH_PUBLIC_KEY", false},
		{"STRIPE_PUBLISHABLE_KEY", false},
		{"CACHE_KEY", false},
		{"IDEMPOTENCY_KEY", false},
		{"NO_AUTH", false},
		{"OPENAI_KEY", true},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, SecretName(tt.key), tt.key)
	}
}

func TestSplitWords(t *testing.T) {
	assert.Equal(t, []string{"open", "ai", "api", "key"}, splitWords("OpenAIApiKey"))
	assert.Equal(t, []string{"x", "api", "key"}, splitWords("x-api-key"))
	assert.Equal(t, []string{"s3", "bucket", "token"}, splitWords("s3Bucket.token"))
}

func TestEnvRef(t *testing.T) {
	tests := []struct {
		value string
		name  string
		ok    bool
	}{
		{"${GITHUB_TOKEN}", "GITHUB_TOKEN", true},
		{"$GITHUB_TOKEN", "GITHUB_TOKEN", true},
		{"${TOKEN:-none}", "TOKEN", true},
		{"Bearer ${TOKEN}", "", false},
		{"literal", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			name, ok := EnvRef(tt.value)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.name, name)
		})
	}
}

func TestSecretParam(t *testing.T) {
	for name, want := range map[string]bool{
		"key": true, "auth": true, "pat": true, "access-key": true, "api-key": true, "token": true,
		"sort-key": false, "mode": false, "key-file": false, "": false,
		"account-key": true, "admin-key": true, "subscription-key": true, "Ocp-Apim-Subscription-Key": true,
		"consumer-key": true, "write-key": true, "sas-key": true, "openai-key": true, "proxy-auth": true, "ado-pat": true,
		"public-key": false, "cache-key": false, "no-auth": false, "idempotency-key": false,
		"routing-key": false, "primary-key": false, "foreign-key": false, "object-key": false,
		"partition-key": false, "component-key": false, "use-auth": false, "with-auth": false,
		"without-auth": false, "allow-auth": false, "enable-auth": false, "require-auth": false,
		"enabled-auth": false, "disabled-auth": false, "required-auth": false,
	} {
		assert.Equal(t, want, SecretParam(name), name)
	}
}

func TestScanBasicAuthAndPGP(t *testing.T) {
	hits := Scan(`["mcp-remote", "--header", "Authorization: Basic dXNlcjpwYXNzd29yZDEyMw=="]`)
	require.Len(t, hits, 1)
	assert.Equal(t, KindBasicAuth, hits[0].Kind)
	assert.NotContains(t, hits[0].Excerpt, "dXNlcjpw")

	assert.Empty(t, Scan("basic setup instructions"), "a word after basic is not base64 of user:password")
	assert.Empty(t, Scan("Basic aGVsbG93b3JsZA=="), "no colon in the decoded value")

	pgp := "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nlQOYBF...\n-----END PGP PRIVATE KEY BLOCK-----\n"
	hits = Scan(pgp)
	require.Len(t, hits, 1)
	assert.Equal(t, KindPrivateKey, hits[0].Kind)
}

func TestScanYAMLIgnoresCredentialNameLists(t *testing.T) {
	doc := "discovery:\n  credentials: [access_token, refresh_token, account_id, id_token]\n" +
		"refresh:\n  additional_outputs:\n    - credential: secret_access_key\n"
	hits, err := ScanYAML([]byte(doc))
	require.NoError(t, err)
	assert.Empty(t, hits)

	hits, err = ScanYAML([]byte("env:\n  AWS_CREDENTIALS: abcdefghijklmnop\n"))
	require.NoError(t, err)
	assert.Len(t, hits, 1, "an upper-case credentials env var still holds a value")
}

func TestScanAuthorizationHeader(t *testing.T) {
	for _, text := range []string{
		`curl -H "Authorization: Token abcdef0123456789"`,
		`command: [srv, --header, "authorization: token 9f8e7d6c5b4a"]`,
		`Proxy-Authorization: Negotiate YIIFzgYGKwYBBQUC1234`,
	} {
		hits := Scan(text)
		require.Len(t, hits, 1, text)
		assert.Equal(t, KindAuthHeader, hits[0].Kind, text)
	}
	hits := Scan("Authorization: Bearer abcdefghijklmnop1234")
	require.Len(t, hits, 1)
	assert.Equal(t, KindBearer, hits[0].Kind, "Bearer keeps its own shape")
	for _, text := range []string{
		"Authorization: Bearer ${SANDCAST_SECRET_1}",
		"Authorization: Bearer $GITHUB_TOKEN",
		"the Authorization: required header",
		"Authorization: Bearer {token}",
		"Authorization: Token {{api_token_1}}",
		"header: Authorization",
		"Authorization: Bearer $GH_TOKEN_2",
		"Authorization: Token <your-token-1>",
	} {
		assert.Empty(t, Scan(text), text)
	}
	assert.True(t, IsCredentialHeader("Proxy-Authorization"))
	assert.True(t, IsCredentialHeader("X-Api-Key"))
	assert.False(t, IsCredentialHeader("Content-Type"))
}

func TestScanPasswordNamesAndAuthBlobs(t *testing.T) {
	for _, text := range []string{
		"export SMTP_PASS=Hunter2Hunter2xyz",
		"export MYSQL_PWD=Hunter2Hunter2xyz",
		`export DOCKER_AUTH_CONFIG='{"auths":{"ghcr.io":{"auth":"dXNlcjpwYXNzd29yZDEyMw=="}}}'`,
		"_auth=dXNlcm5hbWU6cGFzc3dvcmQ=",
		`password: "[Xy9abc123defQ]"`,
		"export ADMIN_PASSWORD={Xy9abc123defQ}",
	} {
		assert.NotEmpty(t, Scan(text), text)
	}
	hits, err := ScanYAML([]byte("password:\n  - Xy9abc123defQrs\n"))
	require.NoError(t, err)
	assert.Len(t, hits, 1, "a list item keeps the key of its list")
	hits, err = ScanYAML([]byte("{\"dbCredentials\": \"Xy9abc123defQrsT\"}"))
	require.NoError(t, err)
	assert.Len(t, hits, 1)

	assert.Empty(t, Scan("auth: required-for-every-call"), "a non-base64 auth value is a plain field")
	assert.Empty(t, Scan("export PWD=/home/user/project"))
	assert.Empty(t, Scan("token: [/run/secrets/github-token]"), "a path inside an inline list")
	assert.Empty(t, Scan("password: [abcdefghijk]"), "an item below the minimum length")
}

func TestHeaderSecret(t *testing.T) {
	for _, tt := range []struct{ name, value, prefix, secret string }{
		{"Authorization", " Token abc123", "Token ", "abc123"},
		{"authorization", "abc123", "", "abc123"},
		{"Proxy-Authorization", "Basic dXNlcjpw", "Basic ", "dXNlcjpw"},
		{"Cookie", " session=abc; theme=dark", "", "session=abc; theme=dark"},
		{"X-Api-Key", "k1 k2", "", "k1 k2"},
		{"Authorization", "ab12cd34 ef56", "", "ab12cd34 ef56"},
	} {
		prefix, secret := HeaderSecret(tt.name, tt.value)
		assert.Equal(t, tt.prefix, prefix, tt.name)
		assert.Equal(t, tt.secret, secret, tt.name)
	}
}

func TestScanQueryAndFlagParams(t *testing.T) {
	for _, text := range []string{
		`curl "https://maps.example.com/api?key=abcdef0123456789abcd"`,
		"--key=abcdef0123456789abcd",
		"az storage list --account-key=abcdef0123456789abcd==",
		"url: https://h/x?subscription-key=abcdef0123456789",
		"AIzaSyA1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q",
		"export GPG_PASSPHRASE=correcthorsebattery",
		"Cookie: session=abcdef123456",
	} {
		assert.NotEmpty(t, Scan(text), text)
	}
	for _, text := range []string{
		"sort_key=customer-name-ascending",
		"https://h/x?sort-key=customer-name-ascending",
		"Cookie: theme=darkmode",
	} {
		assert.Empty(t, Scan(text), text)
	}
}

func TestFinalReviewHeuristics(t *testing.T) {
	assert.True(t, IsCredentialHeader("Ocp-Apim-Subscription-Key"))
	assert.True(t, IsCredentialHeader("X-Auth"))
	assert.False(t, IsCredentialHeader("Idempotency-Key"))

	assert.Empty(t, Scan("export GOOGLE_APPLICATION_CREDENTIALS=$HOME/.config/gcloud/adc.json"), "a variable-prefixed path")
	assert.Empty(t, Scan("export SSH_KEY=${HOME}/.ssh/id_ed25519"))
	assert.False(t, SecretName("SKIP_AUTH"))
	assert.False(t, SecretName("DISABLE_AUTH"))
	assert.True(t, SecretName("BASIC_AUTH"))

	out, hits := Replace("{token: abcdefghijkl123}")
	require.Len(t, hits, 1)
	assert.Equal(t, "{token: ${SANDCAST_SECRET_1}}", out, "the closing brace survives")
	out, _ = Replace("token: [abcdefghijkl123, other]")
	assert.Equal(t, "token: [${SANDCAST_SECRET_1}, other]", out, "the opening bracket survives")
}
