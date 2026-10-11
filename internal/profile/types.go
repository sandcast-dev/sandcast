package profile

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Profile mirrors the OpenShell v0.1.2 provider profile schema
// (ProviderTypeProfile in crates/openshell-providers/src/profiles.rs).
//
// Top-level fields follow the order of the upstream example files rather
// than the Rust struct, so a marshalled profile reads like its source file.
// Nested types follow the Rust struct order.
type Profile struct {
	ID               string            `yaml:"id"`
	ResourceVersion  uint64            `yaml:"resource_version,omitempty"`
	Annotations      map[string]string `yaml:"annotations,omitempty"`
	DisplayName      string            `yaml:"display_name"`
	Description      string            `yaml:"description,omitempty"`
	Category         string            `yaml:"category,omitempty"`
	InferenceCapable bool              `yaml:"inference_capable,omitempty"`
	Credentials      []Credential      `yaml:"credentials,omitempty"`
	Discovery        Discovery         `yaml:"discovery,omitempty"`
	Endpoints        []Endpoint        `yaml:"endpoints,omitempty"`
	Binaries         Binaries          `yaml:"binaries,omitempty"`
	// Source and Scope are set by a gateway on exported profiles.
	Source string `yaml:"source,omitempty"`
	Scope  string `yaml:"scope,omitempty"`
}

// Credential is one credential a provider profile declares.
//
// Refresh and TokenGrant are carried through unchanged (R-07): sandcast never
// writes them, checks only their enum values, and leaves the deep rules to
// the gateway, which re-validates on import.
type Credential struct {
	Name         string        `yaml:"name"`
	Description  string        `yaml:"description,omitempty"`
	EnvVars      []string      `yaml:"env_vars,omitempty,flow"`
	Required     bool          `yaml:"required,omitempty"`
	AuthStyle    string        `yaml:"auth_style,omitempty"`
	HeaderName   string        `yaml:"header_name,omitempty"`
	QueryParam   string        `yaml:"query_param,omitempty"`
	Refresh      yaml.MapSlice `yaml:"refresh,omitempty"`
	PathTemplate string        `yaml:"path_template,omitempty"`
	TokenGrant   yaml.MapSlice `yaml:"token_grant,omitempty"`
}

// Discovery lists the credentials a gateway may discover from the host.
type Discovery struct {
	Credentials []string `yaml:"credentials,omitempty,flow"`
}

// Endpoint is one network endpoint a provider profile grants.
//
// Rules and DenyRules distinguish an absent list (nil) from an explicitly
// empty one, because upstream lint rejects the latter.
type Endpoint struct {
	Host                         string                      `yaml:"host"`
	Port                         uint32                      `yaml:"port,omitempty"`
	Protocol                     string                      `yaml:"protocol,omitempty"`
	TLS                          string                      `yaml:"tls,omitempty"`
	Access                       string                      `yaml:"access,omitempty"`
	Enforcement                  string                      `yaml:"enforcement,omitempty"`
	Rules                        []Rule                      `yaml:"rules,omitempty"`
	AllowedIPs                   []string                    `yaml:"allowed_ips,omitempty"`
	Ports                        []uint32                    `yaml:"ports,omitempty,flow"`
	DenyRules                    []DenyRule                  `yaml:"deny_rules,omitempty"`
	AllowEncodedSlash            bool                        `yaml:"allow_encoded_slash,omitempty"`
	WebsocketCredentialRewrite   bool                        `yaml:"websocket_credential_rewrite,omitempty"`
	RequestBodyCredentialRewrite bool                        `yaml:"request_body_credential_rewrite,omitempty"`
	AllowUninspectedCredentials  bool                        `yaml:"allow_uninspected_credentials,omitempty"`
	PersistedQueries             string                      `yaml:"persisted_queries,omitempty"`
	GraphqlPersistedQueries      map[string]GraphqlOperation `yaml:"graphql_persisted_queries,omitempty"`
	GraphqlMaxBodyBytes          uint32                      `yaml:"graphql_max_body_bytes,omitempty"`
	JSONRPCMaxBodyBytes          uint32                      `yaml:"json_rpc_max_body_bytes,omitempty"`
	MCP                          *MCPOptions                 `yaml:"mcp,omitempty"`
	Path                         string                      `yaml:"path,omitempty"`
	CredentialSigning            string                      `yaml:"credential_signing,omitempty"`
	SigningService               string                      `yaml:"signing_service,omitempty"`
	SigningRegion                string                      `yaml:"signing_region,omitempty"`
}

// MCPOptions holds the MCP-specific options of an endpoint with protocol mcp.
// Versions distinguishes an absent list (nil) from an explicitly empty one.
type MCPOptions struct {
	Versions                []string `yaml:"versions,omitempty,flow"`
	StrictToolNames         *bool    `yaml:"strict_tool_names,omitempty"`
	AllowAllKnownMCPMethods *bool    `yaml:"allow_all_known_mcp_methods,omitempty"`
}

// Rule is one L7 allow rule.
type Rule struct {
	Allow *Allow `yaml:"allow,omitempty,flow"`
}

