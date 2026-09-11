// Package fakeprovider is a deterministic, network-free ToolCallingChatModel.
// It lets the whole /v1/messages path (including the tool loop and SSE framing)
// be exercised in tests and local runs without any upstream API key.
package fakeprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/llm"
)

// Provider implements llm.Provider.
type Provider struct {
	// SkillSlug is the slug the fake model will try to load on its first turn.
	SkillSlug string
}

// New returns a fake provider. skillSlug is the slug passed to load_skill on the
// first turn (when that tool is bound); empty disables the tool call.
func New(skillSlug string) *Provider { return &Provider{SkillSlug: skillSlug} }

func (p *Provider) Name() string { return "fake" }
func (p *Provider) Kind() string { return "fake" }

func (p *Provider) Model(_ context.Context, modelID string, _ llm.Options) (model.ToolCallingChatModel, error) {
	return &fakeModel{model: modelID, skillSlug: p.SkillSlug}, nil
}

type fakeModel struct {
	model     string
	skillSlug string
	tools     []*schema.ToolInfo
}

func (m *fakeModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.tools = tools
	return &cp, nil
}

func (m *fakeModel) hasTool(name string) bool {
	for _, t := range m.tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// firstTurn reports whether the model has not yet seen a tool result.
func firstTurn(input []*schema.Message) bool {
	for _, msg := range input {
		if msg.Role == schema.Tool {
			return false
		}
	}
	return true
}

func lastUserText(input []*schema.Message) string {
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Role == schema.User {
			return input[i].Content
		}
	}
	return ""
}

func (m *fakeModel) plan(input []*schema.Message) (textChunks []string, toolCall *schema.ToolCall, finish string) {
	if firstTurn(input) && m.skillSlug != "" && m.hasTool("load_skill") {
		idx := 0
		return []string{
				"I'll ", "look up ", "the relevant ", "material first.\n",
			},
			&schema.ToolCall{
				Index: &idx,
				ID:    "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
				Type:  "function",
				Function: schema.FunctionCall{
					Name:      "load_skill",
					Arguments: fmt.Sprintf(`{"slug":%q}`, m.skillSlug),
				},
			},
			"tool_calls"
	}

	user := lastUserText(input)
	if len(user) > 160 {
		user = user[:160] + "…"
	}
	return []string{
		"Here is a response from the fake model.\n\n",
		"You asked: ", user, "\n\n",
		"(The fake provider produces deterministic output for local development and tests.)",
	}, nil, "stop"
}

func (m *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	chunks, tc, finish := m.plan(input)
	msg := &schema.Message{
		Role:         schema.Assistant,
		Content:      strings.Join(chunks, ""),
		ResponseMeta: &schema.ResponseMeta{FinishReason: finish, Usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30}},
	}
	if tc != nil {
		msg.Content = ""
		msg.ToolCalls = []schema.ToolCall{*tc}
	}
	return msg, nil
}

func (m *fakeModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	chunks, tc, finish := m.plan(input)
	var frames []*schema.Message
	for _, c := range chunks {
		frames = append(frames, &schema.Message{Role: schema.Assistant, Content: c})
	}
	if tc != nil {
		frames = append(frames, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{*tc}})
	}
	frames = append(frames, &schema.Message{
		Role:         schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{FinishReason: finish, Usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30}},
	})
	return schema.StreamReaderFromArray(frames), nil
}
