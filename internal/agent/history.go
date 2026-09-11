package agent

import (
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/domain"
)

// approxTokens is a cheap heuristic (~4 chars/token) used only for history
// trimming, never for billing.
func approxTokens(s string) int { return (len(s) + 3) / 4 }

// historyToMessages converts stored messages into eino messages. An assistant
// message that carried tool_use / tool_result blocks is expanded into an
// assistant message with ToolCalls followed by one tool message per result, so
// the model sees a well-formed transcript.
func historyToMessages(msgs []domain.Message) []*schema.Message {
	var out []*schema.Message
	for _, m := range msgs {
		switch m.Role {
		case domain.RoleUser:
			out = append(out, &schema.Message{Role: schema.User, Content: blocksToText(m.Blocks)})

		case domain.RoleAssistant:
			am := &schema.Message{Role: schema.Assistant}
			var toolMsgs []*schema.Message
			var text strings.Builder
			for _, b := range m.Blocks {
				switch b.Type {
				case domain.BlockText:
					text.WriteString(b.Text)
				case domain.BlockToolUse:
					idx := len(am.ToolCalls)
					am.ToolCalls = append(am.ToolCalls, schema.ToolCall{
						Index:    &idx,
						ID:       b.ToolUseID,
						Type:     "function",
						Function: schema.FunctionCall{Name: b.ToolName, Arguments: mustJSON(b.ToolInput)},
					})
				case domain.BlockToolResult:
					toolMsgs = append(toolMsgs, &schema.Message{
						Role:       schema.Tool,
						ToolCallID: b.ToolUseID,
						ToolName:   b.ToolName,
						Content:    toolResultText(b.ToolResult),
					})
				}
			}
			am.Content = text.String()
			if am.Content == "" && len(am.ToolCalls) == 0 && len(toolMsgs) == 0 {
				continue
			}
			out = append(out, am)
			out = append(out, toolMsgs...)
		}
	}
	return out
}

// trimHistory keeps the most recent messages within a token budget. It never
// splits an assistant/tool group: trimming happens at message boundaries from
// the front.
func trimHistory(msgs []*schema.Message, budget int) []*schema.Message {
	if budget <= 0 {
		return msgs
	}
	total := 0
	for _, m := range msgs {
		total += approxTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += approxTokens(tc.Function.Arguments)
		}
	}
	if total <= budget {
		return msgs
	}
	// Drop from the front until under budget, but never drop a leading tool
	// message (would orphan it); skip forward to the next user message.
	start := 0
	for start < len(msgs) && total > budget {
		total -= approxTokens(msgs[start].Content)
		for _, tc := range msgs[start].ToolCalls {
			total -= approxTokens(tc.Function.Arguments)
		}
		start++
	}
	for start < len(msgs) && msgs[start].Role == schema.Tool {
		start++
	}
	return msgs[start:]
}

func blocksToText(blocks []domain.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == domain.BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// lastUserText returns the text of the most recent user message.
func lastUserText(msgs []domain.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == domain.RoleUser {
			return blocksToText(msgs[i].Blocks)
		}
	}
	return ""
}
