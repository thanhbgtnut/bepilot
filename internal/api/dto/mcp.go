package dto

import "time"

// MCPServerRequest is the body of POST /v1/mcp/servers. Re-posting an existing
// name replaces its configuration and redials.
type MCPServerRequest struct {
	Name      string `json:"name"`
	Transport string `json:"transport"` // stdio | sse | streamable_http

	// stdio
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// sse | streamable_http
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	ToolAllowlist []string `json:"tool_allowlist,omitempty"`
	AlwaysBound   *bool    `json:"always_bound,omitempty"`
}

// MCPServerView is one attached server as returned by the MCP endpoints.
type MCPServerView struct {
	Name        string    `json:"name"`
	Transport   string    `json:"transport"`
	Source      string    `json:"source"` // file | api
	Tools       []string  `json:"tools"`
	ConnectedAt time.Time `json:"connected_at"`
}

// MCPServerListResponse is the body of GET /v1/mcp/servers.
type MCPServerListResponse struct {
	Data []MCPServerView `json:"data"`
}
