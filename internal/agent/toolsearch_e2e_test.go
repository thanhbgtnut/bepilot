package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/tools"
)

// scriptedModel plays the part of an LLM that needs an external tool: it calls
// tool_search when the tool is not bound, calls the tool once it is, then
// answers. It records which tools were bound on each call.
type scriptedModel struct {
	mu     *sync.Mutex
	calls  *[][]string
	bound  []string
	target string
}

func (m *scriptedModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.bound = nil
	for _, i := range infos {
		cp.bound = append(cp.bound, i.Name)
	}
	return &cp, nil
}

func (m *scriptedModel) has(n string) bool {
	for _, b := range m.bound {
		if b == n {
			return true
		}
	}
	return false
}

func (m *scriptedModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	*m.calls = append(*m.calls, append([]string{}, m.bound...))
	m.mu.Unlock()

	toolCall := func(name string, args any) *schema.Message {
		b, _ := json.Marshal(args)
		idx := 0
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			Index: &idx, ID: fmt.Sprintf("call_%d", len(in)), Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: string(b)},
		}}}
	}
	last := in[len(in)-1]
	switch {
	case m.has(m.target) && last.Role == schema.Tool && last.ToolName == m.target:
		return &schema.Message{Role: schema.Assistant, Content: "done: " + last.Content}, nil
	case m.has(m.target):
		return toolCall(m.target, map[string]any{"text": "hello"}), nil
	case m.has(tools.ToolSearchName):
		return toolCall(tools.ToolSearchName, map[string]any{"query": "select:" + m.target}), nil
	default:
		return &schema.Message{Role: schema.Assistant, Content: "no external tools needed"}, nil
	}
}

func (m *scriptedModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, o...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

type echoIn struct {
	Text string `json:"text" jsonschema:"required"`
}

func runScripted(t *testing.T, register bool) (answer string, bound [][]string, evs []events.Event) {
	t.Helper()
	const target = "mcp__demo__echo"
	reg, err := tools.NewRegistry(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if register {
		echo, err := toolutils.InferTool("echo", "Echo text back", func(_ context.Context, a echoIn) (string, error) {
			return "echoed:" + a.Text, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.Register(context.Background(), tools.Prefixed(echo.(einotool.InvokableTool), target)); err != nil {
			t.Fatal(err)
		}
	}

	sess := reg.NewSession()
	ctx := tools.WithSession(context.Background(), sess)
	var (
		mu    sync.Mutex
		calls [][]string
	)
	cm := sess.WrapModel(&scriptedModel{mu: &mu, calls: &calls, target: target})
	ra, err := buildReactAgent(ctx, cm, sess.Executable(), "sys", 8, nil)
	if err != nil {
		t.Fatal(err)
	}

	asm := newAssembler(func(e events.Event) { evs = append(evs, e) })
	history := []*einoMessage{{Role: schema.User, Content: "use the demo tool"}}
	agentObj := &Agent{}
	if err := agentObj.drive(ctx, ra, history, newCallbackHandler(asm)); err != nil {
		t.Fatalf("drive: %v", err)
	}
	blocks, _ := asm.result("end_turn")
	for _, b := range blocks {
		if b.Type == "text" {
			answer += b.Text
		}
	}
	return answer, calls, evs
}

func contains(list []string, n string) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

func TestToolSearchEndToEnd(t *testing.T) {
	answer, bound, evs := runScripted(t, true)

	if len(bound) != 3 {
		t.Fatalf("expected 3 model calls (search, use, answer), got %d: %v", len(bound), bound)
	}
	if contains(bound[0], "mcp__demo__echo") || !contains(bound[0], tools.ToolSearchName) {
		t.Errorf("step 1 must not see the external tool but must have tool_search: %v", bound[0])
	}
	if !contains(bound[1], "mcp__demo__echo") || !contains(bound[1], "current_time") {
		t.Errorf("step 2 must see the discovered tool and the built-ins: %v", bound[1])
	}
	if !strings.Contains(answer, "echoed:hello") {
		t.Errorf("answer = %q", answer)
	}
	var started []string
	for _, e := range evs {
		if e.Kind == events.KindToolExecStart {
			started = append(started, e.ToolName)
		}
	}
	if strings.Join(started, ",") != tools.ToolSearchName+",mcp__demo__echo" {
		t.Errorf("tool execution order = %v", started)
	}
}

func TestNoExternalToolsEndToEnd(t *testing.T) {
	answer, bound, _ := runScripted(t, false)
	if len(bound) != 1 || contains(bound[0], tools.ToolSearchName) {
		t.Fatalf("with no external tools there is one model call and no tool_search: %v", bound)
	}
	if answer != "no external tools needed" {
		t.Errorf("answer = %q", answer)
	}
}
