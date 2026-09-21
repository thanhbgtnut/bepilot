// Package tools provides the built-in tool set, the registry that owns every
// tool the process knows about, and the tool-search mechanism that gates
// external tools.
//
// Tools live in one of two tiers:
//
//   - Built-in tools (current_time, calculate, json_validate, http_fetch,
//     load_skill, web_search and tool_search itself) are bound to the model on every turn.
//   - Deferred tools are everything registered at runtime — MCP servers and any
//     other external source. Their schemas are NOT sent to the model up front;
//     only their names are listed in the prompt. To use one, the model first
//     calls the built-in tool_search tool, which loads the matching tools'
//     schemas into the current Session so they can be called on the next step.
//     This keeps the context small no matter how many tools MCP servers expose.
//
// The registry is safe for concurrent use: built-ins are added once at startup,
// and dynamic sources may Register/Unregister deferred tools at runtime without
// restarting the process. Each agent turn takes a Session snapshot of the
// registry, so newly registered tools are discoverable on the next request.
package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/skills"
)

// Entry is one registered tool with its cached Info.
type Entry struct {
	Name string
	Desc string
	Info *schema.ToolInfo
	tool tool.InvokableTool
}

// Registry owns the process-wide set of tools.
type Registry struct {
	mu       sync.RWMutex
	builtin  map[string]*Entry
	deferred map[string]*Entry
}

// NewRegistry builds the built-in tool set. httpAllowlist limits http_fetch to
// the given hosts (empty = allow any host, which is only sensible in dev).
func NewRegistry(skillSvc *skills.Service, httpAllowlist []string) (*Registry, error) {
	r := &Registry{
		builtin:  map[string]*Entry{},
		deferred: map[string]*Entry{},
	}

	ct, err := newCurrentTimeTool()
	if err != nil {
		return nil, err
	}
	hf, err := newHTTPFetchTool(httpAllowlist)
	if err != nil {
		return nil, err
	}
	ls, err := newLoadSkillTool(skillSvc)
	if err != nil {
		return nil, err
	}
	ws, err := newWebSearchTool()
	if err != nil {
		return nil, err
	}
	calc, err := newCalculateTool()
	if err != nil {
		return nil, err
	}
	jv, err := newJSONValidateTool()
	if err != nil {
		return nil, err
	}
	ts, err := newToolSearchTool(r)
	if err != nil {
		return nil, err
	}
	for _, t := range []tool.InvokableTool{ct, calc, jv, hf, ls, ws, ts} {
		e, err := newEntry(context.Background(), t)
		if err != nil {
			return nil, err
		}
		r.builtin[e.Name] = e
	}
	return r, nil
}

// Register adds (or replaces) a deferred tool: it is discoverable through
// tool_search but never bound to the model until discovered. It returns the
// resolved tool name. A name that collides with a built-in tool is rejected.
func (r *Registry) Register(ctx context.Context, t tool.InvokableTool) (string, error) {
	e, err := newEntry(ctx, t)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.builtin[e.Name]; ok {
		return "", fmt.Errorf("tool %q collides with a built-in tool", e.Name)
	}
	r.deferred[e.Name] = e
	return e.Name, nil
}

// Unregister removes deferred tools by name. Built-in tools cannot be removed.
func (r *Registry) Unregister(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		delete(r.deferred, n)
	}
}

// Builtin returns the built-in tools, sorted by name.
func (r *Registry) Builtin() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedEntries(r.builtin)
}

// Deferred returns the deferred (externally registered) tools, sorted by name.
func (r *Registry) Deferred() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedEntries(r.deferred)
}

func newEntry(ctx context.Context, t tool.InvokableTool) (*Entry, error) {
	info, err := t.Info(ctx)
	if err != nil {
		return nil, err
	}
	return &Entry{Name: info.Name, Desc: info.Desc, Info: info, tool: t}, nil
}

func sortedEntries(m map[string]*Entry) []*Entry {
	out := make([]*Entry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
