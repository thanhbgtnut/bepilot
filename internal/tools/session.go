package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Session is the per-turn view of the registry: a snapshot of the deferred
// tools plus the set of tools currently visible to the model. It starts with
// the built-ins visible; deferred tools become visible when tool_search
// discovers them (or when they are pinned, e.g. by a skill's allowed_tools).
//
// The tools node is handed every tool in the snapshot (Executable), but a
// deferred tool refuses to run until it is visible, and the model is only ever
// bound to the visible ones (WrapModel) — so what the model can call always
// matches what it has been shown a schema for.
type Session struct {
	mu      sync.RWMutex
	builtin map[string]*Entry
	catalog map[string]*Entry // deferred snapshot
	visible map[string]struct{}
}

// NewSession snapshots the registry for one turn.
func (r *Registry) NewSession() *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := &Session{
		builtin: make(map[string]*Entry, len(r.builtin)),
		catalog: make(map[string]*Entry, len(r.deferred)),
		visible: make(map[string]struct{}, len(r.builtin)),
	}
	for n, e := range r.builtin {
		s.builtin[n] = e
		s.visible[n] = struct{}{}
	}
	for n, e := range r.deferred {
		s.catalog[n] = e
	}
	return s
}

type sessionKey struct{}

// WithSession attaches the turn's session to ctx so tool_search can reach it.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

// SessionFrom returns the session attached by WithSession, or nil.
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	return s
}

// Pin makes tools visible without a tool_search round trip. Names that are not
// in the snapshot are still recorded, which is how per-request tools the
// registry does not own (AG-UI client tools) stay bound.
func (s *Session) Pin(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range names {
		s.visible[n] = struct{}{}
	}
}

// Discover makes deferred tools visible and returns the names that were
// actually in the catalog.
func (s *Session) Discover(names ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found []string
	for _, n := range names {
		if _, ok := s.catalog[n]; ok {
			s.visible[n] = struct{}{}
			found = append(found, n)
		}
	}
	return found
}

// Visible reports whether the model may currently call the named tool.
// tool_search is hidden while there is nothing to search.
func (s *Session) Visible(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if name == ToolSearchName && len(s.catalog) == 0 {
		return false
	}
	_, ok := s.visible[name]
	return ok
}

// Restore re-discovers tools that earlier turns loaded via tool_search, so a
// tool found once stays callable for the rest of the conversation. It reads the
// tool_search results out of the transcript the model is about to see; if
// trimming dropped an old result, the tool is simply searched for again.
func (s *Session) Restore(history []*schema.Message) {
	for _, m := range history {
		if m == nil || m.Role != schema.Tool || m.ToolName != ToolSearchName {
			continue
		}
		var res searchResult
		if err := json.Unmarshal([]byte(m.Content), &res); err != nil {
			continue
		}
		for _, match := range res.Matches {
			s.Discover(match.Name)
		}
	}
}

// Executable returns every tool the tools node must be able to run: the
// built-ins plus the whole deferred snapshot (gated on visibility).
func (s *Session) Executable() []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(s.builtin)+len(s.catalog))
	for _, e := range sortedEntries(s.builtin) {
		out = append(out, e.tool)
	}
	for _, e := range sortedEntries(s.catalog) {
		out = append(out, &gated{base: e.tool, name: e.Name, s: s})
	}
	return out
}

// DeferredNames lists the deferred tools that are not visible yet, sorted.
func (s *Session) DeferredNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for n := range s.catalog {
		if _, ok := s.visible[n]; !ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// VisibleDescriptions maps name → description for the registry tools the model
// can call right now.
func (s *Session) VisibleDescriptions() map[string]string {
	out := map[string]string{}
	for _, e := range s.builtin {
		if s.Visible(e.Name) {
			out[e.Name] = e.Desc
		}
	}
	for _, e := range s.catalog {
		if s.Visible(e.Name) {
			out[e.Name] = e.Desc
		}
	}
	return out
}

// gated runs a deferred tool only once it has been discovered.
type gated struct {
	base tool.InvokableTool
	name string
	s    *Session
}

func (g *gated) Info(ctx context.Context) (*schema.ToolInfo, error) { return g.base.Info(ctx) }

func (g *gated) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	if !g.s.Visible(g.name) {
		return "", fmt.Errorf("tool %q is not loaded yet: call %s with query \"select:%s\" first, then call it", g.name, ToolSearchName, g.name)
	}
	return g.base.InvokableRun(ctx, args, opts...)
}

// WrapModel returns a model that, on every call, binds only the tools that are
// visible at that moment. The ReAct agent binds tools once at construction;
// this is what lets a tool discovered mid-turn appear on the very next model
// step without rebuilding the agent.
func (s *Session) WrapModel(cm model.ToolCallingChatModel) model.ToolCallingChatModel {
	return &sessionModel{inner: cm, s: s}
}

type sessionModel struct {
	inner model.ToolCallingChatModel
	s     *Session
	all   []*schema.ToolInfo
}

func (m *sessionModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &sessionModel{inner: m.inner, s: m.s, all: infos}, nil
}

func (m *sessionModel) bound() (model.BaseChatModel, error) {
	visible := make([]*schema.ToolInfo, 0, len(m.all))
	for _, ti := range m.all {
		if m.s.Visible(ti.Name) {
			visible = append(visible, ti)
		}
	}
	if len(visible) == 0 {
		return m.inner, nil
	}
	return m.inner.WithTools(visible)
}

func (m *sessionModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	bm, err := m.bound()
	if err != nil {
		return nil, err
	}
	return bm.Generate(ctx, in, opts...)
}

func (m *sessionModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	bm, err := m.bound()
	if err != nil {
		return nil, err
	}
	return bm.Stream(ctx, in, opts...)
}

// IsCallbacksEnabled mirrors the wrapped model so eino neither double-fires nor
// drops model callbacks: a model that raises its own callbacks keeps doing so,
// and one that does not still gets the framework's instrumentation.
func (m *sessionModel) IsCallbacksEnabled() bool { return components.IsCallbacksEnabled(m.inner) }

var (
	_ model.ToolCallingChatModel = (*sessionModel)(nil)
	_ components.Checker         = (*sessionModel)(nil)
	_ tool.InvokableTool         = (*gated)(nil)
)
