// Package tools provides the built-in tool set and a registry that resolves
// tool names (including per-skill allowed tools) to eino tool implementations.
//
// The registry is safe for concurrent use: built-in tools are added once at
// startup, and dynamic sources (e.g. MCP servers) may Register/Unregister tools
// at runtime without restarting the process. The agent resolves tools from the
// registry on every turn, so newly registered tools are visible on the next
// request.
package tools

import (
	"context"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components/tool"

	"github.com/thanhenti/bepilot/internal/skills"
)

// builtinAlwaysBound are the built-in tools bound to every request regardless of
// retrieval. Kept intentionally small so the model chooses among them naturally.
var builtinAlwaysBound = []string{"current_time", "http_fetch", "load_skill", "web_search"}

// Registry owns the process-wide set of tools.
type Registry struct {
	mu          sync.RWMutex
	byName      map[string]tool.BaseTool
	alwaysBound []string // names bound on every turn (built-ins + dynamic)
}

// NewRegistry builds the default tool set. httpAllowlist limits http_fetch to
// the given hosts (empty = allow any host, which is only sensible in dev).
func NewRegistry(skillSvc *skills.Service, httpAllowlist []string) (*Registry, error) {
	r := &Registry{
		byName:      map[string]tool.BaseTool{},
		alwaysBound: append([]string{}, builtinAlwaysBound...),
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
	for _, t := range []tool.InvokableTool{ct, hf, ls, ws} {
		info, err := t.Info(context.Background())
		if err != nil {
			return nil, err
		}
		r.byName[info.Name] = t
	}
	return r, nil
}

// Register adds (or replaces) a tool. When alwaysBind is true the tool is bound
// on every turn; otherwise it is only bound when a skill lists it or a caller
// requests it by name. It returns the resolved tool name.
func (r *Registry) Register(ctx context.Context, t tool.BaseTool, alwaysBind bool) (string, error) {
	info, err := t.Info(ctx)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[info.Name] = t
	if alwaysBind {
		r.addAlwaysBoundLocked(info.Name)
	}
	return info.Name, nil
}

// Unregister removes tools by name (and drops them from the always-bound set).
func (r *Registry) Unregister(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		delete(r.byName, n)
		for i, b := range r.alwaysBound {
			if b == n {
				r.alwaysBound = append(r.alwaysBound[:i], r.alwaysBound[i+1:]...)
				break
			}
		}
	}
}

func (r *Registry) addAlwaysBoundLocked(name string) {
	for _, b := range r.alwaysBound {
		if b == name {
			return
		}
	}
	r.alwaysBound = append(r.alwaysBound, name)
}

// AlwaysBoundNames returns a copy of the names bound on every turn.
func (r *Registry) AlwaysBoundNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string{}, r.alwaysBound...)
}

// AlwaysBound resolves the always-bound names to tools.
func (r *Registry) AlwaysBound() []tool.BaseTool {
	return r.ByNames(r.AlwaysBoundNames())
}

// ByNames resolves a list of tool names to tools, skipping unknown names.
func (r *Registry) ByNames(names []string) []tool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []tool.BaseTool
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		if t, ok := r.byName[n]; ok {
			seen[n] = true
			out = append(out, t)
		}
	}
	return out
}

// Names lists every registered tool name, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byName))
	for n := range r.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Describe returns name→description for prompt construction.
func (r *Registry) Describe() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]string{}
	for n, t := range r.byName {
		if info, err := t.Info(context.Background()); err == nil {
			out[n] = info.Desc
		}
	}
	return out
}
