package api

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/config"
	appmcp "github.com/thanhenti/bepilot/internal/mcp"
	"github.com/thanhenti/bepilot/internal/store"
)

// ListMCPServers handles GET /v1/mcp/servers — every attached MCP server,
// whether declared in the config file or added through this API.
//
// @Summary   List attached MCP servers
// @Tags      MCP
// @Produce   json
// @Success   200  {object}  dto.MCPServerListResponse
// @Failure   401  {object}  dto.ErrorResponse
// @Failure   503  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/mcp/servers [get]
func (h *Handlers) ListMCPServers(ctx context.Context, c *app.RequestContext) {
	if h.MCP == nil {
		h.mcpDisabled(c)
		return
	}
	statuses := h.MCP.List()
	out := make([]dto.MCPServerView, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, mcpView(s))
	}
	c.JSON(consts.StatusOK, dto.MCPServerListResponse{Data: out})
}

// AddMCPServer handles POST /v1/mcp/servers — attach a new MCP server (or
// replace one already added through the API) without restarting. The server is
// dialed and its tools registered synchronously; they are available on the next
// message. The spec is persisted so it is reattached on the next restart.
//
// @Summary   Attach or update an MCP server
// @Tags      MCP
// @Accept    json
// @Produce   json
// @Param     request  body      dto.MCPServerRequest  true  "MCP server connection"
// @Success   200      {object}  dto.MCPServerView
// @Failure   400      {object}  dto.ErrorResponse
// @Failure   401      {object}  dto.ErrorResponse
// @Failure   409      {object}  dto.ErrorResponse
// @Failure   502      {object}  dto.ErrorResponse
// @Failure   503      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/mcp/servers [post]
func (h *Handlers) AddMCPServer(ctx context.Context, c *app.RequestContext) {
	if h.MCP == nil {
		h.mcpDisabled(c)
		return
	}
	var req dto.MCPServerRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}

	spec := config.MCPServerSpec{
		Name:          req.Name,
		Transport:     req.Transport,
		Command:       req.Command,
		Args:          req.Args,
		Env:           req.Env,
		URL:           req.URL,
		Headers:       req.Headers,
		ToolAllowlist: req.ToolAllowlist,
	}
	if err := spec.Validate(); err != nil {
		h.badRequest(c, err.Error())
		return
	}

	// A file-declared server owns its name; the API must not shadow it.
	if src, ok := h.MCP.SourceOf(spec.Name); ok && src == appmcp.SourceFile {
		c.JSON(consts.StatusConflict, dto.NewError("conflict",
			"an MCP server named "+spec.Name+" is declared in the config file; edit that file instead"))
		return
	}

	if err := h.MCP.Replace(ctx, spec, appmcp.SourceAPI); err != nil {
		c.JSON(consts.StatusBadGateway, dto.NewError("mcp_error", err.Error()))
		return
	}

	if _, err := h.Store.MCP.Upsert(ctx, spec.Name, spec.Transport, true, specToMap(spec)); err != nil {
		// The server is attached and working; persistence failed. Detach so the
		// runtime state matches what will survive a restart.
		_ = h.MCP.Remove(spec.Name)
		h.serverError(c, err)
		return
	}

	st, _ := findStatus(h.MCP.List(), spec.Name)
	c.JSON(consts.StatusOK, mcpView(st))
}

// DeleteMCPServer handles DELETE /v1/mcp/servers/{name} — detach a server added
// through the API and forget its persisted spec. Servers declared in the config
// file cannot be removed here.
//
// @Summary   Detach an API-registered MCP server
// @Tags      MCP
// @Produce   json
// @Param     name  path      string  true  "Server name"
// @Success   200   {object}  dto.DeleteResponse
// @Failure   401   {object}  dto.ErrorResponse
// @Failure   404   {object}  dto.ErrorResponse
// @Failure   409   {object}  dto.ErrorResponse
// @Failure   503   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/mcp/servers/{name} [delete]
func (h *Handlers) DeleteMCPServer(ctx context.Context, c *app.RequestContext) {
	if h.MCP == nil {
		h.mcpDisabled(c)
		return
	}
	name := c.Param("name")

	if src, ok := h.MCP.SourceOf(name); ok && src == appmcp.SourceFile {
		c.JSON(consts.StatusConflict, dto.NewError("conflict",
			"MCP server "+name+" is declared in the config file; remove it there"))
		return
	}

	err := h.Store.MCP.Delete(ctx, name)
	detached := h.MCP.Remove(name)
	if errors.Is(err, store.ErrNotFound) && detached != nil {
		h.notFound(c, "MCP server "+name+" is not registered")
		return
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.DeleteResponse{ID: name, Deleted: true})
}

func (h *Handlers) mcpDisabled(c *app.RequestContext) {
	c.JSON(consts.StatusServiceUnavailable, dto.NewError("not_available",
		"MCP is not enabled on this deployment (set mcp.enabled in the config)"))
}

func mcpView(s appmcp.Status) dto.MCPServerView {
	tools := s.Tools
	if tools == nil {
		tools = []string{}
	}
	return dto.MCPServerView{
		Name:        s.Name,
		Transport:   s.Transport,
		Source:      string(s.Source),
		Tools:       tools,
		ConnectedAt: s.ConnectedAt,
	}
}

func findStatus(list []appmcp.Status, name string) (appmcp.Status, bool) {
	for _, s := range list {
		if s.Name == name {
			return s, true
		}
	}
	return appmcp.Status{}, false
}

// specToMap converts a spec to the JSON shape persisted in mcp_servers.spec.
func specToMap(s config.MCPServerSpec) map[string]any {
	m := map[string]any{
		"name":      s.Name,
		"transport": s.Transport,
	}
	if s.Command != "" {
		m["command"] = s.Command
	}
	if len(s.Args) > 0 {
		m["args"] = s.Args
	}
	if len(s.Env) > 0 {
		m["env"] = s.Env
	}
	if s.URL != "" {
		m["url"] = s.URL
	}
	if len(s.Headers) > 0 {
		m["headers"] = s.Headers
	}
	if len(s.ToolAllowlist) > 0 {
		m["tool_allowlist"] = s.ToolAllowlist
	}
	return m
}
