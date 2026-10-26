package agent

import (
	"context"
	"encoding/json"
	"fmt"

	einotool "github.com/cloudwego/eino/components/tool"
	einoschema "github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// ClientTool is one AG-UI-supplied tool definition (the protocol's `tools`
// array): a tool implemented and executed by the calling frontend, not by
// bepilot. The agent may call it like any other tool; bepilot only forwards
// the call as AG-UI TOOL_CALL_* frames and ends the turn immediately after,
// since it has no way to execute the tool itself. The client is expected to
// run it and send the result back as a `tool` message on the next run.
type ClientTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object describing the arguments; may be empty
}

// clientTool adapts one ClientTool to an eino tool. Info is real (so the
// model can see and call it); InvokableRun never performs any action — it
// only lets the ReAct graph close out the step before ToolReturnDirectly
// stops the run.
type clientTool struct {
	info *einoschema.ToolInfo
}

func (t *clientTool) Info(context.Context) (*einoschema.ToolInfo, error) { return t.info, nil }

func (t *clientTool) InvokableRun(context.Context, string, ...einotool.Option) (string, error) {
	return "", nil
}

// buildClientTools converts AG-UI tool definitions into bound eino tools plus
// the set of names that must return-directly (end the turn on call, since
// bepilot cannot execute them). Unnamed or duplicate-named definitions are
// skipped rather than failing the whole turn.
func buildClientTools(defs []ClientTool) ([]einotool.BaseTool, map[string]struct{}, error) {
	if len(defs) == 0 {
		return nil, nil, nil
	}
	out := make([]einotool.BaseTool, 0, len(defs))
	returnDirectly := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		if d.Name == "" {
			continue
		}
		if _, dup := returnDirectly[d.Name]; dup {
			continue
		}
		info := &einoschema.ToolInfo{Name: d.Name, Desc: d.Description}
		if len(d.Parameters) > 0 {
			var js jsonschema.Schema
			if err := json.Unmarshal(d.Parameters, &js); err != nil {
				return nil, nil, fmt.Errorf("client tool %q: invalid parameters schema: %w", d.Name, err)
			}
			info.ParamsOneOf = einoschema.NewParamsOneOfByJSONSchema(&js)
		}
		out = append(out, &clientTool{info: info})
		returnDirectly[d.Name] = struct{}{}
	}
	return out, returnDirectly, nil
}