// Allow is the request selector of an L7 allow rule.
type Allow struct {
	Method        string                  `yaml:"method,omitempty"`
	Path          string                  `yaml:"path,omitempty"`
	Command       string                  `yaml:"command,omitempty"`
	Query         map[string]QueryMatcher `yaml:"query,omitempty"`
	OperationType string                  `yaml:"operation_type,omitempty"`
	OperationName string                  `yaml:"operation_name,omitempty"`
	Fields        []string                `yaml:"fields,omitempty"`
	Params        map[string]QueryMatcher `yaml:"params,omitempty"`
	Tool          *QueryMatcher           `yaml:"tool,omitempty"`
}

// DenyRule is one L7 deny rule. It carries the selector fields directly.
type DenyRule struct {
	Method        string                  `yaml:"method,omitempty"`
	Path          string                  `yaml:"path,omitempty"`
	Command       string                  `yaml:"command,omitempty"`
	Query         map[string]QueryMatcher `yaml:"query,omitempty"`
	OperationType string                  `yaml:"operation_type,omitempty"`
	OperationName string                  `yaml:"operation_name,omitempty"`
	Fields        []string                `yaml:"fields,omitempty"`
	Params        map[string]QueryMatcher `yaml:"params,omitempty"`
	Tool          *QueryMatcher           `yaml:"tool,omitempty"`
}

// GraphqlOperation is one registered GraphQL persisted query.
type GraphqlOperation struct {
	OperationType string   `yaml:"operation_type,omitempty"`
	OperationName string   `yaml:"operation_name,omitempty"`
	Fields        []string `yaml:"fields,omitempty"`
}

// QueryMatcher matches a query parameter, an MCP params value or an MCP tool
// name. In YAML it is either a scalar glob or a mapping with glob or any.
type QueryMatcher struct {
	Glob string
	Any  []string
}

// queryMatcherObject is the mapping form of QueryMatcher.
type queryMatcherObject struct {
	Glob string   `yaml:"glob,omitempty"`
	Any  []string `yaml:"any,omitempty"`
}

// UnmarshalYAML accepts the scalar and the mapping form.
func (m *QueryMatcher) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		*m = QueryMatcher{Glob: v}
		return nil
	case yaml.MapSlice, map[string]any:
		var obj queryMatcherObject
		if err := unmarshal(&obj); err != nil {
			return err
		}
		*m = QueryMatcher(obj)
		return nil
	default:
		return fmt.Errorf("matcher must be a string or a mapping with glob or any, got %T", raw)
	}
}

// MarshalYAML writes the scalar form when only a glob is set, as the upstream
// examples do, and the mapping form otherwise.
func (m QueryMatcher) MarshalYAML() (any, error) {
	if len(m.Any) == 0 && m.Glob != "" {
		return m.Glob, nil
	}
	return queryMatcherObject(m), nil
}

// Binaries is the list of executable paths or globs allowed to reach a
// profile's endpoints. Entries are scalar strings; the transitional
// `{path: X}` form is normalized to X on load.
type Binaries []string

// UnmarshalYAML normalizes `{path: X}` entries to scalars and rejects any
// other key, with the upstream message.
func (b *Binaries) UnmarshalYAML(unmarshal func(any) error) error {
	var raw []any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	out := make(Binaries, 0, len(raw))
	for i, item := range raw {
		path, err := binaryPath(item)
		if err != nil {
			return fmt.Errorf("binaries[%d]: %w", i, err)
		}
		out = append(out, path)
	}
	*b = out
	return nil
}

// binaryPath extracts the path of one binaries entry.
func binaryPath(item any) (string, error) {
	switch v := item.(type) {
	case string:
		return v, nil
	case yaml.MapSlice:
		fields := make(map[string]any, len(v))
		for _, kv := range v {
			fields[fmt.Sprint(kv.Key)] = kv.Value
		}
		return binaryPathFromFields(fields)
	case map[string]any:
		return binaryPathFromFields(v)
	default:
		return "", fmt.Errorf("binary entry must be a path string or a mapping with path, got %T", item)
	}
}

// binaryPathFromFields validates the mapping form of a binaries entry.
func binaryPathFromFields(fields map[string]any) (string, error) {
	var extra []string
	for k := range fields {
		if k != "path" {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return "", fmt.Errorf("unsupported provider profile binary fields: %s; binaries accept only 'path' and the deprecated 'harness' field was removed in 0.1.0",
			strings.Join(extra, ", "))
	}
	raw, ok := fields["path"]
	if !ok {
		return "", errors.New(`missing field "path"`)
	}
	path, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("binary path must be a string, got %T", raw)
	}
	return path, nil
}

// requiredFields records the presence of the fields upstream deserialization
// requires (serde fields without a default), so that a missing field fails
// to parse as it does upstream instead of decoding to an empty string.
type requiredFields struct {
	ID          *string `yaml:"id"`
	DisplayName *string `yaml:"display_name"`
	Credentials []struct {
		Name *string `yaml:"name"`
	} `yaml:"credentials"`
	Endpoints []struct {
		Host *string `yaml:"host"`
	} `yaml:"endpoints"`
}

// missing returns an error naming the first required field that is absent.
func (r requiredFields) missing() error {
	if r.ID == nil {
		return errors.New(`missing field "id"`)
	}
	if r.DisplayName == nil {
		return errors.New(`missing field "display_name"`)
	}
	for i, c := range r.Credentials {
		if c.Name == nil {
			return fmt.Errorf(`credentials[%d]: missing field "name"`, i)
		}
	}
	for i, e := range r.Endpoints {
		if e.Host == nil {
			return fmt.Errorf(`endpoints[%d]: missing field "host"`, i)
		}
	}
	return nil
}
