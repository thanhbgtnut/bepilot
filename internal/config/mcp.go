package config

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MCPServerSpecFromMap decodes the JSON object persisted in mcp_servers.spec
// back into a spec. Used to reattach API-registered servers on startup.
func MCPServerSpecFromMap(m map[string]any) (MCPServerSpec, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return MCPServerSpec{}, err
	}
	var s MCPServerSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return MCPServerSpec{}, err
	}
	return s, s.Validate()
}

// LoadMCPServers reads the MCP server-list file at path, expands ${ENV}
// references the same way the main config does, and returns the enabled,
// validated specs. A missing file is not an error: it returns nil so the API
// remains the only source.
func LoadMCPServers(path string) ([]MCPServerSpec, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read mcp file %s: %w", path, err)
	}
	expanded := os.Expand(string(raw), os.Getenv)

	var f MCPServersFile
	if err := yaml.Unmarshal([]byte(expanded), &f); err != nil {
		return nil, fmt.Errorf("parse mcp file %s: %w", path, err)
	}

	seen := map[string]bool{}
	out := make([]MCPServerSpec, 0, len(f.Servers))
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
