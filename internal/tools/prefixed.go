package tools

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Prefixed wraps an invokable tool so it is exposed to the model under a
// namespaced name (e.g. "mcp__github__search") while delegating execution to
// the underlying tool unchanged. This keeps tools from different sources
// (multiple MCP servers) from colliding in the registry and in the model's
// tool list.
//
// The underlying tool still calls its own original name on the wire, so only
// Info().Name is rewritten here. MCP tools are invokable-only; eino's tool node
// adapts them to the streaming path when needed.
func Prefixed(t tool.InvokableTool, name string) tool.InvokableTool {
	return &prefixed{base: t, name: name}
}

type prefixed struct {
	base tool.InvokableTool
	name string
}

func (p *prefixed) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := p.base.Info(ctx)
	if err != nil {
		return nil, err
	}
	cp := *info
	cp.Name = p.name
	return &cp, nil
}

func (p *prefixed) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	return p.base.InvokableRun(ctx, argumentsInJSON, opts...)
}

var _ tool.InvokableTool = (*prefixed)(nil)
