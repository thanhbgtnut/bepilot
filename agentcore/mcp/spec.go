package mcp

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ServersFile is the schema of the server-list file read by LoadServers.
type ServersFile struct {
	Servers []ServerSpec `yaml:"servers"`
}

// ServerSpec describes one MCP server connection. It is used both for the
// file source and as the persisted/API shape.
type ServerSpec struct {
	Name      string `yaml:"name" json:"name"`
	Transport string `yaml:"transport" json:"transport"` // stdio | sse | streamable_http
	Disabled  bool   `yaml:"disabled" json:"disabled,omitempty"`

	// stdio transport
	Command string            `yaml:"command" json:"command,omitempty"`
	Args    []string          `yaml:"args" json:"args,omitempty"`
	Env     map[string]string `yaml:"env" json:"env,omitempty"`

	// sse / streamable_http transport
	URL     string            `yaml:"url" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`

	// ToolAllowlist, when non-empty, imports only these tool names from the
	// server (matched against the server-side name, before prefixing).
	ToolAllowlist []string `yaml:"tool_allowlist" json:"tool_allowlist,omitempty"`
}

// Validate checks a single server spec.
func (s ServerSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("mcp server: name is required")
	}
	for _, r := range s.Name {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return fmt.Errorf("mcp server %q: name may only contain letters, digits, '-' and '_'", s.Name)
		}
	}
	switch s.Transport {
	case "stdio":
		if s.Command == "" {
			return fmt.Errorf("mcp server %q: command is required for the stdio transport", s.Name)
		}
	case "sse", "streamable_http":
		if s.URL == "" {
			return fmt.Errorf("mcp server %q: url is required for the %s transport", s.Name, s.Transport)
		}
	default:
		return fmt.Errorf("mcp server %q: transport %q is not supported (want stdio | sse | streamable_http)", s.Name, s.Transport)
	}
	return nil
}

// ServerSpecFromMap decodes the JSON object persisted in mcp_servers.spec
// back into a spec. Used to reattach API-registered servers on startup.
func ServerSpecFromMap(m map[string]any) (ServerSpec, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return ServerSpec{}, err
	}
	var s ServerSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return ServerSpec{}, err
	}
	return s, s.Validate()
}

// LoadServers reads the MCP server-list file at path, expands ${ENV}
// references with the process environment, and returns the enabled,
// validated specs. A missing file is not an error: it returns nil so the API
// remains the only source.
func LoadServers(path string) ([]ServerSpec, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read mcp file %s: %w", path, err)
	}
	expanded := os.Expand(string(raw), os.Getenv)

	var f ServersFile
	if err := yaml.Unmarshal([]byte(expanded), &f); err != nil {
		return nil, fmt.Errorf("parse mcp file %s: %w", path, err)
	}

	seen := map[string]bool{}
	out := make([]ServerSpec, 0, len(f.Servers))
	for i, s := range f.Servers {
		if s.Disabled {
			continue
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("mcp file %s: servers[%d]: %w", path, i, err)
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("mcp file %s: duplicate server name %q", path, s.Name)
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return out, nil
}
